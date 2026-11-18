package hidraw

import (
	"encoding/hex"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Report descriptors captured from real devices.
func TestParseDescriptor(t *testing.T) {
	cases := []struct {
		name    string
		desc    string
		want    []Usage
		feature bool
	}{
		{
			name:    "Glorious Model D- interface 2",
			desc:    "06ffff0900a10109001500250175089540b102c0",
			want:    []Usage{{0xFFFF, 0x0000}},
			feature: true,
		},
		{
			name: "ASTRO A50 Gen 5 interface 8",
			desc: "0632ff0974a101a10385027508150026ff00953f097581027508150026ff00953f09769102c0c0",
			want: []Usage{{0xFF32, 0x0074}},
		},
		{
			name: "Logitech receiver HID++ interface",
			desc: "0600ff0901a101851095067508150026ff000901810009019100c00600ff0902a101851195137508150026ff000902810009029100c0",
			want: []Usage{{0xFF00, 0x0001}, {0xFF00, 0x0002}},
		},
	}
	for _, c := range cases {
		d := ParseDescriptor(mustHex(t, c.desc))
		if len(d.TopLevel) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, d.TopLevel, c.want)
			continue
		}
		for i := range c.want {
			if d.TopLevel[i] != c.want[i] {
				t.Errorf("%s: collection %d = %v, want %v", c.name, i, d.TopLevel[i], c.want[i])
			}
		}
		if d.HasFeature != c.feature {
			t.Errorf("%s: HasFeature = %v", c.name, d.HasFeature)
		}
	}
}

func TestParseDescriptorTruncated(t *testing.T) {
	// Must not panic on a cut-off item.
	ParseDescriptor([]byte{0x06, 0xFF})
	ParseDescriptor([]byte{0xFE})
	ParseDescriptor(nil)
}

func TestWindowsPaths(t *testing.T) {
	const guid = "#{4d1e55b2-f16f-11cf-88cb-001111000030}"
	short := `\\?\HID#VID_046D&PID_C547&MI_02&Col01#8&2a2b3c4d&0&0000` + guid
	long := `\\?\HID#VID_046D&PID_C547&MI_02&Col02#8&2a2b3c4d&0&0001` + guid
	other := `\\?\HID#VID_046D&PID_C547&MI_02&Col01#8&99999999&0&0000` + guid
	if PhysicalKey(short) != PhysicalKey(long) {
		t.Errorf("collections of one interface must share a key: %q vs %q", PhysicalKey(short), PhysicalKey(long))
	}
	if PhysicalKey(short) == PhysicalKey(other) {
		t.Error("two receivers must get different keys")
	}
	if got := PhysicalKey(short); got != "vid_046d&pid_c547&mi_02#8&2a2b3c4d&0" {
		t.Errorf("PhysicalKey = %q", got)
	}
	if got := shortNode(short); got != "vid_046d&pid_c547&mi_02&col01#8&2a2b3c4d&0&0000" {
		t.Errorf("shortNode = %q", got)
	}
}
