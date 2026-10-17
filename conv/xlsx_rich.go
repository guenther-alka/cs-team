package conv

// .xlsx mit Formaten: Schrift (fett, kursiv, unterstrichen, durchgestrichen, Farbe, Groesse), Hintergrund, Zahlenformat (Nachkommastellen,
// Prozent, Waehrung), Ausrichtung, Zeilenumbruch und Notizen (Kommentare der Zelle, xl/comments1.xml + VML). Nur Go-Standardbibliothek.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

const nsXlMain = `http://schemas.openxmlformats.org/spreadsheetml/2006/main`

func addUniq(list *[]string, idx map[string]int, x string) int {
	if i, ok := idx[x]; ok {
		return i
	}
	*list = append(*list, x)
	idx[x] = len(*list) - 1
	return len(*list) - 1
}

// xlStyles sammelt die verwendeten Schriften, Fuellungen, Zahlenformate und Zellformate fuer xl/styles.xml.
type xlStyles struct {
	fonts, fills, numFmts, xfs     []string
	fontIdx, fillIdx, numIdx, xfIdx map[string]int
}

func newXlStyles() *xlStyles {
	s := &xlStyles{fontIdx: map[string]int{}, fillIdx: map[string]int{}, numIdx: map[string]int{}, xfIdx: map[string]int{}}
	addUniq(&s.fonts, s.fontIdx, `<font><sz val="11"/><name val="Calibri"/><family val="2"/></font>`)
	addUniq(&s.fills, s.fillIdx, `<fill><patternFill patternType="none"/></fill>`)
	addUniq(&s.fills, s.fillIdx, `<fill><patternFill patternType="gray125"/></fill>`)
	addUniq(&s.xfs, s.xfIdx, `<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>`)
	return s
}

func xlNumCode(f rFmt) string {
	zeros := func(d int) string {
		if d <= 0 {
			return ""
		}
		return "." + strings.Repeat("0", d)
	}
	switch {
	case f.pc:
		return "0" + zeros(max(f.d, 0)) + "%"
	case f.cu:
		d := 2
		if f.d >= 0 {
			d = f.d
		}
		return `#,##0` + zeros(d) + `\ "€"`
	case f.d >= 0:
		return "0" + zeros(f.d)
	}
	return ""
}

// idFor liefert den Index in cellXfs (0 = Standard, wenn das Format nichts Sichtbares enthaelt).
func (s *xlStyles) idFor(f rFmt) int {
	if !(f.b || f.i || f.u || f.t || f.s > 0 || f.c != "" || f.g != "" || f.d >= 0 || f.pc || f.cu || f.h != 0 || f.w) {
		return 0
	}
	var fb strings.Builder
	fb.WriteString("<font>")
	if f.b {
		fb.WriteString("<b/>")
	}
	if f.i {
		fb.WriteString("<i/>")
	}
	if f.t {
		fb.WriteString("<strike/>")
	}
	if f.u {
		fb.WriteString("<u/>")
	}
	sz := 11.0
	if f.s > 0 {
		sz = float64(f.s) * 0.75
	}
	fb.WriteString(`<sz val="` + strconv.FormatFloat(sz, 'f', -1, 64) + `"/>`)
	if f.c != "" {
		fb.WriteString(`<color rgb="FF` + strings.ToUpper(f.c) + `"/>`)
	}
	fb.WriteString(`<name val="Calibri"/><family val="2"/></font>`)
	fontID := addUniq(&s.fonts, s.fontIdx, fb.String())
	fillID := 0
	if f.g != "" {
		fillID = addUniq(&s.fills, s.fillIdx, `<fill><patternFill patternType="solid"><fgColor rgb="FF`+strings.ToUpper(f.g)+`"/><bgColor indexed="64"/></patternFill></fill>`)
	}
	numID := 0
	if code := xlNumCode(f); code != "" {
		numID = 164 + addUniq(&s.numFmts, s.numIdx, code)
	}
	xf := fmt.Sprintf(`<xf numFmtId="%d" fontId="%d" fillId="%d" borderId="0" xfId="0"`, numID, fontID, fillID)
	if numID != 0 {
		xf += ` applyNumberFormat="1"`
	}
	if fontID != 0 {
		xf += ` applyFont="1"`
	}
	if fillID != 0 {
		xf += ` applyFill="1"`
	}
	if f.h != 0 || f.w {
		al := `<alignment`
		if f.h != 0 {
			al += ` horizontal="` + map[byte]string{'l': "left", 'c': "center", 'r': "right"}[f.h] + `"`
		}
		if f.w {
			al += ` wrapText="1"`
		}
		xf += ` applyAlignment="1">` + al + `/></xf>`
	} else {
		xf += `/>`
	}
	return addUniq(&s.xfs, s.xfIdx, xf)
}

func (s *xlStyles) xml() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<styleSheet xmlns="` + nsXlMain + `">`)
	if len(s.numFmts) > 0 {
		b.WriteString(`<numFmts count="` + strconv.Itoa(len(s.numFmts)) + `">`)
		for i, c := range s.numFmts {
			b.WriteString(`<numFmt numFmtId="` + strconv.Itoa(164+i) + `" formatCode="` + xmlEsc(c) + `"/>`)
		}
		b.WriteString(`</numFmts>`)
	}
	b.WriteString(`<fonts count="` + strconv.Itoa(len(s.fonts)) + `">` + strings.Join(s.fonts, "") + `</fonts>`)
	b.WriteString(`<fills count="` + strconv.Itoa(len(s.fills)) + `">` + strings.Join(s.fills, "") + `</fills>`)
	b.WriteString(`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>`)
	b.WriteString(`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`)
	b.WriteString(`<cellXfs count="` + strconv.Itoa(len(s.xfs)) + `">` + strings.Join(s.xfs, "") + `</cellXfs>`)
	b.WriteString(`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`)
	return b.String()
}

type xlNote struct {
	ref      string
	col, row int // row 1-basiert wie ColRow
	text     string
}

func xlVML(notes []xlNote) string {
	var b strings.Builder
	b.WriteString(`<xml xmlns:v="urn:schemas-microsoft-com:vml" xmlns:o="urn:schemas-microsoft-com:office:office" xmlns:x="urn:schemas-microsoft-com:office:excel">` +
		`<o:shapelayout v:ext="edit"><o:idmap v:ext="edit" data="1"/></o:shapelayout>` +
		`<v:shapetype id="_x0000_t202" coordsize="21600,21600" o:spt="202" path="m,l,21600r21600,l21600,xe"><v:stroke joinstyle="miter"/><v:path gradientshapeok="t" o:connecttype="rect"/></v:shapetype>`)
	for i, n := range notes {
		r0 := n.row - 1
		b.WriteString(fmt.Sprintf(`<v:shape id="_x0000_s%d" type="#_x0000_t202" style="position:absolute;margin-left:80pt;margin-top:2pt;width:140pt;height:60pt;z-index:%d;visibility:hidden" fillcolor="#ffffe1" o:insetmode="auto">`+
			`<v:fill color2="#ffffe1"/><v:shadow on="t" color="black" obscured="t"/><v:path o:connecttype="none"/><v:textbox style="mso-direction-alt:auto"><div style="text-align:left"></div></v:textbox>`+
			`<x:ClientData ObjectType="Note"><x:MoveWithCells/><x:SizeWithCells/><x:Anchor>%d, 15, %d, 2, %d, 15, %d, 16</x:Anchor><x:AutoFill>False</x:AutoFill><x:Row>%d</x:Row><x:Column>%d</x:Column></x:ClientData></v:shape>`,
			1025+i, i+1, n.col+1, r0, n.col+3, r0+3, r0, n.col))
	}
	b.WriteString(`</xml>`)
	return b.String()
}

func ToXLSX(cells map[string]string) ([]byte, error) {
	m := make(map[string]FCell, len(cells))
	for k, v := range cells {
		m[k] = FCell{V: v}
	}
	return ToXLSXRich(m)
}

func ToXLSXRich(cells map[string]FCell) ([]byte, error) {
	type ent struct {
		k        string
		col, row int
	}
	var es []ent
	for k, c := range cells {
		col, row, ok := ColRow(k)
		if !ok || (c.V == "" && c.F == "") {
			continue
		}
		es = append(es, ent{k, col, row})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].row != es[j].row {
			return es[i].row < es[j].row
		}
		return es[i].col < es[j].col
	})
	st := newXlStyles()
	var notes []xlNote
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<worksheet xmlns="` + nsXlMain + `" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheetData>`)
	curRow := 0
	for _, e := range es {
		if e.row != curRow {
			if curRow != 0 {
				b.WriteString(`</row>`)
			}
			curRow = e.row
			b.WriteString(`<row r="` + strconv.Itoa(curRow) + `">`)
		}
		c := cells[e.k]
		f := parseFmt(c.F)
		s := ""
		if si := st.idFor(f); si > 0 {
			s = ` s="` + strconv.Itoa(si) + `"`
		}
		if f.m != "" {
			notes = append(notes, xlNote{e.k, e.col, e.row, cmDecode(f.m)})
		}
		v := c.V
		switch {
		case v == "":
			b.WriteString(`<c r="` + e.k + `"` + s + `/>`)
		case strings.HasPrefix(v, "=") && len(v) > 1 && safeFormula(xlFormula(v[1:])):
			b.WriteString(`<c r="` + e.k + `"` + s + `><f>` + xmlEsc(xlFormula(v[1:])) + `</f></c>`)
		case isNumber(v):
			b.WriteString(`<c r="` + e.k + `"` + s + `><v>` + v + `</v></c>`)
		default:
			b.WriteString(`<c r="` + e.k + `"` + s + ` t="inlineStr"><is><t xml:space="preserve">` + xmlEsc(v) + `</t></is></c>`)
		}
	}
	if curRow != 0 {
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData>`)
	if len(notes) > 0 {
		b.WriteString(`<legacyDrawing r:id="rId2"/>`)
	}
	b.WriteString(`</worksheet>`)

	hasStyles := len(st.xfs) > 1
	ct := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>`
	if len(notes) > 0 {
		ct += `<Default Extension="vml" ContentType="application/vnd.openxmlformats-officedocument.vmlDrawing"/>`
	}
	ct += `<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`
	if hasStyles {
		ct += `<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`
	}
	if len(notes) > 0 {
		ct += `<Override PartName="/xl/comments1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.comments+xml"/>`
	}
	ct += `</Types>`
	wbRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>`
	if hasStyles {
		wbRels += `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`
	}
	wbRels += `</Relationships>`
	parts := [][2]string{{"[Content_Types].xml", ct}, {"_rels/.rels", relsXlsx}, {"xl/workbook.xml", wbXlsx}, {"xl/_rels/workbook.xml.rels", wbRels}}
	if hasStyles {
		parts = append(parts, [2]string{"xl/styles.xml", st.xml()})
	}
	parts = append(parts, [2]string{"xl/worksheets/sheet1.xml", b.String()})
	if len(notes) > 0 {
		var cb strings.Builder
		cb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<comments xmlns="` + nsXlMain + `"><authors><author>cs-team</author></authors><commentList>`)
		for _, n := range notes {
			cb.WriteString(`<comment ref="` + n.ref + `" authorId="0"><text><r><t xml:space="preserve">` + xmlEsc(n.text) + `</t></r></text></comment>`)
		}
		cb.WriteString(`</commentList></comments>`)
		parts = append(parts,
			[2]string{"xl/worksheets/_rels/sheet1.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="../comments1.xml"/>` +
				`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/vmlDrawing" Target="../drawings/vmlDrawing1.vml"/></Relationships>`},
			[2]string{"xl/comments1.xml", cb.String()},
			[2]string{"xl/drawings/vmlDrawing1.vml", xlVML(notes)})
	}
	return zipParts(parts)
}

// ---------- Import ----------

// xlNumToCS: Excel-Zahlenformat -> Nachkommastellen (-1 = keine Angabe), Prozent, Waehrung.
func xlNumToCS(id int, code string) (d int, pc, cu bool) {
	switch id {
	case 1, 3:
		return 0, false, false
	case 2, 4:
		return 2, false, false
	case 9:
		return 0, true, false
	case 10:
		return 2, true, false
	}
	if code == "" || strings.EqualFold(code, "General") || isDateFormat(code) {
		return -1, false, false
	}
	pc = strings.Contains(code, "%")
	cu = strings.ContainsAny(code, "€$£") || strings.Contains(code, "[$")
	d = -1
	if i := strings.IndexByte(code, '.'); i >= 0 {
		n := 0
		for j := i + 1; j < len(code) && code[j] == '0'; j++ {
			n++
		}
		d = n
	} else if strings.ContainsAny(code, "0#") {
		d = 0
	}
	if d < 0 && !pc && !cu {
		return -1, false, false
	}
	return d, pc, cu
}

// xlStyleFormats: je Eintrag in cellXfs der entsprechende Formatstring des Calc.
func xlStyleFormats(zr *zip.Reader) []string {
	sb, err := readPart(zr, "xl/styles.xml")
	if err != nil {
		return nil
	}
	dec := xml.NewDecoder(bytes.NewReader(sb))
	numFmts := map[int]string{}
	var fonts []rFmt
	var fills, out []string
	inFonts, inFont, inFills, inFill, inXfs, inXf, solid := false, false, false, false, false, false, false
	font := rFmt{d: -1}
	fill := ""
	var xf struct {
		num, font, fill int
		h               byte
		w               bool
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "numFmt":
				if id, err := strconv.Atoi(xAttr(t, "numFmtId")); err == nil {
					numFmts[id] = xAttr(t, "formatCode")
				}
			case "fonts":
				inFonts = true
			case "font":
				if inFonts {
					inFont, font = true, rFmt{d: -1}
				}
			case "b":
				if inFont {
					font.b = xOnOff(t)
				}
			case "i":
				if inFont {
					font.i = xOnOff(t)
				}
			case "u":
				if inFont {
					font.u = xOnOff(t)
				}
			case "strike":
				if inFont {
					font.t = xOnOff(t)
				}
			case "sz":
				if inFont {
					if v, err := strconv.ParseFloat(xAttr(t, "val"), 64); err == nil && v > 0 {
						font.s = int(v*4/3 + 0.5)
					}
				}
			case "color":
				if inFont {
					if rgb := xAttr(t, "rgb"); len(rgb) == 8 && isHex6(rgb[2:]) && !strings.EqualFold(rgb[2:], "000000") {
						font.c = strings.ToLower(rgb[2:])
					}
				}
			case "fills":
				inFills = true
			case "fill":
				if inFills {
					inFill, fill, solid = true, "", false
				}
			case "patternFill":
				if inFill {
					solid = xAttr(t, "patternType") == "solid"
				}
			case "fgColor":
				if inFill && solid {
					if rgb := xAttr(t, "rgb"); len(rgb) == 8 && isHex6(rgb[2:]) {
						fill = strings.ToLower(rgb[2:])
					}
				}
			case "cellXfs":
				inXfs = true
			case "xf":
				if inXfs {
					inXf = true
					xf.num, _ = strconv.Atoi(xAttr(t, "numFmtId"))
					xf.font, _ = strconv.Atoi(xAttr(t, "fontId"))
					xf.fill, _ = strconv.Atoi(xAttr(t, "fillId"))
					xf.h, xf.w = 0, false
				}
			case "alignment":
				if inXf {
					switch xAttr(t, "horizontal") {
					case "left":
						xf.h = 'l'
					case "center":
						xf.h = 'c'
					case "right":
						xf.h = 'r'
					}
					xf.w = xAttr(t, "wrapText") == "1" || xAttr(t, "wrapText") == "true"
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "font":
				if inFont {
					fonts = append(fonts, font)
					inFont = false
				}
			case "fonts":
				inFonts = false
			case "fill":
				if inFill {
					fills = append(fills, fill)
					inFill = false
				}
			case "fills":
				inFills = false
			case "xf":
				if inXf {
					inXf = false
					f := rFmt{d: -1}
					if xf.font >= 0 && xf.font < len(fonts) {
						fo := fonts[xf.font]
						f.b, f.i, f.u, f.t, f.c = fo.b, fo.i, fo.u, fo.t, fo.c
						if fo.s > 0 && len(fonts) > 0 && fo.s != fonts[0].s {
							f.s = fo.s
						}
					}
					if xf.fill >= 0 && xf.fill < len(fills) {
						f.g = fills[xf.fill]
					}
					f.d, f.pc, f.cu = xlNumToCS(xf.num, numFmts[xf.num])
					f.h, f.w = xf.h, xf.w
					out = append(out, f.str())
				}
			case "cellXfs":
				inXfs = false
			}
		}
		if len(out) > 20000 {
			break
		}
	}
	return out
}

// xlNotes liest die Notizen (Kommentare) des Blatts: Zellbezug -> Text.
func xlNotes(zr *zip.Reader, sheet string) map[string]string {
	out := map[string]string{}
	rb, err := readPart(zr, path.Join(path.Dir(sheet), "_rels", path.Base(sheet)+".rels"))
	if err != nil {
		return out
	}
	target := ""
	rd := xml.NewDecoder(bytes.NewReader(rb))
	for {
		tok, err := rd.Token()
		if err != nil {
			break
		}
		if t, ok := tok.(xml.StartElement); ok && t.Name.Local == "Relationship" && strings.HasSuffix(xAttr(t, "Type"), "/comments") {
			target = xAttr(t, "Target")
		}
	}
	if target == "" {
		return out
	}
	p := path.Join(path.Dir(sheet), target)
	if strings.HasPrefix(target, "/") {
		p = target[1:]
	}
	cb, err := readPart(zr, p)
	if err != nil {
		return out
	}
	dec := xml.NewDecoder(bytes.NewReader(cb))
	var authors []string
	var cur strings.Builder
	ref, aid := "", -1
	inAuthor, inComment, inT, inPh := false, false, false, false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "author":
				inAuthor = true
				authors = append(authors, "")
			case "comment":
				inComment, ref = true, xAttr(t, "ref")
				aid, _ = strconv.Atoi(xAttr(t, "authorId"))
				cur.Reset()
			case "rPh":
				inPh = true
			case "t":
				inT = inComment && !inPh
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "author":
				inAuthor = false
			case "rPh":
				inPh = false
			case "t":
				inT = false
			case "comment":
				inComment = false
				txt := cur.String()
				if i := strings.Index(txt, "\nComment:\n"); i >= 0 && strings.HasPrefix(txt, "[Threaded comment]") {
					txt = txt[i+len("\nComment:\n"):]
				}
				if aid >= 0 && aid < len(authors) && authors[aid] != "" && strings.HasPrefix(txt, authors[aid]+":") {
					txt = txt[len(authors[aid])+1:]
				}
				txt = strings.Join(strings.Fields(txt), " ")
				if txt != "" && ref != "" && len(out) < MaxCells {
					out[ref] = txt
				}
			}
		case xml.CharData:
			switch {
			case inAuthor && len(authors) > 0:
				authors[len(authors)-1] += string(t)
			case inT:
				cur.Write(t)
			}
		}
	}
	return out
}

func FromXLSX(b []byte) (map[string]string, error) {
	m, err := FromXLSXRich(b)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, c := range m {
		if c.V != "" {
			out[k] = c.V
		}
	}
	return out, nil
}

// FromXLSXRich liest das erste Blatt: Werte, Text und Formeln (als "=..."), Daten als Text "JJJJ-MM-TT", Wahrheitswerte als TRUE/FALSE,
// dazu Zellformate (Schrift, Hintergrund, Zahlenformat, Ausrichtung) und Notizen.
func FromXLSXRich(b []byte) (map[string]FCell, error) {
	zr, err := openZip(b)
	if err != nil {
		return nil, err
	}
	var sst []string
	if s, err := readPart(zr, "xl/sharedStrings.xml"); err == nil {
		sst = sharedStrings(s)
	}
	dates := dateStyles(zr)
	xfFmt := xlStyleFormats(zr)
	sheet := ""
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") && (sheet == "" || f.Name < sheet) {
			sheet = f.Name
		}
	}
	if sheet == "" {
		return nil, ErrFormat
	}
	raw, err := readPart(zr, sheet)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	out := map[string]FCell{}
	var ref, typ, val, formula string
	sty := -1
	var text strings.Builder
	inC, inV, inF, inT, inIS := false, false, false, false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrFormat
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "c":
				inC = true
				ref, typ, val, formula, sty = "", "", "", "", -1
				text.Reset()
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "s":
						sty, _ = strconv.Atoi(a.Value)
					case "r":
						ref = a.Value
					case "t":
						typ = a.Value
					}
				}
			case "v":
				inV = inC
			case "f":
				inF = inC
			case "is":
				inIS = inC
			case "t":
				inT = inIS
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "f":
				inF = false
			case "t":
				inT = false
			case "is":
				inIS = false
			case "c":
				if !inC {
					break
				}
				inC = false
				v := val
				switch typ {
				case "s":
					if i, err := strconv.Atoi(val); err == nil && i >= 0 && i < len(sst) {
						v = sst[i]
					} else {
						v = ""
					}
				case "inlineStr":
					v = text.String()
				case "", "n":
					if sty >= 0 && sty < len(dates) && dates[sty] && val != "" {
						v = xlDate(val)
					}
				case "b":
					if val == "1" {
						v = "TRUE"
					} else if val == "0" {
						v = "FALSE"
					}
				}
				if formula != "" {
					v = "=" + formula
				}
				f := ""
				if sty >= 0 && sty < len(xfFmt) {
					f = xfFmt[sty]
				}
				if _, _, ok := ColRow(ref); ok && (v != "" || f != "") {
					if v == "" && len(out) >= MaxCells { // reine Formatzellen fuellen die Grenze nie auf
						break
					}
					out[ref] = FCell{V: v, F: f}
					if len(out) > MaxCells {
						return nil, ErrFormat
					}
				}
			}
		case xml.CharData:
			switch {
			case inV:
				val += string(t)
			case inF:
				formula += string(t)
			case inT:
				text.Write(t)
			}
		}
	}
	for ref, txt := range xlNotes(zr, sheet) {
		if _, _, ok := ColRow(ref); !ok {
			continue
		}
		c, had := out[ref]
		if !had && len(out) >= MaxCells {
			continue
		}
		f := parseFmt(c.F)
		f.m = cmEncode(txt)
		c.F = f.str()
		out[ref] = c
	}
	return out, nil
}
