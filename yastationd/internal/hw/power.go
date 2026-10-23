package hw

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PD is the USB-C Power Delivery contract as the vendor sink driver reports it under
// /sys/class/power_supply/<name> (ya_pd: a HUSB238 on both bergamot and pickle). A healthy
// contract reads 12000 mV / 2000 mA, health "Good"; without one (a non-PD charger, an A-to-C or
// charge-only cable) voltage_now reads 0 and health "Dead" — the speaker then runs on bare 5 V.
type PD struct {
	MilliVolts int
	MilliAmps  int
	Health     string
}

// FindPD returns the power_supply directory of the named PD sink under root, or "".
func FindPD(root, name string) string {
	if name == "" {
		return ""
	}
	d := filepath.Join(root, "/sys/class/power_supply", name)
	if _, err := os.Stat(filepath.Join(d, "voltage_now")); err != nil {
		return ""
	}
	return d
}

// ReadPD reads the contract. Each read makes the driver re-query the controller (it logs to
// dmesg), so poll it sparingly.
func ReadPD(dir string) (PD, error) {
	read := func(f string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, f))
		return strings.TrimSpace(string(b)), err
	}
	var pd PD
	v, err := read("voltage_now")
	if err != nil {
		return pd, err
	}
	if pd.MilliVolts, err = strconv.Atoi(v); err != nil {
		return pd, err
	}
	if c, err := read("current_now"); err == nil {
		pd.MilliAmps, _ = strconv.Atoi(c)
	}
	pd.Health, _ = read("health")
	return pd, nil
}
