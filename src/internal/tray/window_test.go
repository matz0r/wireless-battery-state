//go:build !windows

package tray

import (
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/matz0r/wireless-battery-state/src/internal/battery"
	"github.com/matz0r/wireless-battery-state/src/internal/config"
	"github.com/matz0r/wireless-battery-state/src/internal/install"
	"github.com/matz0r/wireless-battery-state/src/internal/ui"
)

func testApp() *app {
	a := &app{opts: Options{Version: "0.1.0"}}
	a.devices = []battery.Status{
		{Key: "m", Name: "Glorious Model D- Wireless", Kind: battery.Mouse, Via: "2.4 GHz receiver", Percent: 72},
		{Key: "k", Name: "G515 LIGHTSPEED TKL", Kind: battery.Keyboard, Via: "Lightspeed receiver", Percent: 12},
		{Key: "h", Name: "ASTRO A50", Kind: battery.Headset, Via: "USB base station", Percent: 100, Charging: true},
	}
	a.settings = config.Settings{Hidden: []string{"k"}}
	return a
}

// TestWindowLayout builds the settings window offscreen. With BATTERY_STATE_SNAPSHOT=dir it
// also writes PNGs there to look at.
func TestWindowLayout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cases := map[string]install.State{
		"not-installed": {},
		"older":         {Installed: true, Version: "0.0.9"},
		"running":       {Installed: true, Running: true, Same: true},
	}
	for name, inst := range cases {
		a := testApp()
		a.ws.inst = inst
		for theme, th := range map[string]ui.Theme{"dark": ui.Dark, "light": ui.Light} {
			img := ui.Snapshot(ui.Options{Width: 440, Theme: th, Build: a.buildWindow}, 2, image.Pt(300, 260))
			if img.Bounds().Dy() < 400 {
				t.Errorf("%s: window only %dpx high", name, img.Bounds().Dy())
			}
			if dir := os.Getenv("BATTERY_STATE_SNAPSHOT"); dir != "" {
				f, err := os.Create(dir + "/" + name + "-" + theme + ".png")
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(f, img)
				f.Close()
			}
		}
	}
}

func TestInstallRow(t *testing.T) {
	if _, _, l := installRow(install.State{}, "0.2.0"); l == "" {
		t.Error("not installed: no install button")
	}
	if _, _, l := installRow(install.State{Installed: true, Same: true}, "0.2.0"); l != "" {
		t.Error("same copy: install button shown")
	}
	if _, _, l := installRow(install.State{Installed: true, Version: "0.3.0"}, "0.2.0"); l != "" {
		t.Error("downgrade offered")
	}
	if _, _, l := installRow(install.State{Installed: true, Version: "0.1.0"}, "0.2.0"); l == "" {
		t.Error("older installed copy: no install button")
	}
}
