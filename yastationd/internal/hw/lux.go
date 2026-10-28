package hw

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FindLux locates the ltr308 ambient-light IIO channel under root. It prefers a processed *_input
// over *_raw. The bergamot ltr308als01 driver exposes in_intensity_* (input = raw*scale); the
// standard in_illuminance_* names are tried first in case a driver uses them. Returns "" if none.
func FindLux(root, prefix string) string {
	if prefix == "" {
		return ""
	}
	devs, _ := filepath.Glob(root + "/sys/bus/iio/devices/iio:device*")
	for _, d := range devs {
		name, _ := os.ReadFile(filepath.Join(d, "name"))
		if !strings.HasPrefix(strings.TrimSpace(string(name)), prefix) {
			continue
		}
		for _, f := range []string{"in_illuminance_input", "in_intensity_input", "in_illuminance_raw", "in_intensity_raw"} {
			p := filepath.Join(d, f)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// ReadLux reads one value of an IIO light sensor file (its raw scale; see package luxcal).
func ReadLux(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}

// ConfigureLux sets the hardware gain and integration time next to the channel FindLux returned
// ("" = leave the driver's value). Attribute names follow the channel's (in_intensity_* etc.).
func ConfigureLux(path, gain, integration string) error {
	dir, base := filepath.Dir(path), filepath.Base(path)
	ch := base[:strings.LastIndex(base, "_")] // in_intensity_input -> in_intensity
	for attr, v := range map[string]string{"_hardwaregain": gain, "_integration_time": integration} {
		if v == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, ch+attr), []byte(v), 0o644); err != nil {
			return err
		}
	}
	return nil
}
