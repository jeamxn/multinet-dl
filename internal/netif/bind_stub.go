//go:build !darwin && !windows && !linux

package netif

import "syscall"

func bindControl(int, bool) func(string, string, syscall.RawConn) error { return nil }

func portLabels() map[string]string { return map[string]string{} }

func bindingNote() string {
	return "이 OS에서는 출발 주소만 고정함(라우팅에 따라 다른 네트워크로 나갈 수 있음)"
}
