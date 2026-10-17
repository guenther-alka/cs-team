package conv

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func zipPart(t *testing.T, b []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			d, _ := io.ReadAll(rc)
			rc.Close()
			return string(d)
		}
	}
	t.Fatalf("Teil fehlt: %s", name)
	return ""
}

func mkPara(f string, segs ...rSeg) Para {
	v := ""
	for _, s := range segs {
		v += s.text
	}
	return Para{V: v, F: f, R: runsFrom(segs)}
}

func sg(text, f string) rSeg { return rSeg{text, parseFmt(f)} }

func TestRichRuns(t *testing.T) {
	segs := splitRuns("ab😀cd", "2:b;2:i t;3:")
	if len(segs) != 3 || segs[0].text != "ab" || segs[1].text != "😀" || segs[2].text != "cd" || !segs[1].f.t || !segs[0].f.b {
		t.Fatalf("splitRuns: %+v", segs)
	}
	if r := runsFrom(segs); r != "2:b;2:i t;2:" {
		t.Fatal("runsFrom:", r)
	}
	if r := runsFrom([]rSeg{sg("abc", ""), sg("def", "")}); r != "" {
		t.Fatal("ein leerer Lauf muss leer sein:", r)
	}
	if e := cmEncode("Bitte prüfen; ok (1)!"); e != "Bitte%20pr%C3%BCfen%3B%20ok%20%281%29%21" || cmDecode(e) != "Bitte prüfen; ok (1)!" {
		t.Fatal("cmEncode:", e)
	}
	if e := cmEncode(strings.Repeat("ä", 500)); len(e) > 600 || strings.Count(cmDecode(e), "ä") > 200 {
		t.Fatal("Kommentar nicht gekuerzt:", len(e))
	}
}

func TestDOCXRichRoundTrip(t *testing.T) {
	cm := cmEncode("Bitte prüfen; ok")
	in := []Para{
		mkPara("", sg("Hallo ", ""), sg("fett", "b"), sg(" ", ""), sg("kursiv durchgestrichen", "i t"), sg(" rot gross", "c#ff0000 s24"), sg(" gelb", "g#ffff00 u")),
		mkPara("", sg("mit ", ""), sg("Kommentar", "m"+cm), sg(" davor", "")),
		mkPara("l", sg("Punkt eins", "")),
		mkPara("l n1", sg("eingerückt", "")),
		mkPara("o", sg("eins", "")),
		mkPara("o", sg("zwei", "")),
		mkPara("", sg("Text", "")),
		mkPara("o", sg("neu eins", "")),
		mkPara("n2", sg("Einzug 2", "")),
		{V: "Tab\tText mit Emoji 😀"},
	}
	data, err := ToDOCXRich(in)
	if err != nil {
		t.Fatal(err)
	}
	doc := zipPart(t, data, "word/document.xml")
	for _, want := range []string{"<w:strike/>", "<w:b/>", "<w:i/>", `w:color w:val="FF0000"`, `<w:sz w:val="36"/>`, `w:fill="FFFF00"`, "<w:commentRangeStart", "<w:tab/>", `<w:numId w:val="3"/>`} {
		if !strings.Contains(doc, want) {
			t.Fatalf("document.xml ohne %s", want)
		}
	}
	if c := zipPart(t, data, "word/comments.xml"); !strings.Contains(c, "Bitte prüfen; ok") {
		t.Fatal("comments.xml:", c)
	}
	if n := zipPart(t, data, "word/numbering.xml"); strings.Count(n, "<w:num ") != 3 {
		t.Fatal("numbering.xml:", n)
	}
	out, err := FromDOCXRich(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("Absaetze: %d statt %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("Absatz %d: %+v statt %+v", i, out[i], in[i])
		}
	}
	// die alte Schnittstelle liefert weiter nur den Text
	if p, err := FromDOCX(data); err != nil || p[1] != "mit Kommentar davor" {
		t.Fatal("FromDOCX:", p, err)
	}
}

func TestDOCXRichWordStyle(t *testing.T) {
	const w = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	docx, _ := zipParts([][2]string{
		{"word/document.xml", `<w:document ` + w + `><w:body>` +
			`<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="5"/></w:numPr><w:ind w:left="720" w:hanging="360"/></w:pPr><w:r><w:t>Punkt</w:t></w:r></w:p>` +
			`<w:p><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="6"/></w:numPr><w:ind w:left="1440"/><w:rPr><w:b/></w:rPr></w:pPr><w:r><w:rPr><w:b/><w:highlight w:val="yellow"/><w:sz w:val="28"/></w:rPr><w:t>Unterpunkt</w:t></w:r></w:p>` +
			`<w:p><w:commentRangeStart w:id="7"/><w:r><w:rPr><w:b w:val="0"/><w:u w:val="none"/></w:rPr><w:t>kommentiert</w:t></w:r><w:commentRangeEnd w:id="7"/><w:r><w:commentReference w:id="7"/></w:r><w:r><w:t xml:space="preserve"> danach</w:t></w:r></w:p>` +
			`</w:body></w:document>`},
		{"word/numbering.xml", `<w:numbering ` + w + `><w:abstractNum w:abstractNumId="7"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum>` +
			`<w:abstractNum w:abstractNumId="8"><w:lvl w:ilvl="0"><w:numFmt w:val="lowerLetter"/></w:lvl></w:abstractNum>` +
			`<w:num w:numId="5"><w:abstractNumId w:val="7"/></w:num><w:num w:numId="6"><w:abstractNumId w:val="8"/></w:num></w:numbering>`},
		{"word/comments.xml", `<w:comments ` + w + `><w:comment w:id="7" w:author="A"><w:p><w:r><w:t>Prüfen</w:t></w:r></w:p></w:comment></w:comments>`},
	})
	got, err := FromDOCXRich(docx)
	if err != nil || len(got) != 3 {
		t.Fatal(got, err)
	}
	want := []Para{
		{V: "Punkt", F: "l"},
		{V: "Unterpunkt", F: "o n2", R: "10:b s19 g#ffff00"},
		{V: "kommentiert danach", R: "11:m" + cmEncode("Prüfen") + ";7:"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Absatz %d: %+v statt %+v", i, got[i], want[i])
		}
	}
}

func TestXLSXRichRoundTrip(t *testing.T) {
	cells := map[string]FCell{
		"A1": {"Name", "b"},
		"B1": {"Wert", "b hc"},
		"A2": {"Test", "t m" + cmEncode("Notiz da")},
		"B2": {"12.5", "c#ff0000 g#ffff00 d2"},
		"B3": {"0.125", "d0 pc"},
		"B4": {"1234.5", "d2 cu"},
		"C1": {"", "g#00ff00"},
		"B5": {"=SUM(B2:B4)", "b"},
		"A5": {"lang", "s18 hr w"},
		"A6": {"ohne Format", ""},
	}
	data, err := ToXLSXRich(cells)
	if err != nil {
		t.Fatal(err)
	}
	st := zipPart(t, data, "xl/styles.xml")
	for _, want := range []string{"<strike/>", "<b/>", `rgb="FFFF0000"`, `rgb="FFFFFF00"`, `horizontal="center"`, `wrapText="1"`, `formatCode="0%"`} {
		if !strings.Contains(st, want) {
			t.Fatalf("styles.xml ohne %s", want)
		}
	}
	if c := zipPart(t, data, "xl/comments1.xml"); !strings.Contains(c, `ref="A2"`) || !strings.Contains(c, "Notiz da") {
		t.Fatal("comments1.xml:", c)
	}
	zipPart(t, data, "xl/drawings/vmlDrawing1.vml")
	zipPart(t, data, "xl/worksheets/_rels/sheet1.xml.rels")
	back, err := FromXLSXRich(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(cells) {
		t.Fatalf("Zellen: %d statt %d", len(back), len(cells))
	}
	for k, c := range cells {
		if back[k] != c {
			t.Fatalf("Zelle %s: %+v statt %+v", k, back[k], c)
		}
	}
	// ohne Formate entsteht weder styles.xml noch ein Kommentarteil, und die alte Schnittstelle bleibt gleich
	plain, _ := ToXLSX(map[string]string{"A1": "x", "B1": "2"})
	for _, n := range []string{"xl/styles.xml", "xl/comments1.xml"} {
		zr, _ := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
		for _, f := range zr.File {
			if f.Name == n {
				t.Fatal("unerwarteter Teil", n)
			}
		}
	}
	if m, err := FromXLSX(plain); err != nil || m["A1"] != "x" || m["B1"] != "2" {
		t.Fatal(m, err)
	}
}

func TestXLSXRichExcelStyle(t *testing.T) {
	const ns = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"`
	x, _ := zipParts([][2]string{
		{"xl/styles.xml", `<styleSheet ` + ns + `><numFmts count="1"><numFmt numFmtId="164" formatCode="0.0%"/></numFmts>` +
			`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><strike/><sz val="14"/><color rgb="FF0000FF"/><name val="Calibri"/></font></fonts>` +
			`<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FFFFFF00"/></patternFill></fill></fills>` +
			`<cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0"/><xf numFmtId="164" fontId="1" fillId="2"><alignment horizontal="right" wrapText="1"/></xf><xf numFmtId="4" fontId="0" fillId="0"/></cellXfs></styleSheet>`},
		{"xl/worksheets/sheet1.xml", `<worksheet ` + ns + `><sheetData><row r="1"><c r="A1" s="1"><v>0.25</v></c><c r="B1" s="2"><v>3</v></c><c r="C1" t="inlineStr"><is><t>x</t></is></c></row></sheetData></worksheet>`},
		{"xl/worksheets/_rels/sheet1.xml.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="../comments1.xml"/></Relationships>`},
		{"xl/comments1.xml", `<comments ` + ns + `><authors><author>Anna</author></authors><commentList><comment ref="C1" authorId="0"><text><r><rPr><b/></rPr><t>Anna:</t></r><r><t xml:space="preserve">
Bitte pruefen</t></r></text></comment></commentList></comments>`},
	})
	got, err := FromXLSXRich(x)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FCell{
		"A1": {"0.25", "b t s19 c#0000ff g#ffff00 d1 pc hr w"},
		"B1": {"3", "d2"},
		"C1": {"x", "mBitte%20pruefen"},
	}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for k, c := range want {
		if got[k] != c {
			t.Fatalf("Zelle %s: %+v statt %+v", k, got[k], c)
		}
	}
}

func TestCSFormatsV2(t *testing.T) {
	p := []Para{{V: "a", F: "l", R: "1:b"}, {V: "b"}}
	b, err := ToCSTextRich(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FromCSTextRich(b)
	if err != nil || got[0] != p[0] || got[1] != p[1] {
		t.Fatal(got, err)
	}
	if plain, _ := ToCSText([]string{"x"}); !strings.Contains(string(plain), `"version": 1`) || strings.Contains(string(plain), "formats") {
		t.Fatal("ohne Formate bleibt es Version 1:", string(plain))
	}
	old := []byte(`{"format":"cs-team/text","version":1,"paragraphs":["alt","text"]}`)
	if g, err := FromCSTextRich(old); err != nil || g[0].V != "alt" || g[0].F != "" {
		t.Fatal(g, err)
	}
	cells := map[string]FCell{"A1": {"1", "b t"}, "B2": {"", "g#ffff00"}, "C3": {"x", ""}}
	cb, err := ToCSCalcRich(cells)
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromCSCalcRich(cb)
	if err != nil || len(back) != 3 {
		t.Fatal(back, err)
	}
	for k, c := range cells {
		if back[k] != c {
			t.Fatalf("Zelle %s: %+v", k, back[k])
		}
	}
	if m, err := FromCSCalc(cb); err != nil || len(m) != 2 || m["A1"] != "1" {
		t.Fatal(m, err)
	}
	if g, err := FromCSCalc([]byte(`{"format":"cs-team/calc","version":1,"cells":{"A1":"5"}}`)); err != nil || g["A1"] != "5" {
		t.Fatal(g, err)
	}
}
