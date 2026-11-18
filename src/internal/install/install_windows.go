package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// Windows: the binary goes to %LOCALAPPDATA%\Programs\battery-state, the Start menu gets a
// shortcut, and "Start on login" is a value under HKCU\...\CurrentVersion\Run.

const (
	exeName      = "battery-state.exe"
	shortcutName = "Battery State.lnk"
	runValue     = "Battery State"
)

// runKey is the per-user autostart key; tests point it elsewhere.
var runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func localAppData() string {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "AppData", "Local")
}

func installDir() string { return filepath.Join(localAppData(), "Programs", "battery-state") }

// BinPath is where Install puts the binary.
func BinPath() string { return filepath.Join(installDir(), exeName) }

func shortcutPath() string {
	d := os.Getenv("APPDATA")
	if d == "" {
		h, _ := os.UserHomeDir()
		d = filepath.Join(h, "AppData", "Roaming")
	}
	return filepath.Join(d, "Microsoft", "Windows", "Start Menu", "Programs", shortcutName)
}

func self() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// Install copies the running binary to %LOCALAPPDATA%\Programs\battery-state and adds a Start
// menu shortcut.
func Install() (string, error) {
	src, err := self()
	if err != nil {
		return "", err
	}
	dst := BinPath()
	if !strings.EqualFold(src, dst) {
		if err := replaceFile(src, dst); err != nil {
			return "", err
		}
	}
	if err := createShortcut(shortcutPath(), dst); err != nil {
		return "", fmt.Errorf("installed to %s, but the Start menu shortcut failed: %w", dst, err)
	}
	// Keep autostart pointing at the installed copy.
	if AutostartEnabled() {
		if err := SetAutostart(true); err != nil {
			return "", err
		}
	}
	return dst, nil
}

// Uninstall removes what Install and SetAutostart created. A running copy of the installed
// binary can't be deleted: if it is the one running this, it is deleted right after this
// process exits; if a tray is running from it, quit that first.
func Uninstall() error {
	if err := SetAutostart(false); err != nil {
		return err
	}
	if err := os.Remove(shortcutPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	os.Remove(BinPath() + ".old")
	bin := BinPath()
	err := os.Remove(bin)
	switch {
	case err == nil || os.IsNotExist(err):
		os.Remove(installDir())
		return nil
	case isSelf(bin):
		return deleteAfterExit(bin, installDir())
	default:
		return fmt.Errorf("can't remove %s; quit Battery State (tray menu, Quit) and try again: %w", bin, err)
	}
}

func isSelf(path string) bool {
	s, err := self()
	return err == nil && strings.EqualFold(s, path)
}

// deleteAfterExit removes a running binary and its folder once this process has exited.
func deleteAfterExit(bin, dir string) error {
	script := fmt.Sprintf(`ping -n 3 127.0.0.1 >nul & del /f /q "%s" & rmdir "%s"`, bin, dir)
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CmdLine:       `cmd.exe /c "` + script + `"`, // replaces the whole command line, argv[0] included
		CreationFlags: 0x08000000,                    // CREATE_NO_WINDOW
	}
	return cmd.Start()
}

// AutostartEnabled reports whether the tray starts on login.
func AutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

// SetAutostart turns starting on login on or off. It points at the installed copy if there is
// one, otherwise at the running binary.
func SetAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	exe := BinPath()
	if _, err := os.Stat(exe); err != nil {
		if exe, err = self(); err != nil {
			return err
		}
	}
	return k.SetStringValue(runValue, `"`+exe+`"`)
}

// replaceFile copies src over dst. Windows can't overwrite a running binary but can rename it,
// so an existing dst is moved aside to dst.old first (and removed if it isn't running).
func replaceFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	old := dst + ".old"
	os.Remove(old)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	os.Remove(old) // fails while the old copy is still running; the next install cleans it up
	return nil
}

// createShortcut writes a .lnk through the WScript.Shell COM object in PowerShell, which is
// part of every Windows 10 and 11. Paths go through environment variables, not the command
// line, so no quoting is needed.
func createShortcut(lnk, target string) error {
	if err := os.MkdirAll(filepath.Dir(lnk), 0o755); err != nil {
		return err
	}
	const script = `$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:BS_LNK); ` +
		`$s.TargetPath = $env:BS_TARGET; $s.WorkingDirectory = $env:BS_DIR; ` +
		`$s.Description = 'Battery levels of wireless mice, keyboards and headsets in the tray'; $s.Save()`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "BS_LNK="+lnk, "BS_TARGET="+target, "BS_DIR="+filepath.Dir(target))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
