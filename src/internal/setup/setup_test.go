package setup

import (
	"strings"
	"testing"
)

func TestRules(t *testing.T) {
	r := Rules()
	for _, want := range []string{
		`ATTRS{idVendor}=="258a"`, "2025", // Glorious Model D- Wireless
		`ATTRS{idVendor}=="046d"`, "0b1c", // ASTRO A50 Gen 5
		`TAG+="uaccess"`,
	} {
		if !strings.Contains(r, want) {
			t.Errorf("rules lack %q:\n%s", want, r)
		}
	}
	if strings.Contains(r, "c547") {
		t.Error("the Logitech receiver is read through the kernel and needs no rule")
	}
}
