// Package conv: Ein-/Ausgabeformate für Text (Absätze) und Calc (Zellen), nur Go-Standardbibliothek.
//
// Text:  .txt  .rtf (nur Export)  .docx  .cstext (JSON, eigenes Format)
// Calc:  .csv  .xlsx  .cscalc (JSON, eigenes Format)
// Formatierung (Schrift, Fett, Zellformate) wird weder gelesen noch geschrieben - nur Text bzw. Werte/Formeln.
package conv

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	MaxParas = 20000
	MaxCells = 200000
	maxZip   = 64 << 20 // entpackte Größe je Teil
)

var ErrFormat = errors.New("unsupported or damaged file")

func xmlEsc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return strings.NewReplacer("\x00", "", "\x0b", "", "\x0c", "").Replace(b.String())
}

// ---------- .txt ----------

func ToTXT(p []string) []byte { return []byte(strings.Join(p, "\n")) }

func FromTXT(b []byte) ([]string, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(b) { // Latin-1 (Windows-Editor ohne UTF-8)
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		b = []byte(string(r))
	}
	s := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return []string{""}, nil
	}
	p := strings.Split(s, "\n")
	if len(p) > MaxParas {
		return nil, ErrFormat
	}
	return p, nil
}

// ---------- .rtf (Export) ----------

func rtfEsc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\\' || r == '{' || r == '}':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\tab `)
		case r < 0x20:
		case r < 0x80:
			b.WriteRune(r)
		case r <= 0xFFFF:
			fmt.Fprintf(&b, `\u%d?`, int16(r))
		default: // Ersatzpaar
			r -= 0x10000
			fmt.Fprintf(&b, `\u%d?\u%d?`, int16(0xD800+(r>>10)), int16(0xDC00+(r&0x3FF)))
		}
	}
	return b.String()
}

func ToRTF(p []string) []byte {
	var b strings.Builder
	b.WriteString(`{\rtf1\ansi\ansicpg1252\deff0{\fonttbl{\f0\fswiss Calibri;}}\f0\fs22 ` + "\n")
	for _, s := range p {
		b.WriteString(rtfEsc(s) + `\par` + "\n")
	}
	b.WriteString("}")
	return []byte(b.String())
}

// ---------- .docx ----------

const ctypesDocx = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`

const relsDocx = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

func zipParts(parts [][2]string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := zw.Create(p[0])
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(p[1])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ToDOCX(p []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, s := range p {
		b.WriteString(`<w:p><w:r><w:t xml:space="preserve">` + xmlEsc(s) + `</w:t></w:r></w:p>`)
	}
	b.WriteString(`</w:body></w:document>`)
	return zipParts([][2]string{{"[Content_Types].xml", ctypesDocx}, {"_rels/.rels", relsDocx}, {"word/document.xml", b.String()}})
}

func readPart(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			b, err := io.ReadAll(io.LimitReader(rc, maxZip+1))
			if err != nil || len(b) > maxZip {
				return nil, ErrFormat
			}
			return b, nil
		}
	}
	return nil, ErrFormat
}

func openZip(b []byte) (*zip.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, ErrFormat
	}
	return zr, nil
}

// FromDOCX liest den reinen Text der Absätze aus word/document.xml. Eine Tabellenzeile wird ein Absatz (Zellen durch Tab getrennt),
// Alternativinhalt (mc:Fallback, doppelt vorhandener Text in Textfeldern) wird übersprungen. Keine Formate.
func FromDOCX(b []byte) ([]string, error) {
	zr, err := openZip(b)
	if err != nil {
		return nil, err
	}
	doc, err := readPart(zr, "word/document.xml")
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var out []string
	var cur strings.Builder
	inP, inT := false, false
	tbl := 0                   // Schachtelungstiefe der Tabellen; nur die äußerste wird zu Zeilen
	var row, cell []string     // Zellen der Zeile, Absätze der Zelle
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
			case "t":
				inT = true
			case "tab":
				cur.WriteByte('\t')
			case "br", "cr":
				cur.WriteByte(' ')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "p":
				if inP {
					inP = false
					if tbl > 0 {
						if s := strings.TrimSpace(cur.String()); s != "" {
							cell = append(cell, s)
						}
						break
					}
					out = append(out, cur.String())
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
					out = append(out, strings.Join(row, "\t"))
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
				cur.Write(t)
			}
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out, nil
}

// ---------- .cstext (JSON) ----------

type cstext struct {
	Format     string   `json:"format"`
	Version    int      `json:"version"`
	Paragraphs []string `json:"paragraphs"`
}

func ToCSText(p []string) ([]byte, error) {
	return json.MarshalIndent(cstext{"cs-team/text", 1, p}, "", " ")
}

func FromCSText(b []byte) ([]string, error) {
	var c cstext
	if json.Unmarshal(b, &c) != nil || c.Format != "cs-team/text" || len(c.Paragraphs) > MaxParas {
		return nil, ErrFormat
	}
	if len(c.Paragraphs) == 0 {
		c.Paragraphs = []string{""}
	}
	return c.Paragraphs, nil
}
