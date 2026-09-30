package doc

import "testing"

func TestValidRuns(t *testing.T) {
	for _, r := range []string{"", "5:b;3:;4:b c#ff0000", "2:s18 g#ffff00 u"} {
		if !ValidRuns(r) {
			t.Fatal("soll gueltig sein:", r)
		}
	}
	for _, r := range []string{"b", "5:l", "5:n2", "5:x", "5:c#zzzzzz", "a:b", "5:b;<script>", "1:b;;2:i"} {
		if ValidRuns(r) {
			t.Fatal("soll ungueltig sein:", r)
		}
	}
	if !ValidFmt("o n2") || ValidFmt("o x") {
		t.Fatal("ValidFmt o")
	}
}
