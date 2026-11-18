package battery

import (
	"fmt"
	"time"
)

// Glorious wireless mice (Sinowealth chip, VID 258A).
//
// Request: 65-byte feature report (report id 0 + 64 bytes), bytes [3]=02 [4]=02 [6]=83.
// Reply:   [1]=status, [6]=83, [7]=charging (1), [8]=percent.
// Status A1 = awake, A4 = asleep, A0 = waking up, others = not this protocol.
//
// Protocol from krisgfx9/Wireless-Device-Battery-Monitor (src/drivers/glorious.cpp),
// ikajdan/glorious-indicator and louis4craft/glorious-ctl.

const gloriousVID = 0x258A

var gloriousModels = map[uint16]struct {
	name  string
	cable bool
}{
	0x2022: {"Glorious Model O Wireless", false},
	0x2011: {"Glorious Model O Wireless", true},
	0x2023: {"Glorious Model D Wireless", false},
	0x2012: {"Glorious Model D Wireless", true},
	0x2024: {"Glorious Model O- Wireless", false},
	0x2013: {"Glorious Model O- Wireless", true},
	0x2025: {"Glorious Model D- Wireless", false},
	0x2014: {"Glorious Model D- Wireless", true},
	0x2033: {"Glorious Model O 2 Wireless", false},
	0x2034: {"Glorious Model D 2 PRO", false},
	0x2036: {"Glorious Model D 2 PRO", false},
	0x201A: {"Glorious Model D 2 PRO", true},
}

var gloriousDriver = Driver{
	Name: "glorious",
	Poll: pollGlorious,
	Udev: []UdevID{{Vendor: gloriousVID, Products: sortedKeys(gloriousModels)}},
}

func pollGlorious(env *Env) []Status {
	var out []Status
	done := map[string]bool{} // one entry per physical device (all its interfaces share Phys up to "/input")
	for _, n := range env.Nodes {
		model, ok := gloriousModels[n.ProductID]
		if n.VendorID != gloriousVID || !ok || !n.Descriptor.HasFeature || !n.Descriptor.VendorDefined() {
			continue
		}
		key := fmt.Sprintf("glorious:%04x:%s", n.ProductID, physDevice(n.Phys))
		if done[key] {
			continue
		}
		s := Status{
			Key:     key,
			Name:    model.name,
			Kind:    Mouse,
			Via:     "2.4 GHz receiver",
			Percent: -1,
			Path:    n.Path,
		}
		if model.cable {
			s.Via = "USB cable"
		}
		d, err := env.Open(n.Path)
		if err != nil {
			if isPermission(err) {
				s.NoAccess = true
				out = append(out, s)
				done[key] = true
			}
			continue
		}
		r, ok := queryGlorious(env, d)
		d.Close()
		if !ok {
			continue
		}
		s.Charging = model.cable || r[7] == 0x01
		if r[1] == 0xA1 && r[8] <= 100 {
			s.Percent = max(int(r[8]), 1) // 0 while awake means "almost empty", not unknown
		} else {
			s.Asleep = true
		}
		out = append(out, s)
		done[key] = true
	}
	return out
}

func queryGlorious(env *Env, d HID) ([]byte, bool) {
	for attempt := 0; attempt < 2; attempt++ {
		w := make([]byte, 65)
		w[3], w[4], w[6] = 0x02, 0x02, 0x83
		if err := d.SetFeature(w); err != nil {
			return nil, false
		}
		env.sleep(50 * time.Millisecond)
		r := make([]byte, 65)
		n, err := d.GetFeature(r)
		if err != nil || n < 9 {
			return nil, false
		}
		if r[6] != 0x83 || r[1]&0xF0 != 0xA0 {
			return nil, false // not this protocol
		}
		if r[1] == 0xA0 && attempt == 0 { // waking up: ask again
			env.sleep(400 * time.Millisecond)
			continue
		}
		return r, true
	}
	return nil, false
}
