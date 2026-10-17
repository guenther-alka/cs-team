package conv

// PDF-Ausgabe (nur Go-Standardbibliothek) fuer Text und Calc: Standardschriften Helvetica (WinAnsi, also Latin-1 mit Euro und den
// ueblichen Satzzeichen; andere Schriften wie Kyrillisch, Arabisch oder Chinesisch erscheinen als "?"), Zeilenumbruch nach echten
// Zeichenbreiten, Seitenumbruch, fett/kursiv/unterstrichen/durchgestrichen, Textfarbe, Groesse, Hintergrund, Aufzaehlung/Nummerierung,
// Einrueckung. Kommentare erscheinen als Marke [n] im Text und als Liste am Ende. Calc: Tabelle mit Spaltenbuchstaben und Zeilennummern.

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	pdfMargin  = 56.7 // 2 cm
	pdfPortW   = 595.28
	pdfPortH   = 841.89
	pdfBaseSz  = 11.0
	pdfMaxRows = 20000
)

// ---------- Zeichen ----------

func winAnsi(r rune) byte {
	switch {
	case r >= 32 && r < 127, r >= 160 && r <= 255:
		return byte(r)
	}
	switch r {
	case '€':
		return 0x80
	case '‚':
		return 0x82
	case 'ƒ':
		return 0x83
	case '„':
		return 0x84
	case '…':
		return 0x85
	case '†':
		return 0x86
	case '‡':
		return 0x87
	case 'ˆ':
		return 0x88
	case '‰':
		return 0x89
	case 'Š':
		return 0x8A
	case '‹':
		return 0x8B
	case 'Œ':
		return 0x8C
	case 'Ž':
		return 0x8E
	case '‘':
		return 0x91
	case '’':
		return 0x92
	case '“':
		return 0x93
	case '”':
		return 0x94
	case '•':
		return 0x95
	case '–':
		return 0x96
	case '—':
		return 0x97
	case '˜':
		return 0x98
	case '™':
		return 0x99
	case 'š':
		return 0x9A
	case '›':
		return 0x9B
	case 'œ':
		return 0x9C
	case 'ž':
		return 0x9E
	case 'Ÿ':
		return 0x9F
	}
	return '?'
}

// Zeichenbreiten Helvetica / Helvetica-Bold fuer ASCII 32..126 (1/1000 Geviert).
var wReg = [95]int{278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556,
	278, 278, 584, 584, 584, 556, 1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944,
	667, 667, 611, 278, 278, 278, 469, 556, 333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556, 556, 556, 333, 500, 278, 556,
	500, 722, 500, 500, 500, 334, 260, 334, 584}

var wBold = [95]int{278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556,
	333, 333, 584, 584, 584, 611, 975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944,
	667, 667, 611, 333, 278, 333, 584, 556, 333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611, 611, 611, 389, 556, 333, 611,
	556, 778, 556, 556, 500, 389, 280, 389, 584}

// Buchstaben mit Akzent: Breite des Grundbuchstabens.
var (
	hiUpper = [32]byte{'A', 'A', 'A', 'A', 'A', 'A', 'W', 'C', 'E', 'E', 'E', 'E', 'I', 'I', 'I', 'I', 'D', 'N', 'O', 'O', 'O', 'O', 'O', 'x', 'O', 'U', 'U', 'U', 'U', 'Y', 'P', 'B'} // 192..223
	hiLower = [32]byte{'a', 'a', 'a', 'a', 'a', 'a', 'm', 'c', 'e', 'e', 'e', 'e', 'i', 'i', 'i', 'i', 'o', 'n', 'o', 'o', 'o', 'o', 'o', 'o', 'o', 'u', 'u', 'u', 'u', 'y', 'p', 'y'} // 224..255
)

var hiSym = map[byte]int{160: 278, 161: 333, 162: 556, 163: 556, 164: 556, 165: 556, 166: 260, 167: 556, 168: 333, 169: 737, 170: 370, 171: 556, 172: 584, 173: 333,
	174: 737, 175: 333, 176: 400, 177: 584, 178: 333, 179: 333, 180: 333, 181: 556, 182: 537, 183: 278, 184: 333, 185: 333, 186: 365, 187: 556, 188: 834, 189: 834,
	190: 834, 191: 611, 128: 556, 130: 222, 131: 556, 132: 333, 133: 1000, 134: 556, 135: 556, 136: 333, 137: 1000, 138: 667, 139: 333, 140: 1000, 142: 611,
	145: 222, 146: 222, 147: 333, 148: 333, 149: 350, 150: 556, 151: 1000, 152: 333, 153: 1000, 154: 500, 155: 333, 156: 944, 158: 500, 159: 667, 208: 722, 215: 584, 247: 584}

func adv(b byte, bold bool) float64 {
	tab := &wReg
	if bold {
		tab = &wBold
	}
	switch {
	case b >= 32 && b < 127:
		return float64(tab[b-32])
	case b >= 192 && b <= 223 && b != 208 && b != 215:
		return adv(hiUpper[b-192], bold)
	case b >= 224 && b <= 255 && b != 247:
		if b == 230 {
			return 889
		}
		return adv(hiLower[b-224], bold)
	}
	if w, ok := hiSym[b]; ok {
		return float64(w)
	}
	return 556
}

func outStr(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		if c == '(' || c == ')' || c == '\\' {
			s.WriteByte('\\')
		}
		s.WriteByte(c)
	}
	return s.String()
}

func ansi(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, winAnsi(r))
	}
	return out
}

func textW(b []byte, size float64, bold bool) float64 {
	w := 0.0
	for _, c := range b {
		w += adv(c, bold)
	}
	return w * size / 1000
}

func rgb(hex string) string {
	if !isHex6(hex) {
		return "0 0 0"
	}
	v, _ := strconv.ParseUint(hex, 16, 32)
	return fmt.Sprintf("%.3f %.3f %.3f", float64(v>>16&255)/255, float64(v>>8&255)/255, float64(v&255)/255)
}

// ---------- Dokument ----------

type outPage struct {
	w, h float64
	b    bytes.Buffer
}

type outDoc struct {
	pages []*outPage
	y     float64 // aktuelle Zeile (Oberkante der naechsten Zeile)
}

func (d *outDoc) page() *outPage { return d.pages[len(d.pages)-1] }

func (d *outDoc) newPage(w, h float64) {
	d.pages = append(d.pages, &outPage{w: w, h: h})
	d.y = h - pdfMargin
}

func fontName(bold, italic bool) string {
	n := 1
	if bold {
		n++
	}
	if italic {
		n += 2
	}
	return "F" + strconv.Itoa(n)
}

// text setzt s mit der Grundlinie y; liefert die Breite.
func (p *outPage) text(x, y float64, s []byte, size float64, bold, italic bool, color string) float64 {
	w := textW(s, size, bold)
	fmt.Fprintf(&p.b, "BT /%s %.2f Tf %s rg %.2f %.2f Td (%s) Tj ET\n", fontName(bold, italic), size, rgb(color), x, y, outStr(s))
	return w
}

func (p *outPage) line(x1, y1, x2, y2 float64, color string, width float64) {
	fmt.Fprintf(&p.b, "q %.2f w %s RG %.2f %.2f m %.2f %.2f l S Q\n", width, rgb(color), x1, y1, x2, y2)
}

func (p *outPage) rect(x, y, w, h float64, fill, stroke string) {
	if fill != "" {
		fmt.Fprintf(&p.b, "q %s rg %.2f %.2f %.2f %.2f re f Q\n", rgb(fill), x, y, w, h)
	}
	if stroke != "" {
		fmt.Fprintf(&p.b, "q 0.25 w %s RG %.2f %.2f %.2f %.2f re S Q\n", rgb(stroke), x, y, w, h)
	}
}

func (d *outDoc) bytes(title string) []byte {
	if len(d.pages) == 0 {
		d.newPage(pdfPortW, pdfPortH)
	}
	var out bytes.Buffer
	var offs []int
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	var kids strings.Builder
	for i := range d.pages {
		kids.WriteString(strconv.Itoa(8+2*i) + " 0 R ")
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj("<< /Type /Pages /Kids [" + kids.String() + "] /Count " + strconv.Itoa(len(d.pages)) + " >>")
	for _, f := range []string{"Helvetica", "Helvetica-Bold", "Helvetica-Oblique", "Helvetica-BoldOblique"} {
		obj("<< /Type /Font /Subtype /Type1 /BaseFont /" + f + " /Encoding /WinAnsiEncoding >>")
	}
	obj("<< /Title (" + outStr(ansi(title)) + ") /Producer (cs-team) >>")
	for i, p := range d.pages {
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /Font << /F1 3 0 R /F2 4 0 R /F3 5 0 R /F4 6 0 R >> >> /Contents %d 0 R >>", p.w, p.h, 9+2*i))
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		zw.Write(p.b.Bytes())
		zw.Close()
		obj(fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n%s\nendstream", z.Len(), z.String()))
	}
	start := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 7 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, start)
	return out.Bytes()
}

// ---------- Text ----------

type pItem struct {
	text      []byte
	bold, ita bool
	size, w   float64
	c, g      string
	u, t      bool
	space     bool
	brk       bool
}

func (it *pItem) measure() { it.w = textW(it.text, it.size, it.bold) }

// outItems zerlegt einen Teil in Woerter, Leerraum und Zeilenumbrueche.
func outItems(s rSeg, size float64) []pItem {
	if s.f.s > 0 {
		size = float64(s.f.s) * 0.75
	}
	base := pItem{bold: s.f.b, ita: s.f.i, size: size, c: s.f.c, g: s.f.g, u: s.f.u, t: s.f.t}
	var out []pItem
	var word []rune
	flush := func() {
		if len(word) > 0 {
			it := base
			it.text = ansi(string(word))
			it.measure()
			out = append(out, it)
			word = word[:0]
		}
	}
	for _, r := range s.text {
		switch r {
		case ' ', '\t':
			flush()
			it := base
			it.space = true
			it.text = []byte{' '}
			if r == '\t' {
				it.text = []byte("    ")
			}
			it.measure()
			out = append(out, it)
		case '\n':
			flush()
			out = append(out, pItem{brk: true, size: size})
		default:
			word = append(word, r)
		}
	}
	flush()
	return out
}

type pLine struct {
	items []pItem
	w, sz float64
}

func outBreak(items []pItem, avail float64) []pLine {
	var lines []pLine
	cur := pLine{}
	flush := func() {
		for len(cur.items) > 0 && cur.items[len(cur.items)-1].space { // Leerraum am Zeilenende zaehlt nicht
			cur.w -= cur.items[len(cur.items)-1].w
			cur.items = cur.items[:len(cur.items)-1]
		}
		lines = append(lines, cur)
		cur = pLine{}
	}
	for _, it := range items {
		if it.brk {
			if cur.sz == 0 {
				cur.sz = it.size
			}
			flush()
			continue
		}
		if it.space && len(cur.items) == 0 {
			continue
		}
		for !it.space && cur.w+it.w > avail && it.w > avail { // einzelnes Wort breiter als die Zeile: nach Zeichen trennen
			n, w := 0, 0.0
			for n < len(it.text) && w+adv(it.text[n], it.bold)*it.size/1000 <= avail {
				w += adv(it.text[n], it.bold) * it.size / 1000
				n++
			}
			if n == 0 {
				n = 1
			}
			head := it
			head.text = it.text[:n]
			head.measure()
			if len(cur.items) > 0 {
				flush()
			}
			cur.items, cur.w, cur.sz = append(cur.items, head), head.w, max(cur.sz, head.size)
			flush()
			it.text = it.text[n:]
			it.measure()
		}
		if !it.space && cur.w+it.w > avail && len(cur.items) > 0 {
			flush()
		}
		cur.items = append(cur.items, it)
		cur.w += it.w
		cur.sz = max(cur.sz, it.size)
	}
	if len(cur.items) > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}

// ToPDFRich erzeugt ein PDF (A4 hoch) aus den Absaetzen mit Formaten.
func ToPDFRich(title string, p []Para) []byte {
	d := &outDoc{}
	d.newPage(pdfPortW, pdfPortH)
	var comments []string
	num := 0
	for _, para := range p {
		f := parseFmt(para.F)
		if f.o {
			num++
		} else {
			num = 0
		}
		var items []pItem
		segs := splitRuns(para.V, para.R)
		for i := 0; i < len(segs); {
			m := segs[i].f.m
			j := i + 1
			if m != "" {
				for j < len(segs) && segs[j].f.m == m {
					j++
				}
			}
			for _, s := range segs[i:j] {
				items = append(items, outItems(s, pdfBaseSz)...)
			}
			if m != "" && len(comments) < 500 {
				comments = append(comments, cmDecode(m))
				mk := pItem{text: ansi("[" + strconv.Itoa(len(comments)) + "]"), size: 7, c: "1a56c4"}
				mk.measure()
				items = append(items, mk)
			}
			i = j
		}
		d.paragraph(items, 18*float64(f.n), f.l, num, f.o)
	}
	if len(comments) > 0 {
		d.y -= 10
		d.paragraph([]pItem{{text: ansi("Kommentare / Comments"), bold: true, size: 12}}, 0, false, 0, false)
		for i, c := range comments {
			it := outItems(rSeg{text: "[" + strconv.Itoa(i+1) + "] " + c}, 10)
			d.paragraph(it, 0, false, 0, false)
		}
	}
	return d.bytes(title)
}

// paragraph setzt einen Absatz; bullet: Aufzaehlungspunkt, numbered: Nummer num mit Punkt.
func (d *outDoc) paragraph(items []pItem, indent float64, bullet bool, num int, numbered bool) {
	textX := pdfMargin + indent
	if bullet || numbered {
		textX += 16
	}
	avail := pdfPortW - pdfMargin - textX
	for li, ln := range outBreak(items, avail) {
		sz := ln.sz
		if sz == 0 {
			sz = pdfBaseSz
		}
		h := sz * 1.3
		if d.y-h < pdfMargin {
			d.newPage(pdfPortW, pdfPortH)
		}
		pg := d.page()
		base := d.y - sz
		if li == 0 && (bullet || numbered) {
			mark := []byte{0x95}
			if numbered {
				mark = ansi(strconv.Itoa(num) + ".")
			}
			pg.text(pdfMargin+indent, base, mark, sz, false, false, "")
		}
		x := textX
		for _, it := range ln.items {
			if it.g != "" {
				pg.rect(x, base-sz*0.22, it.w, sz*1.2, it.g, "")
			}
			if !it.space {
				pg.text(x, base, it.text, it.size, it.bold, it.ita, it.c)
			}
			if it.u {
				pg.line(x, base-sz*0.12, x+it.w, base-sz*0.12, it.c, 0.5)
			}
			if it.t {
				pg.line(x, base+sz*0.3, x+it.w, base+sz*0.3, it.c, 0.5)
			}
			x += it.w
		}
		d.y -= h
	}
	d.y -= 4
}

// ---------- Calc ----------

var reNumLike = regexp.MustCompile(`^[-+]?[0-9][0-9.,' ]*[%€$]?$`)

// ToPDFCells erzeugt ein PDF mit der Tabelle. vals: die im Browser angezeigten Werte (Formeln berechnet, Zahlenformate angewendet);
// ohne vals werden die gespeicherten Werte bzw. Formeln ausgegeben.
func ToPDFCells(title string, cells map[string]FCell, vals map[string]string) []byte {
	type cellT struct {
		s []byte
		f rFmt
	}
	grid := map[[2]int]cellT{}
	maxC, maxR := -1, 0
	put := func(k, v string, f rFmt) {
		c, r, ok := ColRow(k)
		if !ok || r > pdfMaxRows || c > 701 {
			return
		}
		x := grid[[2]int{c, r}]
		if v != "" {
			x.s = ansi(strings.Join(strings.Fields(strings.ReplaceAll(v, "\n", " ")), " "))
		}
		x.f = f
		grid[[2]int{c, r}] = x
		maxC, maxR = max(maxC, c), max(maxR, r)
	}
	for k, c := range cells {
		v := c.V
		if vals != nil {
			v = vals[k]
		}
		if v != "" || c.F != "" {
			put(k, v, parseFmt(c.F))
		}
	}
	for k, v := range vals {
		if _, ok := cells[k]; !ok && v != "" {
			put(k, v, rFmt{d: -1})
		}
	}
	d := &outDoc{}
	if maxC < 0 {
		d.newPage(pdfPortW, pdfPortH)
		d.page().text(pdfMargin, pdfPortH-pdfMargin-12, ansi(title), 12, true, false, "")
		return d.bytes(title)
	}
	size := func(f rFmt) float64 {
		if f.s > 0 {
			return min(float64(f.s)*0.75, 24)
		}
		return 10
	}
	// Spaltenbreiten und Zeilenhoehen
	colW := make([]float64, maxC+1)
	for c := range colW {
		colW[c] = 28
	}
	rowH := make([]float64, maxR+1)
	for r := range rowH {
		rowH[r] = 14
	}
	for pos, x := range grid {
		sz := size(x.f)
		colW[pos[0]] = max(colW[pos[0]], min(textW(x.s, sz, x.f.b)+8, 220))
		rowH[pos[1]] = max(rowH[pos[1]], sz*1.4)
	}
	const rnW, gap = 30.0, 40.0
	total := rnW
	for _, w := range colW {
		total += w
	}
	pw, ph := pdfPortW, pdfPortH
	if total > pw-2*gap {
		pw, ph = pdfPortH, pdfPortW
	}
	usable := pw - 2*gap
	// Spaltengruppen, wenn die Tabelle auch quer zu breit ist
	var bands [][2]int
	for from := 0; from <= maxC; {
		w, to := rnW, from
		for to <= maxC && (to == from || w+colW[to] <= usable) {
			w += colW[to]
			to++
		}
		bands = append(bands, [2]int{from, to})
		from = to
	}
	hdrH := 14.0
	for bi, bd := range bands {
		r := 1
		for r <= maxR || (r == 1 && maxR == 0) {
			d.newPage(pw, ph)
			pg := d.page()
			y := ph - gap
			if bi == 0 && len(d.pages) == 1 {
				pg.text(gap, y-12, ansi(title), 12, true, false, "")
				y -= 22
			}
			// Kopfzeile: Spaltenbuchstaben
			x := gap + rnW
			for c := bd[0]; c < bd[1]; c++ {
				pg.rect(x, y-hdrH, colW[c], hdrH, "eeeeee", "bbbbbb")
				l := ansi(ColName(c))
				pg.text(x+(colW[c]-textW(l, 8, true))/2, y-hdrH+4, l, 8, true, false, "555555")
				x += colW[c]
			}
			y -= hdrH
			for ; r <= maxR && y-rowH[r] >= gap; r++ {
				h := rowH[r]
				pg.rect(gap, y-h, rnW, h, "eeeeee", "bbbbbb")
				rn := ansi(strconv.Itoa(r))
				pg.text(gap+rnW-4-textW(rn, 8, false), y-h+(h-6)/2, rn, 8, false, false, "555555")
				x := gap + rnW
				for c := bd[0]; c < bd[1]; c++ {
					x0 := x
					x += colW[c]
					cl, has := grid[[2]int{c, r}]
					if !has {
						pg.rect(x0, y-h, colW[c], h, "", "dddddd")
						continue
					}
					pg.rect(x0, y-h, colW[c], h, cl.f.g, "cccccc")
					sz := size(cl.f)
					s := cl.s
					if textW(s, sz, cl.f.b) > colW[c]-8 { // zu lang: kuerzen und Auslassungspunkte anhaengen
						for len(s) > 0 && textW(append(append([]byte{}, s...), 0x85), sz, cl.f.b) > colW[c]-8 {
							s = s[:len(s)-1]
						}
						if len(s) > 0 {
							s = append(append([]byte{}, s...), 0x85)
						}
					}
					if len(s) == 0 {
						continue
					}
					w := textW(s, sz, cl.f.b)
					tx := x0 + 4
					switch {
					case cl.f.h == 'r' || (cl.f.h == 0 && reNumLike.Match(s)):
						tx = x0 + colW[c] - 4 - w
					case cl.f.h == 'c':
						tx = x0 + (colW[c]-w)/2
					}
					base := y - h + (h-sz*0.7)/2
					pg.text(tx, base, s, sz, cl.f.b, cl.f.i, cl.f.c)
					if cl.f.u {
						pg.line(tx, base-1, tx+w, base-1, cl.f.c, 0.5)
					}
					if cl.f.t {
						pg.line(tx, base+sz*0.3, tx+w, base+sz*0.3, cl.f.c, 0.5)
					}
				}
				y -= h
			}
			if maxR == 0 {
				break
			}
		}
	}
	return d.bytes(title)
}
