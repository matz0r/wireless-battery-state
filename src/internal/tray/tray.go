// Package tray runs the tray: one icon per device, a shared menu, polling and notifications.
// The logic here is shared; the icons themselves come from a platform backend:
// StatusNotifierItem over D-Bus on Linux (sni_unix.go), the notification area on Windows
// (tray_windows.go).
package tray

import (
	"fmt"
	"image"
	"os"
	"sync"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/battery"
	"github.com/matz0r/wireless-battery-state/src/internal/icon"
	"github.com/matz0r/wireless-battery-state/src/internal/install"
	"github.com/matz0r/wireless-battery-state/src/internal/notify"
	"github.com/matz0r/wireless-battery-state/src/internal/setup"
)

// Options configure the tray.
type Options struct {
	Interval time.Duration
	Version  string
}

const placeholderKey = "\x00none"

// appearance is what one tray icon shows.
type appearance struct {
	Title    string                      // one line, e.g. "Model D-: 77%"
	Render   func(size int) *image.NRGBA // the icon at any size
	TipTitle string
	TipText  string
	TipIcon  bool // show the icon in the tooltip too (devices, not the placeholder)
}

// platform is a tray backend.
type platform interface {
	// newItem shows a new icon; key is the stable device key.
	newItem(key string, look appearance, m *menuState) (trayItem, error)
	// watchHotplug calls changed when devices may have come or gone, until quit is closed.
	watchHotplug(changed func(), quit <-chan struct{})
	// stop releases what the backend holds, after all items are closed.
	stop()
}

type trayItem interface {
	update(look appearance)
	close()
}

type app struct {
	opts     Options
	platform platform
	menu     *menuState
	monitor  *battery.Monitor

	mu       sync.Mutex
	items    map[string]trayItem
	devices  []battery.Status
	warned   map[string]bool // low-battery notification already shown
	setupMsg string

	refresh  chan struct{}
	quit     chan struct{}
	quitOnce sync.Once
}

// Run shows the tray icons and blocks until Quit is chosen.
func Run(opts Options) error {
	unlock, err := singleInstance()
	if err != nil {
		return err
	}
	defer unlock()

	a := &app{
		opts:    opts,
		menu:    &menuState{},
		monitor: battery.NewMonitor(),
		items:   map[string]trayItem{},
		warned:  map[string]bool{},
		refresh: make(chan struct{}, 1),
		quit:    make(chan struct{}),
	}
	p, err := newPlatform(a.requestQuit)
	if err != nil {
		return err
	}
	defer p.stop()
	a.platform = p
	a.rebuildMenu()

	// The first poll runs before the loop so a missing tray is reported as an error.
	if err := a.poll(); err != nil {
		a.closeAll()
		return err
	}
	go p.watchHotplug(a.triggerRefresh, a.quit)

	timer := time.NewTimer(opts.Interval)
	for {
		select {
		case <-timer.C:
		case <-a.refresh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-a.quit:
			a.closeAll()
			return nil
		}
		if err := a.poll(); err != nil {
			fmt.Fprintln(os.Stderr, "battery-state:", err)
		}
		timer.Reset(opts.Interval)
	}
}

func (a *app) requestQuit() { a.quitOnce.Do(func() { close(a.quit) }) }

func (a *app) triggerRefresh() {
	select {
	case a.refresh <- struct{}{}:
	default:
	}
}

func (a *app) poll() error {
	env, err := battery.DefaultEnv()
	if err != nil {
		return err
	}
	devs := a.monitor.Update(battery.Poll(env))
	err = a.apply(devs)
	a.notifyLow(devs) // after apply: on Windows a notification needs an icon to come from
	return err
}

// apply makes the tray icons match the devices: adds, updates and removes icons.
func (a *app) apply(devs []battery.Status) error {
	a.mu.Lock()
	a.devices = devs
	a.mu.Unlock()
	a.rebuildMenu()

	want := map[string]battery.Status{}
	var order []string
	for _, d := range devs {
		want[d.Key] = d
		order = append(order, d.Key)
	}
	if len(devs) == 0 {
		order = []string{placeholderKey}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for key, it := range a.items {
		if _, ok := want[key]; !ok && !(key == placeholderKey && len(devs) == 0) {
			it.close()
			delete(a.items, key)
		}
	}
	var firstErr error
	for _, key := range order {
		look := a.look(key, want[key])
		if it, ok := a.items[key]; ok {
			it.update(look)
			continue
		}
		it, err := a.platform.newItem(key, look, a.menu)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		a.items[key] = it
	}
	return firstErr
}

func (a *app) look(key string, d battery.Status) appearance {
	if key == placeholderKey {
		return appearance{Title: "Battery State", Render: icon.AppIcon, TipTitle: "Battery State", TipText: "No supported devices found"}
	}
	st := icon.State{Percent: d.Percent, Charging: d.Charging, Asleep: d.Asleep, NoAccess: d.NoAccess, Low: battery.Low}
	text := d.Detail()
	if d.Via != "" {
		text += "\n" + d.Via
	}
	return appearance{
		Title:    d.Name + ": " + d.Detail(),
		Render:   func(size int) *image.NRGBA { return icon.Render(st, size) },
		TipTitle: d.Name,
		TipText:  text,
		TipIcon:  true,
	}
}

func (a *app) rebuildMenu() {
	a.mu.Lock()
	devs := append([]battery.Status(nil), a.devices...)
	setupMsg := a.setupMsg
	a.mu.Unlock()

	var items []menuItem
	if len(devs) == 0 {
		items = append(items, menuItem{Label: "No supported devices found"})
	}
	for _, d := range devs {
		items = append(items, menuItem{Label: fmt.Sprintf("%s: %s", d.Name, d.Detail())})
	}
	items = append(items, menuItem{Separator: true})
	items = append(items, menuItem{Label: "Refresh now", Enabled: true, Action: a.triggerRefresh})
	if setup.Available && setup.NeedsAccess(devs) {
		items = append(items, menuItem{Label: "Set up access…", Enabled: true, Action: a.runSetup})
	}
	if setupMsg != "" {
		items = append(items, menuItem{Label: setupMsg})
	}
	auto := install.AutostartEnabled()
	items = append(items, menuItem{Label: "Start on login", Enabled: true, Checkbox: true, Checked: auto, Action: func() {
		if err := install.SetAutostart(!auto); err != nil {
			notify.Send("Battery State", "Can't change autostart: "+err.Error())
		}
		a.rebuildMenu()
	}})
	items = append(items, menuItem{Separator: true})
	items = append(items, menuItem{Label: "Quit", Enabled: true, Action: a.requestQuit})
	a.menu.set(items)
}

func (a *app) runSetup() {
	msg := ""
	if err := setup.Run(); err != nil {
		msg = "Setup: " + err.Error()
	}
	a.mu.Lock()
	a.setupMsg = msg
	a.mu.Unlock()
	// udev applies the new permissions asynchronously; give it a moment.
	time.Sleep(time.Second)
	a.triggerRefresh()
}

func (a *app) notifyLow(devs []battery.Status) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, d := range devs {
		switch {
		case d.IsLow() && !d.Asleep && !a.warned[d.Key]:
			a.warned[d.Key] = true
			notify.Send("Low battery", fmt.Sprintf("%s: %d%%", d.Name, d.Percent))
		case d.Charging || d.Percent > battery.Low+5:
			delete(a.warned, d.Key)
		}
	}
}

func (a *app) closeAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, it := range a.items {
		it.close()
		delete(a.items, k)
	}
}
