package app

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Assets (docs/assets.md): the owner's own media on the speaker, by kind. Only one kind exists so
// far — videos for the video mode (docs/video-mode.md), converted in the browser and uploaded as
// a frames file plus a WAV into <cfw>/media/videos/. The API is shaped for more kinds (sounds,
// animations, packs; docs/wishlist.md).

const (
	videoLibDir    = "media/videos"
	maxFramesBytes = 64 << 20  // a 3:39 Bad Apple is ~0.5 MB gzipped, 11.6 MB as text
	maxAudioBytes  = 400 << 20 // 48 kHz stereo WAV: ~11.5 MB a minute
	assetFreeKeep  = 20 << 20  // never fill the CFW's file system to the brim
)

var assetName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

type videoAsset struct {
	Name        string `json:"name"`
	FramesBytes int64  `json:"frames_bytes"`
	AudioBytes  int64  `json:"audio_bytes"`
	Current     bool   `json:"current"`  // the one the video mode plays
	Modified    int64  `json:"modified"` // unix ms
}

type assetsView struct {
	Videos struct {
		Supported bool         `json:"supported"` // a pixel screen: the video mode exists
		Screen    [2]int       `json:"screen"`    // its size, what the frames must be
		Items     []videoAsset `json:"items"`
		Current   string       `json:"current"` // the video mode's frames file, wherever it is
		MaxFrames int64        `json:"max_frames"`
		MaxAudio  int64        `json:"max_audio"`
	} `json:"videos"`
	Free int64 `json:"free"` // bytes free where the assets live
}

func (a *App) videoLib() string {
	return a.cfwFile(videoLibDir)
}

// videoFiles: a library video's frames file (gzipped or not) and its WAV.
func (a *App) videoFiles(name string) (frames, audio string) {
	dir := a.videoLib()
	frames = filepath.Join(dir, name+".led.gz")
	if !fileExists(frames) {
		frames = filepath.Join(dir, name+".led")
	}
	return frames, filepath.Join(dir, name+".wav")
}

func (a *App) assetsNow() assetsView {
	var v assetsView
	v.Videos.Supported = a.screen != nil
	if a.screen != nil {
		w, h := a.screen.Size()
		v.Videos.Screen = [2]int{w, h}
	}
	v.Videos.MaxFrames, v.Videos.MaxAudio = maxFramesBytes, maxAudioBytes
	v.Videos.Items = []videoAsset{}
	v.Videos.Current = a.cfg().VideoFrames
	ents, _ := os.ReadDir(a.videoLib())
	seen := map[string]bool{}
	for _, e := range ents {
		name, ok := strings.CutSuffix(strings.TrimSuffix(e.Name(), ".gz"), ".led")
		if !ok || seen[name] || !assetName.MatchString(name) {
			continue
		}
		seen[name] = true
		frames, audio := a.videoFiles(name)
		it := videoAsset{Name: name, Current: a.isCurrentVideo(frames)}
		if st, err := os.Stat(frames); err == nil {
			it.FramesBytes, it.Modified = st.Size(), st.ModTime().UnixMilli()
		}
		if st, err := os.Stat(audio); err == nil {
			it.AudioBytes = st.Size()
		}
		v.Videos.Items = append(v.Videos.Items, it)
	}
	sort.Slice(v.Videos.Items, func(i, j int) bool { return v.Videos.Items[i].Name < v.Videos.Items[j].Name })
	v.Free = freeBytes(a.cfg().Path(a.prof.Platform.CFWRoot))
	return v
}

// isCurrentVideo: frames (a library video's frames file) is the one the video mode plays.
func (a *App) isCurrentVideo(frames string) bool {
	cur := a.cfg().Path(a.cfg().VideoFrames)
	return frames == cur || strings.TrimSuffix(frames, ".gz") == cur
}

// isGzip: b starts with the gzip magic.
func isGzip(b []byte) bool { return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b }

// freeBytes: the bytes free for us on the file system of path (-1: unknown).
func freeBytes(path string) int64 {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// apiAssets: GET /api/assets; PUT /api/assets/videos/<name>/frames|audio (the file as the body);
// POST /api/assets/videos/<name>/use (the video mode plays it); DELETE /api/assets/videos/<name>.
func (a *App) apiAssets(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/assets"), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET")
			return
		}
		writeJSON(w, http.StatusOK, a.assetsNow())
		return
	}
	if parts[0] != "videos" || len(parts) < 2 || len(parts) > 3 {
		writeErr(w, http.StatusNotFound, "no such asset kind; there are: videos")
		return
	}
	if a.screen == nil {
		writeErr(w, http.StatusConflict, "this speaker has no pixel screen: no video mode")
		return
	}
	name := parts[1]
	if !assetName.MatchString(name) {
		writeErr(w, http.StatusBadRequest, "name: lower-case letters, digits, - and _, up to 40")
		return
	}
	what := ""
	if len(parts) == 3 {
		what = parts[2]
	}
	var err error
	code := http.StatusOK
	out := map[string]any{"ok": true}
	switch {
	case r.Method == http.MethodPut && what == "frames":
		var n int
		var d time.Duration
		n, d, err = a.putVideoFrames(name, r)
		out["frames"], out["duration_ms"] = n, d.Milliseconds()
	case r.Method == http.MethodPut && what == "audio":
		err = a.putVideoAudio(name, r)
	case r.Method == http.MethodPost && what == "use":
		err = a.useVideo(name)
	case r.Method == http.MethodDelete && what == "":
		err = a.deleteVideo(name)
	default:
		code, err = http.StatusMethodNotAllowed, errors.New("PUT frames|audio, POST use, DELETE")
	}
	if err != nil {
		if code == http.StatusOK {
			code = http.StatusUnprocessableEntity
		}
		writeErr(w, code, err.Error())
		return
	}
	a.log.Info("assets: video", "name", name, "op", r.Method+" "+what)
	writeJSON(w, code, out)
}

// receive writes the request body to a temporary file in dir, at most max bytes, leaving room on
// the file system; the caller renames or removes it.
func (a *App) receive(r *http.Request, dir string, max int64) (string, error) {
	if r.ContentLength > max {
		return "", errors.New("too big")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if free := freeBytes(dir); free >= 0 && r.ContentLength > 0 && r.ContentLength > free-assetFreeKeep {
		return "", errors.New("not enough room on the speaker")
	}
	f, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(r.Body, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = errors.New("too big")
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (a *App) putVideoFrames(name string, r *http.Request) (int, time.Duration, error) {
	tmp, err := a.receive(r, a.videoLib(), maxFramesBytes)
	if err != nil {
		return 0, 0, err
	}
	w, h := a.screen.Size()
	v, err := display.LoadVideo(tmp, w, h, a.prof.Matrix.Gamma) // the same parser the mode uses
	if err != nil {
		os.Remove(tmp)
		return 0, 0, err
	}
	if v.Frames() == 0 {
		os.Remove(tmp)
		return 0, 0, errors.New("no frames")
	}
	head := make([]byte, 2)
	if f, err := os.Open(tmp); err == nil {
		io.ReadFull(f, head)
		f.Close()
	}
	dst := filepath.Join(a.videoLib(), name+".led")
	if isGzip(head) {
		dst += ".gz"
	}
	os.Remove(strings.TrimSuffix(dst, ".gz"))
	os.Remove(strings.TrimSuffix(dst, ".gz") + ".gz") // the other form of an older upload
	return v.Frames(), v.Duration(), os.Rename(tmp, dst)
}

func (a *App) putVideoAudio(name string, r *http.Request) error {
	tmp, err := a.receive(r, a.videoLib(), maxAudioBytes)
	if err != nil {
		return err
	}
	head := make([]byte, 12)
	f, err := os.Open(tmp)
	if err == nil {
		_, err = io.ReadFull(f, head)
		f.Close()
	}
	if err != nil || string(head[:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		os.Remove(tmp)
		return errors.New("not a WAV file")
	}
	_, audio := a.videoFiles(name)
	return os.Rename(tmp, audio)
}

// useVideo points the video mode at a library video (video_frames / video_audio, runtime keys).
func (a *App) useVideo(name string) error {
	frames, audio := a.videoFiles(name)
	if !fileExists(frames) {
		return errors.New("no such video")
	}
	root := a.cfg().SysRoot // the config holds paths on the speaker, not under a test's sysroot
	ch := map[string][]string{"video_frames": {strings.TrimPrefix(frames, root)}, "video_audio": {strings.TrimPrefix(audio, root)}}
	_, _, err := a.applyConfigKeys(ch, "set from the Assets page", "assets")
	return err
}

func (a *App) deleteVideo(name string) error {
	frames, audio := a.videoFiles(name)
	if !fileExists(frames) && !fileExists(audio) {
		return errors.New("no such video")
	}
	if a.isCurrentVideo(frames) {
		if a.videoActive() {
			a.stopVideo("its video deleted")
			a.publishDisplayMode()
		}
		ch := map[string][]string{"video_frames": nil, "video_audio": nil} // back to the defaults
		if _, _, err := a.applyConfigKeys(ch, "set from the Assets page", "assets"); err != nil {
			return err
		}
	}
	os.Remove(frames)
	os.Remove(audio)
	return nil
}
