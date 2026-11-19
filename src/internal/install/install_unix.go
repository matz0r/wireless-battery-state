//go:build !windows

// Linux: everything goes under ~/.local and ~/.config.

package install

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/matz0r/wireless-battery-state/assets"
	"github.com/matz0r/wireless-battery-state/src/internal/icon"
)

const (
	binName = "battery-state"
	appID   = "battery-state"
)

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func dataHome() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	return filepath.Join(home(), ".local", "share")
}

func configHome() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(home(), ".config")
}

// BinPath is where Install puts the binary.
func BinPath() string { return filepath.Join(home(), ".local", "bin", binName) }

func desktopPath() string { return filepath.Join(dataHome(), "applications", appID+".desktop") }

func autostartPath() string { return filepath.Join(configHome(), "autostart", appID+".desktop") }

// Logo sizes written into the hicolor theme; 256 and 512 are the generated PNGs as is.
var iconSizes = []int{32, 48, 64, 128, 256, 512}

func iconPath(size int) string {
	return filepath.Join(dataHome(), "icons", "hicolor", fmt.Sprintf("%dx%d", size, size), "apps", appID+".png")
}

func svgIconPath() string {
	return filepath.Join(dataHome(), "icons", "hicolor", "scalable", "apps", appID+".svg")
}

func iconPNG(size int) ([]byte, error) {
	switch size {
	case 256:
		return assets.Icon256, nil
	case 512:
		return assets.Icon512, nil
	}
	var buf bytes.Buffer
	err := png.Encode(&buf, icon.AppIcon(size))
	return buf.Bytes(), err
}

func desktopEntry(exec string, autostart bool) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")
	b.WriteString("Type=Application\n")
	b.WriteString("Name=Battery State\n")
	b.WriteString("Comment=Battery levels of wireless mice, keyboards and headsets in the tray\n")
	fmt.Fprintf(&b, "Exec=%s\n", quoteExec(exec))
	fmt.Fprintf(&b, "Icon=%s\n", appID)
	b.WriteString("Categories=Utility;System;\n")
	b.WriteString("Terminal=false\n")
	if autostart {
		b.WriteString("X-GNOME-Autostart-enabled=true\n")
	}
	return b.String()
}

// quoteExec quotes a path for the Exec key if it needs it.
func quoteExec(p string) string {
	if !strings.ContainsAny(p, " \t\"'\\$`") {
		return p
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	return `"` + r.Replace(p) + `"`
}

// Install copies the running binary to ~/.local/bin and adds it to the application menu.
func Install() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	self, _ = filepath.EvalSymlinks(self)
	dst := BinPath()
	if self != dst {
		if err := copyFile(self, dst, 0o755); err != nil {
			return "", err
		}
	}
	for _, s := range iconSizes {
		data, err := iconPNG(s)
		if err != nil {
			return "", err
		}
		if err := writeFile(iconPath(s), data, 0o644); err != nil {
			return "", err
		}
	}
	if err := writeFile(svgIconPath(), assets.IconSVG, 0o644); err != nil {
		return "", err
	}
	if err := writeFile(desktopPath(), []byte(desktopEntry(dst, false)), 0o644); err != nil {
		return "", err
	}
	// Keep autostart pointing at the installed copy.
	if AutostartEnabled() {
		if err := writeFile(autostartPath(), []byte(desktopEntry(dst, true)), 0o644); err != nil {
			return "", err
		}
	}
	return dst, nil
}

// Uninstall removes what Install and SetAutostart created.
func Uninstall() error {
	paths := []string{BinPath(), desktopPath(), autostartPath(), svgIconPath()}
	for _, s := range iconSizes {
		paths = append(paths, iconPath(s))
	}
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// AutostartEnabled reports whether the tray starts on login.
func AutostartEnabled() bool {
	_, err := os.Stat(autostartPath())
	return err == nil
}

// SetAutostart turns starting on login on or off. It points at the installed copy if there
// is one, otherwise at the running binary.
func SetAutostart(on bool) error {
	if !on {
		if err := os.Remove(autostartPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe := BinPath()
	if _, err := os.Stat(exe); err != nil {
		if exe, err = os.Executable(); err != nil {
			return err
		}
	}
	return writeFile(autostartPath(), []byte(desktopEntry(exe, true)), 0o644)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// copyFile copies via a temporary file and rename, so a running copy of the target is
// replaced instead of overwritten ("text file busy").
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
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
	return os.Rename(tmp, dst)
}

func hideWindow(*exec.Cmd) {}
