package battery

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
)

// Devices whose kernel driver already reports a battery in /sys/class/power_supply with
// SCOPE=Device: Logitech HID++ mice and keyboards on Unifying / Bolt / Lightspeed receivers
// (driver hid-logitech-hidpp, "hidpp_battery_N"), Bluetooth devices, game controllers, ...
// No extra permissions are needed for these.

// PowerSupplyRoot is where the kernel lists batteries; tests point it at a fake tree.
var PowerSupplyRoot = "/sys/class/power_supply"

var powerSupplyDriver = Driver{Name: "power_supply", Poll: pollPowerSupply}

func pollPowerSupply(_ *Env) []Status {
	entries, err := os.ReadDir(PowerSupplyRoot)
	if err != nil {
		return nil
	}
	var out []Status
	for _, e := range entries {
		dir := filepath.Join(PowerSupplyRoot, e.Name())
		u := readUevent(filepath.Join(dir, "uevent"))
		if u["POWER_SUPPLY_TYPE"] != "Battery" || u["POWER_SUPPLY_SCOPE"] != "Device" {
			continue // laptop batteries, AC adapters, ...
		}
		name := strings.TrimSpace(u["POWER_SUPPLY_MODEL_NAME"])
		maker := strings.TrimSpace(u["POWER_SUPPLY_MANUFACTURER"])
		if name == "" {
			name = e.Name()
		}
		if maker != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(maker)) {
			name = maker + " " + name
		}
		key := "ps:" + e.Name()
		if serial := u["POWER_SUPPLY_SERIAL_NUMBER"]; serial != "" {
			key = "ps:" + serial
		}
		s := Status{Key: key, Name: name, Percent: -1, Path: dir, Via: viaFor(e.Name())}
		if v, err := strconv.Atoi(u["POWER_SUPPLY_CAPACITY"]); err == nil && v >= 0 && v <= 100 {
			s.Percent = v
		} else {
			s.Percent = levelPercent(u["POWER_SUPPLY_CAPACITY_LEVEL"])
		}
		switch u["POWER_SUPPLY_STATUS"] {
		case "Charging", "Full":
			s.Charging = true
		}
		if u["POWER_SUPPLY_ONLINE"] == "0" {
			s.Asleep = true
			s.Percent = -1 // the kernel keeps a stale value; let the monitor show the last good one
		}
		s.Kind = GuessKind(name)
		if s.Kind == Other {
			s.Kind = kindFromParent(dir)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Key < out[b].Key })
	return out
}

func viaFor(psName string) string {
	switch {
	case strings.HasPrefix(psName, "hidpp_battery"):
		return "Logitech receiver"
	default:
		return "kernel"
	}
}

func levelPercent(level string) int {
	switch level {
	case "Full":
		return 100
	case "High":
		return 80
	case "Normal":
		return 50
	case "Low":
		return 15
	case "Critical":
		return 5
	}
	return -1
}

// kindFromParent looks at the HID device the battery belongs to (power_supply/X/device) and
// guesses mouse or keyboard from its report descriptor.
func kindFromParent(dir string) Kind {
	desc, err := os.ReadFile(filepath.Join(dir, "device", "report_descriptor"))
	if err != nil {
		return Other
	}
	return KindFromDescriptor(hidraw.ParseDescriptor(desc))
}

func readUevent(path string) map[string]string {
	m := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			m[k] = v
		}
	}
	return m
}
