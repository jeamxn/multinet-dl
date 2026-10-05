// Package netif lists the machine's network interfaces and builds HTTP clients
// whose connections are pinned to one interface, so traffic really leaves
// through that network (Wi-Fi, iPhone tethering, USB LAN, ...).
package netif

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Interface is one usable network interface.
type Interface struct {
	ID      string   `json:"id"`      // OS name: en0, "Wi-Fi 2", wlan0 ...
	Index   int      `json:"index"`   // OS interface index
	Label   string   `json:"label"`   // human name: Wi-Fi, iPhone USB ...
	Kind    string   `json:"kind"`    // wifi | ethernet | usb | vpn | virtual | other
	Virtual bool     `json:"virtual"` // VPN, bridge, VM adapter ...
	MAC     string   `json:"mac"`
	Addrs   []string `json:"addrs"`

	v4 []net.IP
	v6 []net.IP
}

// List returns interfaces that are up and have a routable address.
func List() ([]Interface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	labels := portLabels()
	var out []Interface
	for _, ni := range ifs {
		if ni.Flags&net.FlagUp == 0 || ni.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ni.Addrs()
		if err != nil {
			continue
		}
		it := Interface{ID: ni.Name, Index: ni.Index, MAC: ni.HardwareAddr.String()}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				it.v4 = append(it.v4, v4)
			} else {
				it.v6 = append(it.v6, ip)
			}
			it.Addrs = append(it.Addrs, ip.String())
		}
		if len(it.v4) == 0 && len(it.v6) == 0 {
			continue
		}
		it.Label, it.Kind, it.Virtual = classify(ni.Name, labels[ni.Name])
		if it.Kind == "vpn" && it.Label == ni.Name {
			it.Label = "VPN " + ni.Name
		}
		if it.Kind == "hidden" {
			continue
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Virtual != out[b].Virtual {
			return !out[a].Virtual
		}
		return rank(out[a].Kind) < rank(out[b].Kind)
	})
	return out, nil
}

func rank(kind string) int {
	switch kind {
	case "wifi":
		return 0
	case "ethernet":
		return 1
	case "usb":
		return 2
	case "other":
		return 3
	case "vpn":
		return 4
	}
	return 5
}

// BindingNote explains when traffic can't be pinned strictly (Linux without
// CAP_NET_RAW). Empty when pinning works.
func BindingNote() string { return bindingNote() }

// Find returns the interface with the given OS name.
func Find(id string) (*Interface, error) {
	all, err := List()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("네트워크 %q 를 찾을 수 없거나 연결돼 있지 않음", id)
}

// DialContext opens a TCP connection that goes out through this interface only.
func (it *Interface) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		res, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, r := range res {
			ips = append(ips, r.IP)
		}
	}
	// IPv4 first (most tethering/Wi-Fi only has v4 routes), then IPv6.
	sort.SliceStable(ips, func(a, b int) bool { return ips[a].To4() != nil && ips[b].To4() == nil })
	lastErr := errors.New("이 네트워크로 갈 수 있는 주소가 없음")
	for _, ip := range ips {
		is6 := ip.To4() == nil
		var local net.IP
		if is6 {
			if len(it.v6) == 0 {
				continue
			}
			local = it.v6[0]
		} else {
			if len(it.v4) == 0 {
				continue
			}
			local = it.v4[0]
		}
		d := net.Dialer{
			LocalAddr: &net.TCPAddr{IP: local},
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   bindControl(it.Index, is6),
		}
		nw := "tcp4"
		if is6 {
			nw = "tcp6"
		}
		c, err := d.DialContext(ctx, nw, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// Client returns an HTTP/1.1 client pinned to this interface. HTTP/2 is off on
// purpose: it would multiplex every range request onto one TCP connection.
func (it *Interface) Client(conns int) *http.Client {
	if conns < 1 {
		conns = 1
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           it.DialContext,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConns:          conns * 2,
		MaxIdleConnsPerHost:   conns,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
	}
	return &http.Client{Transport: tr}
}

// classify turns an OS interface name (+ macOS hardware port name) into a
// label, kind and virtual flag. kind "hidden" means never show it.
func classify(name, port string) (label, kind string, virtual bool) {
	low := strings.ToLower(name)
	lp := strings.ToLower(port)
	label = name
	if port != "" {
		label = port
	}
	// macOS / Linux internal or system plumbing
	for _, p := range []string{"awdl", "llw", "anpi", "gif", "stf", "nan", "ap1", "lo", "docker", "veth"} {
		if strings.HasPrefix(low, p) && port == "" {
			return label, "hidden", true
		}
	}
	switch {
	case strings.HasPrefix(low, "utun"), strings.HasPrefix(low, "ipsec"), strings.HasPrefix(low, "ppp"), strings.HasPrefix(low, "tun"), strings.HasPrefix(low, "wg"),
		containsAny(low, "tailscale", "zerotier", "wireguard", "openvpn", "vpn", "tap-", "forticlient", "cisco"):
		return label, "vpn", true
	case strings.HasPrefix(low, "bridge"), strings.HasPrefix(low, "vmenet"), strings.HasPrefix(low, "vnic"), strings.HasPrefix(low, "virbr"),
		containsAny(low, "vethernet", "vmware", "virtualbox", "hyper-v", "loopback", "bluetooth", "wsl"), lp == "thunderbolt bridge":
		return label, "virtual", true
	case containsAny(lp, "wi-fi", "wifi", "airport") || containsAny(low, "wi-fi", "wifi", "wlan", "wireless", "무선") || strings.HasPrefix(low, "wl"):
		return label, "wifi", false
	case containsAny(lp, "iphone", "ipad", "android", "usb") || containsAny(low, "usb", "rndis", "iphone"):
		return label, "usb", false
	case containsAny(lp, "ethernet", "lan", "thunderbolt") || containsAny(low, "ethernet", "이더넷") || strings.HasPrefix(low, "eth") || strings.HasPrefix(low, "en"):
		return label, "ethernet", false
	}
	return label, "other", false
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}
