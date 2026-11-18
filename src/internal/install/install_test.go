package install

import (
	"os"
	"strings"
	"testing"
)

func TestInstallAutostartUninstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	if err := SetAutostart(true); err != nil {
		t.Fatal(err)
	}
	dst, err := Install()
	if err != nil {
		t.Fatal(err)
	}
	if dst != BinPath() {
		t.Fatalf("installed to %s", dst)
	}
	for _, p := range []string{BinPath(), desktopPath(), iconPath(64)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	auto, _ := os.ReadFile(autostartPath())
	if !strings.Contains(string(auto), "Exec="+BinPath()+"\n") {
		t.Errorf("autostart should point at the installed copy:\n%s", auto)
	}

	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if AutostartEnabled() {
		t.Error("autostart still enabled")
	}
	if _, err := os.Stat(BinPath()); !os.IsNotExist(err) {
		t.Error("binary still there")
	}
}

func TestQuoteExec(t *testing.T) {
	if quoteExec("/home/a/.local/bin/battery-state") != "/home/a/.local/bin/battery-state" {
		t.Error("plain path must stay unquoted")
	}
	if got := quoteExec("/home/a b/x"); got != `"/home/a b/x"` {
		t.Errorf("got %s", got)
	}
}
