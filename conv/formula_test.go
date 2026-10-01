package conv

import (
	"strings"
	"testing"
)

func TestCSVFormula(t *testing.T) {
	cells := map[string]string{"A1": "=SUMME(B1:B3)", "A2": "-5", "A3": "+cmd|' /C calc'!A0", "A4": "@SUM(1)", "A5": "Text", "A6": "-abc", "A7": "3.5"}
	out := string(ToCSV(cells))
	for _, want := range []string{"'=SUMME(B1:B3)", "\n-5\n", "'+cmd", "'@SUM(1)", "'-abc", "\nText\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("CSV fehlt %q in %q", want, out)
		}
	}
	back, err := FromCSV([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range cells {
		if back[k] != v {
			t.Errorf("Rundlauf %s: %q != %q", k, back[k], v)
		}
	}
}

func TestXLSXFormulaWhitelist(t *testing.T) {
	good := []string{"SUMME(A1:A5)", "WENN(A1>0;\"ja\";\"nein\")", "A1+B2*3", "ROUND(A1;2)&\"x\"", "MAX(A1:B2)"}
	bad := []string{"HYPERLINK(\"http://x\";\"y\")", "WEBSERVICE(\"http://x\")", "cmd|' /C calc'!A0", "SUMME([1]Sheet1!A1)", "A1+Sheet2!B1", "IMPORTDATA(\"u\")"}
	for _, f := range good {
		if !safeFormula(xlFormula(f)) {
			t.Errorf("gut abgelehnt: %s", f)
		}
	}
	for _, f := range bad {
		if safeFormula(xlFormula(f)) {
			t.Errorf("schlecht erlaubt: %s", f)
		}
	}
	b, err := ToXLSX(map[string]string{"A1": "=HYPERLINK(\"http://x\";\"y\")", "A2": "=SUMME(B1:B2)"})
	if err != nil {
		t.Fatal(err)
	}
	z := string(b)
	_ = z // Inhalt ist gezippt; Prüfung über Rundlauf
	back, err := FromXLSX(b)
	if err != nil {
		t.Fatal(err)
	}
	if back["A1"] != "=HYPERLINK(\"http://x\";\"y\")" {
		t.Errorf("A1 als Text erwartet: %q", back["A1"])
	}
}
