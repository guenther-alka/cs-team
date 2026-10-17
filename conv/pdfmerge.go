package conv

// PDFs zusammenfuegen (nur Go-Standardbibliothek, auf dem vorhandenen Leser pdf.go). Aus jedem Teil werden die Seiten samt Inhalt,
// Ressourcen (Schriften, Bilder) und Anmerkungen (z. B. Links) uebernommen und neu nummeriert; geerbte Seiteneigenschaften
// (Resources, MediaBox, CropBox, Rotate) werden in jede Seite geschrieben. Verloren gehen Lesezeichen, Formularfelder (AcroForm), Dokumentinfo,
// Beschriftungen des Strukturbaums und Seitenmarken. Verschluesselte PDFs werden abgelehnt (ErrPDFEncrypted).

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	PDFMergeMaxParts = 20
	pdfMergeMaxPages = 2000
	pdfMergeMaxObjs  = 400000
	pdfMergeMaxOut   = 256 << 20
)

var pdfInheritable = []string{"Resources", "MediaBox", "CropBox", "Rotate"}

type mpage struct {
	num int
	d   pdfDict
	inh map[string]any
	nn  int
}

// mergePages: die Seiten in Lesereihenfolge mit den von den Knoten geerbten Eigenschaften.
func (d *pdfDoc) mergePages() []mpage {
	var nums []int
	for n := range d.obj {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	var root any
	for _, n := range nums {
		if dd := d.dict(d.obj[n]); dd != nil && d.name(dd["Type"]) == "Catalog" {
			root = dd["Pages"]
		}
	}
	var out []mpage
	seen := map[int]bool{}
	var walk func(v any, inh map[string]any, depth int)
	walk = func(v any, inh map[string]any, depth int) {
		if depth > pdfMaxDepth || len(out) >= pdfMergeMaxPages {
			return
		}
		num := 0
		if r, ok := v.(pdfRef); ok {
			if seen[int(r)] {
				return
			}
			seen[int(r)] = true
			num = int(r)
		}
		nd := d.dict(v)
		if nd == nil {
			return
		}
		ni := make(map[string]any, len(inh)+4)
		for k, x := range inh {
			ni[k] = x
		}
		for _, k := range pdfInheritable {
			if x, ok := nd[k]; ok {
				ni[k] = x
			}
		}
		kids := d.array(nd["Kids"])
		if d.name(nd["Type"]) == "Pages" || kids != nil {
			for _, k := range kids {
				walk(k, ni, depth+1)
			}
			return
		}
		out = append(out, mpage{num: num, d: nd, inh: ni})
	}
	if root != nil {
		walk(root, map[string]any{}, 0)
	}
	if len(out) == 0 { // kaputter Baum: alle Seitenobjekte der Reihe nach
		for _, n := range nums {
			if dd := d.dict(d.obj[n]); dd != nil && d.name(dd["Type"]) == "Page" && len(out) < pdfMergeMaxPages {
				inh := map[string]any{}
				for _, k := range pdfInheritable {
					if x, ok := dd[k]; ok {
						inh[k] = x
					}
				}
				out = append(out, mpage{num: n, d: dd, inh: inh})
			}
		}
	}
	return out
}

type mqItem struct{ part, old, nn int }

type mergeCtx struct {
	next  int
	out   map[int]any
	maps  []map[int]int
	docs  []*pdfDoc
	queue []mqItem
	over  bool
}

// ref liefert die neue Nummer fuer das Objekt old des Teils part (und merkt es zum Kopieren vor).
func (m *mergeCtx) ref(part, old int) any {
	if n, ok := m.maps[part][old]; ok {
		return pdfRef(n)
	}
	obj, ok := m.docs[part].obj[old]
	if !ok {
		return nil
	}
	if dd := m.docs[part].dict(obj); dd != nil { // Seitenbaum, Katalog und fremde Seiten nie mitziehen
		switch m.docs[part].name(dd["Type"]) {
		case "Pages", "Catalog", "Page":
			return nil
		}
	}
	if m.next > pdfMergeMaxObjs {
		m.over = true
		return nil
	}
	n := m.next
	m.next++
	m.maps[part][old] = n
	m.queue = append(m.queue, mqItem{part, old, n})
	return pdfRef(n)
}

func (m *mergeCtx) cp(part int, v any) any {
	switch x := v.(type) {
	case pdfRef:
		return m.ref(part, int(x))
	case pdfDict:
		c := make(pdfDict, len(x))
		for k, e := range x {
			c[k] = m.cp(part, e)
		}
		return c
	case []any:
		c := make([]any, len(x))
		for i, e := range x {
			c[i] = m.cp(part, e)
		}
		return c
	}
	return v
}

func (m *mergeCtx) drain() {
	for len(m.queue) > 0 {
		it := m.queue[0]
		m.queue = m.queue[1:]
		switch x := m.docs[it.part].obj[it.old].(type) {
		case *pdfStream:
			d, _ := m.cp(it.part, x.d).(pdfDict)
			delete(d, "Length")
			m.out[it.nn] = &pdfStream{d: d, data: x.data}
		default:
			m.out[it.nn] = m.cp(it.part, x)
		}
	}
}

// MergePDF fuegt die PDFs in der gegebenen Reihenfolge zu einem zusammen.
func MergePDF(parts [][]byte) ([]byte, error) {
	if len(parts) < 2 || len(parts) > PDFMergeMaxParts {
		return nil, ErrPDFBad
	}
	m := &mergeCtx{next: 4, out: map[int]any{}} // 1 Katalog, 2 Seitenbaum, 3 Info
	var kids []any
	for i, b := range parts {
		if encRe.Match(b) {
			return nil, fmt.Errorf("part %d: %w", i+1, ErrPDFEncrypted)
		}
		d, err := parsePDF(b)
		if err != nil {
			return nil, fmt.Errorf("part %d: %w", i+1, err)
		}
		m.docs = append(m.docs, d)
		m.maps = append(m.maps, map[int]int{})
		pg := d.mergePages()
		if len(pg) == 0 {
			return nil, fmt.Errorf("part %d: %w", i+1, ErrPDFBad)
		}
		if len(kids)+len(pg) > pdfMergeMaxPages {
			return nil, fmt.Errorf("too many pages (max %d)", pdfMergeMaxPages)
		}
		for j := range pg { // zuerst alle Seitennummern vergeben, damit Verweise zwischen Seiten (Links) aufgehen
			pg[j].nn = m.next
			m.next++
			if pg[j].num > 0 {
				m.maps[i][pg[j].num] = pg[j].nn
			}
			kids = append(kids, pdfRef(pg[j].nn))
		}
		for _, p := range pg {
			nd := pdfDict{}
			for k, v := range p.d {
				if k != "Parent" {
					nd[k] = m.cp(i, v)
				}
			}
			for _, k := range pdfInheritable {
				if _, ok := nd[k]; !ok {
					if x, ok := p.inh[k]; ok {
						nd[k] = m.cp(i, x)
					}
				}
			}
			nd["Type"] = pdfName("Page")
			nd["Parent"] = pdfRef(2)
			m.out[p.nn] = nd
		}
		m.drain()
		if m.over {
			return nil, fmt.Errorf("pdf too complex")
		}
	}
	m.out[1] = pdfDict{"Type": pdfName("Catalog"), "Pages": pdfRef(2)}
	m.out[2] = pdfDict{"Type": pdfName("Pages"), "Kids": kids, "Count": float64(len(kids))}
	m.out[3] = pdfDict{"Producer": []byte("cs-team")}
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offs := make([]int, m.next)
	for n := 1; n < m.next; n++ {
		offs[n] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", n)
		pdfWrite(&out, m.out[n])
		out.WriteString("\nendobj\n")
		if out.Len() > pdfMergeMaxOut {
			return nil, fmt.Errorf("result too large")
		}
	}
	start := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", m.next)
	for n := 1; n < m.next; n++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offs[n])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info 3 0 R >>\nstartxref\n%d\n%%%%EOF\n", m.next, start)
	return out.Bytes(), nil
}

func pdfWriteName(out *bytes.Buffer, s string) {
	out.WriteByte('/')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 32 || c >= 127 || strings.IndexByte("()<>[]{}/%#", c) >= 0 {
			fmt.Fprintf(out, "#%02X", c)
		} else {
			out.WriteByte(c)
		}
	}
}

func pdfWrite(out *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(x))
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			out.WriteString(strconv.FormatInt(int64(x), 10))
		} else {
			out.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
		}
	case pdfName:
		pdfWriteName(out, string(x))
	case pdfRef:
		fmt.Fprintf(out, "%d 0 R", int(x))
	case []byte:
		out.WriteByte('<')
		out.WriteString(hex.EncodeToString(x))
		out.WriteByte('>')
	case []any:
		out.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				out.WriteByte(' ')
			}
			pdfWrite(out, e)
		}
		out.WriteByte(']')
	case pdfDict:
		out.WriteString("<<")
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			pdfWriteName(out, k)
			out.WriteByte(' ')
			pdfWrite(out, x[k])
			out.WriteByte(' ')
		}
		out.WriteString(">>")
	case *pdfStream:
		x.d["Length"] = float64(len(x.data))
		pdfWrite(out, x.d)
		out.WriteString("\nstream\n")
		out.Write(x.data)
		out.WriteString("\nendstream")
	default:
		out.WriteString("null")
	}
}
