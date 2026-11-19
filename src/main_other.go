//go:build !windows

package main

const installHint = "~/.local/bin, application menu"

func attachConsole() {}

func fatal(err error) { exitOn(err) }
