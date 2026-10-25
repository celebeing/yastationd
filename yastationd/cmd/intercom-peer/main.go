// Command intercom-peer is a stand-in for a second speaker when testing the intercom with one
// (docs/intercom.md, "Testing with one speaker"): it joins the same MQTT broker as a peer, calls
// or answers, records what the speaker sends, and sends it a file, a tone, an echo of itself or
// silence. Runs on any host on the LAN (static Go; on WSL2 in NAT mode build it for Windows, so
// it binds on the LAN side).
//
//	intercom-peer -config peer.conf [-call <device id>] [-record rx.wav] [-play tx.wav | -tone 440 | -echo 300ms]
//
// peer.conf is in yastationd.conf format: mqtt_host/port/user/pass, topic_prefix, device_id (the
// peer's own id, e.g. "testpeer"), intercom_name, intercom_secret (the speakers'), intercom_port.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/intercom"
	"github.com/celebeing/yastationd/yastationd/internal/mqtt"
)

func main() {
	confPath := flag.String("config", "peer.conf", "yastationd-format config with the broker and the intercom secret")
	callTo := flag.String("call", "", "call this speaker (device id) instead of waiting to be called")
	record := flag.String("record", "", "write what the speaker sends to this WAV (16 kHz mono)")
	play := flag.String("play", "", "send this WAV (16 kHz mono 16-bit), looped")
	tone := flag.Float64("tone", 0, "send a sine of this frequency (Hz)")
	toneDB := flag.Float64("tone-dbfs", -20, "level of -tone")
	toneOn := flag.Duration("tone-on", 0, "with -tone: on for this long, then off as long (0 = steady)")
	echo := flag.Duration("echo", 0, "send back what is received, this much later")
	secs := flag.Duration("for", 0, "hang up after this long in the call (0 = until Ctrl-C)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load(*confPath)
	if err != nil {
		fatal(log, "config", err)
	}
	if cfg.IntercomSecret == "" || cfg.MQTTHost == "" {
		fatal(log, "config", errors.New("needs mqtt_host and intercom_secret"))
	}
	name := cfg.IntercomName
	if name == "" {
		name = cfg.DeviceID
	}
	var src func(k int, rx []int16) []int16
	switch {
	case *play != "":
		pcm, err := readWAV(*play)
		if err != nil {
			fatal(log, "play", err)
		}
		src = func(k int, _ []int16) []int16 {
			f := make([]int16, intercom.FrameSamples)
			for i := range f {
				f[i] = pcm[(k*intercom.FrameSamples+i)%len(pcm)]
			}
			return f
		}
	case *tone > 0:
		amp := math.Pow(10, *toneDB/20) * 32767
		period := int(*toneOn / intercom.FrameDur)
		src = func(k int, _ []int16) []int16 {
			f := make([]int16, intercom.FrameSamples)
			if period > 0 && (k/period)%2 == 1 {
				return f
			}
			for i := range f {
				n := k*intercom.FrameSamples + i
				f[i] = int16(amp * math.Sin(2*math.Pi**tone*float64(n)/intercom.Rate))
			}
			return f
		}
	case *echo > 0:
		delay := int(*echo / intercom.FrameDur)
		var q [][]int16
		src = func(_ int, rx []int16) []int16 {
			q = append(q, rx)
			if len(q) <= delay {
				return make([]int16, intercom.FrameSamples)
			}
			f := q[0]
			q = q[1:]
			return f
		}
	default:
		src = func(int, []int16) []int16 { return make([]int16, intercom.FrameSamples) }
	}

	topics := mqtt.NewTopics(cfg.TopicPrefix, cfg.DiscoveryPrefix, cfg.DeviceID)
	var client *mqtt.Client
	ended := make(chan string, 1)
	m := intercom.NewManager(intercom.Options{
		ID: cfg.DeviceID, Name: name, Secret: cfg.IntercomSecret, Port: cfg.IntercomPort,
		JitterMs: cfg.IntercomJitterMs, RingTimeout: cfg.IntercomRing, AutoAnswer: true,
		LocalIP: func() string { return localIP(cfg.MQTTHost, cfg.MQTTPort) },
		Send:    func(to string, b []byte) { client.PublishEvent(topics.IntercomSig(to), string(b)) },
		OnChange: func(s intercom.Status) {
			log.Info("state", "state", s.State, "peer", s.Peer, "name", s.PeerName, "ended", s.Ended)
			if s.State == intercom.Idle && *callTo != "" {
				select {
				case ended <- s.Ended:
				default:
				}
			}
		},
		Log: log,
	})
	info, _ := json.Marshal(map[string]any{"id": cfg.DeviceID, "name": name, "port": cfg.IntercomPort, "v": 1})
	connected := make(chan struct{}, 1)
	client = mqtt.New(mqtt.Options{Host: cfg.MQTTHost, Port: cfg.MQTTPort, User: cfg.MQTTUser, Pass: cfg.MQTTPass,
		ClientID: "intercom-peer-" + cfg.DeviceID, Topics: topics, Log: log,
		OnConnect: func() {
			client.Publish(topics.IntercomInfo(), string(info))
			client.Subscribe(topics.IntercomSig(cfg.DeviceID), m.Handle)
			client.Subscribe(topics.IntercomInfoAll(), func(b []byte) {
				if len(b) > 0 {
					log.Info("speaker", "info", string(b))
				}
			})
			select {
			case connected <- struct{}{}:
			default:
			}
		}})
	client.Connect()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-connected:
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
		fatal(log, "mqtt", errors.New("no connection to the broker"))
	}
	if *callTo != "" {
		time.Sleep(time.Second) // the speaker learns our info first
		if err := m.Call(*callTo, *callTo); err != nil {
			fatal(log, "call", err)
		}
	}

	var wav *wavWriter
	if *record != "" {
		if wav, err = createWAV(*record); err != nil {
			fatal(log, "record", err)
		}
	}
	tick := time.NewTicker(intercom.FrameDur)
	defer tick.Stop()
	report := time.NewTicker(5 * time.Second)
	defer report.Stop()
	var callStart time.Time
	k := 0
	var sum float64
	var n int
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case why := <-ended:
			log.Info("call over", "why", why)
			break loop
		case <-report.C:
			st := m.Stats()
			db := -120.0
			if n > 0 && sum > 0 {
				db = 10 * math.Log10(sum/float64(n))
			}
			log.Info("stats", "rx_rms_dbfs", fmt.Sprintf("%.1f", db), "sent", st.Sent, "bad", st.Bad,
				"played", st.Jitter.Frames, "missing", st.Jitter.Missing, "late", st.Jitter.Late,
				"overflow", st.Jitter.Overflow, "drift_drop", st.Jitter.Dropped,
				"drift_insert", st.Jitter.Inserted, "rebuffers", st.Jitter.Rebuffers, "jitter_ms", st.Jitter.TargetMs)
			sum, n = 0, 0
		case <-tick.C:
			rx := m.Downlink()
			if rx == nil {
				callStart = time.Time{}
				continue
			}
			if callStart.IsZero() {
				callStart, k = time.Now(), 0
			}
			for _, v := range rx {
				x := float64(v) / 32768
				sum += x * x
			}
			n += len(rx)
			if wav != nil {
				wav.write(rx)
			}
			m.Uplink(src(k, rx))
			k++
			if *secs > 0 && time.Since(callStart) > *secs {
				m.Hangup("peer: time is up")
			}
		}
	}
	m.Hangup("peer: exiting")
	if wav != nil {
		wav.close()
	}
	client.Publish(topics.IntercomInfo(), "")
	time.Sleep(300 * time.Millisecond)
	client.Disconnect()
}

func fatal(log *slog.Logger, what string, err error) {
	log.Error(what, "err", err)
	os.Exit(1)
}

func localIP(host string, port int) string {
	c, err := net.Dial("udp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

// ---- minimal 16-bit PCM WAV ----

func readWAV(path string) ([]int16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("not a WAV file")
	}
	var rate, ch, bits int
	for p := 12; p+8 <= len(b); {
		id, size := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		body := b[p+8:]
		if size > len(body) {
			size = len(body)
		}
		switch id {
		case "fmt ":
			ch = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			if rate != intercom.Rate || ch != 1 || bits != 16 {
				return nil, fmt.Errorf("want 16 kHz mono 16-bit, got %d Hz %d ch %d bit (ffmpeg -ar 16000 -ac 1)", rate, ch, bits)
			}
			s := make([]int16, size/2)
			for i := range s {
				s[i] = int16(binary.LittleEndian.Uint16(body[2*i:]))
			}
			if len(s) == 0 {
				return nil, errors.New("empty")
			}
			return s, nil
		}
		p += 8 + size + size%2
	}
	return nil, errors.New("no data chunk")
}

type wavWriter struct {
	f *os.File
	n int
}

func createWAV(path string) (*wavWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &wavWriter{f: f}
	w.header()
	return w, nil
}

func (w *wavWriter) header() {
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+w.n))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], intercom.Rate)
	binary.LittleEndian.PutUint32(h[28:], intercom.Rate*2)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(w.n))
	w.f.WriteAt(h, 0)
}

func (w *wavWriter) write(s []int16) {
	b := make([]byte, 2*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	w.f.Seek(0, io.SeekEnd)
	w.f.Write(b)
	w.n += len(b)
}

func (w *wavWriter) close() {
	w.header()
	w.f.Close()
}
