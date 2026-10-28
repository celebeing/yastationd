package app

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

func assetsReq(t *testing.T, method, url, tok string, body []byte) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(b)
}

func TestAssetsVideos(t *testing.T) {
	a, srv := newWebApp(t)
	text := "device_id = t1\nsysroot = " + t.TempDir() + "\n" // in the file, so a config write keeps it
	os.WriteFile(a.cfg().File, []byte(text), 0o600)
	a.adoptConfig([]byte(text))
	a.prof.Platform.CFWRoot = "/cfw"
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	u := srv.URL + "/api/assets"

	if code, _ := assetsReq(t, "PUT", u+"/videos/clip/frames", tok, []byte("x")); code != http.StatusConflict {
		t.Fatalf("no pixel screen: %d", code)
	}
	a.screen = hw.SizedDotMatrix(3, 2)

	// Two frames of 3×2, as the browser's converter (and videoconv) write them, gzipped.
	var led []byte
	led = display.AppendLedFrame(led, bytes.Repeat([]byte{0x10, 0x20, 0x30}, 6), 33)
	led = display.AppendLedFrame(led, bytes.Repeat([]byte{0xff, 0xff, 0xff}, 6), 34)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(led)
	zw.Close()

	if code, body := assetsReq(t, "PUT", u+"/videos/Bad Apple/frames", tok, gz.Bytes()); code != 400 {
		t.Fatalf("a bad name: %d %s", code, body)
	}
	if code, body := assetsReq(t, "PUT", u+"/videos/clip/frames", tok, []byte("not frames\n00 11\n")); code != 422 {
		t.Fatalf("garbage frames: %d %s", code, body)
	}
	code, body := assetsReq(t, "PUT", u+"/videos/clip/frames", tok, gz.Bytes())
	if code != 200 || !strings.Contains(body, `"frames":2`) || !strings.Contains(body, `"duration_ms":67`) {
		t.Fatalf("frames: %d %s", code, body)
	}
	if code, _ := assetsReq(t, "PUT", u+"/videos/clip/audio", tok, []byte("ID3 not a wav file")); code != 422 {
		t.Fatalf("a non-WAV: %d", code)
	}
	wav := wavFile(make([]byte, 400), 48000)
	if code, body := assetsReq(t, "PUT", u+"/videos/clip/audio", tok, wav); code != 200 {
		t.Fatalf("audio: %d %s", code, body)
	}
	// Plain (not gzipped) frames work too: a file made elsewhere.
	if code, body := assetsReq(t, "PUT", u+"/videos/plain/frames", tok, led); code != 200 {
		t.Fatalf("plain frames: %d %s", code, body)
	}
	lib := filepath.Join(a.cfg().SysRoot, "cfw/media/videos")
	for _, f := range []string{"clip.led.gz", "clip.wav", "plain.led"} {
		if _, err := os.Stat(filepath.Join(lib, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(lib, ".upload-*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}

	// Use: the video mode's keys point at it, live (runtime keys), without the sysroot.
	if code, body := assetsReq(t, "POST", u+"/videos/clip/use", tok, nil); code != 200 {
		t.Fatalf("use: %d %s", code, body)
	}
	if a.cfg().VideoFrames != "/cfw/media/videos/clip.led.gz" || a.cfg().VideoAudio != "/cfw/media/videos/clip.wav" {
		t.Fatalf("not in force: %q %q", a.cfg().VideoFrames, a.cfg().VideoAudio)
	}
	code, out = call(t, "GET", u, tok, "")
	items := out["videos"].(map[string]any)["items"].([]any)
	if code != 200 || len(items) != 2 || items[0].(map[string]any)["name"] != "clip" || items[0].(map[string]any)["current"] != true ||
		items[1].(map[string]any)["current"] != false || items[0].(map[string]any)["audio_bytes"].(float64) != float64(len(wav)) {
		t.Fatalf("list: %d %v", code, out)
	}

	// Deleting the current one sends the video mode back to its defaults.
	if code, body := assetsReq(t, "DELETE", u+"/videos/clip", tok, nil); code != 200 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if a.cfg().Has("video_frames") || fileExists(filepath.Join(lib, "clip.wav")) {
		t.Fatalf("after delete: %q", a.cfg().VideoFrames)
	}
	if code, _ := assetsReq(t, "DELETE", u+"/videos/clip", tok, nil); code != 422 {
		t.Fatalf("delete a missing one: %d", code)
	}
}
