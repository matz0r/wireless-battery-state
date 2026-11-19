//go:build !windows

package tray

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/battery"
	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
	"github.com/matz0r/wireless-battery-state/src/internal/icon"
)

// sniPlatform shows each device as a StatusNotifierItem on its own session-bus connection.
type sniPlatform struct{}

func newPlatform(quit func()) (platform, error) { return sniPlatform{}, nil }

func (sniPlatform) stop() {}

func (sniPlatform) newItem(key string, look appearance, m *menuState, activate func()) (trayItem, error) {
	icons := pixmaps(look.Render)
	it, err := newItem(itemID(key), look.Title, icons, sniTooltip(look, icons), m, activate)
	if err != nil {
		return nil, err
	}
	return sniItem{it}, nil
}

type sniItem struct{ it *item }

func (s sniItem) update(look appearance) {
	icons := pixmaps(look.Render)
	s.it.update(look.Title, icons, sniTooltip(look, icons))
}

func (s sniItem) close() { s.it.close() }

func sniTooltip(look appearance, icons []pixmap) tooltip {
	t := tooltip{Title: look.TipTitle, Text: look.TipText}
	if look.TipIcon {
		t.Pixmaps = icons
	}
	return t
}

func pixmaps(render func(size int) *image.NRGBA) []pixmap {
	var out []pixmap
	for _, s := range icon.Sizes {
		out = append(out, pixmap{W: int32(s), H: int32(s), Pix: icon.ARGB32(render(s))})
	}
	return out
}

func itemID(key string) string {
	r := strings.NewReplacer(":", "-", "/", "-", " ", "-", "\x00", "")
	return "battery-state-" + r.Replace(key)
}

// watchHotplug polls quickly when hidraw nodes or batteries come or go, so a plugged-in
// receiver shows up within seconds instead of at the next interval.
func (sniPlatform) watchHotplug(changed func(), quit <-chan struct{}) {
	last := hotplugSignature()
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-quit:
			return
		case <-t.C:
			if sig := hotplugSignature(); sig != last {
				last = sig
				time.Sleep(1500 * time.Millisecond) // let the device finish setting up
				changed()
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
