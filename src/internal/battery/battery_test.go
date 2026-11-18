package battery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
)

// fakeHID answers feature and output reports from canned replies.
type fakeHID struct {
	features [][]byte // successive GetFeature replies (report id first)
	inputs   [][]byte // successive Read replies
	setCalls [][]byte
	writes   [][]byte
	setErr   error
}

func (f *fakeHID) Write(r []byte) (int, error) {
	f.writes = append(f.writes, append([]byte(nil), r...))
	return len(r), nil
}

func (f *fakeHID) Read(buf []byte, _ time.Duration) (int, error) {
	if len(f.inputs) == 0 {
		return 0, nil
	}
	n := copy(buf, f.inputs[0])
	f.inputs = f.inputs[1:]
	return n, nil
}

func (f *fakeHID) Flush() {}

func (f *fakeHID) SetFeature(r []byte) error {
	f.setCalls = append(f.setCalls, append([]byte(nil), r...))
	return f.setErr
}

func (f *fakeHID) GetFeature(buf []byte) (int, error) {
	if len(f.features) == 0 {
		return 0, os.ErrDeadlineExceeded
	}
	copy(buf, f.features[0])
	f.features = f.features[1:]
	return len(buf), nil
}

func (f *fakeHID) Close() error { return nil }

func envWith(nodes []hidraw.Info, devs map[string]HID, openErr error) *Env {
	return &Env{
		Nodes: nodes,
		Open: func(path string) (HID, error) {
			if openErr != nil {
				return nil, openErr
			}
			return devs[path], nil
		},
		Sleep: func(time.Duration) {},
	}
}

var gloriousNodes = []hidraw.Info{
	{Path: "/dev/hidraw0", VendorID: 0x258A, ProductID: 0x2025, Phys: "usb-x-1/input0",
		Descriptor: hidraw.Descriptor{TopLevel: []hidraw.Usage{{Page: 1, ID: 2}}}},
	{Path: "/dev/hidraw2", VendorID: 0x258A, ProductID: 0x2025, Phys: "usb-x-1/input2",
		Descriptor: hidraw.Descriptor{TopLevel: []hidraw.Usage{{Page: 0xFFFF, ID: 0}}, HasFeature: true}},
}

func gloriousReply(status, charging, pct byte) []byte {
	r := make([]byte, 65)
	r[1], r[4], r[6], r[7], r[8] = status, 0x02, 0x83, charging, pct
	return r
}

func TestGloriousAwake(t *testing.T) {
	dev := &fakeHID{features: [][]byte{gloriousReply(0xA1, 0, 77)}}
	got := pollGlorious(envWith(gloriousNodes, map[string]HID{"/dev/hidraw2": dev}, nil))
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(got), got)
	}
	s := got[0]
	if s.Name != "Glorious Model D- Wireless" || s.Kind != Mouse || s.Percent != 77 || s.Charging || s.Asleep {
		t.Errorf("unexpected status %+v", s)
	}
	req := dev.setCalls[0]
	if len(req) != 65 || req[0] != 0 || req[3] != 2 || req[4] != 2 || req[6] != 0x83 {
		t.Errorf("unexpected request % x", req[:8])
	}
}

func TestGloriousWakingUpRetries(t *testing.T) {
	dev := &fakeHID{features: [][]byte{gloriousReply(0xA0, 0, 0), gloriousReply(0xA1, 1, 50)}}
	got := pollGlorious(envWith(gloriousNodes, map[string]HID{"/dev/hidraw2": dev}, nil))
	if len(got) != 1 || got[0].Percent != 50 || !got[0].Charging || len(dev.setCalls) != 2 {
		t.Fatalf("unexpected %+v after %d requests", got, len(dev.setCalls))
	}
}

func TestGloriousAsleep(t *testing.T) {
	dev := &fakeHID{features: [][]byte{gloriousReply(0xA4, 0, 0)}}
	got := pollGlorious(envWith(gloriousNodes, map[string]HID{"/dev/hidraw2": dev}, nil))
	if len(got) != 1 || !got[0].Asleep || got[0].Percent != -1 {
		t.Fatalf("unexpected %+v", got)
	}
}

func TestGloriousOtherProtocolIgnored(t *testing.T) {
	r := make([]byte, 65)
	r[1], r[6] = 0x55, 0x11
	dev := &fakeHID{features: [][]byte{r}}
	if got := pollGlorious(envWith(gloriousNodes, map[string]HID{"/dev/hidraw2": dev}, nil)); len(got) != 0 {
		t.Fatalf("want nothing, got %+v", got)
	}
}

func TestGloriousNoAccess(t *testing.T) {
	err := &os.PathError{Op: "open", Path: "/dev/hidraw2", Err: os.ErrPermission}
	got := pollGlorious(envWith(gloriousNodes, nil, err))
	if len(got) != 1 || !got[0].NoAccess {
		t.Fatalf("want one NoAccess device, got %+v", got)
	}
}

var astroNodes = []hidraw.Info{
	{Path: "/dev/hidraw7", VendorID: 0x046D, ProductID: 0x0B1C, Phys: "usb-y-2/input8",
		Descriptor: hidraw.Descriptor{TopLevel: []hidraw.Usage{{Page: 0xFF32, ID: 0x74}}}},
}

func TestAstroBattery(t *testing.T) {
	volume := []byte{0x02, 0x0B, 0x02, 0x00, 0x10, 0x20, 0, 0, 0} // unsolicited event: skipped
	reply := make([]byte, 64)
	copy(reply, []byte{0x02, 0x0C, 0x06, 0x00, 0x06, 0x0C, 94, 0x00, 0x01})
	dev := &fakeHID{inputs: [][]byte{volume, reply}}
	got := pollAstro(envWith(astroNodes, map[string]HID{"/dev/hidraw7": dev}, nil))
	if len(got) != 1 || got[0].Percent != 94 || !got[0].Charging || got[0].Kind != Headset {
		t.Fatalf("unexpected %+v", got)
	}
	w := dev.writes[0]
	if len(w) != 64 || w[0] != 2 || w[1] != 0x0C || w[2] != 3 || w[4] != 6 || w[5] != 0x0C {
		t.Errorf("unexpected request % x", w[:8])
	}
}

func TestAstroHeadsetOff(t *testing.T) {
	dev := &fakeHID{}
	got := pollAstro(envWith(astroNodes, map[string]HID{"/dev/hidraw7": dev}, nil))
	if len(got) != 1 || !got[0].Asleep || got[0].Percent != -1 {
		t.Fatalf("unexpected %+v", got)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPowerSupply(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"hidpp_battery_0/uevent": "POWER_SUPPLY_NAME=hidpp_battery_0\nPOWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_STATUS=Discharging\n" +
			"POWER_SUPPLY_ONLINE=1\nPOWER_SUPPLY_CAPACITY=88\nPOWER_SUPPLY_SCOPE=Device\nPOWER_SUPPLY_MODEL_NAME=G515 LS TKL\n" +
			"POWER_SUPPLY_MANUFACTURER=Logitech\nPOWER_SUPPLY_SERIAL_NUMBER=f1-43-66-7c\n",
		"hidpp_battery_1/uevent": "POWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_SCOPE=Device\nPOWER_SUPPLY_MODEL_NAME=Wireless Mouse\n" +
			"POWER_SUPPLY_MANUFACTURER=Logitech\nPOWER_SUPPLY_CAPACITY_LEVEL=Low\nPOWER_SUPPLY_STATUS=Charging\nPOWER_SUPPLY_ONLINE=1\n",
		"hidpp_battery_2/uevent": "POWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_SCOPE=Device\nPOWER_SUPPLY_MODEL_NAME=Sleepy\n" +
			"POWER_SUPPLY_CAPACITY=40\nPOWER_SUPPLY_ONLINE=0\n",
		"BAT0/uevent": "POWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_CAPACITY=50\n", // laptop battery: no SCOPE=Device
		"AC/uevent":   "POWER_SUPPLY_TYPE=Mains\n",
	})
	old := PowerSupplyRoot
	PowerSupplyRoot = root
	defer func() { PowerSupplyRoot = old }()

	got := pollPowerSupply(nil)
	if len(got) != 3 {
		t.Fatalf("got %d devices, want 3: %+v", len(got), got)
	}
	byName := map[string]Status{}
	for _, s := range got {
		byName[s.Name] = s
	}
	kb := byName["Logitech G515 LS TKL"]
	if kb.Percent != 88 || kb.Kind != Keyboard || kb.Charging || kb.Key != "ps:f1-43-66-7c" {
		t.Errorf("keyboard: %+v", kb)
	}
	m := byName["Logitech Wireless Mouse"]
	if m.Percent != 15 || !m.Charging || m.Kind != Mouse {
		t.Errorf("mouse: %+v", m)
	}
	if s := byName["Sleepy"]; !s.Asleep || s.Percent != -1 {
		t.Errorf("offline device: %+v", s)
	}
}

func TestMonitorKeepsLastKnown(t *testing.T) {
	m := NewMonitor()
	m.Update([]Status{{Key: "a", Percent: 60}})
	got := m.Update([]Status{{Key: "a", Percent: -1, Asleep: true}})
	if got[0].Percent != 60 || !got[0].Asleep {
		t.Fatalf("want last known 60 while asleep, got %+v", got[0])
	}
	got = m.Update([]Status{{Key: "a", Percent: 55}})
	if got[0].Percent != 55 {
		t.Fatalf("want fresh 55, got %+v", got[0])
	}
}

func TestSortByKind(t *testing.T) {
	s := []Status{{Name: "H", Kind: Headset}, {Name: "K", Kind: Keyboard}, {Name: "M", Kind: Mouse}}
	Sort(s)
	if s[0].Kind != Mouse || s[1].Kind != Keyboard || s[2].Kind != Headset {
		t.Fatalf("bad order %+v", s)
	}
}
