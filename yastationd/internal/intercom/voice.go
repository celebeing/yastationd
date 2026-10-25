package intercom

import (
	"strings"
	"unicode"
)

// Voice phrases (docs/intercom.md, "The voice scenario"): the daemon reads the transcript of an
// ordinary Assist session and acts on the intercom ones itself. Matching is by word stems, so
// Russian case endings don't matter («спальня» / «со спальней»).

// Peer is a speaker that can be called.
type Peer struct {
	ID, Name string
}

var (
	callVerbs = []string{"соедин", "позвон", "звони", "свяж", "вызов", "вызв", "набер"}
)

func words(text string) []string {
	text = strings.ReplaceAll(strings.ToLower(text), "ё", "е")
	return strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// stem keeps the start of a word: all of a short one, otherwise all but its last two letters
// (case endings), never less than three.
func stem(w string) string {
	r := []rune(w)
	if len(r) <= 4 {
		return w
	}
	n := len(r) - 2
	if n < 3 {
		n = 3
	}
	return string(r[:n])
}

func hasPrefix(ws []string, prefixes []string) int {
	for i, w := range ws {
		for _, p := range prefixes {
			if strings.HasPrefix(w, p) {
				return i
			}
		}
	}
	return -1
}

// nameIn reports whether every word of name appears (by stem) among ws.
func nameIn(name string, ws []string) bool {
	nw := words(name)
	if len(nw) == 0 {
		return false
	}
	for _, n := range nw {
		s, found := stem(n), false
		for _, w := range ws {
			if strings.HasPrefix(w, s) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// MatchCall finds «соедини со спальней»-like requests: a call verb and one peer's name after it.
// With several peers matching, the longest name wins («детская 2» over «детская»).
func MatchCall(text string, peers []Peer) (Peer, bool) {
	ws := words(text)
	i := hasPrefix(ws, callVerbs)
	if i < 0 {
		return Peer{}, false
	}
	rest := ws[i+1:]
	var best Peer
	bestLen := 0
	for _, p := range peers {
		if n := len(words(p.Name)); n > bestLen && nameIn(p.Name, rest) {
			best, bestLen = p, n
		}
	}
	return best, bestLen > 0
}

// Call commands from the vendor command spotter (docs/intercom.md, "Voice control"). The model
// has no «да»/«нет», and «включи»/«выключи» count only with an object («включи свет»), so:

// IsAnswerCommand: answers a ringing call.
func IsAnswerCommand(phrase string) bool {
	switch strings.ToLower(phrase) {
	case "продолжи", "играй":
		return true
	}
	return false
}

// IsDeclineCommand: declines a ringing call or ends a running one.
func IsDeclineCommand(phrase string) bool {
	switch strings.ToLower(phrase) {
	case "стоп", "хватит", "пауза":
		return true
	}
	return false
}
