//go:build linux

package netif

import (
	"net"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Linux and others: SO_BINDTODEVICE when allowed (needs CAP_NET_RAW),
// otherwise rely on the source address bind (works with policy routing).
func bindControl(index int, _ bool) func(string, string, syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		ifi, err := net.InterfaceByIndex(index)
		if err != nil {
			return nil
		}
		_ = c.Control(func(fd uintptr) { _ = unix.BindToDevice(int(fd), ifi.Name) })
		return nil
	}
}

var (
	noteOnce sync.Once
	note     string
)

// bindingNote probes once whether SO_BINDTODEVICE is allowed.
func bindingNote() string {
	noteOnce.Do(func() {
		fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
		if err != nil {
			return
		}
		defer unix.Close(fd)
		if err := unix.BindToDevice(fd, "lo"); err != nil {
			note = "리눅스에서 네트워크를 확실히 고정하려면 권한이 필요함: sudo setcap cap_net_raw+ep " + exePath() + "  (없으면 출발 주소만 고정해서 라우팅에 따라 다른 네트워크로 나갈 수 있음)"
		}
	})
	return note
}

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return "mndl"
	}
	return p
}

func portLabels() map[string]string { return map[string]string{} }
