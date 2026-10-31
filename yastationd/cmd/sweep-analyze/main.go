// sweep-analyze derives reproducible acoustic measurements from Orion direct LOOPBACK-A sweeps.
// It runs on the host in the pinned Go container; it never connects to a speaker or alters audio.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"sort"
)

const schemaVersion = 1

type config struct {
	sampleRate        int
	channels          int
	referenceChannel  int
	micChannels       []int
	sweepSeconds      float64
	maxDelaySeconds   float64
	bands             []float64
	motionThresholdDB float64
}

type report struct {
	SchemaVersion        int             `json:"schema_version"`
	SampleRateHz         int             `json:"sample_rate_hz"`
	Channels             int             `json:"channels"`
	ReferenceChannel     int             `json:"reference_channel"`
	MicChannels          []int           `json:"mic_channels"`
	SweepSeconds         float64         `json:"sweep_seconds"`
	MaxDelayMilliseconds float64         `json:"max_delay_ms"`
	MotionThresholdDB    float64         `json:"motion_threshold_db"`
	Captures             []captureReport `json:"captures"`
}

type captureReport struct {
	File      string        `json:"file"`
	Reference referenceInfo `json:"reference"`
	Mics      []micReport   `json:"microphones"`
	Quality   quality       `json:"quality"`
}

type referenceInfo struct {
	StartFrame      int     `json:"start_frame"`
	StartSeconds    float64 `json:"start_seconds"`
	DurationFrames  int     `json:"duration_frames"`
	DurationSeconds float64 `json:"duration_seconds"`
	PeakLSB         int     `json:"peak_lsb"`
}

type micReport struct {
	RawChannel                   int      `json:"raw_channel"`
	CorrelationDelayMilliseconds float64  `json:"correlation_delay_ms"`
	StrongestPeakMilliseconds    float64  `json:"strongest_impulse_peak_ms"`
	PeakToTailDB                 float64  `json:"peak_to_tail_db"`
	OneThirdOctaveDB             []bandDB `json:"one_third_octave_db"`
}

type bandDB struct {
	CenterHz float64 `json:"center_hz"`
	DB       float64 `json:"db"`
}

type quality struct {
	Accepted            bool     `json:"accepted"`
	HighBandDeviationDB float64  `json:"high_band_deviation_db,omitempty"`
	Reasons             []string `json:"reasons,omitempty"`
}

func main() {
	var out, micCSV string
	cfg := defaultConfig()
	flag.StringVar(&out, "out", "", "output JSON path (required)")
	flag.IntVar(&cfg.sampleRate, "rate", cfg.sampleRate, "capture sample rate")
	flag.IntVar(&cfg.channels, "channels", cfg.channels, "interleaved capture channels")
	flag.IntVar(&cfg.referenceChannel, "reference-channel", cfg.referenceChannel, "digital reference channel")
	flag.StringVar(&micCSV, "mic-channels", "0,2,3,4,5,6", "comma-separated raw mic channels")
	flag.Float64Var(&cfg.sweepSeconds, "sweep-seconds", cfg.sweepSeconds, "sweep duration")
	flag.Float64Var(&cfg.maxDelaySeconds, "max-delay-seconds", cfg.maxDelaySeconds, "impulse-response search window")
	flag.Float64Var(&cfg.motionThresholdDB, "motion-threshold-db", cfg.motionThresholdDB, "high-band outlier threshold")
	flag.Parse()
	if out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: sweep-analyze -out <report.json> [options] <capture.raw>...")
		os.Exit(2)
	}
	var err error
	if cfg.micChannels, err = parseChannels(micCSV, cfg.channels); err != nil {
		fatal(err)
	}
	r, err := analyzeFiles(cfg, flag.Args())
	if err != nil {
		fatal(err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fatal(err)
	}
	if err := writeAtomic(out, append(data, '\n')); err != nil {
		fatal(err)
	}
	for _, capture := range r.Captures {
		status := "accepted"
		if !capture.Quality.Accepted {
			status = "rejected"
		}
		fmt.Printf("%s: %s", capture.File, status)
		if capture.Quality.HighBandDeviationDB != 0 {
			fmt.Printf(" (high-band deviation %.2f dB)", capture.Quality.HighBandDeviationDB)
		}
		fmt.Println()
	}
}

func defaultConfig() config {
	return config{
		sampleRate: 96000, channels: 14, referenceChannel: 8,
		micChannels:  []int{0, 2, 3, 4, 5, 6},
		sweepSeconds: 4, maxDelaySeconds: .06,
		bands:             []float64{125, 160, 200, 250, 315, 400, 500, 630, 800, 1000, 1250, 1600, 2000, 2500, 3150, 4000, 5000, 6300, 8000},
		motionThresholdDB: 1.5,
	}
}

func analyzeFiles(cfg config, paths []string) (report, error) {
	if cfg.sampleRate <= 0 || cfg.channels <= 0 || cfg.sweepSeconds <= 0 || cfg.maxDelaySeconds <= 0 {
		return report{}, errors.New("rate, channels, sweep duration and maximum delay must be positive")
	}
	if cfg.referenceChannel < 0 || cfg.referenceChannel >= cfg.channels {
		return report{}, fmt.Errorf("reference channel %d outside 0..%d", cfg.referenceChannel, cfg.channels-1)
	}
	r := report{SchemaVersion: schemaVersion, SampleRateHz: cfg.sampleRate, Channels: cfg.channels, ReferenceChannel: cfg.referenceChannel, MicChannels: cfg.micChannels, SweepSeconds: cfg.sweepSeconds, MaxDelayMilliseconds: cfg.maxDelaySeconds * 1000, MotionThresholdDB: cfg.motionThresholdDB}
	for _, path := range paths {
		capture, err := analyzeCapture(cfg, path)
		if err != nil {
			return report{}, err
		}
		r.Captures = append(r.Captures, capture)
	}
	classifyMotion(&r, cfg)
	return r, nil
}

func analyzeCapture(cfg config, path string) (captureReport, error) {
	frames, err := readFrames(path, cfg.channels)
	if err != nil {
		return captureReport{}, err
	}
	start, err := referenceStart(frames, cfg.referenceChannel)
	if err != nil {
		return captureReport{}, fmt.Errorf("%s: %w", path, err)
	}
	sweepFrames := int(math.Round(cfg.sweepSeconds * float64(cfg.sampleRate)))
	if start+sweepFrames > len(frames) {
		return captureReport{}, fmt.Errorf("%s: reference starts at frame %d but %d sweep frames do not fit", path, start, sweepFrames)
	}
	ref := channelSegment(frames, cfg.referenceChannel, start, sweepFrames)
	peak := maxAbsInt16(ref)
	if peak == 0 {
		return captureReport{}, fmt.Errorf("%s: reference sweep is silent", path)
	}
	x := centeredComplex(ref)
	tailFrames := int(math.Round(cfg.maxDelaySeconds * float64(cfg.sampleRate)))
	n := nextPowerOfTwo(sweepFrames + tailFrames)
	X := make([]complex128, n)
	copy(X, x)
	fft(X, false)
	power := make([]float64, len(X))
	for i, v := range X {
		power[i] = cmplx.Abs(v) * cmplx.Abs(v)
	}
	regularization := percentile(power[1:], .05) * .01
	if regularization == 0 {
		regularization = 1
	}
	result := captureReport{
		File:      filepath.Base(path),
		Reference: referenceInfo{StartFrame: start, StartSeconds: float64(start) / float64(cfg.sampleRate), DurationFrames: sweepFrames, DurationSeconds: float64(sweepFrames) / float64(cfg.sampleRate), PeakLSB: peak},
		Quality:   quality{Accepted: true},
	}
	for _, channel := range cfg.micChannels {
		if channel < 0 || channel >= cfg.channels {
			return captureReport{}, fmt.Errorf("%s: mic channel %d outside 0..%d", path, channel, cfg.channels-1)
		}
		yRaw := channelSegment(frames, channel, start, sweepFrames+tailFrames)
		y := centeredComplex(yRaw)
		Y := make([]complex128, n)
		copy(Y, y)
		fft(Y, false)
		H := make([]complex128, n)
		for i := range H {
			H[i] = Y[i] * cmplx.Conj(X[i]) / complex(power[i]+regularization, 0)
		}
		impulse := append([]complex128(nil), H...)
		fft(impulse, true)
		strongest, peakToTail := impulseTiming(impulse, cfg.sampleRate, cfg.maxDelaySeconds)
		delay := correlationDelay(x, y, cfg.sampleRate, cfg.maxDelaySeconds)
		result.Mics = append(result.Mics, micReport{
			RawChannel: channel, CorrelationDelayMilliseconds: ms(delay, cfg.sampleRate), StrongestPeakMilliseconds: ms(strongest, cfg.sampleRate), PeakToTailDB: peakToTail,
			OneThirdOctaveDB: bandResponse(H, cfg.sampleRate, cfg.bands),
		})
	}
	return result, nil
}

func readFrames(path string, channels int) ([][]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	frameBytes := channels * 2
	if len(data) == 0 || len(data)%frameBytes != 0 {
		return nil, fmt.Errorf("%s: %d bytes is not whole %d-channel S16_LE frames", path, len(data), channels)
	}
	frames := make([][]int16, len(data)/frameBytes)
	for i := range frames {
		frames[i] = make([]int16, channels)
		for c := 0; c < channels; c++ {
			frames[i][c] = int16(binary.LittleEndian.Uint16(data[(i*channels+c)*2:]))
		}
	}
	return frames, nil
}

func referenceStart(frames [][]int16, channel int) (int, error) {
	for i, frame := range frames {
		if frame[channel] != 0 {
			return i, nil
		}
	}
	return 0, errors.New("digital reference contains no non-zero samples")
}

func channelSegment(frames [][]int16, channel, start, count int) []int16 {
	out := make([]int16, count)
	for i := range out {
		if start+i < len(frames) {
			out[i] = frames[start+i][channel]
		}
	}
	return out
}

func centeredComplex(samples []int16) []complex128 {
	mean := 0.0
	for _, sample := range samples {
		mean += float64(sample)
	}
	mean /= float64(len(samples))
	out := make([]complex128, len(samples))
	for i, sample := range samples {
		out[i] = complex(float64(sample)-mean, 0)
	}
	return out
}

func maxAbsInt16(samples []int16) int {
	maximum := 0
	for _, sample := range samples {
		value := int(sample)
		if value < 0 {
			value = -value
		}
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}
func impulseTiming(impulse []complex128, rate int, maxDelay float64) (int, float64) {
	limit := min(len(impulse), int(math.Round(maxDelay*float64(rate))))
	strongest, strongestAbs := 0, 0.0
	for i := range limit {
		if value := cmplx.Abs(impulse[i]); value > strongestAbs {
			strongest, strongestAbs = i, value
		}
	}
	noiseStart, noiseEnd := min(limit, int(.025*float64(rate))), min(limit, int(.055*float64(rate)))
	if noiseEnd <= noiseStart {
		noiseStart, noiseEnd = limit/2, limit
	}
	noise := 0.0
	for i := noiseStart; i < noiseEnd; i++ {
		value := cmplx.Abs(impulse[i])
		noise += value * value
	}
	noise = math.Sqrt(noise / float64(max(1, noiseEnd-noiseStart)))
	return strongest, 20 * math.Log10(strongestAbs/(noise+1e-15))
}

// correlationDelay returns the strongest non-negative sweep-reference correlation lag. It is a
// stable timing metric; strongestImpulsePeakMilliseconds separately describes room reflections.
func correlationDelay(reference, microphone []complex128, rate int, maxDelay float64) int {
	n := nextPowerOfTwo(len(reference) + len(microphone) - 1)
	reversed := make([]complex128, n)
	for i := range reference {
		reversed[i] = reference[len(reference)-1-i]
	}
	signal := make([]complex128, n)
	copy(signal, microphone)
	fft(reversed, false)
	fft(signal, false)
	for i := range signal {
		signal[i] *= reversed[i]
	}
	fft(signal, true)
	offset := len(reference) - 1
	limit := min(int(math.Round(maxDelay*float64(rate))), len(signal)-offset)
	best, bestValue := 0, 0.0
	for lag := range limit {
		if value := cmplx.Abs(signal[offset+lag]); value > bestValue {
			best, bestValue = lag, value
		}
	}
	return best
}

func bandResponse(h []complex128, rate int, bands []float64) []bandDB {
	result := make([]bandDB, 0, len(bands))
	for _, center := range bands {
		low, high := center/math.Pow(2, 1.0/6), center*math.Pow(2, 1.0/6)
		values := make([]float64, 0)
		for k := 1; k < len(h)/2; k++ {
			freq := float64(k*rate) / float64(len(h))
			if freq >= low && freq <= high {
				values = append(values, 20*math.Log10(maxFloat(cmplx.Abs(h[k]), 1e-15)))
			}
		}
		result = append(result, bandDB{CenterHz: center, DB: round(median(values), 3)})
	}
	return result
}

func classifyMotion(r *report, cfg config) {
	if len(r.Captures) < 3 {
		return
	}
	features := make([][]float64, len(r.Captures))
	for i, capture := range r.Captures {
		for _, mic := range capture.Mics {
			for _, band := range mic.OneThirdOctaveDB {
				if band.CenterHz >= 1000 {
					features[i] = append(features[i], band.DB)
				}
			}
		}
	}
	if len(features[0]) == 0 {
		return
	}
	baseline := make([]float64, len(features[0]))
	for j := range baseline {
		values := make([]float64, len(features))
		for i := range features {
			values[i] = features[i][j]
		}
		baseline[j] = median(values)
	}
	for i := range r.Captures {
		deviation := make([]float64, len(baseline))
		for j := range deviation {
			deviation[j] = math.Abs(features[i][j] - baseline[j])
		}
		score := median(deviation)
		r.Captures[i].Quality.HighBandDeviationDB = round(score, 3)
		if score > cfg.motionThresholdDB {
			r.Captures[i].Quality.Accepted = false
			r.Captures[i].Quality.Reasons = append(r.Captures[i].Quality.Reasons, fmt.Sprintf("high-band response differs from repeated-capture median by %.3f dB (limit %.3f dB)", score, cfg.motionThresholdDB))
		}
	}
}

func parseChannels(text string, channels int) ([]int, error) {
	var values []int
	for _, token := range splitComma(text) {
		var value int
		if _, err := fmt.Sscanf(token, "%d", &value); err != nil {
			return nil, fmt.Errorf("invalid mic channel %q", token)
		}
		if value < 0 || value >= channels {
			return nil, fmt.Errorf("mic channel %d outside 0..%d", value, channels-1)
		}
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, errors.New("at least one mic channel is required")
	}
	return values, nil
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if start < i {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
func nextPowerOfTwo(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func ms(frame, rate int) float64          { return round(1000*float64(frame)/float64(rate), 3) }
func round(v float64, digits int) float64 { p := math.Pow10(digits); return math.Round(v*p) / p }
func median(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
func percentile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "sweep-analyze:", err); os.Exit(1) }

// fft is an iterative radix-2 Cooley-Tukey transform. inverse normalizes its output.
func fft(a []complex128, inverse bool) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		angle := 2 * math.Pi / float64(length)
		if inverse {
			angle = -angle
		}
		wlen := cmplx.Exp(complex(0, angle))
		half := length / 2
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for j := 0; j < half; j++ {
				u, v := a[i+j], a[i+j+half]*w
				a[i+j] = u + v
				a[i+j+half] = u - v
				w *= wlen
			}
		}
	}
	if inverse {
		for i := range a {
			a[i] /= complex(float64(n), 0)
		}
	}
}
