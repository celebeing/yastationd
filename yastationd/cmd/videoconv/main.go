// Command videoconv turns a local video file into what the daemon's "video" display mode plays
// (docs/video-mode.md): a frames file for a pixel front screen (the .led screen pattern format,
// gzipped when the name ends in .gz) and the audio track as a WAV aplay can play (S16LE, 48 kHz,
// stereo). Host-side; it runs ffmpeg, which must be on PATH. It never downloads anything: the
// video is yours to supply, and its output is never committed.
//
//	videoconv -frames build/video.led.gz -audio build/video.wav <video file>
//	videoconv -check build/video.led.gz          (parse only: frames, length)
//
// The picture is scaled to the screen with area averaging (grey levels from the edges), keeping
// its aspect: -fit pad (default) letterboxes/pillarboxes the whole picture in black, -fit crop
// fills the screen and cuts the overhang. Levels are written as the video has them (gamma-encoded,
// like the firmware's own patterns); the daemon applies the screen's gamma (matrix.gamma) on load.
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

func main() {
	frames := flag.String("frames", "video.led.gz", "frames file to write (gzipped if it ends in .gz; \"\" = none)")
	audio := flag.String("audio", "video.wav", "audio file to write (\"\" = none)")
	size := flag.String("size", "21x12", "screen size WxH")
	fps := flag.Int("fps", 30, "frame rate of the frames file (the screen ticks every 20 ms, so up to 50)")
	fit := flag.String("fit", "pad", "pad: the whole picture, black bars; crop: fill the screen")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	check := flag.Bool("check", false, "only parse the given frames files")
	flag.Parse()

	var w, h int
	if _, err := fmt.Sscanf(*size, "%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		fail(fmt.Errorf("bad -size %q", *size))
	}
	if *check {
		for _, p := range flag.Args() {
			v, err := display.LoadVideo(p, w, h, 1)
			if err != nil {
				fail(err)
			}
			fmt.Printf("%s: %d frames, %v\n", p, v.Frames(), v.Duration())
		}
		return
	}
	if flag.NArg() != 1 || *fps < 1 || *fps > 50 || (*fit != "pad" && *fit != "crop") {
		fmt.Fprintln(os.Stderr, "usage: videoconv [-frames f.led.gz] [-audio f.wav] [-size 21x12] [-fps 30] [-fit pad|crop] <video>")
		os.Exit(2)
	}
	in := flag.Arg(0)
	if *frames != "" {
		n, err := convertFrames(*ffmpeg, in, *frames, w, h, *fps, *fit)
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s: %d frames at %d fps\n", *frames, n, *fps)
	}
	if *audio != "" {
		if err := convertAudio(*ffmpeg, in, *audio); err != nil {
			fail(err)
		}
		fmt.Printf("%s: S16LE 48 kHz stereo\n", *audio)
	}
}

// videoFilter scales to w x h keeping the display aspect, then pads or crops to the screen.
func videoFilter(w, h, fps int, fit string) string {
	f := fmt.Sprintf("fps=%d,scale='iw*sar':ih,setsar=1,", fps)
	if fit == "crop" {
		f += fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase:flags=area,crop=%d:%d", w, h, w, h)
	} else {
		f += fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=area,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black", w, h, w, h)
	}
	return f + ",format=rgb24"
}

// frameMs is the duration of frame i at fps, in whole ms, with the rounding carried over so the
// frames stay on the true timeline (30 fps: 33, 34, 33, …, never drifting by more than 0.5 ms).
// Plain 33 ms frames would run 1 % fast: 2 s ahead of the audio by the end of a 3.5 min video.
func frameMs(i, fps int) int {
	at := func(n int) int { return (n*1000 + fps/2) / fps }
	return at(i+1) - at(i)
}

func convertFrames(ffmpeg, in, out string, w, h, fps int, fit string) (int, error) {
	cmd := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-i", in, "-an", "-sn",
		"-vf", videoFilter(w, h, fps, fit), "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	cmd.Stderr = os.Stderr
	raw, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	tmp := out + ".tmp"
	n, werr := writeFrames(tmp, bufio.NewReaderSize(raw, 1<<20), w*h*3, fps, strings.HasSuffix(out, ".gz"))
	if werr != nil {
		cmd.Process.Kill()
	}
	if err := cmd.Wait(); err != nil && werr == nil {
		werr = fmt.Errorf("ffmpeg: %w", err)
	}
	if werr == nil && n == 0 {
		werr = fmt.Errorf("%s: no video frames", in)
	}
	if werr != nil {
		os.Remove(tmp)
		return 0, werr
	}
	return n, os.Rename(tmp, out)
}

// writeFrames reads packed RGB frames of size bytes from r until EOF and writes them as .led lines.
func writeFrames(path string, r io.Reader, size, fps int, gz bool) (int, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 1<<20)
	var dst io.Writer = bw
	var zw *gzip.Writer
	if gz {
		zw, _ = gzip.NewWriterLevel(bw, gzip.BestCompression)
		dst = zw
	}
	buf := make([]byte, size)
	var line []byte
	n := 0
	for {
		if _, err := io.ReadFull(r, buf); err == io.EOF {
			break
		} else if err != nil {
			return n, fmt.Errorf("frame %d: %w", n, err)
		}
		line = display.AppendLedFrame(line[:0], buf, frameMs(n, fps))
		if _, err := dst.Write(line); err != nil {
			return n, err
		}
		n++
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			return n, err
		}
	}
	if err := bw.Flush(); err != nil {
		return n, err
	}
	return n, f.Close()
}

func convertAudio(ffmpeg, in, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp.wav"
	cmd := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-y", "-i", in, "-vn", "-sn",
		"-ac", "2", "-ar", "48000", "-c:a", "pcm_s16le", tmp)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg (audio): %w", err)
	}
	return os.Rename(tmp, out)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "videoconv:", err)
	os.Exit(1)
}
