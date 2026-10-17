package conv

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var cellRe = regexp.MustCompile(`^([A-Z]{1,2})([0-9]{1,5})$`) // höchstens ZZ (702 Spalten)

// ColRow zerlegt "AB12" in (Spalte 0-basiert, Zeile 1-basiert).
func ColRow(k string) (col, row int, ok bool) {
	m := cellRe.FindStringSubmatch(k)
	if m == nil {
		return 0, 0, false
	}
	for _, c := range m[1] {
		col = col*26 + int(c-'A') + 1
	}
	row, _ = strconv.Atoi(m[2])
	return col - 1, row, row >= 1
}

func ColName(c int) string {
	s := ""
	for c++; c > 0; c = (c - 1) / 26 {
		s = string(rune('A'+(c-1)%26)) + s
	}
	return s
}

func Key(col, row int) string { return ColName(col) + strconv.Itoa(row) }

type pos struct{ col, row int }

func sorted(cells map[string]string) (keys []string, ps map[string]pos, maxCol, maxRow int) {
	ps = map[string]pos{}
	for k, v := range cells {
		if v == "" {
			continue
		}
		c, r, ok := ColRow(k)
		if !ok {
			continue
		}
		ps[k] = pos{c, r}
		keys = append(keys, k)
		maxCol, maxRow = max(maxCol, c+1), max(maxRow, r)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := ps[keys[i]], ps[keys[j]]
		if a.row != b.row {
			return a.row < b.row
		}
		return a.col < b.col
	})
	return
}

// ---------- .csv ----------

// ToCSV schreibt zeilenweise (nie ein ganzes Gitter im Speicher): Zellen weit auseinander kosten nur leere Felder.
func ToCSV(cells map[string]string) []byte {
	_, ps, maxCol, maxRow := sorted(cells)
	rows := map[int][]string{} // Zeile -> Schlüssel
	for k, p := range ps {
		rows[p.row] = append(rows[p.row], k)
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	rec := make([]string, maxCol)
	for r := 1; r <= maxRow; r++ {
		for i := range rec {
			rec[i] = ""
		}
		for _, k := range rows[r] {
			rec[ps[k].col] = csvSafe(cells[k])
		}
		w.Write(rec)
	}
	w.Flush()
	return buf.Bytes()
}

// csvSafe: Zellen, die in Excel/LibreOffice als Formel gelesen würden (=, +, -, @, Tab, CR am Anfang; Zahlen
// wie -5 ausgenommen), bekommen ein ' vorangestellt (Formel-Injektion, Audit F7). FromCSV nimmt es wieder weg.
func csvSafe(v string) string {
	if v == "" || isNumber(v) {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}

// csvUnsafe: Gegenstück zu csvSafe beim Import.
func csvUnsafe(v string) string {
	if len(v) > 1 && v[0] == '\'' && strings.ContainsRune("=+-@\t\r", rune(v[1])) {
		return v[1:]
	}
	return v
}

// FromCSV erkennt Komma oder Semikolon (deutsches Excel) und Latin-1.
func FromCSV(b []byte) (map[string]string, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	p, err := FromTXT(b)
	if err != nil {
		return nil, err
	}
	text := strings.Join(p, "\n")
	comma := ','
	first := p[0]
	if strings.Count(first, ";") > strings.Count(first, ",") {
		comma = ';'
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = comma
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	out := map[string]string{}
	for row := 1; ; row++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrFormat
		}
		for c, v := range rec {
			if v != "" && c < 702 && row <= 99999 {
				out[Key(c, row)] = csvUnsafe(v)
			}
		}
		if len(out) > MaxCells {
			return nil, ErrFormat
		}
	}
	return out, nil
}

// ---------- .xlsx ----------

const ctypesXlsx = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`

const relsXlsx = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`

const wbXlsx = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Tabelle1" sheetId="1" r:id="rId1"/></sheets><calcPr fullCalcOnLoad="1"/></workbook>`

const wbRelsXlsx = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`

var numRe = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)

// isNumber: nur echte Dezimalzahlen (führende Nullen, Hex, NaN, "+5" bleiben Text).
func isNumber(s string) bool { return numRe.MatchString(s) }

func sharedStrings(b []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	var out []string
	var cur strings.Builder
	inSI, inT := false, false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "si":
				if inSI {
					out = append(out, cur.String())
					inSI = false
				}
			}
		case xml.CharData:
			if inSI && inT {
				cur.Write(t)
			}
		}
	}
	return out
}

// dateStyles: je Zellformat (Position in cellXfs von xl/styles.xml) ob es ein Datum/eine Uhrzeit ist. Excel speichert Daten als
// Zahl (Tage seit 1900); ohne diese Auswertung kaeme "45000" statt "2023-03-15" an (0.60.0).
func dateStyles(zr *zip.Reader) []bool {
	b, err := readPart(zr, "xl/styles.xml")
	if err != nil {
		return nil
	}
	custom := map[string]bool{}
	var out []bool
	inXfs := false
	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "numFmt":
				id, code := "", ""
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "numFmtId":
						id = a.Value
					case "formatCode":
						code = a.Value
					}
				}
				custom[id] = isDateFormat(code)
			case "cellXfs":
				inXfs = true
			case "xf":
				if !inXfs {
					break
				}
				d := false
				for _, a := range t.Attr {
					if a.Name.Local == "numFmtId" {
						n, _ := strconv.Atoi(a.Value)
						d = custom[a.Value] || (n >= 14 && n <= 22) || (n >= 45 && n <= 47)
					}
				}
				out = append(out, d)
			}
		case xml.EndElement:
			if t.Name.Local == "cellXfs" {
				inXfs = false
			}
		}
	}
	return out
}

var fmtSkip = regexp.MustCompile(`"[^"]*"|\\.|\[[^\]]*\]`)

// isDateFormat: Formatcode mit Tag/Monat/Jahr/Stunde/Sekunde ausserhalb von Text in Anfuehrungszeichen und [Klammern].
func isDateFormat(code string) bool {
	return strings.ContainsAny(strings.ToLower(fmtSkip.ReplaceAllString(code, "")), "ymdhs")
}

// xlDate wandelt eine Excel-Tageszahl in "2006-01-02", "2006-01-02 15:04[:05]" bzw. "15:04:05" (nur Uhrzeit); unplausible Werte bleiben.
func xlDate(val string) string {
	f, err := strconv.ParseFloat(val, 64)
	if err != nil || f < 0 || f >= 2958466 {
		return val
	}
	days := math.Floor(f)
	secs := int(math.Round((f - days) * 86400))
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC) // ab 1.3.1900; davor fehlt der Schalttag, den Excel faelschlich kennt
	if days < 60 {
		base = base.AddDate(0, 0, 1)
	}
	t := base.AddDate(0, 0, int(days)).Add(time.Duration(secs) * time.Second)
	switch {
	case days == 0:
		return t.Format("15:04:05")
	case secs == 0:
		return t.Format("2006-01-02")
	case secs%60 == 0:
		return t.Format("2006-01-02 15:04")
	}
	return t.Format("2006-01-02 15:04:05")
}

// ---------- .cscalc (JSON) ----------

// xlFormula übersetzt eine Calc-Formel (deutsche oder englische Funktionsnamen, ; oder , als Trenner) in die
// Schreibweise der xlsx-Datei (englische Namen, Komma). Text in Anführungszeichen bleibt unverändert.
var deFn = map[string]string{"SUMME": "SUM", "MITTELWERT": "AVERAGE", "ANZAHL2": "COUNTA", "ANZAHL": "COUNT", "WENN": "IF", "RUNDEN": "ROUND",
	"WURZEL": "SQRT", "PRODUKT": "PRODUCT", "UND": "AND", "ODER": "OR", "NICHT": "NOT", "BETRAG": "ABS"}

var (
	reXlStr  = regexp.MustCompile(`"(?:[^"]|"")*"`)
	reXlFn   = regexp.MustCompile(`([A-Za-zÄÖÜäöüß_][A-Za-z0-9ÄÖÜäöüß_.]*)\(`)
	reXlBool = regexp.MustCompile(`\b(WAHR|FALSCH)\b`)
)

// safeFormula: nur Formeln aus den Funktionen des Calc (Whitelist) mit Zellbezügen, Zahlen, Text und Operatoren werden als
// echte Formeln in die xlsx geschrieben. Alles andere (HYPERLINK, WEBSERVICE, DDE "|", externe Bezüge "[...]"/"!") bleibt Text.
var xlAllowed = map[string]bool{"SUM": true, "AVERAGE": true, "MIN": true, "MAX": true, "COUNT": true, "COUNTA": true, "PRODUCT": true,
	"ROUND": true, "ABS": true, "SQRT": true, "AND": true, "OR": true, "NOT": true, "IF": true}

var reXlPlain = regexp.MustCompile(`^[A-Za-z0-9_.$(),:;+\-*/^%&<>= \t]*$`)

func safeFormula(f string) bool {
	rest, last := strings.Builder{}, 0
	for _, loc := range reXlStr.FindAllStringIndex(f, -1) {
		rest.WriteString(f[last:loc[0]] + " ")
		last = loc[1]
	}
	rest.WriteString(f[last:])
	p := rest.String()
	if strings.Contains(p, `"`) || !reXlPlain.MatchString(p) {
		return false
	}
	for _, m := range reXlFn.FindAllStringSubmatch(p, -1) {
		if !xlAllowed[strings.ToUpper(m[1])] {
			return false
		}
	}
	return true
}

func xlFormula(f string) string {
	conv := func(p string) string {
		p = strings.ReplaceAll(p, ";", ",")
		p = reXlFn.ReplaceAllStringFunc(p, func(m string) string {
			name := strings.ToUpper(m[:len(m)-1])
			if en, ok := deFn[name]; ok {
				return en + "("
			}
			return name + "("
		})
		return reXlBool.ReplaceAllStringFunc(strings.ToUpper(p), func(m string) string {
			if m == "WAHR" {
				return "TRUE"
			}
			return "FALSE"
		})
	}
	var b strings.Builder
	last := 0
	for _, loc := range reXlStr.FindAllStringIndex(f, -1) {
		b.WriteString(conv(f[last:loc[0]]))
		b.WriteString(f[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(conv(f[last:]))
	return b.String()
}
