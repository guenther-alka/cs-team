// Package office verbindet Calc/Text-Dokumente mit der Dateiablage (Export nach Files, Import aus Files)
// und liefert den Download in Standardformaten. Die Umwandlung selbst steckt in package conv.
//
//	Text: .txt .rtf .docx .cstext        Calc: .csv .xlsx .cscalc
//	Import: .txt .docx .cstext           Import: .csv .xlsx .cscalc
package office

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cs-team/auth"
	"cs-team/conv"
	"cs-team/doc"
	"cs-team/files"
	"cs-team/store"
)

const maxImport = 32 << 20

type Svc struct {
	Hub   *doc.Hub
	Files *files.Svc
}

func (s *Svc) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	f := func(fn http.HandlerFunc) http.Handler { return wrap(fn) }
	mux.Handle("GET /api/docs/{id}/export", f(s.export))
	mux.Handle("POST /api/docs/{id}/tofiles", f(s.toFiles))
	mux.Handle("POST /api/docs/{id}/export", f(s.export)) // Calc als PDF: der Browser schickt die angezeigten Werte (siehe pdfVals)
	mux.Handle("POST /api/docs/import", f(s.importDoc))
	mux.Handle("POST /api/docs/merge", f(s.merge))       // Quellen zusammenfuegen -> cs-text oder PDF (compose.go)
	mux.Handle("POST /api/docs/fromtext", f(s.fromText)) // Text (z. B. KI-Zusammenfassung) -> cs-text oder PDF
}

// render: Standardformat je Dokumenttyp, wenn format leer ist (csv / txt).
func render(m doc.Meta, d *doc.Doc, format string, vals map[string]string) (data []byte, ext string, err error) {
	if m.Type == "sheet" {
		switch strings.ToLower(format) {
		case "", "csv":
			return conv.ToCSV(d.Cells()), "csv", nil
		case "xlsx":
			data, err = conv.ToXLSXRich(richCells(d))
			return data, "xlsx", err
		case "pdf":
			return conv.ToPDFCells(m.Name, richCells(d), vals), "pdf", nil
		case "cscalc":
			data, err = conv.ToCSCalcRich(richCells(d))
			return data, "cscalc", err
		}
		return nil, "", conv.ErrFormat
	}
	switch strings.ToLower(format) {
	case "", "txt":
		return conv.ToTXT(d.Paragraphs()), "txt", nil
	case "rtf":
		return conv.ToRTF(d.Paragraphs()), "rtf", nil
	case "docx":
		data, err = conv.ToDOCXRich(richParas(d))
		return data, "docx", err
	case "pdf":
		return conv.ToPDFRich(m.Name, richParas(d)), "pdf", nil
	case "cstext":
		data, err = conv.ToCSTextRich(richParas(d))
		return data, "cstext", err
	}
	return nil, "", conv.ErrFormat
}

// richParas / richCells: Absaetze und Zellen mit Formaten (Absatzformat, Zeichenformate, Zellformat inkl. Kommentar/Notiz).
func richParas(d *doc.Doc) []conv.Para {
	l := d.ParagraphItems()
	out := make([]conv.Para, len(l))
	for i, it := range l {
		out[i] = conv.Para{V: it.V, F: it.F, R: it.R}
	}
	return out
}

func richCells(d *doc.Doc) map[string]conv.FCell {
	out := map[string]conv.FCell{}
	for k, it := range d.CellItems() {
		out[k] = conv.FCell{V: it.V, F: it.F}
	}
	return out
}
func ctype(ext string) string {
	switch ext {
	case "csv":
		return "text/csv; charset=utf-8"
	case "pdf":
		return "application/pdf"
	case "txt":
		return "text/plain; charset=utf-8"
	case "rtf":
		return "application/rtf"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return "application/octet-stream"
}

func fileName(name, ext string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			r = '_'
		}
		b.WriteRune(r)
	}
	n := b.String()
	if len(n) > 100 {
		n = n[:100]
	}
	if n == "" || n == "." || n == ".." {
		n = "dokument"
	}
	return n + "." + ext
}

func (s *Svc) snapshot(w http.ResponseWriter, r *http.Request) (doc.Meta, []byte, string, bool) {
	m, d, err := s.Hub.Snapshot(r.Context(), auth.User(r.Context()), r.PathValue("id"))
	if err != nil || !auth.Can(r.Context(), doc.Area(m.Type)) {
		http.Error(w, "not found", http.StatusNotFound)
		return m, nil, "", false
	}
	data, ext, err := render(m, d, r.URL.Query().Get("format"), pdfVals(w, r))
	if err != nil {
		http.Error(w, "unsupported format", http.StatusBadRequest)
		return m, nil, "", false
	}
	return m, data, ext, true
}

func (s *Svc) export(w http.ResponseWriter, r *http.Request) {
	m, data, ext, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", ctype(ext))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName(m.Name, ext)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

// POST /api/docs/<id>/tofiles?format=docx  -> legt <Name>.<ext> in der Ablage des Benutzers an (ersetzt gleichnamige).
func (s *Svc) toFiles(w http.ResponseWriter, r *http.Request) {
	if !auth.Can(r.Context(), "files") {
		http.Error(w, "no permission for files", http.StatusForbidden)
		return
	}
	m, data, ext, ok := s.snapshot(w, r)
	if !ok {
		return
	}
	me := auth.User(r.Context())
	name := fileName(m.Name, ext)
	if _, err := s.Files.Put(r.Context(), me, me, name, bytes.NewReader(data), int64(len(data))); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, files.ErrTooLarge) {
			code = http.StatusRequestEntityTooLarge
		} else if errors.Is(err, files.ErrDenied) || errors.Is(err, files.ErrBadName) {
			code = http.StatusBadRequest
		}
		http.Error(w, err.Error(), code)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"name": name})
}

// items: Startinhalt für ein neues Dokument (Zeitstempel = jetzt, damit LWW sauber startet). Formate aus fremden Dateien werden
// gegen dieselbe Whitelist geprüft wie Eingaben im Editor; ungültige Formate entfallen, der Text bleibt.
func items(user string, paras []conv.Para, cells map[string]conv.FCell) map[string]doc.Item {
	now := time.Now().UnixNano()
	out := map[string]doc.Item{}
	for i, p := range paras {
		it := doc.Item{V: p.V, Pos: float64(i + 1), TS: now + int64(i), By: user}
		if doc.ValidFmt(p.F) {
			it.F = p.F
		}
		if doc.ValidRuns(p.R) {
			it.R = p.R
		}
		out[fmt.Sprintf("p%05d", i+1)] = it
	}
	for k, c := range cells {
		it := doc.Item{V: c.V, TS: now, By: user}
		if doc.ValidFmt(c.F) {
			it.F = c.F
		}
		out[k] = it
	}
	return out
}
func parseImport(ext string, b []byte) (typ string, paras []conv.Para, cells map[string]conv.FCell, err error) {
	plain := func(p []string) []conv.Para {
		out := make([]conv.Para, len(p))
		for i, v := range p {
			out[i] = conv.Para{V: v}
		}
		return out
	}
	switch ext {
	case ".txt":
		typ = "text"
		var p []string
		p, err = conv.FromTXT(b)
		paras = plain(p)
	case ".docx":
		typ = "text"
		paras, err = conv.FromDOCXRich(b)
	case ".cstext":
		typ = "text"
		paras, err = conv.FromCSTextRich(b)
	case ".csv":
		typ = "sheet"
		var m map[string]string
		m, err = conv.FromCSV(b)
		cells = map[string]conv.FCell{}
		for k, v := range m {
			cells[k] = conv.FCell{V: v}
		}
	case ".xlsx":
		typ = "sheet"
		cells, err = conv.FromXLSXRich(b)
	case ".cscalc":
		typ = "sheet"
		cells, err = conv.FromCSCalcRich(b)
	default:
		err = conv.ErrFormat
	}
	return
}
// POST /api/docs/import {"owner":"anna","file":"bericht.docx","name":"optional"}
// Quelle: eine Datei aus der Ablage (eigene oder mit dem Benutzer geteilte). Ergebnis: neues Dokument im Besitz des Benutzers.
func (s *Svc) importDoc(w http.ResponseWriter, r *http.Request) {
	var in struct{ Owner, File, Name string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || in.File == "" {
		http.Error(w, "owner/file required", http.StatusBadRequest)
		return
	}
	if !auth.Can(r.Context(), "files") {
		http.Error(w, "no permission for files", http.StatusForbidden)
		return
	}
	me := auth.User(r.Context())
	if in.Owner == "" {
		in.Owner = me
	}
	ext := strings.ToLower(filepath.Ext(in.File))
	m, rc, err := s.Files.Open(r.Context(), me, in.Owner, in.File)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer rc.Close()
	if m.Size > maxImport {
		http.Error(w, "file too large for import", http.StatusRequestEntityTooLarge)
		return
	}
	b, err := io.ReadAll(io.LimitReader(rc, maxImport+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	typ, paras, cells, err := parseImport(ext, b)
	if err != nil {
		http.Error(w, "cannot read "+ext+": "+err.Error(), http.StatusBadRequest)
		return
	}
	if !auth.Can(r.Context(), doc.Area(typ)) {
		http.Error(w, "no permission for "+doc.Area(typ), http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.TrimSuffix(in.File, filepath.Ext(in.File))
	}
	if len(name) > 100 {
		name = name[:100]
	}
	id, err := s.Hub.Create(r.Context(), me, name, typ, items(me, paras, cells))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"id": id, "type": typ})
}
