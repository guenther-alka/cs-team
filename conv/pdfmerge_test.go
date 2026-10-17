package conv

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func pageCount(t *testing.T, b []byte) int {
	t.Helper()
	d, err := parsePDF(b)
	if err != nil {
		t.Fatal(err)
	}
	return len(d.pages())
}

// mergedCheck: xref-Tabelle und Objektoffsets des zusammengefuegten PDFs stimmen.
func mergedCheck(t *testing.T, b []byte) {
	t.Helper()
	if !bytes.HasPrefix(b, []byte("%PDF-1.7")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("kein PDF")
	}
	m := regexp.MustCompile(`startxref\n(\d+)\n`).FindSubmatch(b)
	if m == nil {
		t.Fatal("startxref")
	}
	off, _ := strconv.Atoi(string(m[1]))
	var n int
	if _, err := fmt.Sscanf(string(b[off:]), "xref\n0 %d\n", &n); err != nil || n < 5 {
		t.Fatal("xref", err, n)
	}
	lines := strings.Split(string(b[off:]), "\n")
	for i := 1; i < n; i++ {
		o, err := strconv.Atoi(strings.Fields(lines[2+i])[0])
		if err != nil || !bytes.HasPrefix(b[o:], []byte(strconv.Itoa(i)+" 0 obj")) {
			t.Fatalf("Objekt %d: Offset passt nicht", i)
		}
	}
}

func norm(s string) string { return strings.Join(strings.Fields(s), "") }

func TestMergeOwnPDFs(t *testing.T) {
	a := ToPDFRich("A", []Para{{V: "ALPHA erste Datei"}, {V: strings.Repeat("Fuelltext fuer mehrere Seiten. ", 400)}})
	b := ToPDFRich("B", []Para{mkPara("", sg("BETA zweite Datei", "b"))})
	c := ToPDFCells("C", map[string]FCell{"A1": {V: "GAMMA"}}, nil)
	na, nb, nc := pageCount(t, a), pageCount(t, b), pageCount(t, c)
	if na < 2 {
		t.Fatal("erste Datei braucht mehrere Seiten", na)
	}
	out, err := MergePDF([][]byte{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	mergedCheck(t, out)
	if got := pageCount(t, out); got != na+nb+nc {
		t.Fatalf("Seiten: %d statt %d", got, na+nb+nc)
	}
	txt, err := FromPDF(out)
	if err != nil {
		t.Fatal(err)
	}
	i, j, k := strings.Index(txt, "ALPHA"), strings.Index(txt, "BETA"), strings.Index(txt, "GAMMA")
	if i < 0 || j < i || k < j {
		t.Fatalf("Reihenfolge der Texte: %d %d %d", i, j, k)
	}
	// Ergebnis laesst sich erneut zusammenfuegen
	if o2, err := MergePDF([][]byte{out, b}); err != nil || pageCount(t, o2) != na+nb+nc+nb {
		t.Fatal("zweite Runde", err)
	}
}

// Dateien aus der Praxis (Chrome mit Objektstroemen, ReportLab mit Standard- und TrueType-Schriften).
func TestMergeTestdata(t *testing.T) {
	var files []string
	for _, n := range []string{"chrome_objstm.pdf", "rl_std.pdf", "rl_ttf.pdf"} {
		files = append(files, filepath.Join("testdata", n))
	}
	var data [][]byte
	var texts []string
	pages := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Skip("testdata fehlt:", err)
		}
		data = append(data, b)
		pages += pageCount(t, b)
		tx, err := FromPDF(b)
		if err != nil {
			t.Fatal(f, err)
		}
		texts = append(texts, norm(tx))
	}
	out, err := MergePDF(data)
	if err != nil {
		t.Fatal(err)
	}
	mergedCheck(t, out)
	if got := pageCount(t, out); got != pages {
		t.Fatalf("Seiten: %d statt %d", got, pages)
	}
	mt, err := FromPDF(out)
	if err != nil {
		t.Fatal(err)
	}
	all := norm(mt)
	pos := 0
	for i, tx := range texts { // der Text jeder Datei steht in der Summe, in der richtigen Reihenfolge
		probe := tx[:min(len(tx), 60)]
		p := strings.Index(all[pos:], probe)
		if p < 0 {
			t.Fatalf("Text von %s fehlt im Ergebnis (%q)", files[i], probe)
		}
		pos += p + len(probe)
	}
	// einzelne Paare
	for i := range data {
		for j := range data {
			if i == j {
				continue
			}
			o, err := MergePDF([][]byte{data[i], data[j]})
			if err != nil || pageCount(t, o) != pageCount(t, data[i])+pageCount(t, data[j]) {
				t.Fatalf("Paar %d/%d: %v", i, j, err)
			}
		}
	}
}

func TestMergeErrors(t *testing.T) {
	ok := ToPDFRich("x", []Para{{V: "text"}})
	if _, err := MergePDF([][]byte{ok}); err == nil {
		t.Fatal("ein Teil reicht nicht")
	}
	if _, err := MergePDF([][]byte{ok, []byte("kein pdf")}); !errors.Is(err, ErrPDFBad) || !strings.Contains(err.Error(), "part 2") {
		t.Fatal("kaputter Teil:", err)
	}
	if enc, err := os.ReadFile(filepath.Join("testdata", "enc.pdf")); err == nil {
		if _, err := MergePDF([][]byte{ok, enc}); !errors.Is(err, ErrPDFEncrypted) || !strings.Contains(err.Error(), "part 2") {
			t.Fatal("verschluesselt:", err)
		}
	}
	many := make([][]byte, PDFMergeMaxParts+1)
	for i := range many {
		many[i] = ok
	}
	if _, err := MergePDF(many); err == nil {
		t.Fatal("zu viele Teile")
	}
	if _, err := MergePDF(many[:PDFMergeMaxParts]); err != nil {
		t.Fatal("20 Teile muessen gehen:", err)
	}
}

func TestPDFWriteValues(t *testing.T) {
	var b bytes.Buffer
	pdfWrite(&b, pdfDict{"A b": pdfName("N(x)"), "Z": []any{float64(3), 1.5, true, nil, pdfRef(7), []byte{0xAB, 0x01}}})
	if got := b.String(); got != "<</A#20b /N#28x#29 /Z [3 1.5 true null 7 0 R <ab01>] >>" {
		t.Fatalf("%q", got)
	}
}
