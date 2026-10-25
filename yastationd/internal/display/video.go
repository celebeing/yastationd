package display

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Video is a long screen clip played against an external clock — the audio track's — rather than
// from its own first frame: the frame shown is the one under the elapsed time since the audio
// started, so the picture follows the sound however the render ticks fall. The frames are a
// screen pattern (.led, ParseLed) as written by cmd/videoconv, optionally gzipped.
type Video struct {
	Clip   PixelClip
	starts []time.Duration // starts[i] = when frame i begins
	total  time.Duration
}

// NewVideo indexes a clip's frames by start time.
func NewVideo(c PixelClip) *Video {
	v := &Video{Clip: c, starts: make([]time.Duration, len(c.Frames))}
	for i, f := range c.Frames {
		v.starts[i] = v.total
		v.total += f.Dur
	}
	return v
}

// LoadVideo reads a frames file for a w x h screen, correcting it with gamma like a stock pattern.
// A gzipped file (by its magic, whatever its name) is decompressed on the fly. When path itself
// is missing, path+".gz" is tried.
func LoadVideo(path string, w, h int, gamma float64) (*Video, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		f, err = os.Open(path + ".gz")
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 256*1024)
	var r io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		defer zr.Close()
		r = zr
	}
	c, err := ParseLed(filepath.Base(path), r, w, h, gamma)
	if err != nil {
		return nil, err
	}
	return NewVideo(c), nil
}

// Duration is the length of the picture.
func (v *Video) Duration() time.Duration { return v.total }

// Frames is the number of frames.
func (v *Video) Frames() int { return len(v.starts) }

// Index is the frame under elapsed time el: the first before the start, -1 after the end.
func (v *Video) Index(el time.Duration) int {
	if el >= v.total {
		return -1
	}
	if el < 0 {
		return 0
	}
	// the last frame starting at or before el
	return sort.Search(len(v.starts), func(i int) bool { return v.starts[i] > el }) - 1
}

// AppendLedFrame appends one .led frame line: the pixels (packed RGB, 3 bytes each, row-major) as
// RRGGBB and the duration in ms. The inverse of one ParseLed line (with gamma 1).
func AppendLedFrame(dst, rgb []byte, ms int) []byte {
	const hex = "0123456789abcdef"
	for i, b := range rgb {
		if i > 0 && i%3 == 0 {
			dst = append(dst, ' ')
		}
		dst = append(dst, hex[b>>4], hex[b&0xf])
	}
	dst = append(dst, ' ')
	dst = strconv.AppendInt(dst, int64(ms), 10)
	return append(dst, '\n')
}

// Frame draws the frame under el at the display brightness; ok is false past the end.
func (v *Video) Frame(el time.Duration, brightness int) (f FrontFrame, ok bool) {
	i := v.Index(el)
	if i < 0 {
		return FrontFrame{}, false
	}
	return v.Clip.frame(i, brightness), true
}
