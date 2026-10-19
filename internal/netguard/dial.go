// Package netguard restricts HTTPS clients to a fixed host and public IPs.
package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
)

func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		if netip.MustParsePrefix(p).Contains(ip) {
			return false
		}
	}
	return true
}

func DialContext(ctx context.Context, network, addr, allowedHost string) (net.Conn, error) {
	return dial(ctx, network, addr, allowedHost, net.DefaultResolver.LookupNetIP, (&net.Dialer{}).DialContext)
}

func dial(ctx context.Context, network, addr, allowedHost string,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	connect func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != allowedHost || port != "443" {
		return nil, errors.New("blocked destination")
	}
	ips, err := lookup(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("DNS unavailable")
	}
	for _, ip := range ips {
		if !PublicIP(ip) {
			return nil, errors.New("blocked destination")
		}
	}
	for _, ip := range ips {
		conn, err := connect(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("connection unavailable")
}
