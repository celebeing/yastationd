// Package wyoming speaks the Wyoming protocol (rhasspy/wyoming) as a satellite server: Home
// Assistant's Wyoming integration connects to us over TCP and streams events both ways.
package wyoming

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Version is the protocol version stamped on outgoing events (matches HA 2026.9's wyoming 1.10).
const Version = "1.10.0"

// maxLine bounds the JSON header line; real headers are well under 1 KiB.
const maxLine = 64 << 10

// Event is one Wyoming message: a type, a JSON data object and an optional binary payload.
type Event struct {
	Type    string
	Data    map[string]any
	Payload []byte
}

type header struct {
	Type          string         `json:"type"`
	Data          map[string]any `json:"data,omitempty"`
	DataLength    int            `json:"data_length,omitempty"`
	PayloadLength int            `json:"payload_length,omitempty"`
	Version       string         `json:"version,omitempty"`
}

// Read reads one event. Both encodings are accepted: data inline in the header line (older
// peers) and data as data_length bytes after it (current peers); the two are merged.
func Read(r *bufio.Reader) (Event, error) {
	line, err := readLine(r)
	if err != nil {
		return Event{}, err
	}
	var h header
	if err := json.Unmarshal(line, &h); err != nil {
		return Event{}, fmt.Errorf("wyoming header: %w", err)
	}
	ev := Event{Type: h.Type, Data: h.Data}
	if ev.Data == nil {
		ev.Data = map[string]any{}
	}
	if h.DataLength > 0 {
		b := make([]byte, h.DataLength)
		if _, err := io.ReadFull(r, b); err != nil {
			return Event{}, fmt.Errorf("wyoming data: %w", err)
		}
		var extra map[string]any
		if err := json.Unmarshal(b, &extra); err != nil {
			return Event{}, fmt.Errorf("wyoming data: %w", err)
		}
		for k, v := range extra {
			ev.Data[k] = v
		}
	}
	if h.PayloadLength > 0 {
		ev.Payload = make([]byte, h.PayloadLength)
		if _, err := io.ReadFull(r, ev.Payload); err != nil {
			return Event{}, fmt.Errorf("wyoming payload: %w", err)
		}
	}
	return ev, nil
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > maxLine {
			return nil, fmt.Errorf("wyoming header longer than %d bytes", maxLine)
		}
		if !isPrefix {
			return line, nil
		}
	}
}

// Write encodes ev the way the Python library does: header line, then data bytes, then payload.
func Write(w io.Writer, ev Event) error {
	h := header{Type: ev.Type, Version: Version, PayloadLength: len(ev.Payload)}
	var data []byte
	if len(ev.Data) > 0 {
		var err error
		if data, err = json.Marshal(ev.Data); err != nil {
			return err
		}
		h.DataLength = len(data)
	}
	line, err := json.Marshal(h)
	if err != nil {
		return err
	}
	buf := make([]byte, 0, len(line)+1+len(data)+len(ev.Payload))
	buf = append(append(append(append(buf, line...), '\n'), data...), ev.Payload...)
	_, err = w.Write(buf)
	return err
}

// Int reads a numeric data field (JSON numbers decode as float64), or def if absent.
func (ev Event) Int(key string, def int) int {
	if v, ok := ev.Data[key].(float64); ok {
		return int(v)
	}
	return def
}

// String reads a string data field, or "" if absent.
func (ev Event) String(key string) string {
	s, _ := ev.Data[key].(string)
	return s
}
