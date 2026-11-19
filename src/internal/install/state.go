package install

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// State describes the installed copy relative to the running binary.
type State struct {
	Installed bool   // the installed binary (BinPath) exists
	Running   bool   // the running binary is the installed copy
	Same      bool   // the installed copy has the same contents as the running binary
	Version   string // the installed copy's --version, "" if unknown
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Running reports whether the running binary is the installed copy.
func Running() bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	if _, err := os.Stat(BinPath()); err != nil {
		return false
	}
	return samePath(resolve(self), resolve(BinPath()))
}

// Current looks at the installed copy. It runs it with --version, so call it once in a
// while (when the settings window opens), not on every redraw.
func Current() State {
	var s State
	if _, err := os.Stat(BinPath()); err != nil {
		return s
	}
	s.Installed = true
	s.Running = Running()
	if s.Running {
		s.Same = true
	} else if self, err := os.Executable(); err == nil {
		s.Same = sameFile(self, BinPath())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, BinPath(), "--version")
	hideWindow(cmd)
	if out, err := cmd.Output(); err == nil {
		s.Version = strings.TrimPrefix(strings.TrimSpace(string(out)), "battery-state ")
	}
	return s
}

func sameFile(a, b string) bool {
	da, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	db, err := os.ReadFile(b)
	return err == nil && bytes.Equal(da, db)
}
