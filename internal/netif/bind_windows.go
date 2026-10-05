//go:build windows

package netif

import (
	"encoding/binary"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ipUnicastIF   = 31 // IP_UNICAST_IF
	ipv6UnicastIF = 31 // IPV6_UNICAST_IF
)

// Windows: IP_UNICAST_IF pins outgoing traffic to one interface. For IPv4 the
// index must be in network byte order, for IPv6 in host order.
func bindControl(index int, is6 bool) func(string, string, syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			h := windows.Handle(fd)
			if is6 {
				serr = windows.SetsockoptInt(h, windows.IPPROTO_IPV6, ipv6UnicastIF, index)
			} else {
				var b [4]byte
				binary.BigEndian.PutUint32(b[:], uint32(index))
				v := *(*int32)(unsafe.Pointer(&b[0]))
				serr = windows.SetsockoptInt(h, windows.IPPROTO_IP, ipUnicastIF, int(v))
			}
		})
		if err != nil {
			return err
		}
		return serr
	}
}

// On Windows net.Interface.Name is already the friendly name (Wi-Fi, 이더넷 2).
func portLabels() map[string]string { return map[string]string{} }

func bindingNote() string { return "" }
