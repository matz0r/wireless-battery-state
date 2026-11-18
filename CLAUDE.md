# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

battery-state is a tray app in Go for Linux and Windows that shows battery levels of wireless mice, keyboards and headsets: one tray icon per device (StatusNotifierItem on Linux, the notification area on Windows). It is a Go port of the Windows app github.com/krisgfx9/Wireless-Device-Battery-Monitor; its `src/drivers/*.cpp` are the protocol reference when adding devices, and its `src/hid.cpp` is the reference for the Windows HID calls.

Platform code is split by file: `*_windows.go` for Windows, `*_unix.go`/`*_other.go` with `//go:build !windows` for Linux. There is no cgo anywhere (Win32 is called through `golang.org/x/sys/windows` and lazy DLL procs), so both targets cross-compile from either OS. Check Windows code from Linux with `GOOS=windows go vet ./...`.

## Commands

- Build: `go build -o battery-state ./src` (releases use `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X main.version=…"` for a static binary; the main package is in `src/`)
- Test all: `go test ./...`; single test: `go test ./src/internal/battery -run TestGloriousAwake`
- Lint/format: `go vet ./...`, `gofmt -w .`
- Try against real hardware: `./battery-state --list` (or `--json`), `./battery-state --dump` lists all hidraw nodes with their top-level HID collections
- Run the tray: `./battery-state` (only one per user, flock in `$XDG_RUNTIME_DIR`)
- Install for the user: `./install.sh` (runs `battery-state --install`); `./install.sh uninstall`
- Windows build: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o battery-state.exe ./src` (`-H windowsgui` so the tray starts without a console; the CLI modes attach to the parent console in `main_windows.go`)
- Release: commit, then `git tag vX.Y.Z && git push origin main vX.Y.Z` (`.github/workflows/release.yml` tests on Linux and Windows, builds both static binaries and publishes the GitHub release; `test.yml` runs the tests on both for every push)

## Architecture

- `src/main.go`: flags and modes (tray by default, `--list`, `--dump`, `--setup`, `--install`, …); `main_windows.go` attaches to the parent console and shows fatal tray errors in a message box.
- `src/internal/hidraw`: `Info` (one node) and `Device`. Linux (`hidraw_unix.go`): enumerates `/sys/class/hidraw` (vid/pid, name, interface, driver), parses report descriptors for top-level collections (`descriptor.go`), and opens `/dev/hidraw*` for write/read-with-timeout and feature reports (HIDIOCSFEATURE/HIDIOCGFEATURE). Windows (`hidraw_windows.go`): one `Info` per HID collection from `CM_Get_Device_Interface_List`, caps from `HidP_GetCaps` (also the report lengths), overlapped ReadFile/WriteFile, `HidD_SetFeature`/`HidD_GetFeature`; `Open` falls back to a zero-access handle (feature reports only) where Windows holds a collection exclusively. `Phys` is the interface's instance key (`winpath.go`). Report buffers start with the report id (0 if unnumbered) on both, and a leading 0 is dropped from reads as hidraw does, so protocol byte offsets from the reference carry over 1:1.
- `src/internal/battery`: `Status` (Percent -1 = unknown; `Asleep`; `NoAccess` when the node can't be opened) and `Drivers`. Each driver matches hidraw nodes by vid/pid plus the HID collection and talks through the `HID` interface, which tests fake. `glorious.go` (feature report 0x83), `astro.go` (A50 Gen 5 / A50 X, report 02 0C), `powersupply.go` (Linux only: anything the kernel exposes in `/sys/class/power_supply` with SCOPE=Device, which covers Logitech HID++ receivers through `hid-logitech-hidpp`), `hidpp.go` (Windows only: Logitech HID++ 1.0/2.0 over the receiver's FF00 collections, with a 30-minute grace for sleeping receiver devices). `drivers_windows.go`/`drivers_other.go` pick the per-OS drivers. `Monitor` keeps the last good reading so sleeping devices show it dimmed. A driver's `Udev` ids feed the udev rule.
- `src/internal/tray`: `tray.go` is shared: it polls on an interval, re-polls on hotplug, adds/removes icons as devices come and go through the `platform` interface (an `appearance` per icon: title, render func, tooltip), and sends low-battery notifications; `menu.go` is the shared `menuState`. Linux (`sni_unix.go`, `menu_unix.go`, `platform_unix.go`): own StatusNotifierItem + com.canonical.dbusmenu implementation on godbus, each icon on its own session-bus connection and well-known name; hotplug by watching the hidraw/power_supply directories. Windows (`tray_windows.go`): Shell_NotifyIcon icons owned by a hidden window whose message loop runs on a locked OS thread (other goroutines go through `do()`), icon ids hashed from the device key, popup menu via TrackPopupMenu, balloons for notifications, `WM_DEVICECHANGE` for hotplug, re-adding icons on `TaskbarCreated`, a named mutex for single instance.
- `src/internal/icon`: renders the percentage (Go Bold, outlined) and a level bar to ARGB32 pixmaps at several sizes; also the app icon.
- `src/internal/notify`: org.freedesktop.Notifications on Linux; on Windows the tray registers a handler that shows a balloon.
- `src/internal/setup`: Linux only (`setup.Available`): writes `/etc/udev/rules.d/60-battery-state.rules` (uaccess tag) through pkexec.
- `src/internal/install`: per-user install. Linux: `~/.local` and the autostart entry in `~/.config/autostart`. Windows: `%LOCALAPPDATA%\Programs\battery-state`, a Start menu shortcut (via PowerShell's WScript.Shell), autostart in `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`.

## Adding a device

Find the protocol in the reference repo's `src/drivers/`, then add a driver in `src/internal/battery` matching on vid/pid and the vendor collection (`--dump` shows them), add it to `Drivers`, list its ids in `Udev` if it needs hidraw access, and add a fake-device test in `battery_test.go`. Drivers are shared by both OSes; on Windows each `Info` holds a single collection, and `Write` pads to the collection's output report length, so match on the collection rather than on interface numbers. Only send read-only queries.
