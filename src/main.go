// battery-state shows the battery levels of wireless mice, keyboards and headsets in the
// system tray (Linux: StatusNotifierItem; Windows: the notification area). Run with --help
// for the command-line options.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/matz0r/wireless-battery-state/src/internal/battery"
	"github.com/matz0r/wireless-battery-state/src/internal/hidraw"
	"github.com/matz0r/wireless-battery-state/src/internal/install"
	"github.com/matz0r/wireless-battery-state/src/internal/setup"
	"github.com/matz0r/wireless-battery-state/src/internal/tray"
	"github.com/matz0r/wireless-battery-state/src/internal/update"
)

var version = "0.3.0"

func main() {
	if len(os.Args) > 1 {
		attachConsole() // Windows: a GUI binary has no console; borrow the terminal's for output
	}
	list := flag.Bool("list", false, "print the devices and their battery levels, then exit")
	asJSON := flag.Bool("json", false, "with --list: print JSON")
	dump := flag.Bool("dump", false, "print all HID nodes (for adding new devices), then exit")
	doSetup := flag.Bool("setup", false, "Linux: install the udev rule that gives you access to the devices (asks for your password)")
	rules := flag.Bool("udev-rules", false, "Linux: print the udev rule used by --setup, then exit")
	doInstall := flag.Bool("install", false, "install for the current user ("+installHint+")")
	doUninstall := flag.Bool("uninstall", false, "remove the per-user installation and autostart entry")
	settings := flag.Bool("settings", false, "open the settings window (of the running tray, or start the tray with it)")
	doUpdate := flag.Bool("update", false, "install the latest release if it is newer than this program")
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
		if setup.Available && setup.NeedsAccess(devs) {
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
	case *doUpdate:
		if !update.Supported() {
			exitOn(fmt.Errorf("no releases are built for this system"))
		}
		v, err := update.Latest("battery-state/" + version)
		exitOn(err)
		if !update.Newer(v, version) {
			fmt.Println("Up to date:", version)
			return
		}
		exitOn(update.Install(v, "battery-state/"+version))
		fmt.Println("Updated to", v, "- restart the tray to use it.")
	case *doUninstall:
		exitOn(install.Uninstall())
		fmt.Println("Uninstalled.")
	default:
		err := tray.Run(tray.Options{Interval: *interval, Version: version, OpenSettings: *settings})
		if errors.Is(err, tray.ErrRunning) && tray.ShowRunning() == nil {
			return // started again, e.g. from the application menu: the running tray opens its window
		}
		if err != nil {
			fatal(err) // Windows: a message box, as there is no console to print to
		}
	}
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "battery-state:", err)
		os.Exit(1)
	}
}
