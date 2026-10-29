package app

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Sounds by name (docs/asset-packs.md): a moment's step plays a sound named, not a file. A name
// resolves through soundDirs, the first that has <name>.wav; none = silent. The stock sounds are
// decoded into <cfw>/sounds by `make sounds`; packs add their own directories in front.

// soundName is what a sound may be called: it is a file name in every sound directory.
var soundName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// soundDirs are the directories a sound name is looked up in, first match wins: the theme
// stack's packs, then the decoded stock sounds.
func (a *App) soundDirs() []string {
	return append(a.packSoundDirs(), a.cfwFile("sounds"))
}

// soundPath is the WAV a sound name plays; "" when no directory has it. A name may list
// fallbacks, "calls_ringing_incoming|notification_sound": the first one found plays.
func (a *App) soundPath(name string) string {
	for _, n := range strings.Split(name, "|") {
		if !soundName.MatchString(n) {
			continue
		}
		for _, d := range a.soundDirs() {
			p := filepath.Join(d, n+".wav")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// soundLen is how long a sound name plays (0: none, or not a readable WAV); of a random pick
// ("a,b"), the longest.
func (a *App) soundLen(name string) time.Duration {
	if alts := strings.Split(name, ","); len(alts) > 1 {
		var d time.Duration
		for _, n := range alts {
			d = max(d, a.soundLen(strings.TrimSpace(n)))
		}
		return d
	}
	p := a.soundPath(name)
	if p == "" {
		return 0
	}
	d, err := wavDuration(p)
	if err != nil {
		return 0
	}
	return d
}

// soundNames lists every sound name this speaker can play, sorted.
func (a *App) soundNames() []string {
	seen := map[string]bool{}
	for _, d := range a.soundDirs() {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if n, ok := strings.CutSuffix(e.Name(), ".wav"); ok && soundName.MatchString(n) {
				seen[n] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// playSound plays a sound name on channel beside everything else (a newer sound on the same
// channel cuts it); phrase plays it as speech instead, stopping other playback first. False when
// the name has no sound here.
func (a *App) playSound(channel, name string, phrase bool) bool {
	p := a.soundPath(name)
	if p == "" || a.player == nil {
		return false
	}
	if phrase {
		a.announce(p)
		return true
	}
	a.unmute()
	if err := a.player.BlipOn(channel, p); err != nil {
		a.log.Warn("sound", "name", name, "err", err)
		return false
	}
	return true
}
