package hw

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Runtime device-tree overlays (docs/ambilight-stream.md, "White through the framebuffer"):
// orion's kernel applies overlays written to configfs (its dtbocfg driver; the firmware loads
// its own i2c_top overlay that way). An overlay lasts until the next boot and touches nothing on
// flash. We use one to switch the ambilight framebuffer's format to one with white.

const overlayDir = "/sys/kernel/config/device-tree/overlays"

// propertyOverlay builds a flattened device tree overlay that sets one string property on the
// node at path: fragment@0 { target-path = path; __overlay__ { prop = value; }; }.
func propertyOverlay(path, prop, value string) []byte {
	var st, str bytes.Buffer
	names := map[string]uint32{}
	nameOff := func(n string) uint32 {
		if o, ok := names[n]; ok {
			return o
		}
		o := uint32(str.Len())
		str.WriteString(n)
		str.WriteByte(0)
		names[n] = o
		return o
	}
	u32 := func(v uint32) { binary.Write(&st, binary.BigEndian, v) }
	pad := func() {
		for st.Len()%4 != 0 {
			st.WriteByte(0)
		}
	}
	begin := func(name string) {
		u32(1) // FDT_BEGIN_NODE
		st.WriteString(name)
		st.WriteByte(0)
		pad()
	}
	propStr := func(name, v string) {
		u32(3) // FDT_PROP
		u32(uint32(len(v) + 1))
		u32(nameOff(name))
		st.WriteString(v)
		st.WriteByte(0)
		pad()
	}
	end := func() { u32(2) } // FDT_END_NODE

	begin("")
	begin("fragment@0")
	propStr("target-path", path)
	begin("__overlay__")
	propStr(prop, value)
	end()
	end()
	end()
	u32(9) // FDT_END

	const hdr, rsv = 40, 16
	offStruct := uint32(hdr + rsv)
	offStrings := offStruct + uint32(st.Len())
	total := offStrings + uint32(str.Len())
	var out bytes.Buffer
	for _, v := range []uint32{0xd00dfeed, total, offStruct, offStrings, hdr, 17, 16, 0,
		uint32(str.Len()), uint32(st.Len())} {
		binary.Write(&out, binary.BigEndian, v)
	}
	out.Write(make([]byte, rsv)) // an empty memory reservation map
	out.Write(st.Bytes())
	out.Write(str.Bytes())
	return out.Bytes()
}

// ensureFBCompatible makes the framebuffer named name come from a device-tree node whose
// compatible is want: when it is not, it applies an overlay (named label) setting it and rebinds
// the device to its driver, which then registers the framebuffer anew. It returns the
// framebuffer's /dev path afterwards.
func ensureFBCompatible(root, name, want, label string) (string, error) {
	sysfb := findFBSys(root, name)
	if sysfb == "" {
		return "", fmt.Errorf("no framebuffer named %q", name)
	}
	node, err := filepath.EvalSymlinks(sysfb + "/device/of_node")
	if err != nil {
		return "", fmt.Errorf("its device-tree node: %w", err)
	}
	if c, _ := os.ReadFile(node + "/compatible"); strings.TrimRight(string(c), "\x00") == want {
		return fbDev(root, sysfb), nil // already (an earlier start of ours, this boot)
	}
	dtPath := strings.TrimPrefix(node, filepath.Join(root, "/sys/firmware/devicetree/base")) // "/ambilight-framebuffer"
	dev, err := filepath.EvalSymlinks(sysfb + "/device")
	if err != nil {
		return "", err
	}
	drv, err := filepath.EvalSymlinks(sysfb + "/device/driver")
	if err != nil {
		return "", fmt.Errorf("its driver: %w", err)
	}
	devName := filepath.Base(dev)
	if err := os.WriteFile(drv+"/unbind", []byte(devName), 0o200); err != nil {
		return "", fmt.Errorf("unbind: %w", err)
	}
	apply := func() error {
		dir := filepath.Join(root+overlayDir, label)
		if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
		if err := os.WriteFile(dir+"/dtbo", propertyOverlay(dtPath, "compatible", want), 0o644); err != nil {
			return err
		}
		return os.WriteFile(dir+"/status", []byte("1"), 0o644)
	}
	applyErr := apply()
	if err := os.WriteFile(drv+"/bind", []byte(devName), 0o200); err != nil {
		return "", fmt.Errorf("bind: %w (overlay: %v)", err, applyErr)
	}
	if applyErr != nil {
		return "", fmt.Errorf("overlay: %w", applyErr)
	}
	for i := 0; i < 40; i++ { // the framebuffer comes back as the driver probes
		if s := findFBSys(root, name); s != "" {
			return fbDev(root, s), nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", fmt.Errorf("no framebuffer named %q after rebinding", name)
}

func findFBSys(root, name string) string {
	for i := 0; i < 16; i++ {
		d := root + "/sys/class/graphics/fb" + fmt.Sprint(i)
		if b, err := os.ReadFile(d + "/name"); err == nil && strings.TrimSpace(string(b)) == name {
			return d
		}
	}
	return ""
}

func fbDev(root, sysfb string) string { return root + "/dev/" + filepath.Base(sysfb) }
