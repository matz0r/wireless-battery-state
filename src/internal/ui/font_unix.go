//go:build !windows

package ui

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// systemFontPath asks fontconfig for the sans-serif font file.
func systemFontPath(isBold bool) string {
	pattern := "sans-serif"
	if isBold {
		pattern += ":bold"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fc-match", "-f", "%{file}", pattern).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
