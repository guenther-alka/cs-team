package conv

// Formate (Zeichen-, Absatz- und Zellformate) fuer den Austausch mit Word/Excel und die eigenen Formate .cstext/.cscalc.
// Die Token entsprechen den Formaten im Dokument (doc.Item.F / .R): b i u t (durchgestrichen) mTEXT (Kommentar/Notiz, url-kodiert)
// sNN (Groesse px) c#rrggbb g#rrggbb, nur Absatz: l o nN, nur Zelle: dN pc cu hl/hc/hr w.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Para: ein Absatz mit Formaten. F = Absatzformat ("l", "o", "n2"), R = Zeichenformate als Laeufe "anzahl:format;..." (UTF-16-Einheiten).
type Para struct{ V, F, R string }

// FCell: Zellwert bzw. Formel (V) mit dem Zellformat F.
type FCell struct{ V, F string }

func plainParas(p []string) []Para {
	out := make([]Para, len(p))
	for i, s := range p {
		out[i] = Para{V: s}
	}
	return out
}

func paraTexts(p []Para) []string {
	out := make([]string, len(p))
	for i := range p {
		out[i] = p[i].V
	}
	return out
}

// rFmt: ein geparster Formatstring.
type rFmt struct {
	b, i, u, t, l, o, w, pc, cu bool
	m                           string // Kommentar, url-kodiert
	s, n                        int    // Groesse in px, Einrueckung
	d                           int    // Nachkommastellen, -1 = nicht gesetzt
	c, g                        string // Farbe/Hintergrund, 6 Hex-Zeichen klein ohne '#'
	h                           byte   // l c r
}

func isHex6(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func parseFmt(f string) rFmt {
	x := rFmt{d: -1}
	for _, tok := range strings.Fields(f) {
		switch {
		case tok == "b":
			x.b = true
		case tok == "i":
			x.i = true
		case tok == "u":
			x.u = true
		case tok == "t":
			x.t = true
		case tok == "l":
			x.l = true
		case tok == "o":
			x.o = true
		case tok == "w":
			x.w = true
		case tok == "pc":
			x.pc = true
		case tok == "cu":
			x.cu = true
		case tok[0] == 'm' && len(tok) > 1:
			x.m = tok[1:]
		case tok[0] == 's' && len(tok) > 1:
			x.s, _ = strconv.Atoi(tok[1:])
		case tok[0] == 'n' && len(tok) == 2 && tok[1] >= '0' && tok[1] <= '8':
			x.n = int(tok[1] - '0')
		case tok[0] == 'd' && len(tok) == 2 && tok[1] >= '0' && tok[1] <= '9':
			x.d = int(tok[1] - '0')
		case tok[0] == 'h' && len(tok) == 2 && strings.ContainsRune("lcr", rune(tok[1])):
			x.h = tok[1]
		case len(tok) == 8 && tok[:2] == "c#" && isHex6(tok[2:]):
			x.c = strings.ToLower(tok[2:])
		case len(tok) == 8 && tok[:2] == "g#" && isHex6(tok[2:]):
			x.g = strings.ToLower(tok[2:])
		}
	}
	return x
}

// str: Formatstring in der Reihenfolge des Editors (b i u t m l o s n c g d pc cu h w).
func (x rFmt) str() string {
	var p []string
	add := func(c bool, s string) {
		if c {
			p = append(p, s)
		}
	}
	add(x.b, "b")
	add(x.i, "i")
	add(x.u, "u")
	add(x.t, "t")
	add(x.m != "", "m"+x.m)
	add(x.l, "l")
	add(x.o, "o")
	add(x.s > 0 && x.s < 100, "s"+strconv.Itoa(x.s))
	add(x.n > 0, "n"+strconv.Itoa(x.n))
	add(x.c != "", "c#"+x.c)
	add(x.g != "", "g#"+x.g)
	add(x.d >= 0, "d"+strconv.Itoa(x.d))
	add(x.pc, "pc")
	add(x.cu, "cu")
	add(x.h != 0, "h"+string(x.h))
	add(x.w, "w")
	return strings.Join(p, " ")
}

// charStr: nur die Zeichenformate (fuer Laeufe in Absaetzen).
func (x rFmt) charStr() string {
	return rFmt{b: x.b, i: x.i, u: x.u, t: x.t, m: x.m, s: x.s, c: x.c, g: x.g, d: -1}.str()
}

// paraStr: nur das Absatzformat.
func (x rFmt) paraStr() string { return rFmt{l: x.l, o: x.o, n: x.n, d: -1}.str() }

// ---------- Laeufe ----------

type rSeg struct {
	text string
	f    rFmt
}

// splitRuns teilt den Text eines Absatzes nach den Laeufen R (Anzahl in UTF-16-Einheiten).
func splitRuns(v, r string) []rSeg {
	u := utf16.Encode([]rune(v))
	var out []rSeg
	pos := 0
	if r != "" {
		for _, s := range strings.Split(r, ";") {
			i := strings.IndexByte(s, ':')
			if i <= 0 {
				continue
			}
			n, err := strconv.Atoi(s[:i])
			if err != nil || n <= 0 {
				continue
			}
			if pos+n > len(u) {
				n = len(u) - pos
			}
			if n <= 0 {
				break
			}
			out = append(out, rSeg{string(utf16.Decode(u[pos : pos+n])), parseFmt(s[i+1:])})
			pos += n
		}
	}
	if pos < len(u) {
		out = append(out, rSeg{string(utf16.Decode(u[pos:])), rFmt{d: -1}})
	}
	return out
}

// runsFrom setzt die Laeufe R aus den Teilen zusammen (benachbarte gleiche Formate werden vereint, ein einziger leerer Lauf = "").
func runsFrom(segs []rSeg) string {
	var parts []string
	var lastF string
	lastN := 0
	flush := func() {
		if lastN > 0 {
			parts = append(parts, strconv.Itoa(lastN)+":"+lastF)
		}
	}
	for _, s := range segs {
		if s.text == "" {
			continue
		}
		n := len(utf16.Encode([]rune(s.text)))
		f := s.f.charStr()
		if lastN > 0 && f == lastF {
			lastN += n
			continue
		}
		flush()
		lastF, lastN = f, n
	}
	flush()
	if len(parts) == 1 && strings.HasSuffix(parts[0], ":") {
		return ""
	}
	return strings.Join(parts, ";")
}

// ---------- Kommentartext ----------

const (
	cmMaxRunes = 200
	cmMaxEnc   = 600
)

// cmEncode: url-Kodierung wie encodeURIComponent im Editor (nur A-Za-z0-9-_.~ bleiben), gekuerzt auf 200 Zeichen / 600 kodierte Zeichen.
func cmEncode(s string) string {
	rs := []rune(strings.Join(strings.Fields(s), " "))
	if len(rs) > cmMaxRunes {
		rs = rs[:cmMaxRunes]
	}
	for {
		var b strings.Builder
		for _, c := range []byte(string(rs)) {
			if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
		if b.Len() <= cmMaxEnc || len(rs) == 0 {
			return b.String()
		}
		rs = rs[:len(rs)*9/10]
	}
}

func cmDecode(enc string) string {
	if s, err := url.PathUnescape(enc); err == nil {
		return s
	}
	return enc
}

// ---------- .cstext (JSON) ----------

type paraFmt struct {
	F string `json:"f,omitempty"`
	R string `json:"r,omitempty"`
}

type cstext struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	Paragraphs []string  `json:"paragraphs"`
	Formats    []paraFmt `json:"formats,omitempty"` // Version 2: je Absatz Absatzformat und Zeichenformate (gleiche Laenge wie paragraphs)
}

func ToCSText(p []string) ([]byte, error) { return ToCSTextRich(plainParas(p)) }

func ToCSTextRich(p []Para) ([]byte, error) {
	c := cstext{Format: "cs-team/text", Version: 1, Paragraphs: paraTexts(p)}
	for _, x := range p {
		if x.F != "" || x.R != "" {
			c.Version = 2
			break
		}
	}
	if c.Version == 2 {
		c.Formats = make([]paraFmt, len(p))
		for i, x := range p {
			c.Formats[i] = paraFmt{x.F, x.R}
		}
	}
	return json.MarshalIndent(c, "", " ")
}

func FromCSText(b []byte) ([]string, error) {
	p, err := FromCSTextRich(b)
	if err != nil {
		return nil, err
	}
	return paraTexts(p), nil
}

func FromCSTextRich(b []byte) ([]Para, error) {
	var c cstext
	if json.Unmarshal(b, &c) != nil || c.Format != "cs-team/text" || len(c.Paragraphs) > MaxParas {
		return nil, ErrFormat
	}
	if len(c.Paragraphs) == 0 {
		c.Paragraphs = []string{""}
	}
	out := plainParas(c.Paragraphs)
	if len(c.Formats) == len(out) {
		for i := range out {
			out[i].F, out[i].R = c.Formats[i].F, c.Formats[i].R
		}
	}
	return out, nil
}

// ---------- .cscalc (JSON) ----------

type cscalc struct {
	Format  string            `json:"format"`
	Version int               `json:"version"`
	Cells   map[string]string `json:"cells"`
	Formats map[string]string `json:"formats,omitempty"` // Version 2: Zellformate je Zelle
}

func ToCSCalc(cells map[string]string) ([]byte, error) {
	m := map[string]FCell{}
	for k, v := range cells {
		m[k] = FCell{V: v}
	}
	return ToCSCalcRich(m)
}

func ToCSCalcRich(cells map[string]FCell) ([]byte, error) {
	c := cscalc{Format: "cs-team/calc", Version: 1, Cells: map[string]string{}}
	for k, x := range cells {
		if x.V != "" {
			c.Cells[k] = x.V
		}
		if x.F != "" {
			if c.Formats == nil {
				c.Formats = map[string]string{}
				c.Version = 2
			}
			c.Formats[k] = x.F
		}
	}
	return json.MarshalIndent(c, "", " ")
}

func FromCSCalc(b []byte) (map[string]string, error) {
	m, err := FromCSCalcRich(b)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, x := range m {
		if x.V != "" {
			out[k] = x.V
		}
	}
	return out, nil
}

func FromCSCalcRich(b []byte) (map[string]FCell, error) {
	var c cscalc
	if json.Unmarshal(b, &c) != nil || c.Format != "cs-team/calc" || len(c.Cells) > MaxCells || len(c.Formats) > MaxCells {
		return nil, ErrFormat
	}
	out := map[string]FCell{}
	for k, v := range c.Cells {
		if _, _, ok := ColRow(k); ok && v != "" {
			out[k] = FCell{V: v}
		}
	}
	for k, f := range c.Formats {
		if _, _, ok := ColRow(k); ok && f != "" {
			x := out[k]
			x.F = f
			out[k] = x
		}
	}
	return out, nil
}
