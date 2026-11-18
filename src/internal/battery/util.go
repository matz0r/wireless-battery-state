package battery

import (
	"cmp"
	"slices"
	"strings"
)

// physDevice strips the interface part from HID_PHYS ("usb-0000:0b:00.3-1/input2" ->
// "usb-0000:0b:00.3-1"), so all interfaces of one USB device share a key.
func physDevice(phys string) string {
	if i := strings.Index(phys, "/input"); i >= 0 {
		return phys[:i]
	}
	return phys
}

func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
