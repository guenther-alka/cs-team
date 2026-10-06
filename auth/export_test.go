package auth

import (
	"strings"
	"testing"
)

// ZipSafe: keine "..", keine absoluten Pfade, keine Laufwerksbuchstaben/Sonderzeichen (zip-slip).
func TestZipSafe(t *testing.T) {
	for in, want := range map[string]string{
		"a.txt":                "a.txt",
		"ordner/b.txt":         "ordner/b.txt",
		"../../etc/passwd":     "_/_/etc/passwd",
		"/abs/pfad":            "abs/pfad",
		`C:\Windows\x.txt`:     "C_/Windows/x.txt",
		`ord\..\..\x`:          "ord/_/_/x",
		"a:b*c?.txt":           "a_b_c_.txt",
		"":                     "_",
		"..":                   "_",
		"  . ":                 "_",
		"nul\x00byte/ctl\x01x": "nul_byte/ctl_x",
	} {
		if got := ZipSafe(in); got != want {
			t.Errorf("ZipSafe(%q) = %q, erwartet %q", in, got, want)
		}
	}
	if got := ZipSafe(strings.Repeat("ä", 500)); len([]rune(got)) != 120 {
		t.Errorf("lange Namen: %d Zeichen", len([]rune(got)))
	}
	if got := ZipSafe(strings.Repeat("d/", 30) + "f"); strings.Count(got, "/") != 11 {
		t.Errorf("zu tiefer Pfad: %q", got)
	}
}
