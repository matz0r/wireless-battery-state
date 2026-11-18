# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

battery-state is a Linux tray app in Go that shows battery levels of wireless mice, keyboards and headsets: one StatusNotifierItem icon per device. It is a Go port (Linux only for now) of the Windows app github.com/krisgfx9/Wireless-Device-Battery-Monitor; its `src/drivers/*.cpp` are the protocol reference when adding devices.

## Commands

- Build: `go build -o battery-state ./src` (releases use `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X main.version=…"` for a static binary; the main package is in `src/`)
- Test all: `go test ./...`; single test: `go test ./src/internal/battery -run TestGloriousAwake`
- Lint/format: `go vet ./...`, `gofmt -w .`
- Try against real hardware: `./battery-state --list` (or `--json`), `./battery-state --dump` lists all hidraw nodes with their top-level HID collections
- Run the tray: `./battery-state` (only one per user, flock in `$XDG_RUNTIME_DIR`)
- Install for the user: `./install.sh` (runs `battery-state --install`); `./install.sh uninstall`
- Release: commit, then `git tag vX.Y.Z && git push origin main vX.Y.Z` (`.github/workflows/release.yml` tests, builds the static binary and publishes the GitHub release)

## Architecture

- `src/main.go`: flags and modes (tray by default, `--list`, `--dump`, `--setup`, `--install`, …).
- `src/internal/hidraw`: enumerates `/sys/class/hidraw` (vid/pid, name, interface, driver), parses report descriptors for top-level collections (`descriptor.go`), and opens `/dev/hidraw*` for write/read-with-timeout and feature reports (HIDIOCSFEATURE/HIDIOCGFEATURE). Report buffers start with the report id (0 if unnumbered), as on Windows, so protocol byte offsets from the reference carry over 1:1.
- `src/internal/battery`: `Status` (Percent -1 = unknown; `Asleep`; `NoAccess` when the node can't be opened) and `Drivers`. Each driver matches hidraw nodes by vid/pid plus the HID collection and talks through the `HID` interface, which tests fake. `glorious.go` (feature report 0x83), `astro.go` (A50 Gen 5 / A50 X, report 02 0C), `powersupply.go` (anything the kernel exposes in `/sys/class/power_supply` with SCOPE=Device, which covers Logitech HID++ receivers through `hid-logitech-hidpp`). `Monitor` keeps the last good reading so sleeping devices show it dimmed. A driver's `Udev` ids feed the udev rule.
- `src/internal/tray`: own StatusNotifierItem + com.canonical.dbusmenu implementation on godbus. Each icon gets its own session-bus connection and well-known name so several icons can live in one process; all share one `menuState`. `tray.go` polls on an interval, re-polls on hotplug (hidraw/power_supply directory changes), adds/removes icons as devices come and go, and sends low-battery notifications.
- `src/internal/icon`: renders the percentage (Go Bold, outlined) and a level bar to ARGB32 pixmaps at several sizes; also the app icon.
- `src/internal/setup`: writes `/etc/udev/rules.d/60-battery-state.rules` (uaccess tag) through pkexec.
- `src/internal/install`: per-user install into `~/.local` and the autostart entry in `~/.config/autostart`.

## Adding a device

Find the protocol in the reference repo's `src/drivers/`, then add a driver in `src/internal/battery` matching on vid/pid and the vendor collection (`--dump` shows them), add it to `Drivers`, list its ids in `Udev` if it needs hidraw access, and add a fake-device test in `battery_test.go`. Only send read-only queries.
