package netguard

import (
	"net/netip"
	"testing"
)

func TestBlocked(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.0.1", "172.16.0.1", "169.254.169.254", "100.64.0.1", "100.127.255.254",
		"0.0.0.0", "0.1.2.3", "198.18.0.5", "192.0.0.8", "240.0.0.1", "255.255.255.255", "::1", "fe80::1", "fc00::1", "::ffff:10.0.0.1",
		"64:ff9b::a00:1", "64:ff9b::7f00:1", "224.0.0.1", "ff02::1", "2001:db8::1"} {
		if !Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s müsste gesperrt sein", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "100.128.0.1", "172.32.0.1", "2606:4700:4700::1111", "64:ff9b::808:808", "::ffff:8.8.8.8"} {
		if Blocked(netip.MustParseAddr(s)) {
			t.Errorf("%s müsste erlaubt sein", s)
		}
	}
}
