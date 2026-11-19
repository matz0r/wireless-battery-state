//go:build !windows

// Package notify shows desktop notifications: org.freedesktop.Notifications on Linux, a
// notification-area balloon (a toast on Windows 10 and 11) on Windows.
package notify

import (
	"github.com/godbus/dbus/v5"
)

// Send shows a notification. Errors are ignored: a missing notification daemon must not
// stop the tray.
func Send(summary, body string) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	obj.Call("org.freedesktop.Notifications.Notify", 0,
		"Battery State", uint32(0), "battery-caution", summary, body,
		[]string{}, map[string]dbus.Variant{}, int32(-1))
}
