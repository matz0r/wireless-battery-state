//go:build !windows

package setup

import (
	"fmt"
	"os/exec"
	"strings"
)

// Available reports whether this OS needs (and can do) access setup.
const Available = true

// RuleFile must sort before 73-seat-late.rules, which applies the uaccess tag.
const RuleFile = "/etc/udev/rules.d/60-battery-state.rules"

const script = `set -e
cat > "$1"
udevadm control --reload
udevadm trigger --subsystem-match=hidraw --action=change
udevadm settle
`

// Run writes the rule through pkexec and reloads udev.
func Run() error {
	cmd := exec.Command("pkexec", "/bin/sh", "-c", script, "sh", RuleFile)
	cmd.Stdin = strings.NewReader(Rules())
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		switch ee.ExitCode() {
		case 126, 127: // dialog dismissed / not authorized
			return fmt.Errorf("setup cancelled")
		}
		return fmt.Errorf("setup failed (%v)", err)
	}
	return fmt.Errorf("can't start pkexec (%v); see the README for manual setup", err)
}
