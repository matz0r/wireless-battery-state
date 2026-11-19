package tray

import (
	"os"
	"os/exec"

	"golang.org/x/sys/windows"

	"github.com/matz0r/wireless-battery-state/src/internal/install"
)

// Windows: a second start signals a named event the running tray waits on; a restart starts
// the installed copy after this process has given up its icons and the single-instance mutex.

const showEventName = `Local\battery-state-show-settings`

var procAllowSetForeground = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

// ShowRunning asks the running tray to open its settings window.
func ShowRunning() error {
	name, _ := windows.UTF16PtrFromString(showEventName)
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	// This process was started by the user, so it may hand the foreground to the tray.
	const asfwAny = ^uintptr(0)
	procAllowSetForeground.Call(asfwAny)
	return windows.SetEvent(h)
}

// exportService waits for ShowRunning from a second start (e.g. the Start menu entry).
func (a *app) exportService() {
	name, _ := windows.UTF16PtrFromString(showEventName)
	h, err := windows.CreateEvent(nil, 0, 0, name) // auto-reset
	if err != nil {
		return
	}
	go func() {
		for {
			ev, err := windows.WaitForSingleObject(h, windows.INFINITE)
			if err != nil || ev != windows.WAIT_OBJECT_0 {
				return
			}
			go a.showWindow()
		}
	}()
}

// restart hands the tray over to the installed copy, which opens its settings window.
func (a *app) restart() {
	bin := install.BinPath()
	if _, err := os.Stat(bin); err != nil {
		a.setBusy(busy{msg: "Can't start the installed copy: " + err.Error(), isErr: true})
		return
	}
	a.closeWindow()
	a.closeAll()
	if a.unlock != nil {
		a.unlock() // the new copy checks the mutex at start
	}
	cmd := exec.Command(bin, "--settings", "--interval="+a.opts.Interval.String())
	if err := cmd.Start(); err != nil {
		fatalBox("Can't start the installed copy: " + err.Error())
	}
	os.Exit(0)
}

// fatalBox reports an error in a message box and exits: with the icons gone there is no other
// way to tell the user.
func fatalBox(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	title, _ := windows.UTF16PtrFromString("Battery State")
	windows.MessageBox(0, t, title, windows.MB_OK|windows.MB_ICONERROR)
	os.Exit(1)
}
