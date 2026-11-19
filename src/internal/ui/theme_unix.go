//go:build !windows

package ui

import (
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
)

// PrefersDark asks the XDG desktop portal for the color scheme (1 = dark, 2 = light). Without
// an answer it looks at GTK_THEME and the GTK settings.ini.
func PrefersDark() bool {
	if conn, err := dbus.SessionBus(); err == nil {
		obj := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
		var v dbus.Variant
		err := obj.Call("org.freedesktop.portal.Settings.ReadOne", 0, "org.freedesktop.appearance", "color-scheme").Store(&v)
		if err != nil { // portals older than version 2 only have Read, which wraps the value twice
			err = obj.Call("org.freedesktop.portal.Settings.Read", 0, "org.freedesktop.appearance", "color-scheme").Store(&v)
			if inner, ok := v.Value().(dbus.Variant); ok {
				v = inner
			}
		}
		if err == nil {
			if n, ok := v.Value().(uint32); ok && n != 0 {
				return n == 1
			}
		}
	}
	if t := strings.ToLower(os.Getenv("GTK_THEME")); t != "" {
		return strings.Contains(t, "dark")
	}
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(home + "/.config/gtk-3.0/settings.ini")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.ToLower(strings.TrimSpace(v))
		if k == "gtk-application-prefer-dark-theme" && (v == "1" || v == "true") {
			return true
		}
		if k == "gtk-theme-name" && strings.Contains(v, "dark") {
			return true
		}
	}
	return false
}
