package conv

import (
	"reflect"
	"testing"
)

// xlsx-Import: Daten (eingebaute und eigene Datumsformate), Uhrzeit, Zahl ohne Datumsformat, Wahrheitswert, Formel mit Datumsformat.
func TestFromXLSXDatesAndBools(t *testing.T) {
	styles := `<?xml version="1.0"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<numFmts count="2"><numFmt numFmtId="164" formatCode="dd\.mm\.yyyy\ hh:mm"/><numFmt numFmtId="165" formatCode="#,##0.00&quot; Std&quot;"/></numFmts>` +
		`<cellXfs count="5"><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/><xf numFmtId="165"/><xf numFmtId="21"/></cellXfs></styleSheet>`
	sheet := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1">` +
		`<c r="A1" s="1"><v>45000</v></c>` + // 2023-03-15
		`<c r="B1" s="2"><v>45000.5625</v></c>` + // 2023-03-15 13:30
		`<c r="C1" s="3"><v>45000</v></c>` + // Zahl mit Text im Format: bleibt Zahl
		`<c r="D1" s="0"><v>45000</v></c>` + // allgemein: bleibt Zahl
		`<c r="E1" s="4"><v>0.75</v></c>` + // nur Uhrzeit
		`<c r="F1" t="b"><v>1</v></c><c r="G1" t="b"><v>0</v></c>` +
		`<c r="H1" s="1"><f>A1+1</f><v>45001</v></c>` + // Formel bleibt Formel
		`<c r="I1" s="1"><v>1</v></c>` + // 1.1.1900
		`</row></sheetData></worksheet>`
	b, err := zipParts([][2]string{{"[Content_Types].xml", ctypesXlsx}, {"_rels/.rels", relsXlsx},
		{"xl/workbook.xml", wbXlsx}, {"xl/styles.xml", styles}, {"xl/worksheets/sheet1.xml", sheet}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FromXLSX(b)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A1": "2023-03-15", "B1": "2023-03-15 13:30", "C1": "45000", "D1": "45000", "E1": "18:00:00",
		"F1": "TRUE", "G1": "FALSE", "H1": "=A1+1", "I1": "1900-01-01"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("xlsx: %v", got)
	}
	// ohne styles.xml: alles wie bisher (Zahlen bleiben Zahlen)
	b2, _ := zipParts([][2]string{{"xl/worksheets/sheet1.xml", sheet}})
	if got, _ := FromXLSX(b2); got["A1"] != "45000" || got["F1"] != "TRUE" {
		t.Fatalf("ohne styles: %v", got)
	}
	if xlDate("-1") != "-1" || xlDate("abc") != "abc" || xlDate("99999999") != "99999999" {
		t.Fatal("unplausible Werte muessen bleiben")
	}
}

// docx-Import: Tabellenzeilen werden Absaetze (Zellen mit Tab), mc:Fallback wird nicht doppelt gelesen, Text ausserhalb bleibt.
func TestFromDOCXTablesAndFallback(t *testing.T) {
	doc := `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"><w:body>` +
		`<w:p><w:r><w:t>Titel</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Name</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Alter</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr><w:tc><w:p><w:r><w:t>Anna</w:t></w:r></w:p><w:p><w:r><w:t>Maria</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>12</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p><w:r><w:t>Danach</w:t></w:r></w:p>` +
		`<mc:AlternateContent><mc:Choice><w:p><w:r><w:t>Textfeld</w:t></w:r></w:p></mc:Choice><mc:Fallback><w:p><w:r><w:t>Textfeld</w:t></w:r></w:p></mc:Fallback></mc:AlternateContent>` +
		`</w:body></w:document>`
	b, _ := zipParts([][2]string{{"[Content_Types].xml", ctypesDocx}, {"_rels/.rels", relsDocx}, {"word/document.xml", doc}})
	got, err := FromDOCX(b)
	want := []string{"Titel", "Name\tAlter", "Anna Maria\t12", "Danach", "Textfeld"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("docx: %v %q", err, got)
	}
}
