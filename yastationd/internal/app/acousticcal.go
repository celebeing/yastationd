package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Acoustic calibration is deliberately limited to the one measured topology. It records no raw
// audio: each direct LOOPBACK-A capture is analysed and discarded before the next one begins.
const (
	acousticCaptureDevice = "hw:0,3"
	acousticCaptureRate   = 96000
	acousticChannels      = 14
	acousticReference     = 8
	acousticSeconds       = 7
	acousticSweepRate     = 48000
	acousticSweepSeconds  = 4
	acousticMasterRaw     = 134 // verified on Orion with the -40 dBFS sweep
	// The PDM/loopback open transient saturates the mics for ~1 s (measured on device:
	// ref first nonzero at 1.013 s, mic samples pinned at ±32766 until then). Skip it in the
	// clip check and wait for it before starting the sweep.
	acousticTransientSeconds = 1
)

type acousticCaptureView struct {
	ReferencePeakLSB int  `json:"reference_peak_lsb"`
	MicClipped       bool `json:"mic_clipped"`
	Accepted         bool `json:"accepted"`
}

type acousticCalView struct {
	Supported bool                  `json:"supported"`
	Running   bool                  `json:"running"`
	Stage     string                `json:"stage,omitempty"`
	LevelDBFS int                   `json:"level_dbfs,omitempty"`
	Captures  []acousticCaptureView `json:"captures,omitempty"`
	Error     string                `json:"error,omitempty"`
	Updated   int64                 `json:"updated,omitempty"`
}

func (a *App) acousticSupported() bool { return a.prof.Name == "orion" }

func (a *App) acousticView() acousticCalView {
	a.acousticMu.Lock()
	defer a.acousticMu.Unlock()
	v := a.acoustic
	v.Supported = a.acousticSupported()
	v.Captures = append([]acousticCaptureView(nil), v.Captures...)
	return v
}

// apiAcousticCal: GET observes a run; POST starts three bounded, low-level sweeps; DELETE
// cancels an active run. The loudest permitted source is -40 dBFS, independent of the owner's
// ordinary volume, and the tested master ceiling is restored exactly afterwards.
func (a *App) apiAcousticCal(w http.ResponseWriter, r *http.Request) {
	if !a.acousticSupported() {
		writeErr(w, http.StatusNotFound, "acoustic measurement is currently supported only on Station 3")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.acousticView())
	case http.MethodPost:
		var in struct {
			LevelDBFS int `json:"level_dbfs"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, smallBody)).Decode(&in) != nil || !acousticLevel(in.LevelDBFS) {
			writeErr(w, http.StatusBadRequest, "level_dbfs must be -50, -45, or -40")
			return
		}
		if a.speaking() || (a.sat != nil && a.sat.inDialog()) || (a.ic != nil && a.icActive()) || (a.music.host != nil && a.music.streaming.Load()) {
			writeErr(w, http.StatusConflict, "the speaker is playing sound; stop music, calls and speech first")
			return
		}
		a.acousticMu.Lock()
		if a.acoustic.Running {
			a.acousticMu.Unlock()
			writeErr(w, http.StatusConflict, "an acoustic measurement is already running")
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		a.acoustic = acousticCalView{Supported: true, Running: true, Stage: "Preparing voice and wake capture", LevelDBFS: in.LevelDBFS, Updated: time.Now().Unix()}
		a.acousticStop = cancel
		a.acousticMu.Unlock()
		go a.runAcousticCalibration(ctx, in.LevelDBFS)
		writeJSON(w, http.StatusAccepted, a.acousticView())
	case http.MethodDelete:
		a.acousticMu.Lock()
		if a.acousticStop != nil {
			a.acoustic.Stage = "Cancelling"
			a.acoustic.Updated = time.Now().Unix()
			a.acousticStop()
		}
		a.acousticMu.Unlock()
		writeJSON(w, http.StatusOK, a.acousticView())
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET, POST or DELETE")
	}
}

func acousticLevel(level int) bool { return level == -50 || level == -45 || level == -40 }

func (a *App) setAcoustic(stage string, captures []acousticCaptureView, err error) {
	a.acousticMu.Lock()
	defer a.acousticMu.Unlock()
	a.acoustic.Stage = stage
	a.acoustic.Captures = append([]acousticCaptureView(nil), captures...)
	a.acoustic.Error = ""
	if err != nil {
		a.acoustic.Error = err.Error()
	}
	a.acoustic.Updated = time.Now().Unix()
}

func (a *App) finishAcoustic(captures []acousticCaptureView, err error) {
	a.acousticMu.Lock()
	a.acoustic.Running = false
	a.acousticStop = nil
	a.acoustic.Captures = append([]acousticCaptureView(nil), captures...)
	a.acoustic.Stage = ""
	a.acoustic.Error = ""
	if err != nil {
		a.acoustic.Error = err.Error()
	}
	a.acoustic.Updated = time.Now().Unix()
	v := a.acoustic
	v.Captures = append([]acousticCaptureView(nil), v.Captures...)
	a.acousticMu.Unlock()
	a.saveAcousticReport(v)
}

func (a *App) runAcousticCalibration(ctx context.Context, level int) {
	captures := make([]acousticCaptureView, 0, 3)
	resume, err := a.mic.Suspend()
	if err != nil {
		a.finishAcoustic(captures, fmt.Errorf("pause voice and wake capture: %w", err))
		return
	}
	defer resume()
	restoreRoute, err := a.mixer.SetRestoring(a.prof.Mic.RouteControl, a.prof.Mic.RouteValue)
	if err != nil {
		a.finishAcoustic(captures, fmt.Errorf("select speaker reference: %w", err))
		return
	}
	defer func() {
		if restoreErr := restoreRoute(); restoreErr != nil {
			a.log.Warn("acoustic calibration: restore reference route", "err", restoreErr)
		}
	}()
	restoreVolume, err := a.mixer.SetRestoring(a.prof.Audio.Volume, acousticMasterRaw)
	if err != nil {
		a.finishAcoustic(captures, fmt.Errorf("set safe measurement volume: %w", err))
		return
	}
	defer func() {
		if restoreErr := restoreVolume(); restoreErr != nil {
			a.log.Warn("acoustic calibration: restore volume", "err", restoreErr)
		}
	}()

	sweep := acousticSweep(level)
	for i := 0; i < 3; i++ {
		a.setAcoustic(fmt.Sprintf("Capture %d of 3", i+1), captures, nil)
		capture, err := captureAcoustic(ctx, a.prof.Audio.Playback, sweep)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				a.finishAcoustic(captures, errors.New("measurement cancelled"))
			} else {
				a.finishAcoustic(captures, err)
			}
			return
		}
		view := inspectAcousticCapture(capture, level, acousticTransientSeconds)
		captures = append(captures, view)
		if !view.Accepted {
			a.finishAcoustic(captures, errors.New("a capture was invalid; keep the room still and try again"))
			return
		}
	}
	a.finishAcoustic(captures, nil)
}

func captureAcoustic(ctx context.Context, playback string, sweep []byte) ([]byte, error) {
	capture := exec.CommandContext(ctx, "arecord", "-q", "-D", acousticCaptureDevice, "-c", fmt.Sprint(acousticChannels), "-r", fmt.Sprint(acousticCaptureRate), "-f", "S16_LE", "-t", "raw", "-d", fmt.Sprint(acousticSeconds))
	out := &bytes.Buffer{}
	capture.Stdout = out
	if err := capture.Start(); err != nil {
		return nil, fmt.Errorf("start microphone capture: %w", err)
	}
	// The PDM/loopback open transient saturates the first ~1 s (docs/boards/orion.md;
	// measured: mic samples pinned at full scale until 1.01-1.05 s in), so start the
	// sweep only after it has passed, and keep the clip check away from it too
	// (acousticTransientSeconds).
	select {
	case <-ctx.Done():
		_ = capture.Process.Kill()
		_ = capture.Wait()
		return nil, ctx.Err()
	case <-time.After(time.Duration(acousticTransientSeconds)*time.Second + 500*time.Millisecond):
	}
	play := exec.CommandContext(ctx, "aplay", "-q", "-D", playback, "-c", "1", "-r", fmt.Sprint(acousticSweepRate), "-f", "S16_LE", "-t", "raw")
	play.Stdin = bytes.NewReader(sweep)
	if err := play.Run(); err != nil {
		_ = capture.Process.Kill()
		_ = capture.Wait()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("play measurement sweep: %w", err)
	}
	if err := capture.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("capture measurement sweep: %w", err)
	}
	want := acousticSeconds * acousticCaptureRate * acousticChannels * 2
	if out.Len() != want {
		return nil, fmt.Errorf("capture has %d bytes, want %d", out.Len(), want)
	}
	return out.Bytes(), nil
}

func acousticSweep(levelDBFS int) []byte {
	lead := acousticSweepRate / 4
	n := lead + acousticSweepRate*acousticSweepSeconds
	out := make([]byte, n*2)
	amplitude := 32767 * math.Pow(10, float64(levelDBFS)/20)
	fade := acousticSweepRate / 10
	k := math.Log(10000.0/100.0) / acousticSweepSeconds
	for i := 0; i < acousticSweepRate*acousticSweepSeconds; i++ {
		t := float64(i) / acousticSweepRate
		gain := 1.0
		if i < fade {
			gain = float64(i) / float64(fade)
		} else if i >= acousticSweepRate*acousticSweepSeconds-fade {
			gain = float64(acousticSweepRate*acousticSweepSeconds-i) / float64(fade)
		}
		phase := 2 * math.Pi * 100 * (math.Exp(k*t) - 1) / k
		binary.LittleEndian.PutUint16(out[2*(lead+i):], uint16(int16(math.Round(amplitude*gain*math.Sin(phase)))))
	}
	return out
}

func inspectAcousticCapture(data []byte, levelDBFS int, skipSeconds int) acousticCaptureView {
	frameBytes := acousticChannels * 2
	skip := skipSeconds * acousticCaptureRate * frameBytes
	peak, micPeak := 0, 0
	for off := skip; off+frameBytes <= len(data); off += frameBytes {
		v := abs16(int16(binary.LittleEndian.Uint16(data[off+2*acousticReference:])))
		if v > peak {
			peak = v
		}
		for _, channel := range []int{0, 2, 3, 4, 5, 6} {
			v := abs16(int16(binary.LittleEndian.Uint16(data[off+2*channel:])))
			if v > micPeak {
				micPeak = v
			}
		}
	}
	want := int(math.Round(32767 * math.Pow(10, float64(levelDBFS)/20)))
	return acousticCaptureView{ReferencePeakLSB: peak, MicClipped: micPeak >= 32700, Accepted: peak >= want*9/10 && micPeak < 32700}
}

func abs16(v int16) int {
	if v < 0 {
		return -int(v)
	}
	return int(v)
}

func (a *App) acousticReportPath() string {
	return filepath.Join(a.prof.Platform.CFWRoot, "acoustic-calibration.json")
}

func (a *App) saveAcousticReport(v acousticCalView) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	path := a.acousticReportPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		a.log.Warn("acoustic calibration: create report directory", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err == nil {
		if err := os.Rename(tmp, path); err != nil {
			a.log.Warn("acoustic calibration: save report", "err", err)
		}
	} else {
		a.log.Warn("acoustic calibration: write report", "err", err)
	}
}
