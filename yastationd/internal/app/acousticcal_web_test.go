package app

import (
	"os"
	"testing"
)

// The gates that keep the measurement opt-in and bounded: unsupported boards get 404, the level
// selector accepts only the tested ceiling or quieter, and one run at a time.
func TestWebAcousticGates(t *testing.T) {
	a, srv := newWebApp(t)
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)

	if code, _ := call(t, "GET", srv.URL+"/api/calibrate/acoustic", tok, ""); code != 404 {
		t.Fatalf("non-orion board offered the measurement: %d", code)
	}
	a.prof.Name = "orion"
	code, out := call(t, "GET", srv.URL+"/api/calibrate/acoustic", tok, "")
	if code != 200 || out["supported"] != true || out["running"] != false {
		t.Fatalf("orion state: %d %v", code, out)
	}
	for _, bad := range []string{`{"level_dbfs": -39}`, `{"level_dbfs": 0}`, `{"level_dbfs": -100}`, `{}`, `not json`} {
		if code, _ = call(t, "POST", srv.URL+"/api/calibrate/acoustic", tok, bad); code != 400 {
			t.Fatalf("unsafe level %s accepted: %d", bad, code)
		}
	}
	// A cancelled run must cancel (not 404 the DELETE) while active; simulate the running state.
	a.acousticMu.Lock()
	a.acoustic = acousticCalView{Supported: true, Running: true}
	a.acousticStop = func() {}
	a.acousticMu.Unlock()
	if code, _ = call(t, "POST", srv.URL+"/api/calibrate/acoustic", tok, `{"level_dbfs": -40}`); code != 409 {
		t.Fatalf("second run accepted: %d", code)
	}
	if code, out = call(t, "DELETE", srv.URL+"/api/calibrate/acoustic", tok, ""); code != 200 {
		t.Fatalf("cancel: %d", code)
	}
}

// The report file lands under <cfw> only when a run completes (here: forced through the state).
func TestAcousticReportFile(t *testing.T) {
	a, _ := newWebApp(t)
	a.prof.Platform.CFWRoot = t.TempDir()
	v := acousticCalView{Supported: true}
	v.Captures = append(v.Captures, acousticCaptureView{ReferencePeakLSB: 328, Accepted: true})
	a.saveAcousticReport(v)
	data, err := os.ReadFile(a.acousticReportPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("report not written atomically with a trailing newline: %q", data)
	}
}
