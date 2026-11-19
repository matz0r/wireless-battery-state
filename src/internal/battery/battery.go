// Package battery finds wireless devices and reads their battery levels.
package battery

import (
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
)

// Kind is the type of device, used for ordering and the tooltip.
type Kind int

const (
	Mouse Kind = iota
	Keyboard
	Headset
	Other
)

func (k Kind) String() string {
	switch k {
	case Mouse:
		return "mouse"
	case Keyboard:
		return "keyboard"
	case Headset:
		return "headset"
	default:
		return "device"
	}
}

// Status is one device's battery state.
type Status struct {
	Key      string // stable id, used to keep a tray icon per device
	Name     string
	Kind     Kind
	Via      string // "2.4 GHz receiver", "USB base station", ...
	Percent  int    // 0..100, -1 if unknown
	Charging bool
	// Asleep: the receiver is there but the device does not answer right now.
	// Percent then holds the last known value (or -1).
	Asleep bool
	// NoAccess: the device was found but its hidraw node can't be opened (see --setup).
	NoAccess bool
	Path     string // hidraw node or sysfs path, for diagnostics
}

// Detail is a short human-readable state, e.g. "88%, charging".
func (s Status) Detail() string {
	var parts []string
	switch {
	case s.NoAccess:
		return "no access (use \"Set up access\")"
	case s.Percent >= 0:
		parts = append(parts, strconv.Itoa(s.Percent)+"%")
	default:
		parts = append(parts, "unknown")
	}
	if s.Charging {
		parts = append(parts, "charging")
	}
	if s.Asleep {
		if s.Percent >= 0 {
			parts = append(parts, "asleep, last known")
		} else {
			parts = append(parts, "asleep")
		}
	}
	return strings.Join(parts, ", ")
}

// Low is the level at or below which a device counts as low.
const Low = 15

// IsLow reports whether the device should warn: low, known and not charging.
func (s Status) IsLow() bool { return !s.Charging && !s.NoAccess && s.Percent >= 0 && s.Percent <= Low }

// HID is what the drivers need from an open HID node. *hidraw.Device implements it;
// tests use fakes.
type HID interface {
	Write(report []byte) (int, error)
	Read(buf []byte, timeout time.Duration) (int, error)
	Flush()
	SetFeature(report []byte) error
	GetFeature(buf []byte) (int, error)
	Close() error
}

// Env is the world the drivers poll: the hidraw nodes and a way to open them.
type Env struct {
	Nodes []hidraw.Info
	Open  func(path string) (HID, error)
	Sleep func(time.Duration)
}

func (e *Env) sleep(d time.Duration) {
	if e.Sleep != nil {
		e.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Driver polls one family of devices.
type Driver struct {
	Name string
	Poll func(env *Env) []Status
	// Udev lists vendor:product ids (lower-case hex) whose hidraw nodes need user access.
	Udev []UdevID
}

// UdevID is a USB vendor id with the product ids that need a udev rule.
type UdevID struct {
	Vendor   uint16
	Products []uint16
}

// Drivers are all supported device families, in polling order. platformDrivers adds what
// differs per OS: the kernel's power_supply batteries on Linux, Logitech HID++ on Windows.
var Drivers = append([]Driver{gloriousDriver, astroDriver}, platformDrivers...)

// DefaultEnv reads the real hidraw nodes.
func DefaultEnv() (*Env, error) {
	nodes, err := hidraw.Enumerate()
	if err != nil {
		return nil, err
	}
	return &Env{
		Nodes: nodes,
		Open: func(path string) (HID, error) {
			d, err := hidraw.Open(path)
			if err != nil {
				return nil, err
			}
			return d, nil
		},
	}, nil
}

func isPermission(err error) bool { return errors.Is(err, os.ErrPermission) }

// Poll runs every driver once and returns the devices found, ordered by kind and name.
func Poll(env *Env) []Status {
	var out []Status
	for _, d := range Drivers {
		out = append(out, d.Poll(env)...)
	}
	Sort(out)
	return out
}

// Sort orders devices by kind (mouse, keyboard, headset, other), then name, then key.
func Sort(s []Status) {
	sort.SliceStable(s, func(a, b int) bool {
		if s[a].Kind != s[b].Kind {
			return s[a].Kind < s[b].Kind
		}
		if s[a].Name != s[b].Name {
			return s[a].Name < s[b].Name
		}
		return s[a].Key < s[b].Key
	})
}

// Monitor keeps the last known level of each device, so a device that falls asleep keeps
// showing its last reading instead of "unknown".
type Monitor struct {
	mu   sync.Mutex
	last map[string]Status
}

// NewMonitor returns an empty monitor.
func NewMonitor() *Monitor { return &Monitor{last: map[string]Status{}} }

// Update merges a fresh poll with the remembered readings and returns the result.
func (m *Monitor) Update(fresh []Status) []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, len(fresh))
	for i, s := range fresh {
		if (s.Asleep || s.NoAccess) && s.Percent < 0 {
			if prev, ok := m.last[s.Key]; ok && prev.Percent >= 0 {
				s.Percent = prev.Percent
				if s.NoAccess {
					s.Charging = prev.Charging
				}
			}
		}
		if s.Percent >= 0 && !s.Asleep && !s.NoAccess {
			m.last[s.Key] = s
		}
		out[i] = s
	}
	return out
}

// GuessKind guesses the device kind from its name.
func GuessKind(name string) Kind {
	n := strings.ToLower(name)
	switch {
	case containsAny(n, "mouse", "model o", "model d", "mx master", "mx anywhere", "trackball", "superlight", "g502", "g305", "g703", "g903", "g pro"):
		return Mouse
	case containsAny(n, "keyboard", "keys", "tkl", "k380", "k400", "k800", "g915", "g515", "g715", "craft"):
		return Keyboard
	case containsAny(n, "headset", "headphone", "a50", "a40", "a20", "arctis", "earbuds", "buds", "g533", "g535", "g733", "g935"):
		return Headset
	}
	return Other
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// KindFromDescriptor guesses mouse or keyboard from the HID collections.
func KindFromDescriptor(d hidraw.Descriptor) Kind {
	mouse, kbd := d.Has(0x01, 0x02), d.Has(0x01, 0x06)
	switch {
	case mouse && !kbd:
		return Mouse
	case kbd && !mouse:
		return Keyboard
	}
	return Other
}
