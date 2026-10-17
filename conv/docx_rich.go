package conv

// .docx mit Formaten: Fett, Kursiv, Unterstrichen, Durchgestrichen, Farbe, Groesse, Hintergrund, Aufzaehlung/Nummerierung, Einrueckung
// und Kommentare (als Word-Kommentare in word/comments.xml). Nur Go-Standardbibliothek.

import (
	"bytes"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

const nsW = `http://schemas.openxmlformats.org/wordprocessingml/2006/main`

func ToDOCX(p []string) ([]byte, error) { return ToDOCXRich(plainParas(p)) }

func docxRPr(f rFmt) string {
	var b strings.Builder
	if f.b {
		b.WriteString(`<w:b/>`)
	}
	if f.i {
		b.WriteString(`<w:i/>`)
	}
	if f.t {
		b.WriteString(`<w:strike/>`)
	}
	if f.c != "" {
		b.WriteString(`<w:color w:val="` + strings.ToUpper(f.c) + `"/>`)
	}
	if f.s > 0 {
		b.WriteString(`<w:sz w:val="` + strconv.Itoa(int(float64(f.s)*1.5+0.5)) + `"/>`)
	}
	if f.u {
		b.WriteString(`<w:u w:val="single"/>`)
	}
	if f.g != "" {
		b.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="` + strings.ToUpper(f.g) + `"/>`)
	}
	if b.Len() == 0 {
		return ""
	}
	return `<w:rPr>` + b.String() + `</w:rPr>`
}

// docxRun schreibt den Text eines Laufs; Tab und Zeilenumbruch werden zu w:tab / w:br.
func docxRun(b *strings.Builder, s rSeg) {
	if s.text == "" {
		return
	}
	rpr := docxRPr(s.f)
	for _, part := range splitKeep(s.text) {
		b.WriteString(`<w:r>` + rpr)
		switch part {
		case "\t":
			b.WriteString(`<w:tab/>`)
		case "\n":
			b.WriteString(`<w:br/>`)
		default:
			b.WriteString(`<w:t xml:space="preserve">` + xmlEsc(part) + `</w:t>`)
		}
		b.WriteString(`</w:r>`)
	}
}

// splitKeep teilt an Tab und Zeilenumbruch und behaelt diese als eigene Teile.
func splitKeep(s string) []string {
	var out []string
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\t' || s[i] == '\n' {
			if i > last {
				out = append(out, s[last:i])
			}
			out = append(out, s[i:i+1])
			last = i + 1
		}
	}
	if last < len(s) {
		out = append(out, s[last:])
	}
	return out
}

func ToDOCXRich(p []Para) ([]byte, error) {
	var body strings.Builder
	var comments []string
	bullets, groups := false, 0
	prevO := false
	for _, para := range p {
		f := parseFmt(para.F)
		var ppr strings.Builder
		switch {
		case f.l:
			bullets, prevO = true, false
			ppr.WriteString(`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>`)
		case f.o:
			if !prevO {
				groups++
			}
			prevO = true
			ppr.WriteString(`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="` + strconv.Itoa(1+groups) + `"/></w:numPr>`)
		default:
			prevO = false
		}
		if f.n > 0 || f.l || f.o {
			left := 720 * f.n
			if f.l || f.o {
				left += 720
				if f.n > 0 {
					ppr.WriteString(`<w:ind w:left="` + strconv.Itoa(left) + `" w:hanging="360"/>`)
				}
			} else {
				ppr.WriteString(`<w:ind w:left="` + strconv.Itoa(left) + `"/>`)
			}
		}
		body.WriteString(`<w:p>`)
		if ppr.Len() > 0 {
			body.WriteString(`<w:pPr>` + ppr.String() + `</w:pPr>`)
		}
		segs := splitRuns(para.V, para.R)
		for i := 0; i < len(segs); {
			m := segs[i].f.m
			if m == "" {
				docxRun(&body, segs[i])
				i++
				continue
			}
			j := i
			for j < len(segs) && segs[j].f.m == m {
				j++
			}
			id := len(comments)
			comments = append(comments, cmDecode(m))
			body.WriteString(`<w:commentRangeStart w:id="` + strconv.Itoa(id) + `"/>`)
			for _, s := range segs[i:j] {
				docxRun(&body, s)
			}
			body.WriteString(`<w:commentRangeEnd w:id="` + strconv.Itoa(id) + `"/><w:r><w:commentReference w:id="` + strconv.Itoa(id) + `"/></w:r>`)
			i = j
		}
		body.WriteString(`</w:p>`)
	}
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:document xmlns:w="` + nsW + `"><w:body>` + body.String() + `</w:body></w:document>`

	useNum := bullets || groups > 0
	ct := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>`
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
	parts := [][2]string{{"_rels/.rels", relsDocx}}
	if useNum {
		ct += `<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>`
		rels += `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>`
		parts = append(parts, [2]string{"word/numbering.xml", docxNumbering(groups)})
	}
	if len(comments) > 0 {
		ct += `<Override PartName="/word/comments.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"/>`
		rels += `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments" Target="comments.xml"/>`
		var cb strings.Builder
		cb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<w:comments xmlns:w="` + nsW + `">`)
		for id, txt := range comments {
			cb.WriteString(`<w:comment w:id="` + strconv.Itoa(id) + `" w:author="cs-team" w:initials="cs"><w:p><w:r><w:t xml:space="preserve">` + xmlEsc(txt) + `</w:t></w:r></w:p></w:comment>`)
		}
		cb.WriteString(`</w:comments>`)
		parts = append(parts, [2]string{"word/comments.xml", cb.String()})
	}
	ct += `</Types>`
	rels += `</Relationships>`
	all := [][2]string{{"[Content_Types].xml", ct}, parts[0], {"word/document.xml", doc}}
	if useNum || len(comments) > 0 {
		all = append(all, [2]string{"word/_rels/document.xml.rels", rels})
	}
	all = append(all, parts[1:]...)
	return zipParts(all)
}

// docxNumbering: Nummerierungsdefinitionen: numId 1 = Aufzaehlung, numId 2.. = je eine Nummerierung (beginnt bei 1).
func docxNumbering(groups int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" + `<w:numbering xmlns:w="` + nsW + `">`)
	b.WriteString(`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="&#8226;"/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl></w:abstractNum>`)
	b.WriteString(`<w:abstractNum w:abstractNumId="1"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1."/><w:lvlJc w:val="left"/><w:pPr><w:ind w:left="720" w:hanging="360"/></w:pPr></w:lvl></w:abstractNum>`)
	b.WriteString(`<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num>`)
	for g := 1; g <= groups; g++ {
		b.WriteString(`<w:num w:numId="` + strconv.Itoa(1+g) + `"><w:abstractNumId w:val="1"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="1"/></w:lvlOverride></w:num>`)
	}
	b.WriteString(`</w:numbering>`)
	return b.String()
}

// ---------- Import ----------

func xAttr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// xOnOff: Schalter-Element wie <w:b/> oder <w:b w:val="0"/>.
func xOnOff(t xml.StartElement) bool {
	switch xAttr(t, "val") {
	case "0", "false", "off", "none":
		return false
	}
	return true
}

var docxHighlight = map[string]string{"yellow": "ffff00", "green": "00ff00", "cyan": "00ffff", "magenta": "ff00ff", "blue": "0000ff",
	"red": "ff0000", "darkBlue": "000080", "darkCyan": "008080", "darkGreen": "008000", "darkMagenta": "800080", "darkRed": "800000",
	"darkYellow": "808000", "darkGray": "808080", "lightGray": "c0c0c0", "black": "000000", "white": "ffffff"}

// docxNumberingKinds: numId -> numFmt der Ebene 0 ("bullet", "decimal", ...).
func docxNumberingKinds(b []byte) map[string]string {
	absFmt, numAbs := map[string]string{}, map[string]string{}
	curAbs, curNum, lvl0 := "", "", false
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "abstractNum":
				curAbs = xAttr(t, "abstractNumId")
			case "lvl":
				lvl0 = xAttr(t, "ilvl") == "0"
			case "numFmt":
				if curAbs != "" && lvl0 && absFmt[curAbs] == "" {
					absFmt[curAbs] = xAttr(t, "val")
				}
			case "num":
				curNum = xAttr(t, "numId")
			case "abstractNumId":
				if curNum != "" {
					numAbs[curNum] = xAttr(t, "val")
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "abstractNum":
				curAbs = ""
			case "lvl":
				lvl0 = false
			case "num":
				curNum = ""
			}
		}
	}
	out := map[string]string{}
	for n, a := range numAbs {
		out[n] = absFmt[a]
	}
	return out
}

// docxCommentTexts: Kommentar-Id -> Text (Absaetze durch Leerzeichen verbunden).
func docxCommentTexts(b []byte) map[string]string {
	out := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(b))
	id, inT := "", false
	var cur strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "comment":
				id = xAttr(t, "id")
				cur.Reset()
			case "t":
				inT = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "p":
				cur.WriteByte(' ')
			case "comment":
				out[id] = strings.Join(strings.Fields(cur.String()), " ")
				id = ""
			}
		case xml.CharData:
			if inT && id != "" {
				cur.Write(t)
			}
		}
	}
	return out
}

func FromDOCX(b []byte) ([]string, error) {
	p, err := FromDOCXRich(b)
	if err != nil {
		return nil, err
	}
	return paraTexts(p), nil
}

// FromDOCXRich liest die Absaetze aus word/document.xml samt Zeichenformaten (fett, kursiv, unterstrichen, durchgestrichen, Farbe, Groesse,
// Hintergrund), Aufzaehlung/Nummerierung, Einrueckung und Kommentaren. Eine Tabellenzeile wird ein Absatz (Zellen durch Tab getrennt, ohne Formate),
// Alternativinhalt (mc:Fallback, doppelt vorhandener Text in Textfeldern) wird uebersprungen.
func FromDOCXRich(b []byte) ([]Para, error) {
	zr, err := openZip(b)
	if err != nil {
		return nil, err
	}
	docb, err := readPart(zr, "word/document.xml")
	if err != nil {
		return nil, err
	}
	kinds := map[string]string{}
	if nb, err := readPart(zr, "word/numbering.xml"); err == nil {
		kinds = docxNumberingKinds(nb)
	}
	cmText := map[string]string{}
	if cb, err := readPart(zr, "word/comments.xml"); err == nil {
		cmText = docxCommentTexts(cb)
	}
	dec := xml.NewDecoder(bytes.NewReader(docb))
	var out []Para
	var cur strings.Builder
	var segs []rSeg
	rf := rFmt{d: -1}
	inP, inT, inPPr, inRPr := false, false, false, false
	tbl := 0
	var row, cell []string
	numID, ilvl, left, activeCm := "", 0, 0, ""
	addText := func(s string) {
		cur.WriteString(s)
		if tbl == 0 {
			segs = append(segs, rSeg{s, rf})
		}
	}
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
			case "Fallback":
				dec.Skip()
			case "tbl":
				tbl++
			case "p":
				inP = true
				cur.Reset()
				segs = nil
				numID, ilvl, left = "", 0, 0
			case "pPr":
				inPPr = true
			case "numId":
				if inPPr {
					numID = xAttr(t, "val")
				}
			case "ilvl":
				if inPPr {
					ilvl, _ = strconv.Atoi(xAttr(t, "val"))
				}
			case "ind":
				if inPPr {
					v := xAttr(t, "left")
					if v == "" {
						v = xAttr(t, "start")
					}
					left, _ = strconv.Atoi(v)
				}
			case "r":
				rf = rFmt{d: -1}
				if activeCm != "" && cmText[activeCm] != "" {
					rf.m = cmEncode(cmText[activeCm])
				}
			case "rPr":
				inRPr = !inPPr
			case "b":
				if inRPr {
					rf.b = xOnOff(t)
				}
			case "i":
				if inRPr {
					rf.i = xOnOff(t)
				}
			case "u":
				if inRPr {
					rf.u = xOnOff(t)
				}
			case "strike", "dstrike":
				if inRPr {
					rf.t = xOnOff(t)
				}
			case "color":
				if inRPr {
					if v := xAttr(t, "val"); isHex6(v) {
						rf.c = strings.ToLower(v)
					}
				}
			case "sz":
				if inRPr {
					if hp, err := strconv.Atoi(xAttr(t, "val")); err == nil && hp > 0 {
						if px := (hp*2 + 1) / 3; px >= 1 && px <= 99 {
							rf.s = px
						}
					}
				}
			case "shd":
				if inRPr {
					if v := xAttr(t, "fill"); isHex6(v) {
						rf.g = strings.ToLower(v)
					}
				}
			case "highlight":
				if inRPr {
					if hx, ok := docxHighlight[xAttr(t, "val")]; ok {
						rf.g = hx
					}
				}
			case "commentRangeStart":
				activeCm = xAttr(t, "id")
			case "commentRangeEnd":
				if xAttr(t, "id") == activeCm {
					activeCm = ""
				}
			case "t":
				inT = true
			case "tab":
				addText("\t")
			case "br", "cr":
				addText(" ")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "pPr":
				inPPr = false
			case "rPr":
				inRPr = false
			case "p":
				if inP {
					inP = false
					if tbl > 0 {
						if s := strings.TrimSpace(cur.String()); s != "" {
							cell = append(cell, s)
						}
						break
					}
					para := Para{V: cur.String(), R: runsFrom(segs)}
					list := numID != "" && numID != "0"
					n := (left + 360) / 720
					if list {
						n = max(0, n-1) + ilvl
						if kinds[numID] == "bullet" || kinds[numID] == "" {
							para.F = "l"
						} else {
							para.F = "o"
						}
					}
					n = min(n, 8)
					if n > 0 {
						para.F = strings.TrimSpace(para.F + " n" + strconv.Itoa(n))
					}
					out = append(out, para)
					if len(out) > MaxParas {
						return nil, ErrFormat
					}
				}
			case "tc":
				if tbl == 1 {
					row, cell = append(row, strings.Join(cell, " ")), nil
				}
			case "tr":
				if tbl == 1 {
					out = append(out, Para{V: strings.Join(row, "\t")})
					row = nil
					if len(out) > MaxParas {
						return nil, ErrFormat
					}
				}
			case "tbl":
				tbl--
			}
		case xml.CharData:
			if inT {
				addText(string(t))
			}
		}
	}
	if len(out) == 0 {
		out = []Para{{}}
	}
	return out, nil
}

