//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func hideWindow(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

func reveal(path string, file bool) error {
	if file {
		c := exec.Command("explorer", "/select,"+path)
		return c.Start()
	}
	return exec.Command("explorer", path).Start()
}
