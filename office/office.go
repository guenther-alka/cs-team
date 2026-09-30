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
	mux.Handle("POST /api/docs/import", f(s.importDoc))
}

// render: Standardformat je Dokumenttyp, wenn format leer ist (csv / txt).
func render(m doc.Meta, d *doc.Doc, format string) (data []byte, ext string, err error) {
	if m.Type == "sheet" {
		cells := d.Cells()
		switch strings.ToLower(format) {
		case "", "csv":
			return conv.ToCSV(cells), "csv", nil
		case "xlsx":
			data, err = conv.ToXLSX(cells)
			return data, "xlsx", err
		case "cscalc":
			data, err = conv.ToCSCalc(cells)
			return data, "cscalc", err
		}
		return nil, "", conv.ErrFormat
	}
	p := d.Paragraphs()
	switch strings.ToLower(format) {
	case "", "txt":
		return conv.ToTXT(p), "txt", nil
	case "rtf":
		return conv.ToRTF(p), "rtf", nil
	case "docx":
		data, err = conv.ToDOCX(p)
		return data, "docx", err
	case "cstext":
		data, err = conv.ToCSText(p)
		return data, "cstext", err
	}
	return nil, "", conv.ErrFormat
}

func ctype(ext string) string {
	switch ext {
	case "csv":
		return "text/csv; charset=utf-8"
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
	data, ext, err := render(m, d, r.URL.Query().Get("format"))
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

// items: Startinhalt für ein neues Dokument (Zeitstempel = jetzt, damit LWW sauber startet).
func items(user string, kv []string, cells map[string]string) map[string]doc.Item {
	now := time.Now().UnixNano()
	out := map[string]doc.Item{}
	for i, v := range kv {
		out[fmt.Sprintf("p%05d", i+1)] = doc.Item{V: v, Pos: float64(i + 1), TS: now + int64(i), By: user}
	}
	for k, v := range cells {
		out[k] = doc.Item{V: v, TS: now, By: user}
	}
	return out
}

func parseImport(ext string, b []byte) (typ string, paras []string, cells map[string]string, err error) {
	switch ext {
	case ".txt":
		typ = "text"
		paras, err = conv.FromTXT(b)
	case ".docx":
		typ = "text"
		paras, err = conv.FromDOCX(b)
	case ".cstext":
		typ = "text"
		paras, err = conv.FromCSText(b)
	case ".csv":
		typ = "sheet"
		cells, err = conv.FromCSV(b)
	case ".xlsx":
		typ = "sheet"
		cells, err = conv.FromXLSX(b)
	case ".cscalc":
		typ = "sheet"
		cells, err = conv.FromCSCalc(b)
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
