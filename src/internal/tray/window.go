package tray

import (
	"errors"
	"fmt"
	"image"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/matz0r/wireless-battery-state/src/internal/battery"
	"github.com/matz0r/wireless-battery-state/src/internal/config"
	"github.com/matz0r/wireless-battery-state/src/internal/icon"
	"github.com/matz0r/wireless-battery-state/src/internal/install"
	"github.com/matz0r/wireless-battery-state/src/internal/notify"
	"github.com/matz0r/wireless-battery-state/src/internal/setup"
	"github.com/matz0r/wireless-battery-state/src/internal/ui"
	"github.com/matz0r/wireless-battery-state/src/internal/update"
)

// The settings window opens on a click on any tray icon: which devices get an icon, start
// on login, update checks (installed copy only) and installing.

// ErrRunning is returned by Run when another tray already runs for this user.
var ErrRunning = errors.New("already running")

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

// busy is the state of a running install or update, shown in the window.
type busy struct {
	text  string // "Installing…", "" when idle
	msg   string // result or error of the last action
	isErr bool
}

var logoCache sync.Map // int → image.Image

func logo(px int) image.Image {
	if img, ok := logoCache.Load(px); ok {
		return img.(image.Image)
	}
	img := icon.AppIcon(px)
	logoCache.Store(px, img)
	return img
}

func (a *app) toggleWindow() {
	a.mu.Lock()
	w := a.win
	a.mu.Unlock()
	if w != nil {
		w.Close()
		return
	}
	a.openWindow()
}

func (a *app) showWindow() {
	a.mu.Lock()
	w := a.win
	a.mu.Unlock()
	if w != nil {
		w.Raise()
		return
	}
	a.openWindow()
}

func (a *app) openWindow() {
	a.mu.Lock()
	if a.win != nil || a.opening {
		a.mu.Unlock()
		return
	}
	a.opening = true
	a.mu.Unlock()

	inst := install.Current()
	a.mu.Lock()
	a.inst = inst
	a.mu.Unlock()
	w, err := ui.Open(ui.Options{
		Title: "Battery State",
		Class: "battery-state",
		Width: 440,
		Icon:  icon.AppIcon,
		Theme: ui.DefaultTheme(),
		Build: a.buildWindow,
	})
	a.mu.Lock()
	a.opening = false
	a.win = w
	a.mu.Unlock()
	if err != nil {
		notify.Send("Battery State", "Can't open the settings window: "+err.Error())
		return
	}
	go func() {
		<-w.Done()
		a.mu.Lock()
		if a.win == w {
			a.win = nil
		}
		a.mu.Unlock()
	}()
}

func (a *app) refreshWindow() {
	a.mu.Lock()
	w := a.win
	a.mu.Unlock()
	if w != nil {
		w.Refresh()
	}
}

func (a *app) closeWindow() {
	a.mu.Lock()
	w := a.win
	a.mu.Unlock()
	if w == nil {
		return
	}
	w.Close()
	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
	}
}

func iconState(d battery.Status) icon.State {
	return icon.State{Percent: d.Percent, Charging: d.Charging, Asleep: d.Asleep, NoAccess: d.NoAccess, Low: battery.Low, Glyph: glyphFor(d.Kind)}
}

// buildWindow lays out the settings window. It runs on the window's goroutine.
func (a *app) buildWindow(b *ui.Builder) {
	a.mu.Lock()
	devs := append([]battery.Status(nil), a.devices...)
	st := a.settings
	st.Hidden = append([]string(nil), st.Hidden...)
	inst, bz, setupMsg := a.inst, a.busy, a.setupMsg
	a.mu.Unlock()
	version := a.opts.Version

	b.Header(logo, "Battery State", "Version "+version)
	switch v := a.offeredUpdate(); {
	case bz.text != "":
		b.Banner(bz.text, nil)
	case v != "":
		b.Banner("Version "+v+" is available", &ui.Button{Label: "Update to " + v, Primary: true, OnClick: func() { go a.runUpdate(v) }})
	}
	if bz.msg != "" {
		b.Message(bz.msg, bz.isErr)
	}

	b.Section("Devices shown in the tray")
	b.Card(func() {
		if len(devs) == 0 {
			b.TextRow("No supported devices found")
		}
		for _, d := range devs {
			shown := !st.IsHidden(d.Key)
			detail := d.Detail()
			if d.Via != "" {
				detail += " · " + d.Via
			}
			b.CheckRow(func(px int) image.Image { return icon.Render(iconState(d), px) },
				d.Name, detail, shown, func() { a.setHidden(d.Key, shown) })
		}
		if setup.NeedsAccess(devs) {
			b.InfoRow("Some devices can't be read", "Installs a udev rule; asks for your password",
				&ui.Button{Label: "Set up access", OnClick: func() { go a.runSetup() }})
		}
	})
	if setupMsg != "" {
		b.Message(setupMsg, true)
	}

	b.Section("Settings")
	b.Card(func() {
		auto := install.AutostartEnabled()
		b.SwitchRow("Start on login", "", auto, func() {
			if err := install.SetAutostart(!auto); err != nil {
				a.setBusy(busy{msg: "Can't change autostart: " + err.Error(), isErr: true})
			}
			a.rebuildMenu()
		})
		if inst.Running && update.Supported() {
			b.SwitchRow("Check for updates", "Looks for a new release on GitHub once a day", st.UpdateCheck,
				func() { a.setUpdateCheck(!st.UpdateCheck) })
		}
	})

	if !inst.Running {
		b.Section("Installation")
		b.Card(func() {
			title, detail, label := installRow(inst, version)
			var btn *ui.Button
			if label != "" {
				btn = &ui.Button{Label: label, Primary: true, Disabled: bz.text != "", OnClick: func() { go a.runInstall() }}
			}
			b.InfoRow(title, detail, btn)
		})
	}

	b.Buttons(
		[]ui.Button{{Label: "Refresh", OnClick: a.triggerRefresh}},
		[]ui.Button{{Label: "Quit Battery State", OnClick: a.quitApp}},
	)
}

// installRow describes the installed copy; label is the install button's text, or "" when
// installing this copy would change nothing or be a downgrade.
func installRow(inst install.State, version string) (title, detail, label string) {
	switch {
	case !inst.Installed:
		return "Not installed", "Into ~/.local/bin and the application menu", "Install"
	case inst.Same:
		return "Installed", "The installed copy is the same as this one", ""
	case update.Newer(inst.Version, version):
		return "Version " + inst.Version + " is installed", "It is newer than this copy (" + version + ")", ""
	case inst.Version == version:
		return "Version " + inst.Version + " is installed", "A different build; replace it with this one", "Install"
	case inst.Version != "":
		return "Version " + inst.Version + " is installed", "Replace it with this copy (" + version + ")", "Install"
	default:
		return "Another copy is installed", "Replace it with this copy (" + version + ")", "Install"
	}
}

func (a *app) setBusy(bz busy) {
	a.mu.Lock()
	a.busy = bz
	a.mu.Unlock()
	a.refreshWindow()
}

func (a *app) saveSettings() {
	a.mu.Lock()
	s := a.settings
	s.Hidden = append([]string(nil), s.Hidden...)
	a.mu.Unlock()
	if err := config.Save(s); err != nil {
		a.setBusy(busy{msg: "Can't save the settings: " + err.Error(), isErr: true})
	}
}

func (a *app) setHidden(key string, hidden bool) {
	a.mu.Lock()
	a.settings.SetHidden(key, hidden)
	devs := append([]battery.Status(nil), a.devices...)
	a.mu.Unlock()
	a.saveSettings()
	go func() {
		if err := a.apply(devs); err != nil {
			fmt.Fprintln(os.Stderr, "battery-state:", err)
		}
	}()
}

func (a *app) quitApp() { a.quitOnce.Do(func() { close(a.quit) }) }

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

func (a *app) runInstall() {
	a.setBusy(busy{text: "Installing…"})
	if _, err := install.Install(); err != nil {
		a.setBusy(busy{msg: "Install failed: " + err.Error(), isErr: true})
		return
	}
	a.restart()
}

// Updates (installed copy only), like bibi-brightness: checked at most once a day while
// "Check for updates" is on; installing is a click.

func (a *app) agent() string { return "battery-state/" + a.opts.Version }

func (a *app) updateEligible() bool { return update.Supported() && install.Running() }

// offeredUpdate is the newer release the window and menu offer, or "".
func (a *app) offeredUpdate() string {
	a.mu.Lock()
	on, latest := a.settings.UpdateCheck, a.settings.UpdateLatest
	a.mu.Unlock()
	if on && update.Newer(latest, a.opts.Version) && a.updateEligible() {
		return latest
	}
	return ""
}

func (a *app) setUpdateCheck(on bool) {
	a.mu.Lock()
	a.settings.UpdateCheck = on
	a.mu.Unlock()
	a.saveSettings()
	if on {
		go a.checkUpdate()
	}
	a.rebuildMenu()
}

// maybeCheckUpdate starts a check when it is on and the last one is a day old.
func (a *app) maybeCheckUpdate() {
	a.mu.Lock()
	on, last := a.settings.UpdateCheck, a.settings.UpdateLastCheck
	a.mu.Unlock()
	if on && update.Due(last, time.Now()) && a.updateEligible() {
		go a.checkUpdate()
	}
}

func (a *app) checkUpdate() {
	if !a.checking.CompareAndSwap(false, true) {
		return
	}
	defer a.checking.Store(false)
	update.RemoveLeftovers()
	v, err := update.Latest(a.agent())
	if err != nil {
		// Offline is normal; a failed check is not recorded, so the next one tries again.
		fmt.Fprintln(os.Stderr, "battery-state: update check failed:", err)
		return
	}
	a.mu.Lock()
	prev := a.settings.UpdateLatest
	a.settings.UpdateLastCheck = time.Now().Unix()
	a.settings.UpdateLatest = v
	a.mu.Unlock()
	a.saveSettings()
	if v != prev && update.Newer(v, a.opts.Version) {
		notify.Send("Battery State "+v+" is available", "Click a battery icon to update.")
	}
	a.rebuildMenu()
	a.refreshWindow()
}

func (a *app) runUpdate(v string) {
	a.mu.Lock()
	if a.busy.text != "" {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.setBusy(busy{text: "Downloading and installing version " + v + "…"})
	if err := update.Install(v, a.agent()); err != nil {
		a.setBusy(busy{msg: "Update failed: " + err.Error(), isErr: true})
		return
	}
	a.restart()
}
