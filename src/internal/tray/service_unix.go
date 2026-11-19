//go:build !windows

package tray

import (
	"os"
	"syscall"

	"github.com/godbus/dbus/v5"

	"github.com/matz0r/wireless-battery-state/src/internal/install"
	"github.com/matz0r/wireless-battery-state/src/internal/notify"
)

// Linux: a second start reaches the running tray over D-Bus; a restart is an exec.

const (
	busName = "io.github.matz0r.BatteryState"
	busPath = dbus.ObjectPath("/io/github/matz0r/BatteryState")
)

// ShowRunning asks the running tray to open its settings window.
func ShowRunning() error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	return conn.Object(busName, busPath).Call(busName+".ShowSettings", 0).Err
}

type service struct{ a *app }

func (s service) ShowSettings() *dbus.Error {
	go s.a.showWindow()
	return nil
}

// exportService lets a second start (e.g. from the application menu) open the window here.
func (a *app) exportService() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	if conn.Export(service{a}, busPath, busName) != nil {
		conn.Close()
		return
	}
	_, _ = conn.RequestName(busName, dbus.NameFlagDoNotQueue)
}

// restart replaces this process with the installed copy, which takes over the tray and
// opens its settings window.
func (a *app) restart() {
	bin := install.BinPath()
	if _, err := os.Stat(bin); err != nil {
		a.setBusy(busy{msg: "Can't start the installed copy: " + err.Error(), isErr: true})
		return
	}
	a.closeWindow()
	a.closeAll()
	// The lock file and the bus connections are close-on-exec, so the new process can take
	// them over.
	err := syscall.Exec(bin, []string{bin, "--settings", "--interval=" + a.opts.Interval.String()}, os.Environ())
	notify.Send("Battery State", "Can't start the installed copy: "+err.Error())
	os.Exit(1)
}
