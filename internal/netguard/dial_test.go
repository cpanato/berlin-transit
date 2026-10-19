package netguard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestPublicIPs(t *testing.T) {
	for _, s := range []string{"0.0.0.0", "::", "255.255.255.255", "192.88.99.1", "2001::1", "127.0.0.1", "::1", "10.0.0.1", "172.16.0.1", "192.168.0.1", "169.254.169.254", "100.64.0.1", "192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1", "2002::1", "3fff::1"} {
		if PublicIP(netip.MustParseAddr(s)) {
			t.Error("unsafe IP accepted", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !PublicIP(netip.MustParseAddr(s)) {
			t.Error(s)
		}
	}
}

func TestDial(t *testing.T) {
	for _, tc := range []struct {
		name, addr            string
		ips                   []string
		lookupErr, connectErr bool
		want                  string
	}{
		{name: "bad address", addr: "host", want: "blocked destination"},
		{name: "host", addr: "evil:443", want: "blocked destination"},
		{name: "port", addr: "trmnl.com:80", want: "blocked destination"},
		{name: "DNS", addr: "trmnl.com:443", lookupErr: true, want: "DNS unavailable"},
		{name: "empty", addr: "trmnl.com:443", want: "DNS unavailable"},
		{name: "mixed DNS", addr: "trmnl.com:443", ips: []string{"1.1.1.1", "127.0.0.1"}, want: "blocked destination"},
		{name: "failed", addr: "trmnl.com:443", ips: []string{"1.1.1.1"}, connectErr: true, want: "connection unavailable"},
		{name: "public", addr: "trmnl.com:443", ips: []string{"1.1.1.1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connected := false
			lookup := func(_ context.Context, _, host string) ([]netip.Addr, error) {
				if host != "trmnl.com" {
					t.Fatal(host)
				}
				if tc.lookupErr {
					return nil, errors.New("secret DNS")
				}
				ips := []netip.Addr{}
				for _, ip := range tc.ips {
					ips = append(ips, netip.MustParseAddr(ip))
				}
				return ips, nil
			}
			connect := func(_ context.Context, network, addr string) (net.Conn, error) {
				connected = true
				if addr != "1.1.1.1:443" || network != "tcp" {
					t.Fatal("unvalidated connection", addr)
				}
				if tc.connectErr {
					return nil, errors.New("secret connection")
				}
				a, b := net.Pipe()
				_ = b.Close()
				return a, nil
			}
			conn, err := dial(context.Background(), "tcp", tc.addr, "trmnl.com", lookup, connect)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
			} else if err == nil || err.Error() != tc.want {
				t.Fatal(err)
			}
			if tc.want == "blocked destination" && connected {
				t.Fatal("connected before checking all answers")
			}
		})
	}
	if _, err := DialContext(context.Background(), "tcp", "evil:443", "trmnl.com"); err == nil {
		t.Fatal("unsafe wrapper")
	}
}
