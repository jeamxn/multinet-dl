//go:build linux

package main

import (
	"os/exec"
	"path/filepath"
)

func hideWindow(*exec.Cmd) {}

func reveal(path string, file bool) error {
	if file {
		path = filepath.Dir(path)
	}
	return exec.Command("xdg-open", path).Start()
}
