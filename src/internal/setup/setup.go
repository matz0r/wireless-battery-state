// Package setup installs a udev rule so the logged-in user can read the devices' hidraw nodes.
// Only Linux needs this; on Windows the HID collections the drivers use are readable by the
// user already.
//
// By default /dev/hidraw* is root only. The rule tags the supported devices with "uaccess",
// which gives the user at the seat access (like Steam's and HeadsetControl's rules), without a
// group or a new login. It runs once, as root, through pkexec (the desktop's password dialog).
package setup

import (
	"fmt"
	"strings"

	"github.com/matz0r/wireless-battery-state/src/internal/battery"
)

// Rules returns the udev rule text for all drivers.
func Rules() string {
	var b strings.Builder
	b.WriteString("# battery-state: let the user at the seat read the battery level of these devices.\n")
	for _, d := range battery.Drivers {
		for _, u := range d.Udev {
			var pids []string
			for _, p := range u.Products {
				pids = append(pids, fmt.Sprintf("%04x", p))
			}
			fmt.Fprintf(&b, "KERNEL==\"hidraw*\", SUBSYSTEM==\"hidraw\", ATTRS{idVendor}==\"%04x\", ATTRS{idProduct}==\"%s\", TAG+=\"uaccess\"\n",
				u.Vendor, strings.Join(pids, "|"))
		}
	}
	return b.String()
}

// NeedsAccess reports whether any device was found but could not be opened.
func NeedsAccess(devs []battery.Status) bool {
	for _, d := range devs {
		if d.NoAccess {
			return true
		}
	}
	return false
}
