package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTagURL(t *testing.T) {
	for url, want := range map[string]string{
		Repo + "/releases/tag/v0.2.1": "0.2.1",
		Repo + "/releases/tag/1.0.0":  "1.0.0",
		Repo + "/releases":            "",
		Repo + "/releases/tag/latest": "",
	} {
		got, ok := parseTagURL(url)
		if got != want || ok != (want != "") {
			t.Errorf("parseTagURL(%q) = %q, %v; want %q", url, got, ok, want)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.9", true},
		{"0.10.0", "0.9.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.1.0", "0.2.0", false},
		{"dev", "0.1.0", false},
		{"0.2.0", "dev", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestDue(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	if !Due(0, now) {
		t.Error("never checked should be due")
	}
	if Due(now.Unix(), now) || Due(now.Unix(), now.Add(Interval-time.Second)) {
		t.Error("checked within the interval should not be due")
	}
	if !Due(now.Unix(), now.Add(Interval)) {
		t.Error("checked an interval ago should be due")
	}
	if !Due(now.Unix()+60, now) {
		t.Error("a check in the future should be due")
	}
}

func TestAssetURL(t *testing.T) {
	for goos, want := range map[string]string{
		"linux":   "/releases/download/v1.2.3/battery-state-v1.2.3-x86_64-linux.tar.gz",
		"windows": "/releases/download/v1.2.3/battery-state-v1.2.3-x86_64-windows.zip",
	} {
		if got := assetURL("1.2.3", goos); !strings.HasSuffix(got, want) {
			t.Errorf("assetURL(%s) = %s", goos, got)
		}
	}
}

func TestUnpackZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"battery-state-v1.2.3-x86_64-windows/README.md":   "readme",
		"battery-state-v1.2.3-x86_64-windows/" + binary(): "binary",
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	exe := filepath.Join(t.TempDir(), binary())
	if err := unpackZip(buf.Bytes(), exe); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "binary" {
		t.Errorf("unpacked %q", data)
	}
}

func TestUnpack(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	add("battery-state-v1.2.3-x86_64-linux/README.md", "readme")
	add("battery-state-v1.2.3-x86_64-linux/"+binary(), "binary")
	tw.Close()
	gz.Close()

	exe := filepath.Join(t.TempDir(), binary())
	if err := unpack(&buf, exe); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "binary" {
		t.Errorf("unpacked %q", data)
	}
}
