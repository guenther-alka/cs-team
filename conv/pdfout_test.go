package conv

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pdfCheck prueft den Aufbau (Kopf, Ende, xref-Tabelle verweist auf "N 0 obj") und liefert Seitenzahl und entpackten Inhalt aller Seiten.
func pdfCheck(t *testing.T, b []byte) (pages int, content []byte, media []string) {
	t.Helper()
	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("kein PDF")
	}
	m := regexp.MustCompile(`startxref\n(\d+)\n`).FindSubmatch(b)
	if m == nil {
		t.Fatal("startxref fehlt")
	}
	off, _ := strconv.Atoi(string(m[1]))
	var n int
	if _, err := fmt.Sscanf(string(b[off:]), "xref\n0 %d\n", &n); err != nil || n < 9 {
		t.Fatal("xref:", err, n)
	}
	lines := strings.Split(string(b[off:]), "\n")
	for i := 1; i < n; i++ {
		e := lines[2+i] // "xref", "0 N", Eintrag 0 (frei), dann Objekte 1..n-1
		o, err := strconv.Atoi(strings.Fields(e)[0])
		if err != nil || !bytes.HasPrefix(b[o:], []byte(strconv.Itoa(i)+" 0 obj")) {
			t.Fatalf("Objekt %d: Offset %q passt nicht", i, e)
		}
	}
	pages = strings.Count(string(b), "/Type /Page /Parent")
	if !strings.Contains(string(b), fmt.Sprintf("/Count %d", pages)) {
		t.Fatal("Seitenzahl")
	}
	for _, mm := range regexp.MustCompile(`/MediaBox \[0 0 ([0-9.]+) ([0-9.]+)\]`).FindAllStringSubmatch(string(b), -1) {
		media = append(media, mm[1]+"x"+mm[2])
	}
	for rest := b; ; {
		i := bytes.Index(rest, []byte("/Filter /FlateDecode >>\nstream\n"))
		if i < 0 {
			break
		}
		lm := regexp.MustCompile(`/Length (\d+) $`).FindSubmatch(rest[:i])
		if lm == nil {
			t.Fatal("Length")
		}
		l, _ := strconv.Atoi(string(lm[1]))
		start := i + len("/Filter /FlateDecode >>\nstream\n")
		zr, err := zlib.NewReader(bytes.NewReader(rest[start : start+l]))
		if err != nil {
			t.Fatal(err)
		}
		d, _ := io.ReadAll(zr)
		content = append(content, d...)
		rest = rest[start+l:]
	}
	return
}

func TestPDFWidthTables(t *testing.T) {
	for i, w := range wReg {
		if w <= 0 || wBold[i] <= 0 {
			t.Fatalf("Breite fehlt bei Zeichen %d", 32+i)
		}
	}
	if adv('A', false) != 667 || adv('i', false) != 222 || adv('~', false) != 584 || adv('W', true) != 944 || adv('m', true) != 889 || adv(' ', false) != 278 {
		t.Fatal("Breitentabelle")
	}
	if adv(0xE4, false) != adv('a', false) || adv(0xC4, true) != adv('A', true) || adv(0x80, false) != 556 || adv(0xE6, false) != 889 {
		t.Fatal("Akzentbuchstaben")
	}
	if b := ansi("€ä…\u4e2d"); !bytes.Equal(b, []byte{0x80, 0xE4, 0x85, '?'}) {
		t.Fatalf("WinAnsi: %v", b)
	}
	if rgb("ff0000") != "1.000 0.000 0.000" || rgb("") != "0 0 0" {
		t.Fatal("Farbe")
	}
}

func TestPDFRich(t *testing.T) {
	long := strings.Repeat("Das ist ein langer Absatz mit Größe, Ärger und Übermut – 100 € für ein Stück. ", 12)
	in := []Para{
		mkPara("", sg("Titel ", "b s24"), sg("rot", "c#ff0000"), sg(" fett kursiv", "b i"), sg(" durchgestrichen", "t"), sg(" unterstrichen", "u"), sg(" gelb", "g#ffff00")),
		mkPara("", sg("Mit ", ""), sg("Kommentar", "m"+cmEncode("Bitte prüfen (ok)")), sg(" danach", "")),
		mkPara("l", sg("Punkt (eins) \\ zwei", "")),
		mkPara("o", sg("eins", "")), mkPara("o n1", sg("zwei", "")), mkPara("", sg("Absatz", "")), mkPara("o", sg("neu eins", "")),
		{V: "Zeile eins\nZeile zwei\tTab"},
		{V: strings.Repeat("W", 200)}, // ein Wort breiter als die Zeile
		{V: long}, {V: long}, {V: long}, {V: long}, {V: long}, {V: long},
		{},
	}
	b := ToPDFRich("Bericht (Q3)", in)
	pages, c, media := pdfCheck(t, b)
	if pages < 2 {
		t.Fatalf("erwartet mehrere Seiten, %d", pages)
	}
	for _, m := range media {
		if m != "595.28x841.89" {
			t.Fatal("Seitengroesse", m)
		}
	}
	for _, want := range [][]byte{[]byte("/F2 18.00 Tf"), []byte("/F4 11.00 Tf"), []byte("1.000 0.000 0.000 rg"), []byte("1.000 1.000 0.000 rg"), []byte("(1.)"), []byte("(2.)"), []byte("(1.)"),
		[]byte("(Gr\xF6\xDFe,"), []byte("(\x80)"), []byte("(Gr\xF6\xDFe,)"), []byte("[1]"), []byte("Kommentare / Comments"), []byte("(pr\xFCfen)"), []byte("(\\(ok\\))"), []byte("(\\(eins\\))"), []byte("(\\\\)"), []byte("(\x95)")} {
		if !bytes.Contains(c, want) {
			t.Fatalf("Inhalt ohne %q", want)
		}
	}
	// durchgestrichen/unterstrichen: je eine Linie mit "l S"
	if bytes.Count(c, []byte(" l S Q")) < 2 {
		t.Fatal("Linien fuer unterstrichen/durchgestrichen fehlen")
	}
	if !bytes.Contains(b, []byte("/Title (Bericht \\(Q3\\))")) {
		t.Fatal("Titel")
	}
	// leeres Dokument: eine Seite
	if p, _, _ := pdfCheck(t, ToPDFRich("leer", nil)); p != 1 {
		t.Fatal("leeres Dokument", p)
	}
}

func TestPDFCells(t *testing.T) {
	cells := map[string]FCell{
		"A1": {"Name", "b hc"}, "B1": {"Betrag", "b"}, "A2": {"Müller", "t"}, "B2": {"=SUM(B3:B4)", "g#ffff00 d2"},
		"B3": {"1234.5", "d2 cu"}, "C1": {"", "g#00ff00"}, "A3": {"lang " + strings.Repeat("x", 80), ""},
	}
	vals := map[string]string{"A1": "Name", "B1": "Betrag", "A2": "Müller", "B2": "1.234,50 €", "B3": "1.234,50 €", "A3": "lang " + strings.Repeat("x", 80)}
	b := ToPDFCells("Tabelle", cells, vals)
	pages, c, media := pdfCheck(t, b)
	if pages != 1 || len(media) != 1 {
		t.Fatal("Seiten", pages)
	}
	if !bytes.Contains(c, []byte("(1.234,50 \x80)")) || bytes.Contains(c, []byte("SUM(")) {
		t.Fatal("es muessen die angezeigten Werte stehen, nicht die Formel")
	}
	for _, want := range [][]byte{[]byte("(A)"), []byte("(B)"), []byte("(Tabelle)"), []byte("(M\xFCller)"), []byte("1.000 1.000 0.000 rg"), []byte("\x85)")} {
		if !bytes.Contains(c, want) {
			t.Fatalf("Inhalt ohne %q", want)
		}
	}
	// ohne vals: gespeicherte Werte (Formeln als Text)
	if _, c2, _ := pdfCheck(t, ToPDFCells("T", cells, nil)); !bytes.Contains(c2, []byte("=SUM\\(B3:B4\\)")) {
		t.Fatal("ohne vals")
	}
	// breite Tabelle: Querformat und mehrere Spaltengruppen
	wide := map[string]FCell{}
	for c := 0; c < 30; c++ {
		wide[ColName(c)+"1"] = FCell{V: strings.Repeat("w", 18)}
		wide[ColName(c)+"2"] = FCell{V: "2"}
	}
	p, _, m := pdfCheck(t, ToPDFCells("breit", wide, nil))
	if p < 2 || m[0] != "841.89x595.28" {
		t.Fatalf("breite Tabelle: %d Seiten, %v", p, m)
	}
	// viele Zeilen: mehrere Seiten
	tall := map[string]FCell{}
	for r := 1; r <= 120; r++ {
		tall["A"+strconv.Itoa(r)] = FCell{V: strconv.Itoa(r)}
	}
	if p, _, _ := pdfCheck(t, ToPDFCells("hoch", tall, nil)); p < 3 {
		t.Fatal("hohe Tabelle", p)
	}
	// leere Tabelle
	if p, _, _ := pdfCheck(t, ToPDFCells("leer", nil, nil)); p != 1 {
		t.Fatal("leere Tabelle")
	}
}
