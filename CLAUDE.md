# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

battery-state is a Linux tray app in Go that shows battery levels of wireless mice, keyboards and headsets: one StatusNotifierItem icon per device. It is a Go port (Linux only for now) of the Windows app github.com/krisgfx9/Wireless-Device-Battery-Monitor; its `src/drivers/*.cpp` are the protocol reference when adding devices.

## Commands

- Build: `make` (→ `target/debug/battery-state`), `make release` (static, stripped, version from `src/main.go` → `target/release/battery-state`). Build output only goes to `target/` (git-ignored), like cargo. The main package is in `src/`.
- Test all: `make test` / `go test ./...`; single test: `go test ./src/internal/battery -run TestGloriousAwake`
- Logo: `make icon` runs `assets/make_icon.py` (Pillow), which writes `assets/icon.svg` and `icon-{256,512}.png` from one set of shapes; `assets/assets.go` embeds them (used for the installed icons and the tray placeholder).
- Lint/format: `make vet`, `make fmt`
- Try against real hardware: `make run ARGS=--list` (or `--json`); `--dump` lists all hidraw nodes with their top-level HID collections
- Run the tray: `make run` (only one per user, flock in `$XDG_RUNTIME_DIR`; quit the installed one first)
- Install for the user: `make install` or `./install.sh` (release build, then `battery-state --install`); `./install.sh uninstall`
- Release: commit, then `git tag vX.Y.Z && git push origin main vX.Y.Z` (`.github/workflows/release.yml` tests, builds the static binary and publishes the GitHub release)

## Architecture

- `src/main.go`: flags and modes (tray by default, `--list`, `--dump`, `--setup`, `--install`, `--settings`, `--update`, …). A second start finds the running tray over D-Bus (`io.github.matz0r.BatteryState.ShowSettings`) and opens its window.
- `src/internal/hidraw`: enumerates `/sys/class/hidraw` (vid/pid, name, interface, driver), parses report descriptors for top-level collections (`descriptor.go`), and opens `/dev/hidraw*` for write/read-with-timeout and feature reports (HIDIOCSFEATURE/HIDIOCGFEATURE). Report buffers start with the report id (0 if unnumbered), as on Windows, so protocol byte offsets from the reference carry over 1:1.
- `src/internal/battery`: `Status` (Percent -1 = unknown; `Asleep`; `NoAccess` when the node can't be opened) and `Drivers`. Each driver matches hidraw nodes by vid/pid plus the HID collection and talks through the `HID` interface, which tests fake. `glorious.go` (feature report 0x83), `astro.go` (A50 Gen 5 / A50 X, report 02 0C), `powersupply.go` (anything the kernel exposes in `/sys/class/power_supply` with SCOPE=Device, which covers Logitech HID++ receivers through `hid-logitech-hidpp`). `Monitor` keeps the last good reading so sleeping devices show it dimmed. A driver's `Udev` ids feed the udev rule.
- `src/internal/tray`: own StatusNotifierItem + com.canonical.dbusmenu implementation on godbus. Each icon gets its own session-bus connection and well-known name so several icons can live in one process; all share one `menuState`. `tray.go` polls on an interval, re-polls on hotplug (hidraw/power_supply directory changes), adds/removes icons as devices come and go, and sends low-battery notifications. Icon titles are `Battery State NN <name>` on purpose: trays sort by title (Cinnamon's xapp-status applet alphabetically), which keeps our icons together and in device order; don't put changing values (levels) in the title.
- Clicking an icon calls SNI `Activate` (`ItemIsMenu` is false; right-click opens the dbusmenu) and toggles the settings window (`tray/window.go`): which devices get an icon (`config.Settings.Hidden`), start on login, update checks, install. Install and update end with `syscall.Exec` of `~/.local/bin/battery-state --settings`; the lock file and bus connections are close-on-exec, so the new process takes over the tray.
- `src/internal/ui`: the settings window, self-drawn over pure-Go X11 (jezek/xgb) so the binary stays static; on Wayland it runs through XWayland. `builder.go` is an immediate-mode layout (Header, Section, Card with CheckRow/SwitchRow/InfoRow, Buttons) rebuilt on every redraw; `canvas.go` draws antialiased shapes; `x11.go` handles the window, events, Xft.dpi scaling, focus timestamps and PutImage. `ui.Snapshot` renders offscreen: `BATTERY_STATE_SNAPSHOT=/some/dir go test ./src/internal/tray -run TestWindowLayout` writes PNGs of the window to look at.
- `src/internal/config`: settings in `~/.config/battery-state/settings.json`.
- `src/internal/update`: like bibi-brightness: reads the version from the `releases/latest` redirect (no API), downloads `battery-state-vX.Y.Z-x86_64-linux.tar.gz` (the name `release.yml` produces; keep them in sync), checks `--version`, runs it with `--install`. Only the installed copy checks, at most once a day, and only when "Check for updates" is on.
- `src/internal/icon`: renders the tray icons to ARGB32 pixmaps at several sizes: a device pictogram on top (`glyph.go`, vector shapes with cut-outs, from `battery.Kind`), the percentage (Go Bold, outlined) and a level bar. `AppIcon` scales the embedded logo.
- `src/internal/setup`: writes `/etc/udev/rules.d/60-battery-state.rules` (uaccess tag) through pkexec.
- `src/internal/install`: per-user install into `~/.local` and the autostart entry in `~/.config/autostart`.

## Adding a device

Find the protocol in the reference repo's `src/drivers/`, then add a driver in `src/internal/battery` matching on vid/pid and the vendor collection (`--dump` shows them), add it to `Drivers`, list its ids in `Udev` if it needs hidraw access, and add a fake-device test in `battery_test.go`. Only send read-only queries.
