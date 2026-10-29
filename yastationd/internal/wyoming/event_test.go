package wyoming

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	in := Event{Type: TypeAudioChunk, Data: map[string]any{"rate": 22050, "width": 2, "channels": 1}, Payload: []byte{1, 2, 3, 4}}
	if err := Write(&buf, in); err != nil {
		t.Fatal(err)
	}
	if err := Write(&buf, Event{Type: TypePing}); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(&buf)
	out, err := Read(r)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != in.Type || out.Int("rate", 0) != 22050 || !bytes.Equal(out.Payload, in.Payload) {
		t.Fatalf("got %+v", out)
	}
	if ev, err := Read(r); err != nil || ev.Type != TypePing || len(ev.Data) != 0 || ev.Payload != nil {
		t.Fatalf("second event %+v, %v", ev, err)
	}
}

// Older peers put data inline in the header; newer ones send data_length bytes. Both merge.
func TestReadInlineAndSplitData(t *testing.T) {
	raw := `{"type":"audio-start","data":{"rate":16000},"data_length":14,"version":"1.10.0"}` + "\n" + `{"channels":1}`
	ev, err := Read(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Int("rate", 0) != 16000 || ev.Int("channels", 0) != 1 {
		t.Fatalf("data = %v", ev.Data)
	}
}

func TestInfoIsSatellite(t *testing.T) {
	ev := Info(SatelliteInfo{Name: "x", Version: "1"})
	sat, ok := ev.Data["satellite"].(map[string]any)
	if !ok || sat["installed"] != true || sat["name"] != "x" {
		t.Fatalf("satellite = %v", ev.Data["satellite"])
	}
}

func TestReadRejectsMalformedOrTruncatedEvents(t *testing.T) {
	tooLong := strings.Repeat("x", maxLine+1) + "\n"
	cases := []struct {
		name string
		raw  string
	}{
		{"invalid header JSON", "{not-json}\n"},
		{"oversized header", tooLong},
		{"truncated data", "{\"type\":\"x\",\"data_length\":2}\n{"},
		{"invalid split data JSON", "{\"type\":\"x\",\"data_length\":3}\n{]}"},
		{"truncated payload", "{\"type\":\"x\",\"payload_length\":2}\na"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Read(bufio.NewReader(strings.NewReader(tt.raw))); err == nil {
				t.Fatal("Read accepted malformed event")
			}
		})
	}
}

func TestReadAcceptsPayloadWithoutData(t *testing.T) {
	raw := "{\"type\":\"audio-chunk\",\"payload_length\":3}\nabc"
	ev, err := Read(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != TypeAudioChunk || string(ev.Payload) != "abc" || len(ev.Data) != 0 {
		t.Fatalf("event = %+v", ev)
	}
}
