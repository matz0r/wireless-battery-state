// Package config loads and saves the user's settings in
// $XDG_CONFIG_HOME/battery-state/settings.json.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Settings are the choices made in the settings window.
type Settings struct {
	// Hidden lists the keys (battery.Status.Key) of devices that get no tray icon.
	Hidden []string `json:"hidden,omitempty"`
	// UpdateCheck: look for a newer release once a day (installed copy only).
	UpdateCheck bool `json:"update_check"`
	// UpdateLastCheck is the Unix time of the last successful check.
	UpdateLastCheck int64 `json:"update_last_check,omitempty"`
	// UpdateLatest is the newest release found by that check ("1.2.3").
	UpdateLatest string `json:"update_latest,omitempty"`
}

// Path is the settings file.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "battery-state", "settings.json")
}

// Load reads the settings. A missing or broken file gives the defaults.
func Load() Settings {
	var s Settings
	data, err := os.ReadFile(Path())
	if err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

// Save writes the settings atomically.
func Save(s Settings) error {
	sort.Strings(s.Hidden)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// IsHidden reports whether the device with this key is hidden.
func (s Settings) IsHidden(key string) bool {
	for _, k := range s.Hidden {
		if k == key {
			return true
		}
	}
	return false
}

// SetHidden hides or shows the device with this key.
func (s *Settings) SetHidden(key string, hidden bool) {
	out := s.Hidden[:0:0]
	for _, k := range s.Hidden {
		if k != key {
			out = append(out, k)
		}
	}
	if hidden {
		out = append(out, key)
	}
	s.Hidden = out
}
