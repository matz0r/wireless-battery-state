// battery-state shows the battery levels of wireless mice, keyboards and headsets in the
// system tray (Linux, StatusNotifierItem). Run with --help for the command-line options.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/battery"
	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
	"github.com/matz0r/wireless-battery-state/src/internal/install"
	"github.com/matz0r/wireless-battery-state/src/internal/setup"
	"github.com/matz0r/wireless-battery-state/src/internal/tray"
)

var version = "0.1.0"

func main() {
	list := flag.Bool("list", false, "print the devices and their battery levels, then exit")
	asJSON := flag.Bool("json", false, "with --list: print JSON")
	dump := flag.Bool("dump", false, "print all hidraw nodes (for adding new devices), then exit")
	doSetup := flag.Bool("setup", false, "install the udev rule that gives you access to the devices (asks for your password)")
	rules := flag.Bool("udev-rules", false, "print the udev rule used by --setup, then exit")
	doInstall := flag.Bool("install", false, "install for the current user (~/.local/bin, application menu)")
	doUninstall := flag.Bool("uninstall", false, "remove the per-user installation and autostart entry")
	interval := flag.Duration("interval", 60*time.Second, "how often to read the battery levels")
	showVersion := flag.Bool("version", false, "print the version, then exit")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println("battery-state", version)
	case *dump:
		nodes, err := hidraw.Enumerate()
		exitOn(err)
		for _, n := range nodes {
			fmt.Println(n)
		}
	case *list:
		env, err := battery.DefaultEnv()
		exitOn(err)
		devs := battery.Poll(env)
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			exitOn(enc.Encode(devs))
			return
		}
		if len(devs) == 0 {
			fmt.Println("No supported devices found.")
		}
		for _, d := range devs {
			fmt.Printf("%-9s %-30s %-20s %s\n", d.Kind, d.Name, d.Detail(), d.Via)
		}
		if setup.NeedsAccess(devs) {
			fmt.Println("\nSome devices can't be read. Run: battery-state --setup")
		}
	case *rules:
		fmt.Print(setup.Rules())
	case *doSetup:
		exitOn(setup.Run())
		fmt.Println("Access set up.")
	case *doInstall:
		path, err := install.Install()
		exitOn(err)
		fmt.Println("Installed to", path)
	case *doUninstall:
		exitOn(install.Uninstall())
		fmt.Println("Uninstalled.")
	default:
		exitOn(tray.Run(tray.Options{Interval: *interval, Version: version}))
	}
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "battery-state:", err)
		os.Exit(1)
	}
}
