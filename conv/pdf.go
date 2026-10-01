package conv

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Minimaler PDF-Textextraktor nur mit der Standardbibliothek (für die KI-Auswertung).
// Liest normale Text-PDFs: Flate/ASCIIHex/ASCII85, Objektströme, ToUnicode-Tabellen, einfache Schriften.
// Nicht unterstützt: verschlüsselte PDFs, gescannte Seiten (Bilder ohne Text), LZW.
// Der Inhalt ist nicht vertrauenswürdig: es wird nichts ausgeführt, alle Größen sind begrenzt.

var (
	ErrPDFEncrypted = errors.New("pdf is encrypted")
	ErrPDFNoText    = errors.New("pdf contains no text (scanned?)")
	ErrPDFBad       = errors.New("pdf cannot be read")
)

const (
	pdfMaxStream = 32 << 20  // je dekodiertem Stream
	pdfMaxTotal  = 128 << 20 // alle dekodierten Streams zusammen
	pdfMaxPages  = 500
	pdfMaxText   = 1 << 20 // Zeichen insgesamt
	pdfMaxDepth  = 40
)

type pdfName string
type pdfRef int
type pdfOp string
type pdfDict map[string]any
type pdfStream struct {
	d    pdfDict
	data []byte // Rohdaten
}

type pdfParser struct {
	b    []byte
	pos  int
	refs bool // "N G R" als Verweis lesen (Dateikörper) - im Inhaltsstrom aus
}

func isWS(c byte) bool { return c == 0 || c == 9 || c == 10 || c == 12 || c == 13 || c == 32 }
func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func (p *pdfParser) skip() {
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		if isWS(c) {
			p.pos++
		} else if c == '%' {
			for p.pos < len(p.b) && p.b[p.pos] != 10 && p.b[p.pos] != 13 {
				p.pos++
			}
		} else {
			return
		}
	}
}

func (p *pdfParser) word() string {
	s := p.pos
	for p.pos < len(p.b) && !isWS(p.b[p.pos]) && !isDelim(p.b[p.pos]) {
		p.pos++
	}
	return string(p.b[s:p.pos])
}

// value liest den nächsten Wert; ok=false am Ende oder bei einer schließenden Klammer.
func (p *pdfParser) value(depth int) (any, bool) {
	if depth > pdfMaxDepth {
		return nil, false
	}
	p.skip()
	if p.pos >= len(p.b) {
		return nil, false
	}
	c := p.b[p.pos]
	switch {
	case c == '/':
		p.pos++
		w := p.word()
		return pdfName(unescName(w)), true
	case c == '(':
		return p.litString(), true
	case c == '<':
		if p.pos+1 < len(p.b) && p.b[p.pos+1] == '<' {
			p.pos += 2
			d := pdfDict{}
			for {
				p.skip()
				if p.pos >= len(p.b) {
					return d, true
				}
				if p.b[p.pos] == '>' {
					p.pos++
					if p.pos < len(p.b) && p.b[p.pos] == '>' {
						p.pos++
					}
					return d, true
				}
				k, ok := p.value(depth + 1)
				if !ok {
					return d, true
				}
				kn, isN := k.(pdfName)
				v, ok := p.value(depth + 1)
				if !ok {
					return d, true
				}
				if isN {
					d[string(kn)] = v
				}
			}
		}
		return p.hexString(), true
	case c == '[':
		p.pos++
		var a []any
		for {
			p.skip()
			if p.pos >= len(p.b) {
				return a, true
			}
			if p.b[p.pos] == ']' {
				p.pos++
				return a, true
			}
			v, ok := p.value(depth + 1)
			if !ok {
				return a, true
			}
			a = append(a, v)
			if len(a) > 1<<20 {
				return a, true
			}
		}
	case c == ']' || c == '>' || c == ')' || c == '}':
		p.pos++
		return nil, false
	case c == '{':
		p.pos++
		return pdfOp("{"), true
	}
	w := p.word()
	if w == "" {
		p.pos++
		return pdfOp(""), true
	}
	if f, err := strconv.ParseFloat(w, 64); err == nil && (w[0] == '-' || w[0] == '+' || w[0] == '.' || (w[0] >= '0' && w[0] <= '9')) {
		if p.refs && !strings.ContainsAny(w, ".+-") {
			// "N G R"
			save := p.pos
			p.skip()
			g := p.word()
			if _, err := strconv.Atoi(g); err == nil {
				p.skip()
				if p.pos < len(p.b) && p.b[p.pos] == 'R' && (p.pos+1 >= len(p.b) || isWS(p.b[p.pos+1]) || isDelim(p.b[p.pos+1])) {
					p.pos++
					return pdfRef(int(f)), true
				}
			}
			p.pos = save
		}
		return f, true
	}
	switch w {
	case "true":
		return true, true
	case "false":
		return false, true
	case "null":
		return nil, true
	}
	return pdfOp(w), true
}

func unescName(s string) string {
	if !strings.Contains(s, "#") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				sb.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func (p *pdfParser) litString() []byte {
	p.pos++ // (
	var out []byte
	depth := 1
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		p.pos++
		switch c {
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == 0 {
				return out
			}
			out = append(out, c)
		case '\\':
			if p.pos >= len(p.b) {
				return out
			}
			e := p.b[p.pos]
			p.pos++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, 8)
			case 'f':
				out = append(out, 12)
			case '\r':
				if p.pos < len(p.b) && p.b[p.pos] == '\n' {
					p.pos++
				}
			case '\n':
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for i := 0; i < 2 && p.pos < len(p.b) && p.b[p.pos] >= '0' && p.b[p.pos] <= '7'; i++ {
						v = v*8 + int(p.b[p.pos]-'0')
						p.pos++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		default:
			out = append(out, c)
		}
	}
	return out
}

func (p *pdfParser) hexString() []byte {
	p.pos++ // <
	var h []byte
	for p.pos < len(p.b) {
		c := p.b[p.pos]
		p.pos++
		if c == '>' {
			break
		}
		if isWS(c) {
			continue
		}
		h = append(h, c)
	}
	if len(h)%2 == 1 {
		h = append(h, '0')
	}
	out := make([]byte, hex.DecodedLen(len(h)))
	n, _ := hex.Decode(out, h)
	return out[:n]
}

// ---- Dokument ----

type pdfDoc struct {
	obj    map[int]any
	total  int
	fcache map[pdfRef]*pdfFont
}

var objRe = regexp.MustCompile(`(\d+)[ \t\r\n]+(\d+)[ \t\r\n]+obj\b`)

func parsePDF(b []byte) (*pdfDoc, error) {
	if len(b) < 8 || !bytes.Contains(b[:min(len(b), 1024)], []byte("%PDF-")) {
		return nil, ErrPDFBad
	}
	d := &pdfDoc{obj: map[int]any{}, fcache: map[pdfRef]*pdfFont{}}
	pos := 0
	for pos < len(b) {
		m := objRe.FindSubmatchIndex(b[pos:])
		if m == nil {
			break
		}
		num, _ := strconv.Atoi(string(b[pos+m[2] : pos+m[3]]))
		p := &pdfParser{b: b, pos: pos + m[1], refs: true}
		v, ok := p.value(0)
		if !ok {
			pos += m[1]
			continue
		}
		if dict, isD := v.(pdfDict); isD {
			p.skip()
			if bytes.HasPrefix(b[p.pos:], []byte("stream")) {
				s := p.pos + 6
				if s < len(b) && b[s] == '\r' {
					s++
				}
				if s < len(b) && b[s] == '\n' {
					s++
				}
				end := -1
				if l, isN := dict["Length"].(float64); isN && l >= 0 && s+int(l) <= len(b) {
					t := s + int(l)
					for t < len(b) && isWS(b[t]) {
						t++
					}
					if bytes.HasPrefix(b[t:], []byte("endstream")) {
						end = s + int(l)
					}
				}
				if end < 0 {
					i := bytes.Index(b[s:], []byte("endstream"))
					if i < 0 {
						end = len(b)
					} else {
						end = s + i
					}
				}
				v = &pdfStream{d: dict, data: b[s:end]}
				p.pos = end
			}
		}
		d.obj[num] = v
		if p.pos <= pos {
			p.pos = pos + m[1]
		}
		pos = p.pos
	}
	if len(d.obj) == 0 {
		return nil, ErrPDFBad
	}
	// Objektströme auspacken
	nums := make([]int, 0, len(d.obj))
	for n := range d.obj {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for _, n := range nums {
		st, ok := d.obj[n].(*pdfStream)
		if !ok || d.name(st.d["Type"]) != "ObjStm" {
			continue
		}
		data, err := d.decode(st)
		if err != nil {
			continue
		}
		cnt, _ := d.num(st.d["N"])
		first, _ := d.num(st.d["First"])
		hp := &pdfParser{b: data}
		type ent struct{ n, off int }
		var es []ent
		for i := 0; i < int(cnt) && i < 100000; i++ {
			a, ok1 := hp.value(0)
			o, ok2 := hp.value(0)
			af, okA := a.(float64)
			of, okO := o.(float64)
			if !ok1 || !ok2 || !okA || !okO {
				break
			}
			es = append(es, ent{int(af), int(of)})
		}
		for _, e := range es {
			if int(first)+e.off >= len(data) || e.off < 0 {
				continue
			}
			ip := &pdfParser{b: data, pos: int(first) + e.off, refs: true}
			if v, ok := ip.value(0); ok {
				if _, exists := d.obj[e.n]; !exists {
					d.obj[e.n] = v
				}
			}
		}
	}
	return d, nil
}

// get löst Verweise auf (begrenzt).
func (d *pdfDoc) get(v any) any {
	for i := 0; i < 20; i++ {
		r, ok := v.(pdfRef)
		if !ok {
			return v
		}
		v = d.obj[int(r)]
	}
	return nil
}

func (d *pdfDoc) dict(v any) pdfDict {
	switch x := d.get(v).(type) {
	case pdfDict:
		return x
	case *pdfStream:
		return x.d
	}
	return nil
}

func (d *pdfDoc) name(v any) string {
	n, _ := d.get(v).(pdfName)
	return string(n)
}

func (d *pdfDoc) num(v any) (float64, bool) {
	f, ok := d.get(v).(float64)
	return f, ok
}

func (d *pdfDoc) array(v any) []any {
	a, _ := d.get(v).([]any)
	return a
}

func (d *pdfDoc) stream(v any) *pdfStream {
	s, _ := d.get(v).(*pdfStream)
	return s
}

// decode wendet die Filter eines Streams an.
func (d *pdfDoc) decode(st *pdfStream) ([]byte, error) {
	var filters []string
	switch f := d.get(st.d["Filter"]).(type) {
	case pdfName:
		filters = []string{string(f)}
	case []any:
		for _, x := range f {
			filters = append(filters, d.name(x))
		}
	}
	var parms []any
	switch pr := d.get(st.d["DecodeParms"]).(type) {
	case pdfDict:
		parms = []any{pr}
	case []any:
		parms = pr
	}
	data := st.data
	for i, f := range filters {
		switch f {
		case "FlateDecode", "Fl":
			zr, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, ErrPDFBad
			}
			out, _ := io.ReadAll(io.LimitReader(zr, pdfMaxStream+1)) // bei beschädigtem Ende bleibt der gelesene Teil
			if len(out) > pdfMaxStream {
				return nil, ErrPDFBad
			}
			data = out
			if i < len(parms) {
				if pd := d.dict(parms[i]); pd != nil {
					if pr, _ := d.num(pd["Predictor"]); pr >= 10 {
						col, ok := d.num(pd["Columns"])
						if !ok {
							col = 1
						}
						cl, ok := d.num(pd["Colors"])
						if !ok {
							cl = 1
						}
						bp, ok := d.num(pd["BitsPerComponent"])
						if !ok {
							bp = 8
						}
						data = pngUnpredict(data, int(col), int(cl), int(bp))
					}
				}
			}
		case "ASCIIHexDecode", "AHx":
			s := strings.Map(func(r rune) rune {
				if r == '>' || r == ' ' || r == '\n' || r == '\r' || r == '\t' {
					return -1
				}
				return r
			}, string(bytes.SplitN(data, []byte(">"), 2)[0]))
			if len(s)%2 == 1 {
				s += "0"
			}
			out, err := hex.DecodeString(s)
			if err != nil {
				return nil, ErrPDFBad
			}
			data = out
		case "ASCII85Decode", "A85":
			t := bytes.TrimSpace(data)
			t = bytes.TrimPrefix(t, []byte("<~"))
			if i := bytes.Index(t, []byte("~>")); i >= 0 {
				t = t[:i]
			}
			out, err := io.ReadAll(io.LimitReader(ascii85.NewDecoder(bytes.NewReader(t)), pdfMaxStream))
			if err != nil {
				return nil, ErrPDFBad
			}
			data = out
		default:
			return nil, ErrPDFBad
		}
	}
	d.total += len(data)
	if d.total > pdfMaxTotal {
		return nil, ErrPDFBad
	}
	return data, nil
}

func pngUnpredict(b []byte, cols, colors, bits int) []byte {
	bpp := max(1, colors*bits/8)
	row := (cols*colors*bits + 7) / 8
	if row <= 0 || row > 1<<20 {
		return b
	}
	var out []byte
	prev := make([]byte, row)
	for i := 0; i+row+1 <= len(b); i += row + 1 {
		t := b[i]
		cur := append([]byte(nil), b[i+1:i+1+row]...)
		for j := 0; j < row; j++ {
			var left, up, ul byte
			if j >= bpp {
				left = cur[j-bpp]
				ul = prev[j-bpp]
			}
			up = prev[j]
			switch t {
			case 1:
				cur[j] += left
			case 2:
				cur[j] += up
			case 3:
				cur[j] += byte((int(left) + int(up)) / 2)
			case 4:
				pa := int(up) - int(ul)
				pb := int(left) - int(ul)
				pc := pa + pb
				if pa < 0 {
					pa = -pa
				}
				if pb < 0 {
					pb = -pb
				}
				if pc < 0 {
					pc = -pc
				}
				pr := left
				if pb < pa && pb <= pc {
					pr, pa = up, pb
				}
				if pc < pa && pc < pb {
					pr = ul
				}
				cur[j] += pr
			}
		}
		out = append(out, cur...)
		prev = cur
	}
	return out
}

// ---- Schriften ----

type pdfFont struct {
	cmap  map[uint32]string
	width int // Bytes je Zeichencode
	type0 bool
}

var cp1252hi = [32]rune{0x20AC, 0x81, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8D, 0x017D, 0x8F,
	0x90, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x9D, 0x017E, 0x0178}

func (f *pdfFont) decode(s []byte) string {
	var sb strings.Builder
	w := max(f.width, 1)
	for i := 0; i+w <= len(s); i += w {
		var code uint32
		for j := 0; j < w; j++ {
			code = code<<8 | uint32(s[i+j])
		}
		if t, ok := f.cmap[code]; ok {
			sb.WriteString(t)
			continue
		}
		if f.type0 || f.cmap != nil && w > 1 {
			continue
		}
		switch {
		case code >= 0x80 && code < 0xA0:
			sb.WriteRune(cp1252hi[code-0x80])
		case code >= 32:
			sb.WriteRune(rune(code))
		case code == 9 || code == 10:
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

func utf16be(b []byte) string {
	if len(b)%2 == 1 {
		b = append([]byte{0}, b...)
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return string(utf16.Decode(u))
}

func bytesNum(b []byte) uint32 {
	var v uint32
	for _, c := range b {
		v = v<<8 | uint32(c)
	}
	return v
}

func (d *pdfDoc) font(fd pdfDict) *pdfFont {
	f := &pdfFont{width: 1}
	st := fd
	f.type0 = d.name(st["Subtype"]) == "Type0"
	if f.type0 {
		f.width = 2
	}
	tu := d.stream(st["ToUnicode"])
	if tu == nil {
		return f
	}
	data, err := d.decode(tu)
	if err != nil {
		return f
	}
	f.cmap = map[uint32]string{}
	p := &pdfParser{b: data}
	var stack []any
	section := ""
	for {
		v, ok := p.value(0)
		if !ok {
			if p.pos >= len(p.b) {
				break
			}
			continue
		}
		op, isOp := v.(pdfOp)
		if !isOp {
			stack = append(stack, v)
			if len(stack) > 100000 {
				break
			}
			continue
		}
		switch string(op) {
		case "begincodespacerange":
			section = "cs"
			stack = stack[:0]
		case "beginbfchar":
			section = "ch"
			stack = stack[:0]
		case "beginbfrange":
			section = "rg"
			stack = stack[:0]
		case "endcodespacerange", "endbfchar", "endbfrange":
			switch section {
			case "cs":
				if len(stack) > 0 {
					if s, ok := stack[0].([]byte); ok && len(s) > 0 && len(s) <= 4 {
						f.width = len(s)
					}
				}
			case "ch":
				for i := 0; i+1 < len(stack); i += 2 {
					a, ok1 := stack[i].([]byte)
					b, ok2 := stack[i+1].([]byte)
					if ok1 && ok2 {
						f.cmap[bytesNum(a)] = utf16be(b)
					}
				}
			case "rg":
				for i := 0; i+2 < len(stack); i += 3 {
					lo, ok1 := stack[i].([]byte)
					hi, ok2 := stack[i+1].([]byte)
					if !ok1 || !ok2 {
						continue
					}
					l, h := bytesNum(lo), bytesNum(hi)
					if h < l || h-l > 65535 {
						continue
					}
					switch dst := stack[i+2].(type) {
					case []byte:
						base := []rune(utf16be(dst))
						for c := l; c <= h; c++ {
							r := append([]rune(nil), base...)
							if len(r) > 0 {
								r[len(r)-1] += rune(c - l)
							}
							f.cmap[c] = string(r)
						}
					case []any:
						for k, e := range dst {
							if s, ok := e.([]byte); ok && l+uint32(k) <= h {
								f.cmap[l+uint32(k)] = utf16be(s)
							}
						}
					}
				}
			}
			section = ""
			stack = stack[:0]
		default:
			stack = stack[:0]
		}
	}
	return f
}

// ---- Seiten ----

func (d *pdfDoc) pages() [][]pdfDict {
	// Wurzel: Katalog
	var root any
	var nums []int
	for n := range d.obj {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	for _, n := range nums {
		if dd := d.dict(d.obj[n]); dd != nil && d.name(dd["Type"]) == "Catalog" {
			root = dd["Pages"]
		}
	}
	var out [][]pdfDict // je Seite: [Seite, geerbte Ressourcen]
	seen := map[any]bool{}
	var walk func(v any, res any, depth int)
	walk = func(v any, res any, depth int) {
		if depth > pdfMaxDepth || len(out) >= pdfMaxPages {
			return
		}
		if r, ok := v.(pdfRef); ok {
			if seen[r] {
				return
			}
			seen[r] = true
		}
		nd := d.dict(v)
		if nd == nil {
			return
		}
		if r, ok := nd["Resources"]; ok {
			res = r
		}
		kids := d.array(nd["Kids"])
		if d.name(nd["Type"]) == "Pages" || kids != nil {
			for _, k := range kids {
				walk(k, res, depth+1)
			}
			return
		}
		rd := d.dict(res)
		out = append(out, []pdfDict{nd, rd})
	}
	if root != nil {
		walk(root, nil, 0)
	}
	if len(out) == 0 { // kaputter Baum: alle Seiten-Objekte der Reihe nach
		for _, n := range nums {
			if dd := d.dict(d.obj[n]); dd != nil && d.name(dd["Type"]) == "Page" {
				out = append(out, []pdfDict{dd, d.dict(dd["Resources"])})
				if len(out) >= pdfMaxPages {
					break
				}
			}
		}
	}
	return out
}

type pdfText struct {
	sb    strings.Builder
	lastY float64
	haveY bool
	x, y  float64 // Zeilenmatrix (nur Verschiebung)
	pend  bool    // seit dem letzten Text neu positioniert
}

func (t *pdfText) emit(s string) {
	if s == "" {
		return
	}
	cur := t.sb.String()
	if cur != "" && !strings.HasSuffix(cur, "\n") {
		if t.haveY && abs(t.y-t.lastY) > 1 {
			t.sb.WriteByte('\n')
		} else if t.pend && !strings.HasSuffix(cur, " ") && !strings.HasPrefix(s, " ") && !strings.ContainsAny(s[:1], ",.;:!?)%") {
			t.sb.WriteByte(' ')
		}
	}
	t.sb.WriteString(s)
	t.lastY, t.haveY, t.pend = t.y, true, false
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func (d *pdfDoc) pageText(pg pdfDict, res pdfDict) string {
	// Schriften der Seite
	fonts := map[string]*pdfFont{}
	if res != nil {
		fd := d.dict(res["Font"])
		for k, v := range fd {
			r, isRef := v.(pdfRef)
			if isRef && d.fcache[r] != nil {
				fonts[k] = d.fcache[r]
				continue
			}
			if dd := d.dict(v); dd != nil {
				f := d.font(dd)
				fonts[k] = f
				if isRef {
					d.fcache[r] = f
				}
			}
		}
	}
	// Inhalt: ein Stream oder eine Liste
	var content []byte
	var parts []any
	switch c := d.get(pg["Contents"]).(type) {
	case *pdfStream:
		parts = []any{c}
	case []any:
		parts = c
	}
	for _, c := range parts {
		if st := d.stream(c); st != nil {
			b, err := d.decode(st)
			if err != nil {
				continue
			}
			content = append(content, b...)
			content = append(content, '\n')
		}
	}
	t := &pdfText{}
	p := &pdfParser{b: content}
	var st []any
	var font *pdfFont
	num := func(i int) float64 {
		if i < 0 || i >= len(st) {
			return 0
		}
		f, _ := st[i].(float64)
		return f
	}
	show := func(v any) {
		if s, ok := v.([]byte); ok {
			if font == nil {
				font = &pdfFont{width: 1}
			}
			t.emit(font.decode(s))
		}
	}
	for p.pos < len(p.b) && t.sb.Len() < pdfMaxText {
		v, ok := p.value(0)
		if !ok {
			continue
		}
		op, isOp := v.(pdfOp)
		if !isOp {
			st = append(st, v)
			if len(st) > 64 {
				st = st[len(st)-64:]
			}
			continue
		}
		n := len(st)
		switch string(op) {
		case "BT":
			t.x, t.y = 0, 0
		case "Tf":
			if n >= 2 {
				if nm, ok := st[n-2].(pdfName); ok {
					font = fonts[string(nm)]
				}
			}
		case "Td", "TD":
			if n >= 2 {
				t.x += num(n - 2)
				t.y += num(n - 1)
				t.pend = true
			}
		case "Tm":
			if n >= 6 {
				t.x, t.y = num(n-2), num(n-1)
				t.pend = true
			}
		case "T*":
			t.y -= 1000
			t.pend = true
		case "Tj":
			if n >= 1 {
				show(st[n-1])
			}
		case "'":
			t.y -= 1000
			t.pend = true
			if n >= 1 {
				show(st[n-1])
			}
		case "\"":
			t.y -= 1000
			t.pend = true
			if n >= 1 {
				show(st[n-1])
			}
		case "TJ":
			if n >= 1 {
				if arr, ok := st[n-1].([]any); ok {
					for _, e := range arr {
						switch x := e.(type) {
						case []byte:
							show(x)
						case float64:
							if x < -200 {
								t.emit(" ")
							}
						}
					}
				}
			}
		}
		st = st[:0]
	}
	return strings.TrimSpace(t.sb.String())
}

var ligatures = strings.NewReplacer("\ufb00", "ff", "\ufb01", "fi", "\ufb02", "fl", "\ufb03", "ffi", "\ufb04", "ffl")

var multiNL = regexp.MustCompile(`\n{3,}`)

// FromPDF liefert den Text eines PDFs seitenweise (Seiten durch Leerzeilen getrennt).
func FromPDF(b []byte) (string, error) {
	if encRe.Match(b) {
		return "", ErrPDFEncrypted
	}
	d, err := parsePDF(b)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, pg := range d.pages() {
		txt := d.pageText(pg[0], pg[1])
		if txt != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(txt)
		}
		if sb.Len() >= pdfMaxText {
			break
		}
	}
	out := strings.TrimSpace(multiNL.ReplaceAllString(ligatures.Replace(sb.String()), "\n\n"))
	if out == "" {
		return "", ErrPDFNoText
	}
	return out, nil
}

var encRe = regexp.MustCompile(`/Encrypt[ \t\r\n]*(\d+[ \t\r\n]+\d+[ \t\r\n]+R|<<)`)
