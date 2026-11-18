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
