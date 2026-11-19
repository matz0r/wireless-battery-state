// Package update updates the installed copy from the GitHub releases, like
// bibi-brightness: checking is off until the user turns on "Check for updates"; then the
// installed copy looks up the latest release tag at most once a day. Installing downloads
// that release, unpacks the binary into a temporary folder, checks its --version and runs
// it with --install. The caller then restarts into the installed binary.
package update

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository the releases come from.
const Repo = "https://github.com/matz0r/wireless-battery-state"

const (
	binary      = "battery-state"
	tempPrefix  = "battery-state-update-"
	maxDownload = 64 << 20 // release archives are a few MB; guards against a broken response
)

// Interval is the minimum time between two automatic checks.
const Interval = 24 * time.Hour

// Supported reports whether releases are built for this machine.
func Supported() bool { return runtime.GOOS == "linux" && runtime.GOARCH == "amd64" }

// Due reports whether a check is due, given the Unix time of the last successful one. A last
// check in the future (clock changed) also counts as due.
func Due(last int64, now time.Time) bool {
	t := time.Unix(last, 0)
	return now.Before(t) || now.Sub(t) >= Interval
}

// Latest returns the version of the latest release ("1.2.3"). GitHub redirects
// /releases/latest to /releases/tag/<tag>; reading the redirect avoids the rate-limited API.
func Latest(agent string) (string, error) {
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest("GET", Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", agent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("GitHub did not name the latest release (HTTP %d)", resp.StatusCode)
	}
	v, ok := parseTagURL(loc)
	if !ok {
		return "", fmt.Errorf("unexpected release address %s", loc)
	}
	return v, nil
}

func parseTagURL(url string) (string, bool) {
	i := strings.LastIndex(url, "/releases/tag/")
	if i < 0 {
		return "", false
	}
	v := strings.TrimPrefix(url[i+len("/releases/tag/"):], "v")
	if _, ok := parse(v); !ok {
		return "", false
	}
	return v, true
}

// parse reads "1.2.3" (an optional "-suffix" is ignored).
func parse(v string) ([3]int, bool) {
	var out [3]int
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Newer reports whether version a is newer than b. Unparsable versions are never newer.
func Newer(a, b string) bool {
	va, ok1 := parse(a)
	vb, ok2 := parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// AssetURL is the release archive built by .github/workflows/release.yml.
func AssetURL(version string) string {
	return fmt.Sprintf("%s/releases/download/v%s/battery-state-v%s-x86_64-linux.tar.gz", Repo, version, version)
}

// Install downloads version, checks it and runs it with --install, which replaces the
// installed copy. The running tray restarts into it afterwards.
func Install(version, agent string) error {
	RemoveLeftovers()
	dir, err := os.MkdirTemp("", fmt.Sprintf("%s%d-", tempPrefix, os.Getpid()))
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	req, err := http.NewRequest("GET", AssetURL(version), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", agent)
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("can't download version %s: %w", version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("can't download version %s: HTTP %d", version, resp.StatusCode)
	}
	exe := filepath.Join(dir, binary)
	if err := unpack(io.LimitReader(resp.Body, maxDownload), exe); err != nil {
		return err
	}

	// Make sure the download is the announced version before it replaces anything.
	out, err := exec.Command(exe, "--version").Output()
	if err != nil {
		return fmt.Errorf("can't start the downloaded program: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != binary+" "+version {
		return fmt.Errorf("the download reports %q instead of version %s", got, version)
	}
	if out, err := exec.Command(exe, "--install").CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(strings.TrimPrefix(msg, binary+": "))
	}
	return nil
}

// unpack extracts the battery-state binary from a .tar.gz into exe.
func unpack(r io.Reader, exe string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("broken archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s is missing from the download", binary)
		}
		if err != nil {
			return fmt.Errorf("broken archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != binary {
			continue
		}
		f, err := os.OpenFile(exe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("download interrupted: %w", err)
		}
		return f.Close()
	}
}

// RemoveLeftovers removes temporary folders of earlier updates that were cut short.
func RemoveLeftovers() {
	entries, _ := os.ReadDir(os.TempDir())
	own := fmt.Sprintf("%s%d-", tempPrefix, os.Getpid())
	for _, e := range entries {
		if n := e.Name(); strings.HasPrefix(n, tempPrefix) && !strings.HasPrefix(n, own) {
			_ = os.RemoveAll(filepath.Join(os.TempDir(), n)) // fails harmlessly for other users'
		}
	}
}
