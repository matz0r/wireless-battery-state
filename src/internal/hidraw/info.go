// Package hidraw enumerates and talks to HID devices: Linux hidraw nodes (/dev/hidraw*) or,
// on Windows, HID collections through hid.dll. Both backends use the same report layout: the
// first byte of every report buffer is the report id (0 if reports are unnumbered).
package hidraw

import (
	"fmt"
	"strings"
)

// Info describes one HID node: a hidraw node on Linux (read from sysfs without opening the
// device), or one top-level collection on Windows.
type Info struct {
	Node      string // "hidraw3"; on Windows a short form of the device path
	Path      string // "/dev/hidraw3"; on Windows the device interface path
	Bus       uint16 // 0x0003 = USB, 0x0005 = Bluetooth
	VendorID  uint16
	ProductID uint16
	Name      string // HID_NAME, e.g. "Glorious Model D- Wireless"
	Phys      string // HID_PHYS, e.g. "usb-0000:0b:00.3-1/input2"; on Windows the device instance
	// without the collection part, shared by the collections of one interface
	Interface  int    // USB interface number from Phys, -1 if unknown
	Driver     string // kernel driver bound to the HID device (hid-generic, logitech-hidpp-device, ...)
	Descriptor Descriptor

	// Report lengths in bytes including the report id byte. Only the Windows backend fills
	// these in (from HIDP_CAPS); on Linux they are 0.
	InputLen, OutputLen, FeatureLen int
}

func (i Info) String() string {
	var tl []string
	for _, u := range i.Descriptor.TopLevel {
		tl = append(tl, fmt.Sprintf("%04X:%04X", u.Page, u.ID))
	}
	s := fmt.Sprintf("%s %04X:%04X if=%d %q driver=%s collections=[%s] feature=%v",
		i.Path, i.VendorID, i.ProductID, i.Interface, i.Name, i.Driver, strings.Join(tl, " "), i.Descriptor.HasFeature)
	if i.InputLen+i.OutputLen+i.FeatureLen > 0 {
		s += fmt.Sprintf(" len(in/out/feature)=%d/%d/%d", i.InputLen, i.OutputLen, i.FeatureLen)
	}
	return s
}
