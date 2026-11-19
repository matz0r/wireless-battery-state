package ui

import (
	"os"
	"path/filepath"
)

// systemFontPath returns Segoe UI, the Windows UI font.
func systemFontPath(isBold bool) string {
	dir := os.Getenv("WINDIR")
	if dir == "" {
		dir = `C:\Windows`
	}
	name := "segoeui.ttf"
	if isBold {
		name = "segoeuib.ttf"
	}
	return filepath.Join(dir, "Fonts", name)
}
