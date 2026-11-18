package battery

import (
	"testing"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
)

// hidppDevice is a fake HID++ device (or a receiver with paired devices). Requests go through
// answer; replies are queued for Read, like the real collections do.
type hidppDevice struct {
	answer  func(req []byte) []byte // nil reply: no answer
	queue   [][]byte
	written [][]byte
}

func (d *hidppDevice) Write(r []byte) (int, error) {
	d.written = append(d.written, append([]byte(nil), r...))
	if rep := d.answer(r); rep != nil {
		d.queue = append(d.queue, rep)
	}
	return len(r), nil
}

func (d *hidppDevice) Read(buf []byte, _ time.Duration) (int, error) {
	if len(d.queue) == 0 {
		return 0, nil
	}
	n := copy(buf, d.queue[0])
	d.queue = d.queue[1:]
	return n, nil
}

func (d *hidppDevice) Flush()                         {}
func (d *hidppDevice) SetFeature([]byte) error        { return nil }
func (d *hidppDevice) GetFeature([]byte) (int, error) { return 0, nil }
func (d *hidppDevice) Close() error                   { return nil }

// fakeHidpp20 is one HID++ 2.0 device: its features (id -> index) and how each function answers.
type fakeHidpp20 struct {
	features map[uint16]byte
	fn       func(feature uint16, fn byte, params []byte) []byte
}

func (f *fakeHidpp20) reply(req []byte) []byte {
	idx, fi, fnsw := req[1], req[2], req[3]
	out := func(params ...byte) []byte {
		r := make([]byte, 20)
		r[0], r[1], r[2], r[3] = 0x11, idx, fi, fnsw
		copy(r[4:], params)
		return r
	}
	if fi == 0 { // root
		switch fnsw >> 4 {
		case 0: // getFeature
			id := uint16(req[4])<<8 | uint16(req[5])
			return out(f.features[id])
		case 1: // ping
			return out(4, 2, req[6])
		}
	}
	for id, i := range f.features {
		if i == fi {
			return out(f.fn(id, fnsw>>4, req[4:])...)
		}
	}
	return []byte{0x11, idx, 0xFF, fi, fnsw, 0x02, 0} // HID++ 2.0 error: invalid feature
}

func hidppNodes(pid uint16, name string) []hidraw.Info {
	return []hidraw.Info{
		{Path: "short", VendorID: 0x046D, ProductID: pid, Name: name, Phys: "rx1", OutputLen: 7,
			Descriptor: hidraw.Descriptor{TopLevel: []hidraw.Usage{{Page: 0xFF00, ID: 1}}}},
		{Path: "long", VendorID: 0x046D, ProductID: pid, Name: name, Phys: "rx1", OutputLen: 20,
			Descriptor: hidraw.Descriptor{TopLevel: []hidraw.Usage{{Page: 0xFF00, ID: 2}}}},
		{Path: "mouse", VendorID: 0x046D, ProductID: pid, Name: name, Phys: "rx1",
			Descriptor: hidraw.Descriptor{TopLevel: []hidraw.Usage{{Page: 0x01, ID: 2}}}},
	}
}

func g515() *fakeHidpp20 {
	return &fakeHidpp20{
		features: map[uint16]byte{0x0003: 2, 0x0005: 3, 0x1004: 4},
		fn: func(feature uint16, fn byte, params []byte) []byte {
			switch {
			case feature == 0x0005 && fn == 0:
				return []byte{11}
			case feature == 0x0005 && fn == 1:
				return []byte("G515 LS TKL"[params[0]:])
			case feature == 0x0005 && fn == 2:
				return []byte{0x00} // keyboard
			case feature == 0x0003 && fn == 0:
				return []byte{1, 0xF1, 0x43, 0x66, 0x7C}
			case feature == 0x1004 && fn == 1:
				return []byte{88, 0x04, 0x00}
			}
			return nil
		},
	}
}

// receiver answers for its paired devices by index; other indices get the receiver's HID++ 1.0
// "unknown device" error.
func receiver(paired map[byte]*fakeHidpp20) *hidppDevice {
	return &hidppDevice{answer: func(req []byte) []byte {
		if d := paired[req[1]]; d != nil {
			return d.reply(req)
		}
		return []byte{0x10, req[1], 0x8F, req[2], req[3], 0x08, 0}
	}}
}

func resetHidppSeen() {
	hidppSeen.Lock()
	hidppSeen.m = map[string]hidppSeenEntry{}
	hidppSeen.Unlock()
}

func TestHidppReceiver(t *testing.T) {
	resetHidppSeen()
	rx := receiver(map[byte]*fakeHidpp20{1: g515()})
	env := envWith(hidppNodes(0xC547, "USB Receiver"), map[string]HID{"short": rx, "long": rx}, nil)
	got := pollHidpp(env)
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(got), got)
	}
	s := got[0]
	if s.Name != "Logitech G515 LS TKL" || s.Kind != Keyboard || s.Percent != 88 || s.Charging ||
		s.Key != "logi:unit:F143667C" || s.Via != "Logitech receiver" {
		t.Errorf("unexpected status %+v", s)
	}
	for _, w := range rx.written {
		if w[0] == 0x11 && len(w) != 20 || w[0] == 0x10 && len(w) != 7 {
			t.Errorf("bad message length % x", w)
		}
		if w[2] != 0x00 && w[2] != 0x81 && w[3]&0x0F != hidppSwID {
			t.Errorf("message without our software id: % x", w)
		}
	}
}

func TestHidppAsleepKeepsIcon(t *testing.T) {
	resetHidppSeen()
	now := time.Unix(1000, 0)
	hidppNow = func() time.Time { return now }
	defer func() { hidppNow = time.Now }()

	paired := map[byte]*fakeHidpp20{1: g515()}
	rx := receiver(paired)
	env := envWith(hidppNodes(0xC547, "USB Receiver"), map[string]HID{"short": rx, "long": rx}, nil)
	if got := pollHidpp(env); len(got) != 1 {
		t.Fatalf("first poll: %+v", got)
	}

	delete(paired, 1) // keyboard asleep: the receiver no longer reaches it
	now = now.Add(time.Minute)
	got := pollHidpp(env)
	if len(got) != 1 || !got[0].Asleep || got[0].Percent != -1 || got[0].Name != "Logitech G515 LS TKL" {
		t.Fatalf("want the sleeping keyboard kept, got %+v", got)
	}

	now = now.Add(hidppGrace)
	if got := pollHidpp(env); len(got) != 0 {
		t.Fatalf("want it dropped after the grace period, got %+v", got)
	}
}

func TestHidppDirect(t *testing.T) {
	resetHidppSeen()
	mouse := &fakeHidpp20{
		features: map[uint16]byte{0x1000: 5},
		fn: func(feature uint16, fn byte, _ []byte) []byte {
			if feature == 0x1000 && fn == 0 {
				return []byte{42, 0, 1} // 42%, charging
			}
			return nil
		},
	}
	dev := &hidppDevice{answer: func(req []byte) []byte {
		if req[1] != 0xFF {
			return nil
		}
		return mouse.reply(req)
	}}
	env := envWith(hidppNodes(0xC08B, "G502 HERO Gaming Mouse"), map[string]HID{"short": dev, "long": dev}, nil)
	got := pollHidpp(env)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	s := got[0]
	if s.Name != "G502 HERO Gaming Mouse" || s.Kind != Mouse || s.Percent != 42 || !s.Charging || s.Via != "USB / Bluetooth" {
		t.Errorf("unexpected status %+v", s)
	}
}

func TestHidpp10Device(t *testing.T) {
	resetHidppSeen()
	rx := &hidppDevice{answer: func(req []byte) []byte {
		switch {
		case req[1] != 1:
			return []byte{0x10, req[1], 0x8F, req[2], req[3], 0x08, 0}
		case req[2] == 0x00: // HID++ 2.0 ping: HID++ 1.0 devices reject it
			return []byte{0x10, 1, 0x8F, 0x00, req[3], 0x01, 0}
		case req[2] == 0x81 && req[3] == 0x0D:
			return []byte{0x10, 1, 0x81, 0x0D, 70, 0, 0x30}
		}
		return nil
	}}
	env := envWith(hidppNodes(0xC52B, "USB Receiver"), map[string]HID{"short": rx}, nil)
	got := pollHidpp(env)
	if len(got) != 1 || got[0].Percent != 70 || got[0].Charging {
		t.Fatalf("got %+v", got)
	}
}

func TestHidppIgnoresOtherVendors(t *testing.T) {
	nodes := hidppNodes(0xC547, "USB Receiver")
	for i := range nodes {
		nodes[i].VendorID = 0x1234
	}
	if got := pollHidpp(envWith(nodes, nil, nil)); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestVoltageCurves(t *testing.T) {
	for _, c := range []struct{ mv, want int }{{4300, 100}, {3811, 50}, {3400, 0}} {
		if got := liIonPercent(c.mv); got != c.want {
			t.Errorf("liIonPercent(%d) = %d, want %d", c.mv, got, c.want)
		}
	}
	if got := headsetPercent(0x0A66, 3850); got != 50 {
		t.Errorf("G533 at 3850 mV = %d", got)
	}
}
