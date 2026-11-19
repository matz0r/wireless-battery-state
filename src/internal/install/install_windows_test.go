package install

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestAutostartRegistry(t *testing.T) {
	runKey = `Software\battery-state-test\Run`
	defer func() {
		registry.DeleteKey(registry.CURRENT_USER, runKey)
		registry.DeleteKey(registry.CURRENT_USER, `Software\battery-state-test`)
		runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	}()
	t.Setenv("LOCALAPPDATA", t.TempDir())

	if AutostartEnabled() {
		t.Fatal("enabled before SetAutostart")
	}
	if err := SetAutostart(true); err != nil {
		t.Fatal(err)
	}
	if !AutostartEnabled() {
		t.Fatal("not enabled after SetAutostart(true)")
	}
	if err := SetAutostart(false); err != nil {
		t.Fatal(err)
	}
	if AutostartEnabled() {
		t.Fatal("still enabled after SetAutostart(false)")
	}
	if err := SetAutostart(false); err != nil {
		t.Fatalf("turning off twice: %v", err)
	}
}

func TestReplaceFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "new.exe"), filepath.Join(dir, "sub", "app.exe")
	os.WriteFile(src, []byte("v2"), 0o755)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, []byte("v1"), 0o755)
	if err := replaceFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "v2" {
		t.Fatalf("dst = %q", b)
	}
	if _, err := os.Stat(dst + ".old"); !os.IsNotExist(err) {
		t.Error("the old copy should be gone when it isn't running")
	}
}
