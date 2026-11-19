package tray

import (
	"fmt"
	"image"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

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

// windowState is the app's settings-window state.
type windowState struct {
	win      *ui.Window
	opening  bool
	inst     install.State
	busy     busy
	checking atomic.Bool
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
	w := a.ws.win
	a.mu.Unlock()
	if w != nil {
		w.Close()
		return
	}
	a.openWindow()
}

func (a *app) showWindow() {
	a.mu.Lock()
	w := a.ws.win
	a.mu.Unlock()
	if w != nil {
		w.Raise()
		return
	}
	a.openWindow()
}

func (a *app) openWindow() {
	a.mu.Lock()
	if a.ws.win != nil || a.ws.opening {
		a.mu.Unlock()
		return
	}
	a.ws.opening = true
	a.mu.Unlock()

	inst := install.Current()
	a.mu.Lock()
	a.ws.inst = inst
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
	a.ws.opening = false
	a.ws.win = w
	a.mu.Unlock()
	if err != nil {
		notify.Send("Battery State", "Can't open the settings window: "+err.Error())
		return
	}
	go func() {
		<-w.Done()
		a.mu.Lock()
		if a.ws.win == w {
			a.ws.win = nil
		}
		a.mu.Unlock()
	}()
}

func (a *app) refreshWindow() {
	a.mu.Lock()
	w := a.ws.win
	a.mu.Unlock()
	if w != nil {
		w.Refresh()
	}
}

func (a *app) closeWindow() {
	a.mu.Lock()
	w := a.ws.win
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

// buildWindow lays out the settings window. It runs on the window's goroutine.
func (a *app) buildWindow(b *ui.Builder) {
	a.mu.Lock()
	devs := append([]battery.Status(nil), a.devices...)
	st := a.settings
	st.Hidden = append([]string(nil), st.Hidden...)
	inst, bz, setupMsg := a.ws.inst, a.ws.busy, a.setupMsg
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
		if setup.Available && setup.NeedsAccess(devs) {
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
		[]ui.Button{{Label: "Quit Battery State", OnClick: a.requestQuit}},
	)
}

// installRow describes the installed copy; label is the install button's text, or "" when
// installing this copy would change nothing or be a downgrade.
func installRow(inst install.State, version string) (title, detail, label string) {
	switch {
	case !inst.Installed:
		return "Not installed", installHint(), "Install"
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

func installHint() string {
	if runtime.GOOS == "windows" {
		return `Into %LOCALAPPDATA%\Programs and the Start menu`
	}
	return "Into ~/.local/bin and the application menu"
}

func (a *app) setBusy(bz busy) {
	a.mu.Lock()
	a.ws.busy = bz
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
	if !a.ws.checking.CompareAndSwap(false, true) {
		return
	}
	defer a.ws.checking.Store(false)
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
	if a.ws.busy.text != "" {
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
