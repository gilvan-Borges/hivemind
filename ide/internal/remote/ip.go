package remote

import (
	"net"
	"strings"
)

// DetectAddrs returns the best IPv4 candidate addresses for the bridge,
// ordered by preference: Tailscale (100.64.0.0/10) first, then private LAN
// addresses (192.168/16, 10/8, 172.16/12). This lets the mobile app reach the
// PC over Tailscale (works from 4G/5G) or the home Wi-Fi, with no router
// port-forwarding.
func DetectAddrs() []string {
	out := make([]string, 0, 4)
	var tail, lan []string
	for _, a := range interfaceAddrs() {
		ip := net.ParseIP(a)
		if ip == nil || ip.To4() == nil {
			continue
		}
		if isTailscale(ip) {
			tail = append(tail, a)
			continue
		}
		if ip.IsPrivate() {
			lan = append(lan, a)
		}
	}
	out = append(out, tail...)
	out = append(out, lan...)
	if len(out) == 0 {
		if fb := outboundIPv4(); fb != "" {
			out = append(out, fb)
		}
	}
	return out
}

// isTailscale reports whether ip is inside the CGNAT range 100.64.0.0/10 used
// by Tailscale's userspace network.
func isTailscale(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

func interfaceAddrs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil || ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}

// outboundIPv4 dials a UDP socket toward the public internet to discover the
// local IPv4 address of the default route (fallback when no useful interface
// address is visible).
func outboundIPv4() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil {
		if v4 := addr.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}

// joinURLs builds http://host:port URLs from the detected addresses.
func joinURLs(addrs []string, port int) []string {
	urls := make([]string, 0, len(addrs))
	for _, a := range addrs {
		host := a
		if strings.Contains(a, ":") && net.ParseIP(a) != nil {
			// IPv6 literal (not currently produced by DetectAddrs, but safe).
			host = "[" + a + "]"
		}
		urls = append(urls, "http://"+host+":"+itoa(port))
	}
	return urls
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
