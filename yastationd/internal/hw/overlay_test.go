package hw

import (
	"encoding/hex"
	"testing"
)

// The overlay is byte for byte what dtc 1.6.1 makes of:
//
//	/dts-v1/; /plugin/;
//	/ { fragment@0 { target-path = "/ambilight-framebuffer";
//	    __overlay__ { compatible = "yandex,ledfb-rgbw64"; }; }; };
func TestPropertyOverlayMatchesDtc(t *testing.T) {
	want := "d00dfeed000000cb00000038000000b400000028000000110000001000000000000000170000007c00000000000000000000000000000000000000010000000000000001667261676d656e74403000000000000300000017000000002f616d62696c696768742d6672616d656275666665720000000000015f5f6f7665726c61795f5f0000000003000000140000000c79616e6465782c6c656466622d72676277363400000000020000000200000002000000097461726765742d7061746800636f6d70617469626c6500"
	got := hex.EncodeToString(propertyOverlay("/ambilight-framebuffer", "compatible", "yandex,ledfb-rgbw64"))
	if got != want {
		t.Fatalf("overlay\n got %s\nwant %s", got, want)
	}
}
