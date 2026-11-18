//go:build !illumos && !solaris && !windows && !linux && !freebsd

package osacct

// New: auf diesem Betriebssystem (z. B. macOS) legt cs-team keine Betriebssystem-Konten an.
func New() Backend { return nil }
