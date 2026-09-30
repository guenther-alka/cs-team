package conv

import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestTextRoundtrips(t *testing.T) {
	p := []string{"Erster Absatz mit Umlauten äöü ß & <tag>", "", "Zeile { \\ } mit Klammern", "Emoji 😀 Ende"}
	d, err := ToDOCX(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FromDOCX(d)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("docx: %v %q", err, got)
	}
	c, _ := ToCSText(p)
	if got, err := FromCSText(c); err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("cstext: %v %q", err, got)
	}
	if got, _ := FromTXT(ToTXT(p)); !reflect.DeepEqual(got, p) {
		t.Fatalf("txt: %q", got)
	}
	r := string(ToRTF(p))
	if !strings.HasPrefix(r, `{\rtf1`) || !strings.Contains(r, `\u228?`) || !strings.Contains(r, `\{`) || !strings.HasSuffix(r, "}") {
		t.Fatalf("rtf: %s", r)
	}
	if _, err := FromDOCX([]byte("kein zip")); err == nil {
		t.Fatal("garbage must fail")
	}
	if _, err := FromCSText([]byte(`{"format":"anderes"}`)); err == nil {
		t.Fatal("wrong format must fail")
	}
}

func TestLatin1Txt(t *testing.T) {
	got, _ := FromTXT([]byte("gr\xfc\xdfe\r\nzwei"))
	if !reflect.DeepEqual(got, []string{"grüße", "zwei"}) {
		t.Fatalf("%q", got)
	}
}

func TestCalcRoundtrips(t *testing.T) {
	cells := map[string]string{"A1": "5", "B1": "text & <x>", "A2": "=A1*2", "C3": "=SUM(A1:A2)", "D1": "007", "AA10": "1,5"}
	x, err := ToXLSX(cells)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FromXLSX(x)
	if err != nil || !reflect.DeepEqual(got, cells) {
		t.Fatalf("xlsx: %v\n%v", err, got)
	}
	c, _ := ToCSCalc(cells)
	if got, err := FromCSCalc(c); err != nil || !reflect.DeepEqual(got, cells) {
		t.Fatalf("cscalc: %v %v", err, got)
	}
	csvb := ToCSV(map[string]string{"A1": "a", "B1": "b;c", "A2": "1"})
	if got, err := FromCSV(csvb); err != nil || got["B1"] != "b;c" || got["A2"] != "1" {
		t.Fatalf("csv: %v %v", err, got)
	}
}

func TestCSVGermanSemicolon(t *testing.T) {
	got, err := FromCSV([]byte("\xef\xbb\xbfName;Wert\r\nMüller;3,5\r\n"))
	if err != nil || got["A1"] != "Name" || got["B2"] != "3,5" || got["A2"] != "Müller" {
		t.Fatalf("%v %v", err, got)
	}
}

// Excel schreibt Text über sharedStrings und Formeln mit gecachtem Ergebnis.
func TestXLSXFromExcelStyle(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(n, s string) { w, _ := zw.Create(n); w.Write([]byte(s)) }
	add("xl/sharedStrings.xml", `<sst><si><t>Hallo</t></si><si><r><t>Rich</t></r><r><t>Text</t></r></si></sst>`)
	add("xl/worksheets/sheet1.xml", `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1"><v>42</v></c></row>`+
		`<row r="2"><c r="A2"><f>C1*2</f><v>84</v></c><c r="B2" t="str"><f>A1&amp;"!"</f><v>Hallo!</v></c></row></sheetData></worksheet>`)
	zw.Close()
	got, err := FromXLSX(buf.Bytes())
	want := map[string]string{"A1": "Hallo", "B1": "RichText", "C1": "42", "A2": "=C1*2", "B2": `=A1&"!"`}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v\n%v", err, got)
	}
}

func TestColName(t *testing.T) {
	for k, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 701: "ZZ", 702: "AAA"} {
		if got := ColName(k); got != want {
			t.Errorf("ColName(%d)=%s want %s", k, got, want)
		}
		if c, _, _ := ColRow(want + "1"); c != k {
			t.Errorf("ColRow(%s1) col=%d want %d", want, c, k)
		}
	}
}

func TestXlFormula(t *testing.T) {
	for in, want := range map[string]string{
		"SUMME(A1:A3)":                "SUM(A1:A3)",
		`wenn(A1>0;"ja;nein";"Nein")`: `IF(A1>0,"ja;nein","Nein")`,
		"Mittelwert(B1:B4)+$C$1":      "AVERAGE(B1:B4)+$C$1",
		"UND(A1;WAHR)":                "AND(A1,TRUE)",
		"SUM(A1,A2)":                  "SUM(A1,A2)",
	} {
		if got := xlFormula(in); got != want {
			t.Fatalf("%s: got %s want %s", in, got, want)
		}
	}
	x, err := ToXLSX(map[string]string{"A1": "1", "A2": "=SUMME(A1;2)"})
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromXLSX(x)
	if err != nil || back["A2"] != "=SUM(A1,2)" {
		t.Fatal(back, err)
	}
}
