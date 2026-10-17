package doc

import "testing"

func TestValidRuns(t *testing.T) {
	for _, r := range []string{"", "5:b;3:;4:b c#ff0000", "2:s18 g#ffff00 u", "3:t", "5:b t mHallo%20Welt;2:", "4:u t m%C3%A4%3B%20x"} {
		if !ValidRuns(r) {
			t.Fatal("soll gueltig sein:", r)
		}
	}
	for _, r := range []string{"b", "5:l", "5:n2", "5:x", "5:c#zzzzzz", "a:b", "5:b;<script>", "1:b;;2:i", "5:m", "5:m<b>", "5:mx y z!", "3:mx;3:t q"} {
		if ValidRuns(r) {
			t.Fatal("soll ungueltig sein:", r)
		}
	}
	if !ValidFmt("b t mHallo%20Welt d2") || ValidFmt("m") || ValidFmt("t x") || ValidFmt("mx<b>") {
		t.Fatal("ValidFmt t/m")
	}
	if !ValidFmt("o n2") || ValidFmt("o x") {
		t.Fatal("ValidFmt o")
	}
}

func TestValidFmtNumber(t *testing.T) {
	for _, f := range []string{"d0", "d9", "pc", "cu d2", "b pc d1", "cu", "hl", "hc w", "hr"} {
		if !ValidFmt(f) {
			t.Fatal("soll gueltig sein:", f)
		}
	}
	for _, f := range []string{"d", "d10", "dd", "pcx", "cu;", "d-1", "pc  cu x", "hx", "ww", "h"} {
		if ValidFmt(f) {
			t.Fatal("soll ungueltig sein:", f)
		}
	}
}
