package intercom

import "strings"

// Peer is a speaker that can be called.
type Peer struct {
	ID, Name string
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
