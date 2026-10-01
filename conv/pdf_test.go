package conv

import (
	"bytes"
	"compress/zlib"
	"errors"
	"os"
	"strings"
	"testing"
)

func readPDF(t *testing.T, n string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + n)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPDFText(t *testing.T) {
	cases := map[string][]string{
		"rl_std.pdf":        {"Elternbrief: Schulfest am Samstag, 14 Uhr", "äöü ÄÖÜ ß", "€", "„Anführungszeichen“", "1.234,56 EUR", "\n\nZweite Seite"},
		"rl_ttf.pdf":        {"Grüße aus Köln, Preis 42 EUR", "Zeile zwei mit Ärger"},
		"chrome_objstm.pdf": {"Protokoll Sitzung", "Müller, Schmidt und Öztürk", "1.200 €", "Büro 300", "final flow"}, // Type0, ToUnicode, Objektströme
	}
	for n, want := range cases {
		s, err := FromPDF(readPDF(t, n))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		for _, w := range want {
			if !strings.Contains(s, w) {
				t.Errorf("%s: %q fehlt in:\n%s", n, w, s)
			}
		}
	}
}

func TestPDFEncryptedAndBad(t *testing.T) {
	if _, err := FromPDF(readPDF(t, "enc.pdf")); !errors.Is(err, ErrPDFEncrypted) {
		t.Fatalf("verschlüsselt: %v", err)
	}
	for _, b := range [][]byte{nil, []byte("hallo"), []byte("%PDF-1.4\nnonsense"), []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")} {
		if _, err := FromPDF(b); err == nil {
			t.Errorf("Fehler erwartet für %q", b)
		}
	}
}

// Beschädigte und abgeschnittene Dateien dürfen nie abstürzen oder hängen.
func TestPDFRobust(t *testing.T) {
	b := readPDF(t, "chrome_objstm.pdf")
	for i := 0; i < len(b); i += 37 {
		FromPDF(b[:i])
	}
	c := append([]byte(nil), b...)
	for i := 100; i < len(c); i += 53 {
		c[i] ^= 0x5a
		FromPDF(c)
	}
}

// Ein Flate-Stream, der riesig auspackt, wird abgelehnt (Zip-Bombe), ohne den Speicher zu füllen.
func TestPDFBomb(t *testing.T) {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write(make([]byte, 40<<20))
	w.Close()
	var f bytes.Buffer
	f.WriteString("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n3 0 obj\n<< /Type /Page /Contents 4 0 R >>\nendobj\n4 0 obj\n<< /Filter /FlateDecode /Length ")
	f.WriteString(itoa(z.Len()) + " >>\nstream\n")
	f.Write(z.Bytes())
	f.WriteString("\nendstream\nendobj\n")
	if _, err := FromPDF(f.Bytes()); err == nil {
		t.Fatal("Zip-Bombe angenommen")
	}
}

func itoa(n int) string {
	var s []byte
	for ; n > 0; n /= 10 {
		s = append([]byte{byte('0' + n%10)}, s...)
	}
	return string(s)
}

// Handgeschriebenes PDF: unkomprimiert, Standardschrift, Oktalcodes, TJ mit Lücken, Zeilenumbruch per Td.
func TestPDFHandmade(t *testing.T) {
	c := "BT /F1 12 Tf 72 700 Td (Hallo \\344\\366\\374 \\(Klammer\\)) Tj 0 -14 Td [(Wo)-300(rt)] TJ ET"
	pdf := "%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>\nendobj\n" +
		"4 0 obj\n<< /Length " + itoa(len(c)) + " >>\nstream\n" + c + "\nendstream\nendobj\n5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n"
	s, err := FromPDF([]byte(pdf))
	if err != nil {
		t.Fatal(err)
	}
	if s != "Hallo äöü (Klammer)\nWo rt" {
		t.Fatalf("got %q", s)
	}
}
