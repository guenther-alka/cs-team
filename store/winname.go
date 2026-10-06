package store

import (
	"runtime"
	"strings"
)

// WinBad: Namen, die Windows als Gerät oder Datenstrom deutet (CON, NUL, COM1 ..., "a:b", "x::$DATA",
// Namen mit Punkt/Leerzeichen am Ende), mit und ohne Endung ("nul.txt").
func WinBad(name string) bool {
	if strings.ContainsAny(name, ":*?\"<>|") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return true
	}
	b := strings.ToUpper(name)
	if i := strings.Index(b, "."); i >= 0 {
		b = b[:i]
	}
	switch b {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// WinReserved: WinBad, aber nur auf Windows-Servern wirksam (Test-/Entwicklungsbetrieb mit dem Dateispeicher).
// Konten-, Gruppen- und Kalendernamen werden zu Verzeichnisnamen; auf ZFS/Linux sind diese Namen unkritisch.
func WinReserved(name string) bool { return runtime.GOOS == "windows" && WinBad(name) }
