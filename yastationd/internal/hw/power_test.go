package hw

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPD(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "/sys/class/power_supply/ya_pd")
	os.MkdirAll(d, 0o755)
	for f, v := range map[string]string{"voltage_now": "12000\n", "current_now": "2000\n", "health": "Good\n"} {
		os.WriteFile(filepath.Join(d, f), []byte(v), 0o644)
	}
	if FindPD(root, "nope") != "" || FindPD(root, "") != "" {
		t.Fatal("found a missing PD sink")
	}
	pd, err := ReadPD(FindPD(root, "ya_pd"))
	if err != nil || pd != (PD{MilliVolts: 12000, MilliAmps: 2000, Health: "Good"}) {
		t.Fatalf("ReadPD = %+v, %v", pd, err)
	}
}
