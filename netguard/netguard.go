// Package netguard: gemeinsame Sperrliste für ausgehende Verbindungen (Kalender-Abos, Webhooks, KI-Anbieter).
package netguard

import "net/netip"

var blocked = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // "dieses Netz"
		"100.64.0.0/10",   // CGNAT / Cloud-Metadaten einiger Anbieter
		"192.0.0.0/24",    // IETF-Protokollzuweisungen
		"192.0.2.0/24",    // Dokumentation
		"198.18.0.0/15",   // Benchmark
		"198.51.100.0/24", // Dokumentation
		"203.0.113.0/24",  // Dokumentation
		"240.0.0.0/4",     // reserviert
		"64:ff9b:1::/48",  // lokales NAT64
		"2001:db8::/32",   // Dokumentation
		"100::/64",        // Discard
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Blocked meldet, ob ip kein öffentliches Ziel ist (Loopback, privat, link-local, Multicast, unspezifiziert,
// reservierte Bereiche). IPv4-in-IPv6 und NAT64 (64:ff9b::/96) werden anhand der eingebetteten IPv4-Adresse geprüft.
func Blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() {
		return true
	}
	if b := ip.As16(); ip.Is6() && b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b && allZero(b[4:12]) {
		return Blocked(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
