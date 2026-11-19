// Package tray runs the tray: one icon per device, a shared menu, polling and notifications.
package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/battery"
	"github.com/matz0r/wireless-battery-state/src/internal/config"
	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
	"github.com/matz0r/wireless-battery-state/src/internal/icon"
	"github.com/matz0r/wireless-battery-state/src/internal/install"
	"github.com/matz0r/wireless-battery-state/src/internal/notify"
	"github.com/matz0r/wireless-battery-state/src/internal/setup"
	"github.com/matz0r/wireless-battery-state/src/internal/ui"
)

// Options configure the tray.
type Options struct {
	Interval time.Duration
	Version  string
	// OpenSettings opens the settings window right after start.
	OpenSettings bool
}

const placeholderKey = "\x00none"

type app struct {
	opts    Options
	menu    *menuState
	monitor *battery.Monitor

	mu       sync.Mutex
	items    map[string]*item
	devices  []battery.Status // all devices, hidden ones included
	warned   map[string]bool  // low-battery notification already shown
	setupMsg string
	settings config.Settings

	win      *ui.Window
	opening  bool
	inst     install.State
	busy     busy
	checking atomic.Bool

	refresh  chan struct{}
	quit     chan struct{}
	quitOnce sync.Once
}

// Run shows the tray icons and blocks until Quit is chosen. It returns ErrRunning if a tray
// already runs for this user.
func Run(opts Options) error {
	unlock, err := singleInstance()
	if err != nil {
		return err
	}
	defer unlock()

	a := &app{
		opts:     opts,
		menu:     &menuState{},
		monitor:  battery.NewMonitor(),
		items:    map[string]*item{},
		warned:   map[string]bool{},
		refresh:  make(chan struct{}, 1),
		quit:     make(chan struct{}),
		settings: config.Load(),
	}
	a.rebuildMenu()
	a.exportService()

	// The first poll runs before the loop so a missing tray is reported as an error.
	if err := a.poll(); err != nil {
		return err
	}
	go a.watchHotplug()
	if opts.OpenSettings {
		go a.showWindow()
	}
	a.maybeCheckUpdate()
	updates := time.NewTicker(time.Hour)
	defer updates.Stop()

	timer := time.NewTimer(opts.Interval)
	for {
		select {
		case <-updates.C:
			a.maybeCheckUpdate()
			continue
		case <-timer.C:
		case <-a.refresh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-a.quit:
			a.closeWindow()
			a.closeAll()
			return nil
		}
		if err := a.poll(); err != nil {
			fmt.Fprintln(os.Stderr, "battery-state:", err)
		}
		timer.Reset(opts.Interval)
	}
}

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
	a.notifyLow(devs)
	return a.apply(devs)
}

// visible drops the devices hidden in the settings.
func (a *app) visible(devs []battery.Status) []battery.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []battery.Status
	for _, d := range devs {
		if !a.settings.IsHidden(d.Key) {
			out = append(out, d)
		}
	}
	return out
}

// apply makes the tray icons match the devices: adds, updates and removes icons. Hidden
// devices get no icon; with no icon left the app icon stands in, so the settings stay
// reachable.
func (a *app) apply(all []battery.Status) error {
	a.mu.Lock()
	a.devices = all
	a.mu.Unlock()
	a.rebuildMenu()
	a.refreshWindow()
	devs := a.visible(all)
	placeholderText := "No supported devices found"
	if len(all) > 0 {
		placeholderText = "All devices are hidden; click for the settings"
	}

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
	for i, key := range order {
		title, icons, tip := a.look(key, want[key], i, placeholderText)
		if it, ok := a.items[key]; ok {
			it.update(title, icons, tip)
			continue
		}
		it, err := newItem(itemID(key), title, icons, tip, a.menu, a.toggleWindow)
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

// look returns an icon's title, pixmaps and tooltip. Trays sort their icons by title
// (Cinnamon alphabetically), so the titles share a prefix and carry the position: our icons
// stay together, in device order (mouse, keyboard, headset), and keep their place when a
// level changes.
func (a *app) look(key string, d battery.Status, pos int, placeholderText string) (string, []pixmap, tooltip) {
	if key == placeholderKey {
		icons := pixmaps(func(size int) []byte { return icon.ARGB32(icon.AppIcon(size)) })
		return "Battery State", icons, tooltip{Title: "Battery State", Text: placeholderText}
	}
	st := iconState(d)
	icons := pixmaps(func(size int) []byte { return icon.ARGB32(icon.Render(st, size)) })
	text := d.Detail()
	if d.Via != "" {
		text += "\n" + d.Via
	}
	title := fmt.Sprintf("Battery State %02d %s", pos+1, d.Name)
	return title, icons, tooltip{Title: d.Name, Text: text, Pixmaps: icons}
}

func glyphFor(k battery.Kind) icon.Glyph {
	switch k {
	case battery.Mouse:
		return icon.MouseGlyph
	case battery.Keyboard:
		return icon.KeyboardGlyph
	case battery.Headset:
		return icon.HeadsetGlyph
	}
	return icon.NoGlyph
}

func pixmaps(render func(size int) []byte) []pixmap {
	var out []pixmap
	for _, s := range icon.Sizes {
		out = append(out, pixmap{W: int32(s), H: int32(s), Pix: render(s)})
	}
	return out
}

func itemID(key string) string {
	r := strings.NewReplacer(":", "-", "/", "-", " ", "-", "\x00", "")
	return "battery-state-" + r.Replace(key)
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
	items = append(items, menuItem{Label: "Settings…", Enabled: true, Action: a.showWindow})
	if v := a.offeredUpdate(); v != "" {
		items = append(items, menuItem{Label: "Update to " + v, Enabled: true, Action: func() {
			a.showWindow()
			a.runUpdate(v)
		}})
	}
	items = append(items, menuItem{Label: "Refresh now", Enabled: true, Action: a.triggerRefresh})
	if setup.NeedsAccess(devs) {
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
	items = append(items, menuItem{Label: "Quit", Enabled: true, Action: a.quitApp})
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
	a.refreshWindow()
	// udev applies the new permissions asynchronously; give it a moment.
	time.Sleep(time.Second)
	a.triggerRefresh()
}

func (a *app) notifyLow(devs []battery.Status) {
	devs = a.visible(devs) // hidden devices don't notify either
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

// watchHotplug polls quickly when hidraw nodes or batteries come or go, so a plugged-in
// receiver shows up within seconds instead of at the next interval.
func (a *app) watchHotplug() {
	last := hotplugSignature()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
			if sig := hotplugSignature(); sig != last {
				last = sig
				time.Sleep(1500 * time.Millisecond) // let the device finish setting up
				a.triggerRefresh()
			}
		}
	}
}

func hotplugSignature() string {
	var names []string
	for _, dir := range []string{hidraw.SysRoot, battery.PowerSupplyRoot} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// singleInstance makes sure only one tray runs per user.
func singleInstance() (func(), error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("battery-state-%d.lock", os.Getuid()))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrRunning
	}
	return func() { f.Close() }, nil
}
