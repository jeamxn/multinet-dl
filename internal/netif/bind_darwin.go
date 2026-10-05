//go:build darwin

package netif

import (
	"bufio"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// macOS: IP_BOUND_IF / IPV6_BOUND_IF force the socket onto one interface,
// so it uses that interface's own route and gateway.
func bindControl(index int, is6 bool) func(string, string, syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			if is6 {
				serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, index)
			} else {
				serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, index)
			}
		})
		if err != nil {
			return err
		}
		return serr
	}
}

// portLabels maps device (en0) -> hardware port name (Wi-Fi, iPhone USB ...).
func portLabels() map[string]string {
	out := map[string]string{}
	b, err := exec.Command("/usr/sbin/networksetup", "-listallhardwareports").Output()
	if err != nil {
		return out
	}
	var port string
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(l, "Hardware Port:"); ok {
			port = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(l, "Device:"); ok && port != "" {
			out[strings.TrimSpace(v)] = port
			port = ""
		}
	}
	return out
}

func bindingNote() string { return "" }
