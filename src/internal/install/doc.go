// Package install installs battery-state for the current user and manages autostart. Nothing
// here needs administrator rights: on Linux everything goes under ~/.local and ~/.config, on
// Windows under %LOCALAPPDATA%, the Start menu and the user's Run registry key.
package install
