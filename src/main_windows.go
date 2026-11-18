package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const installHint = `%LOCALAPPDATA%\Programs, Start menu`

// attachConsole gives the command-line modes somewhere to print. Releases are linked as GUI
// programs (-H windowsgui) so the tray starts without a console window; such a program has no
// stdout unless it was redirected, so it attaches to the console of the shell that started it.
func attachConsole() {
	if h, _ := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); h != 0 && h != windows.InvalidHandle {
		return // console program, or output redirected to a file or pipe
	}
	const attachParentProcess = ^uint32(0)
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := proc.Call(uintptr(attachParentProcess)); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
		fmt.Println() // the shell has already printed its prompt
	}
}

// fatal reports an error from the tray in a message box and exits.
func fatal(err error) {
	text, _ := windows.UTF16PtrFromString("battery-state: " + err.Error())
	title, _ := windows.UTF16PtrFromString("Battery State")
	windows.MessageBox(0, text, title, windows.MB_OK|windows.MB_ICONERROR)
	os.Exit(1)
}
