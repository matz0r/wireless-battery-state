package battery

import (
	"fmt"
	"time"
)

// Logitech / ASTRO A50 X (046D:0B0B) and A50 Gen 5 (046D:0B1C) base stations.
//
// Vendor HID collection FF32:0074, 64-byte reports with report id 02:
//   request: [02][0C][03][00][06][0C][00...]
//   reply:   [02][0C][len][00][06][handle][percent][..][charging]
// The request only reads the battery; it never changes audio or headset settings.
//
// Protocol from Sapd/HeadsetControl (logitech_astro_a50.hpp) via
// krisgfx9/Wireless-Device-Battery-Monitor (src/drivers/astro.cpp).

const logitechVID = 0x046D

var astroModels = map[uint16]string{
	0x0B0B: "ASTRO A50 X",
	0x0B1C: "ASTRO A50 Gen 5",
}

var astroDriver = Driver{
	Name: "astro",
	Poll: pollAstro,
	Udev: []UdevID{{Vendor: logitechVID, Products: sortedKeys(astroModels)}},
}

func pollAstro(env *Env) []Status {
	var out []Status
	for _, n := range env.Nodes {
		name, ok := astroModels[n.ProductID]
		if n.VendorID != logitechVID || !ok || !n.Descriptor.Has(0xFF32, 0x0074) {
			continue
		}
		s := Status{
			Key:     fmt.Sprintf("astro:%04x:%s", n.ProductID, physDevice(n.Phys)),
			Name:    name,
			Kind:    Headset,
			Via:     "USB base station",
			Percent: -1,
			Path:    n.Path,
		}
		d, err := env.Open(n.Path)
		if err != nil {
			if isPermission(err) {
				s.NoAccess = true
				out = append(out, s)
			}
			continue
		}
		pct, charging, ok := queryAstro(d)
		d.Close()
		if ok {
			s.Percent, s.Charging = pct, charging
		} else {
			// Base station is there but the headset does not answer: off or out of range.
			s.Asleep = true
		}
		out = append(out, s)
	}
	return out
}

func queryAstro(d HID) (percent int, charging, ok bool) {
	d.Flush()
	req := make([]byte, 64)
	req[0], req[1], req[2], req[4], req[5] = 0x02, 0x0C, 3, 0x06, 0x0C
	if _, err := d.Write(req); err != nil {
		return 0, false, false
	}
	deadline := time.Now().Add(time.Second)
	buf := make([]byte, 64)
	for attempt := 0; attempt < 8; attempt++ {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		n, err := d.Read(buf, left)
		if err != nil || n == 0 {
			break
		}
		r := buf[:n]
		// Skip unsolicited volume/mix events and malformed or partial frames.
		if n < 9 || r[0] != 0x02 || r[1] != 0x0C || r[2] < 6 || int(r[2])+3 > n ||
			r[3] != 0 || r[4] != 0x06 || (r[5] != 0 && r[5] != 0x0C) || r[6] > 100 {
			continue
		}
		return int(r[6]), r[8] != 0, true
	}
	return 0, false, false
}
