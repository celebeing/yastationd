// Command demoreel records what a frame tap draws on the front display into an animated GIF, the
// way the glass shows it: the LEDs' light diffused and seen through the speaker's fabric. It
// drives the tap through a script of commands (the same commands as the LED preview), so with the
// host simulator (cmd/ledsim) it makes the README's demo reels. docs/led-preview.md, "Demo reels".
//
//	go run ./cmd/ledsim -board orion -patterns <screen patterns> -media <dir> &
//	go run ./cmd/demoreel -tap http://localhost:8930 -script orion -o orion.gif
package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// info is the part of the tap's /info the reel needs.
type info struct {
	Screen [2]int `json:"screen"`
	Seg7   *struct {
		Colon, Dot, Degree bool
	} `json:"seg7"`
}

type frame struct {
	t    int64  // the daemon's clock, ms
	data []byte // screen RGB, or seg7 drive levels
}

func main() {
	tap := flag.String("tap", "http://localhost:8930", "the frame tap's base URL")
	out := flag.String("o", "reel.gif", "output GIF")
	name := flag.String("script", "orion", "script: "+strings.Join(scriptNames(), ", "))
	scale := flag.Float64("scale", 0, "px per screen dot, or per seg7 unit (a digit is 100); 0 = default")
	media := flag.String("media", "", "the simulator's -media directory: a \"video\" step plays <piece> from it")
	flag.Parse()
	steps, ok := scripts[*name]
	if !ok {
		fail(fmt.Errorf("no script %q", *name))
	}

	var in info
	r, err := http.Get(*tap + "/info")
	if err != nil {
		fail(err)
	}
	err = json.NewDecoder(r.Body).Decode(&in)
	r.Body.Close()
	if err != nil {
		fail(err)
	}
	var pane painter
	switch {
	case in.Seg7 != nil:
		pane = newSeg7Painter(in.Seg7.Colon, in.Seg7.Dot, in.Seg7.Degree, *scale)
	case in.Screen[0] > 0:
		pane = newScreenPainter(in.Screen[0], in.Screen[1], *scale)
	default:
		fail(fmt.Errorf("the tap has no front display"))
	}

	rec := &recorder{seg7: in.Seg7 != nil}
	go rec.read(*tap)
	time.Sleep(time.Second) // the stream's first frames
	for i, s := range steps {
		if s.cmd == "video" { // a piece of video: it becomes video.led.gz, then the video mode plays it
			if err := usePiece(*media, s.payload.(string)); err != nil {
				fail(err)
			}
			s.cmd, s.payload = "display_mode", "video"
		}
		if s.cmd != "" {
			if err := post(*tap, s.cmd, s.payload); err != nil {
				fail(err)
			}
		}
		if i == 0 { // the first command sets the scene; record from there
			time.Sleep(300 * time.Millisecond)
			rec.start()
		}
		time.Sleep(s.wait)
	}
	frames := rec.stop()
	fmt.Fprintf(os.Stderr, "%d frames over %.1f s\n", len(frames), float64(frames[len(frames)-1].t-frames[0].t)/1000)
	if err := writeGIF(*out, frames, pane); err != nil {
		fail(err)
	}
}

// recorder keeps the frames of the front display while recording.
type recorder struct {
	seg7   bool
	mu     sync.Mutex
	on     bool
	last   []byte
	frames []frame
}

func (r *recorder) read(tap string) {
	resp, err := http.Get(tap + "/frames")
	if err != nil {
		fail(err)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var f struct {
			T            int64
			Screen, Seg7 string
		}
		if json.Unmarshal([]byte(l), &f) != nil {
			continue
		}
		s := f.Screen
		if r.seg7 {
			s = f.Seg7
		}
		b, err := hex.DecodeString(s)
		if err != nil || len(b) == 0 {
			continue
		}
		r.mu.Lock()
		r.last = b
		if r.on {
			r.frames = append(r.frames, frame{f.T, b})
		}
		r.mu.Unlock()
	}
}

func (r *recorder) start() {
	r.mu.Lock()
	r.on = true
	if r.last != nil { // the picture already up when recording starts
		r.frames = append(r.frames, frame{time.Now().UnixMilli(), r.last})
	}
	r.mu.Unlock()
}

func (r *recorder) stop() []frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.on = false
	if len(r.frames) == 0 {
		fail(fmt.Errorf("no frames recorded"))
	}
	return r.frames
}

// usePiece copies <media>/<piece> to <media>/video.led.gz, which the video mode loads when it
// starts (the simulator links its -media directory into <cfw>/media).
func usePiece(media, piece string) error {
	if media == "" {
		return fmt.Errorf("a video step needs -media")
	}
	b, err := os.ReadFile(filepath.Join(media, piece))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(media, "video.led.gz"), b, 0o644)
}

func post(tap, name string, payload any) error {
	body, ok := payload.(string)
	if !ok {
		b, _ := json.Marshal(payload)
		body = string(b)
	}
	resp, err := http.Post(tap+"/cmd/"+name, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "command %s: %s\n", name, resp.Status)
	}
	return nil
}

// writeGIF encodes the frames: one picture per 10 ms (GIF's unit; the last frame in it wins), at
// most one per minDelay, identical pictures merged, and after the first only the rectangle that
// changed.
func writeGIF(path string, frames []frame, pane painter) error {
	type pic struct {
		cs   int64
		data []byte
	}
	var pics []pic
	for _, f := range frames {
		cs := (f.t - frames[0].t + 5) / 10
		n := len(pics)
		switch {
		case n > 0 && pics[n-1].cs == cs:
			pics[n-1].data = f.data
		case n > 0 && string(pics[n-1].data) == string(f.data):
		default:
			pics = append(pics, pic{cs, f.data})
		}
	}
	g := &gif.GIF{}
	var prev *image.Gray
	for i, p := range pics {
		d := 150 // the last picture holds before the loop
		if i+1 < len(pics) {
			d = int(pics[i+1].cs - p.cs)
			if d < minDelay { // too soon: let the next one start here
				pics[i+1].cs = p.cs
				continue
			}
		}
		cur := pane.paint(p.data)
		r := cur.Bounds()
		if prev != nil {
			r = changed(prev, cur)
		}
		g.Image = append(g.Image, toPaletted(cur, prev, r))
		prev = cur
		g.Delay = append(g.Delay, d)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := gif.EncodeAll(f, g); err != nil {
		f.Close()
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: %d pictures\n", path, len(g.Image))
	return f.Close()
}

// minDelay caps the GIF at 20 pictures a second (in cs): the size, and browsers stretch delays
// under 2 cs anyway.
const minDelay = 5

// The palette: the 64 greys the painters produce, and a transparent entry for the pixels that
// did not change (long runs of it are what makes the texture affordable for LZW).
const levels = 64

var palette = func() color.Palette {
	p := make(color.Palette, levels+1)
	for i := 0; i < levels; i++ {
		p[i] = color.Gray{uint8(i * 255 / (levels - 1))}
	}
	p[levels] = color.Transparent
	return p
}()

func toPaletted(src, prev *image.Gray, r image.Rectangle) *image.Paletted {
	dst := image.NewPaletted(r, palette)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			v := src.Pix[src.PixOffset(x, y)]
			i := uint8(levels)
			if prev == nil || prev.Pix[prev.PixOffset(x, y)] != v {
				i = uint8((int(v)*(levels-1) + 127) / 255)
			}
			dst.Pix[dst.PixOffset(x, y)] = i
		}
	}
	return dst
}

// changed is the bounding box of the pixels that differ (one pixel when none does).
func changed(a, b *image.Gray) image.Rectangle {
	r := image.Rectangle{}
	w, h := b.Bounds().Dx(), b.Bounds().Dy()
	for y := 0; y < h; y++ {
		ra, rb := a.Pix[y*a.Stride:y*a.Stride+w], b.Pix[y*b.Stride:y*b.Stride+w]
		if string(ra) == string(rb) {
			continue
		}
		x0, x1 := 0, w
		for x0 < w && ra[x0] == rb[x0] {
			x0++
		}
		for x1 > x0 && ra[x1-1] == rb[x1-1] {
			x1--
		}
		r = r.Union(image.Rect(x0, y, x1, y+1))
	}
	if r.Empty() {
		return image.Rect(0, 0, 1, 1)
	}
	return r
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "demoreel:", err)
	os.Exit(1)
}
