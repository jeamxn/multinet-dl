//go:build darwin

package main

import "os/exec"

func hideWindow(*exec.Cmd) {}

func reveal(path string, file bool) error {
	if file {
		return exec.Command("open", "-R", path).Start()
	}
	return exec.Command("open", path).Start()
}
