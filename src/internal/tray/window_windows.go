package tray

import "errors"

// The settings window (window.go) draws over X11 and is Linux only for now. On Windows a
// click opens the menu, as before, and these are no-ops.

const hasSettingsWindow = false

type windowState struct{}

// ShowRunning would open the running tray's settings window; Windows has none yet.
func ShowRunning() error { return errors.New("no settings window on Windows") }

func (a *app) exportService()    {}
func (a *app) toggleWindow()     {}
func (a *app) showWindow()       {}
func (a *app) refreshWindow()    {}
func (a *app) closeWindow()      {}
func (a *app) maybeCheckUpdate() {}

func (a *app) offeredUpdate() string { return "" }
func (a *app) runUpdate(v string)    {}
