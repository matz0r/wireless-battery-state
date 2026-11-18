package battery

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
)

// Logitech HID++: mice and keyboards on Unifying / Bolt / LIGHTSPEED receivers, devices on a
// USB cable or Bluetooth, and G headsets. Features are discovered through the root feature, so
// no per-model table is needed.
//
// Message: [report][device index][feature index][function<<4 | sw id][params...]
//   report 0x10 = short (7 bytes), 0x11 = long (20 bytes)
//   HID++ 2.0 error: [.., 0xFF, feature, fn|sw, code]; HID++ 1.0 error: [.., 0x8F, sub id, reg, code]
// Battery features: 0x1004 unified battery, 0x1000 battery status, 0x1001 battery voltage,
// 0x1F20 ADC measurement (headsets). HID++ 1.0 devices: registers 0x0D and 0x07.
//
// On Linux the kernel's hid-logitech-hidpp driver already reports these devices in
// /sys/class/power_supply, so this driver is only used on Windows (see drivers_*.go).
//
// Protocol from krisgfx9/Wireless-Device-Battery-Monitor (src/drivers/logitech.cpp), the
// Logitech HID++ 2.0 drafts, pwr-Solaar/Solaar and Sapd/HeadsetControl. Only read-only
// functions are called.

const hidppSwID = 0x0D // our software id; replies to other apps (G HUB, Options+) carry theirs

// hidppGrace is how long a device that stopped answering through its receiver keeps its icon
// (dimmed, last known level): receiver devices drop off the link while they sleep.
const hidppGrace = 30 * time.Minute

var hidppDriver = Driver{Name: "logitech-hidpp", Poll: pollHidpp}

// LIGHTSPEED dongles that speak only HID++ 2.0 and relay a single device on index 1.
// 046D:C54F = PRO X SUPERSTRIKE dongle.
func hidppPassthrough(pid uint16) bool { return pid == 0xC54F }

func hidppShort(n hidraw.Info) bool {
	if n.OutputLen != 0 && n.OutputLen != 7 {
		return false
	}
	return n.Descriptor.Has(0xFF00, 0x0001) || (hidppPassthrough(n.ProductID) && n.Descriptor.Has(0xFF43, 0x0301))
}

func hidppLong(n hidraw.Info) bool {
	if n.OutputLen != 0 && n.OutputLen != 20 {
		return false
	}
	return n.Descriptor.Has(0xFF00, 0x0002) || n.Descriptor.Has(0xFF43, 0x0202) ||
		(hidppPassthrough(n.ProductID) && n.Descriptor.Has(0xFF43, 0x0302))
}

// hidppSeen remembers devices found behind a receiver, so one that falls asleep stays listed.
var hidppSeen = struct {
	sync.Mutex
	m map[string]hidppSeenEntry // by receiver slot key
}{m: map[string]hidppSeenEntry{}}

type hidppSeenEntry struct {
	s  Status
	at time.Time
}

var hidppNow = time.Now

func pollHidpp(env *Env) []Status {
	type group struct {
		pid        uint16
		phys       string
		short, lng *hidraw.Info
		product    string
	}
	var groups []*group
	byKey := map[string]*group{}
	for i := range env.Nodes {
		n := &env.Nodes[i]
		if n.VendorID != logitechVID {
			continue
		}
		isShort, isLong := hidppShort(*n), hidppLong(*n)
		if !isShort && !isLong {
			continue
		}
		k := fmt.Sprintf("%04x|%s", n.ProductID, n.Phys)
		g := byKey[k]
		if g == nil {
			g = &group{pid: n.ProductID, phys: n.Phys}
			byKey[k] = g
			groups = append(groups, g)
		}
		if isShort && g.short == nil {
			g.short = n
		}
		if isLong && g.lng == nil {
			g.lng = n
		}
		if g.product == "" {
			g.product = n.Name
		}
	}

	var out []Status
	seq := map[uint16]int{}
	for _, g := range groups {
		seq[g.pid]++
		suffix := ""
		if seq[g.pid] > 1 { // a second identical receiver
			suffix = fmt.Sprintf("#%d", seq[g.pid])
		}
		out = append(out, pollHidppGroup(env, g.pid, g.short, g.lng, g.product, suffix)...)
	}
	return out
}

type hidppLink struct {
	env         *Env
	short, long HID
}

func (h *hidppLink) close() {
	if h.short != nil {
		h.short.Close()
	}
	if h.long != nil {
		h.long.Close()
	}
}

const (
	hidppReply = 1
	hidppError = 0
	hidppNone  = -1
)

// request sends one message and waits for the matching reply or error on either collection.
func (h *hidppLink) request(idx, b2, b3 byte, params []byte, forceShort bool, timeout time.Duration) (int, []byte, byte) {
	dev, msg := h.long, make([]byte, 20)
	msg[0] = 0x11
	if dev == nil || forceShort {
		dev, msg = h.short, make([]byte, 7)
		msg[0] = 0x10
	}
	if dev == nil {
		return hidppNone, nil, 0
	}
	msg[1], msg[2], msg[3] = idx, b2, b3
	copy(msg[4:], params)
	if _, err := dev.Write(msg); err != nil {
		return hidppNone, nil, 0
	}
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 64)
	for time.Now().Before(deadline) {
		got := false
		for _, d := range []HID{h.long, h.short} {
			if d == nil {
				continue
			}
			n, err := d.Read(buf, 15*time.Millisecond)
			if err != nil || n < 7 {
				continue
			}
			got = true
			r := buf[:n]
			if (r[0] != 0x10 && r[0] != 0x11) || r[1] != idx {
				continue
			}
			if r[2] == b2 && r[3] == b3 {
				return hidppReply, append([]byte(nil), r[4:]...), 0
			}
			if (r[2] == 0xFF || r[2] == 0x8F) && r[3] == b2 && r[4] == b3 {
				return hidppError, nil, r[5]
			}
		}
		if !got {
			h.env.sleep(5 * time.Millisecond) // a handle that can't read returns at once; don't spin
		}
	}
	return hidppNone, nil, 0
}

// call is a HID++ 2.0 call: feature index + function number.
func (h *hidppLink) call(idx, feature, fn byte, params ...byte) ([]byte, bool) {
	r, p, _ := h.request(idx, feature, fn<<4|hidppSwID, params, false, 500*time.Millisecond)
	return p, r == hidppReply
}

func (h *hidppLink) featureIndex(idx byte, id uint16) byte {
	if p, ok := h.call(idx, 0x00, 0, byte(id>>8), byte(id)); ok && len(p) > 0 {
		return p[0]
	}
	return 0
}

// ping is the HID++ 2.0 root ping; HID++ 1.0 devices answer it with error 0x01.
func (h *hidppLink) ping(idx byte) (int, []byte, byte) {
	return h.request(idx, 0x00, 0x10|hidppSwID, []byte{0, 0, 0x5A}, false, 400*time.Millisecond)
}

func pollHidppGroup(env *Env, pid uint16, shortN, longN *hidraw.Info, product, suffix string) []Status {
	h := &hidppLink{env: env}
	var path string
	noAccess := false
	for _, x := range []struct {
		n   *hidraw.Info
		dst *HID
	}{{shortN, &h.short}, {longN, &h.long}} {
		if x.n == nil {
			continue
		}
		d, err := env.Open(x.n.Path)
		if err != nil {
			noAccess = noAccess || isPermission(err)
			continue
		}
		*x.dst = d
		path = x.n.Path
	}
	defer h.close()
	if h.short == nil && h.long == nil {
		if noAccess {
			n := shortN
			if n == nil {
				n = longN
			}
			return []Status{{Key: fmt.Sprintf("logi:%04x%s", pid, suffix), Name: product, Kind: GuessKind(product),
				Via: "Logitech receiver", Percent: -1, NoAccess: true, Path: n.Path}}
		}
		return nil
	}

	bluetooth := (shortN != nil && shortN.Bus == 0x0005) || (longN != nil && longN.Bus == 0x0005)
	receiver := strings.Contains(product, "Receiver")
	passthrough := hidppPassthrough(pid)

	// A device that answers the ping on index 0xFF is connected directly (cable, Bluetooth,
	// receiver-less headset); otherwise this is a receiver with up to six paired devices.
	var indices []byte
	switch {
	case passthrough:
		indices = []byte{1}
	case bluetooth:
		indices = []byte{0xFF}
	default:
		if !receiver {
			if r, p, _ := h.ping(0xFF); r == hidppReply && len(p) >= 3 && p[0] >= 2 {
				indices = []byte{0xFF}
			}
		}
		if indices == nil {
			indices = []byte{1, 2, 3, 4, 5, 6}
		}
	}

	var out []Status
	for _, idx := range indices {
		slot := fmt.Sprintf("logi:%04x%s:%d", pid, suffix, idx)
		s := Status{Key: slot, Name: "Logitech wireless device", Via: "Logitech receiver", Percent: -1, Path: path}
		if idx == 0xFF {
			s.Via = "USB / Bluetooth"
			if product != "" {
				s.Name = product
			}
		}
		if passthrough {
			s.Kind = Mouse
		}
		found := false
		switch r, p, code := h.ping(idx); {
		case r == hidppReply && len(p) >= 3 && p[0] >= 2:
			readNameAndType20(h, idx, &s)
			found = readBattery20(h, idx, pid, &s)
			if found {
				if unit := readUnitID20(h, idx); unit != "" {
					s.Key = "logi:unit:" + unit
				}
				if s.Kind == Other && longN != nil && longN.Descriptor.Has(0xFF43, 0x0202) {
					s.Kind = Headset
				}
			}
		case r == hidppError && code == 0x01: // invalid sub id: a HID++ 1.0 device
			found = readBattery10(h, idx, &s)
		}
		if s.Kind == Other {
			s.Kind = GuessKind(s.Name)
		}

		hidppSeen.Lock()
		if found {
			hidppSeen.m[slot] = hidppSeenEntry{s: s, at: hidppNow()}
			out = append(out, s)
		} else if e, ok := hidppSeen.m[slot]; ok && idx != 0xFF {
			if hidppNow().Sub(e.at) < hidppGrace {
				a := e.s
				a.Percent, a.Charging, a.Asleep = -1, false, true // the monitor fills in the last level
				out = append(out, a)
			} else {
				delete(hidppSeen.m, slot)
			}
		}
		hidppSeen.Unlock()
	}
	return out
}

// readBattery20 fills the battery fields from whichever battery feature the device has.
func readBattery20(h *hidppLink, idx byte, pid uint16, s *Status) bool {
	if f := h.featureIndex(idx, 0x1004); f != 0 { // UNIFIED_BATTERY get_status
		if p, ok := h.call(idx, f, 1); ok && len(p) >= 3 {
			soc, level, st := int(p[0]), p[1], p[2]
			switch {
			case soc > 0 && soc <= 100:
				s.Percent = soc
			case level&8 != 0:
				s.Percent = 100
			case level&4 != 0:
				s.Percent = 60
			case level&2 != 0:
				s.Percent = 20
			case level&1 != 0:
				s.Percent = 5
			}
			s.Charging = st == 1 || st == 2 || st == 3
			return true
		}
	}
	if f := h.featureIndex(idx, 0x1000); f != 0 { // BATTERY_STATUS GetBatteryLevelStatus
		if p, ok := h.call(idx, f, 0); ok && len(p) >= 3 {
			st := p[2]
			s.Charging = st >= 1 && st <= 4
			if p[0] <= 100 {
				s.Percent = int(p[0])
			}
			if s.Percent == 0 && st == 3 { // full
				s.Percent = 100
			}
			return true
		}
	}
	if f := h.featureIndex(idx, 0x1001); f != 0 { // BATTERY_VOLTAGE GetBatteryInfo
		if p, ok := h.call(idx, f, 0); ok && len(p) >= 3 {
			mv := int(p[0])<<8 | int(p[1])
			s.Charging = p[2]&0x80 != 0
			if mv > 0 {
				s.Percent = liIonPercent(mv)
			}
			return true
		}
	}
	if f := h.featureIndex(idx, 0x1F20); f != 0 { // ADC_MEASUREMENT (headsets)
		if p, ok := h.call(idx, f, 0); ok && len(p) >= 3 {
			mv := int(p[0])<<8 | int(p[1])
			s.Charging = p[2] == 0x03
			if mv > 0 {
				s.Percent = headsetPercent(pid, mv)
			}
			return true
		}
	}
	return false
}

func readNameAndType20(h *hidppLink, idx byte, s *Status) {
	f := h.featureIndex(idx, 0x0005) // DEVICE_NAME
	if f == 0 {
		return
	}
	if p, ok := h.call(idx, f, 0); ok && len(p) > 0 {
		n := int(p[0])
		var name []byte
		for len(name) < n {
			q, ok := h.call(idx, f, 1, byte(len(name)))
			if !ok || len(q) == 0 {
				break
			}
			before := len(name)
			for _, c := range q {
				if len(name) >= n || c == 0 {
					break
				}
				name = append(name, c)
			}
			if len(name) == before {
				break
			}
		}
		if w := strings.TrimSpace(string(name)); w != "" {
			if strings.HasPrefix(w, "Logitech") || strings.HasPrefix(w, "G ") || strings.HasPrefix(w, "MX") {
				s.Name = w
			} else {
				s.Name = "Logitech " + w
			}
		}
	}
	if p, ok := h.call(idx, f, 2); ok && len(p) > 0 {
		s.Kind = kindFromHidppType(p[0])
	}
}

func kindFromHidppType(t byte) Kind {
	switch t {
	case 0x00, 0x02: // keyboard, numpad
		return Keyboard
	case 0x03, 0x04, 0x05: // mouse, trackpad, trackball
		return Mouse
	case 0x08:
		return Headset
	}
	return Other
}

// readUnitID20 reads DEVICE_INFORMATION's unit id, which identifies the physical device: a
// mouse keeps one icon whether it is reached through its receiver or over its cable.
func readUnitID20(h *hidppLink, idx byte) string {
	f := h.featureIndex(idx, 0x0003)
	if f == 0 {
		return ""
	}
	p, ok := h.call(idx, f, 0)
	if !ok || len(p) < 5 || (p[1]|p[2]|p[3]|p[4]) == 0 {
		return ""
	}
	return fmt.Sprintf("%02X%02X%02X%02X", p[1], p[2], p[3], p[4])
}

// readBattery10 reads HID++ 1.0 register 0x0D (percent) or 0x07 (level 1..7).
func readBattery10(h *hidppLink, idx byte, s *Status) bool {
	if r, p, _ := h.request(idx, 0x81, 0x0D, nil, true, 500*time.Millisecond); r == hidppReply && len(p) >= 3 && p[0] > 0 && p[0] <= 100 {
		s.Percent = int(p[0])
		s.Charging = p[2] >= 0x21 && p[2] <= 0x26
		return true
	}
	if r, p, _ := h.request(idx, 0x81, 0x07, nil, true, 500*time.Millisecond); r == hidppReply && len(p) >= 2 && p[0] > 0 {
		pct := []int{0, 5, 10, 20, 35, 50, 75, 100}
		s.Percent = pct[min(int(p[0]), 7)]
		s.Charging = p[1] >= 0x21 && p[1] <= 0x26
		return true
	}
	return false
}

// interpolate maps a voltage through (mv[i], pct[i]) pairs sorted by descending voltage.
func interpolate(mv int, mvs, pcts []int) int {
	if mv >= mvs[0] {
		return pcts[0]
	}
	for i := 1; i < len(mvs); i++ {
		if mv >= mvs[i] {
			span := max(mvs[i-1]-mvs[i], 1)
			return pcts[i] + (pcts[i-1]-pcts[i])*(mv-mvs[i])/span
		}
	}
	return pcts[len(pcts)-1]
}

// liIonPercent: voltage to percent for a typical Li-ion cell (mice, keyboards).
func liIonPercent(mv int) int {
	return interpolate(mv,
		[]int{4186, 4067, 3989, 3922, 3859, 3811, 3778, 3751, 3717, 3671, 3646, 3579, 3500},
		[]int{100, 90, 80, 70, 60, 50, 40, 30, 20, 10, 5, 2, 0})
}

// headsetPercent: voltage to percent for G headsets (calibration from HeadsetControl).
func headsetPercent(pid uint16, mv int) int {
	p6 := []int{100, 50, 30, 20, 5, 0}
	switch pid {
	case 0x0A66: // G533
		return interpolate(mv, []int{4200, 3850, 3790, 3750, 3680, 3330}, p6)
	case 0x0AC4: // G535
		return interpolate(mv, []int{4175, 3817, 3766, 3730, 3664, 3310}, p6)
	case 0x0A5C, 0x0A89, 0x0A5B, 0x0A87, 0x0AB5, 0x0AFE: // G633 / G933 / G935
		return interpolate(mv, []int{4100, 3950, 3850, 3750, 3650, 3500, 3300, 3150}, []int{100, 80, 60, 40, 20, 10, 5, 0})
	case 0x0AA7, 0x0AAA, 0x0ABA, 0x0AFB: // G PRO
		return interpolate(mv, []int{4150, 3830, 3780, 3740, 3670, 3320}, p6)
	}
	return interpolate(mv, []int{4100, 3850, 3780, 3730, 3650, 3300}, p6)
}
