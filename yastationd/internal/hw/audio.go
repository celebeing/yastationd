package hw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Mixer shells out to amixer; linking the device's armhf libasound would undo the static build.
type Mixer struct {
	Card string
	run  func(args ...string) (string, error)
}

func NewMixer(card string) *Mixer {
	return &Mixer{Card: card, run: func(args ...string) (string, error) {
		out, err := exec.Command("amixer", args...).CombinedOutput()
		return string(out), err
	}}
}

var valuesRe = regexp.MustCompile(`: values=(-?\d+)`)

// MasterControl is the card-0 playback volume. cget/cset need the full control name; the short
// "Master" only resolves for sget/sset simple controls.
const MasterControl = "Master Playback Volume"

// Get reads the first value of a control by its full name, e.g. Get(MasterControl).
func (m *Mixer) Get(control string) (int, error) {
	out, err := m.run("-c", m.Card, "cget", "name="+control)
	if err != nil {
		return 0, fmt.Errorf("amixer cget %s: %w: %s", control, err, strings.TrimSpace(out))
	}
	mm := valuesRe.FindStringSubmatch(out)
	if mm == nil {
		return 0, fmt.Errorf("amixer cget %s: no values in %q", control, out)
	}
	return strconv.Atoi(mm[1])
}

// Set writes a control by its full name. Arguments go straight to amixer (not through
// splitQuoted), so names with spaces need no quoting.
func (m *Mixer) Set(control string, v int) error {
	out, err := m.run("-c", m.Card, "cset", "name="+control, strconv.Itoa(v))
	if err != nil {
		return fmt.Errorf("amixer cset %s: %w: %s", control, err, strings.TrimSpace(out))
	}
	return nil
}

// CSet runs one `amixer cset` line from config, e.g. "name='SPK mute' 0".
func (m *Mixer) CSet(line string) error {
	args := append([]string{"-c", m.Card, "cset"}, splitQuoted(line)...)
	out, err := m.run(args...)
	if err != nil {
		return fmt.Errorf("amixer cset %s: %w: %s", line, err, strings.TrimSpace(out))
	}
	return nil
}

// splitQuoted splits on spaces outside single or double quotes and drops the quotes.
func splitQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	any := false
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '\'' || r == '"'):
			quote, any = r, true
		case quote == 0 && r == ' ':
			if cur.Len() > 0 || any {
				out = append(out, cur.String())
				cur.Reset()
				any = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 || any {
		out = append(out, cur.String())
	}
	return out
}

// Player plays one file at a time through aplay; a new Play interrupts the current one.
type Player struct {
	Device  string
	OnState func(playing bool)
	// OnExit, if set, gets each aplay's exit error and stderr (diagnostics; nil err = clean exit).
	OnExit func(err error, stderr string)

	mu     sync.Mutex
	cancel context.CancelFunc
	gen    int
	blip   context.CancelFunc
}

// Blip plays a short sound beside whatever is playing (dmix mixes them), without touching the
// playback state: no OnState, and Play/Stream/Stop don't cut it. A new Blip cuts the previous one,
// so a burst of key presses gives one tick each instead of a pile-up.
func (p *Player) Blip(path string) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	if p.blip != nil {
		p.blip()
	}
	p.blip = cancel
	p.mu.Unlock()
	cmd := aplayCmd(ctx, "-q", "-D", p.Device, path)
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	go func() { cmd.Wait(); cancel() }()
	return nil
}

// StopBlip cuts the current blip, if any.
func (p *Player) StopBlip() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blip != nil {
		p.blip()
		p.blip = nil
	}
}

func (p *Player) Play(path string) error {
	_, _, err := p.Start(path)
	return err
}

// ErrCut is what Start's done channel carries when the playback was cut short: by Stop, by its
// own stop func, or by a later Play/Stream taking the speaker.
var ErrCut = errors.New("playback cut")

// Start is Play that reports the outcome: done gets one value when this aplay exits — nil at the
// end of the file, ErrCut when it was cut short, else aplay's failure (no device, a bad file).
// stop cuts this playback only: once a later Play/Stream has taken over it does nothing, so a
// caller can never silence someone else's sound with it.
func (p *Player) Start(path string) (done <-chan error, stop func(), err error) {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.gen++
	gen := p.gen
	p.mu.Unlock()

	cmd := aplayCmd(ctx, "-q", "-D", p.Device, path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}
	p.state(true)
	ch := make(chan error, 1)
	go func() {
		werr := cmd.Wait()
		if ctx.Err() != nil {
			werr = ErrCut
		} else if werr != nil {
			werr = fmt.Errorf("%w: %s", werr, strings.TrimSpace(stderr.String()))
		}
		p.mu.Lock()
		last := gen == p.gen
		if last {
			p.cancel = nil
		}
		p.mu.Unlock()
		cancel()
		if last {
			p.state(false)
		}
		ch <- werr
	}()
	return ch, cancel, nil
}

// Stream starts aplay on raw s16le PCM fed through the returned writer; like Play, it interrupts
// whatever is playing. Close the writer at the end of the audio, then wait on done for aplay to
// drain and exit (done also closes if a later Play/Stream/Stop cuts this one short).
func (p *Player) Stream(rate, channels int) (w io.WriteCloser, done <-chan struct{}, err error) {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.gen++
	gen := p.gen
	p.mu.Unlock()

	cmd := aplayCmd(ctx, "-q", "-D", p.Device, "-t", "raw", "-f", "S16_LE",
		"-r", strconv.Itoa(rate), "-c", strconv.Itoa(channels))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}
	p.state(true)
	ch := make(chan struct{})
	go func() {
		werr := cmd.Wait()
		if p.OnExit != nil {
			p.OnExit(werr, stderr.String())
		}
		p.mu.Lock()
		last := gen == p.gen
		if last {
			p.cancel = nil
		}
		p.mu.Unlock()
		cancel()
		if last {
			p.state(false)
		}
		close(ch)
	}()
	return stdin, ch, nil
}

// aplayCmd builds an aplay that is interrupted with SIGTERM, never a bare SIGKILL: playback
// goes through dmix (SysV shm + semaphore, ipc_key 1024), and an aplay killed mid-write leaves
// the semaphore held, after which every later dmix client hangs in PREPARED — the speaker goes
// mute until the IPC objects are removed (seen on-device after a «стоп»). aplay's SIGTERM handler
// closes the PCM cleanly; SIGKILL only if it hasn't exited 2 s later.
func aplayCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "aplay", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *Player) state(playing bool) {
	if p.OnState != nil {
		p.OnState(playing)
	}
}
