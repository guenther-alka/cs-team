package ai

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"cs-team/conv"
)

// Dateien und Dokumente, die der Benutzer im Widget ausdrücklich auswählt.
// Der Server holt sie über die normalen Routen mit dem Authorization-Header des Fragenden:
// es gelten exakt die Rechte der Oberfläche (eigene, geteilte, Gruppenordner, Calc/Text-Dokumente).
// Der Inhalt landet als nicht vertrauenswürdiger Text im Prompt und wird nie ausgeführt.

const (
	maxFileBytes  = 2 << 20 // Dateien größer als 2 MB werden nicht gelesen
	maxFileChars  = 40000   // je Datei an die KI
	maxTotalChars = 80000   // alle Dateien zusammen
	maxItems      = 4
)

type fileRef struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type fileText struct {
	Label string // Anzeigename im Prompt
	Text  string
	Cut   bool
}

var errUnsupported = errors.New("file type not supported")

// raw ruft eine Route der Anwendung (beliebige Methode) mit den Zugangsdaten des Fragenden auf.
func (s *Svc) raw(r *http.Request, method, p string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, p, nil)
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.RemoteAddr = r.RemoteAddr
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		req.Header.Set("X-Forwarded-For", f)
	}
	rec := httptest.NewRecorder()
	s.H.ServeHTTP(rec, req)
	return rec
}

func escPath(p string) string { return (&url.URL{Path: p}).EscapedPath() }

var textExt = map[string]bool{".txt": true, ".md": true, ".csv": true, ".tsv": true, ".json": true, ".xml": true, ".log": true,
	".yaml": true, ".yml": true, ".ini": true, ".conf": true, ".cfg": true, ".html": true, ".htm": true, ".css": true, ".js": true,
	".ts": true, ".py": true, ".go": true, ".java": true, ".c": true, ".h": true, ".cpp": true, ".cs": true, ".php": true, ".pl": true,
	".sh": true, ".ps1": true, ".bat": true, ".sql": true, ".rb": true, ".rs": true, ".ics": true, ".vcf": true, ".tex": true, ".rtf": false}

var imgExt = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}

// Supported meldet, ob die Endung ausgewertet werden kann (für die Auswahl im Widget).
func supported(name string) bool {
	e := strings.ToLower(path.Ext(name))
	switch e {
	case ".docx", ".xlsx", ".cscalc", ".cstext":
		return true
	}
	return textExt[e] || imgExt[e] != ""
}

// readFile liefert Text oder (bei Bildern) ein Bild.
func (s *Svc) readFile(r *http.Request, f fileRef, vision bool) (*fileText, *image, error) {
	f.Name = strings.Trim(f.Name, "/")
	if f.Owner == "" || f.Name == "" || len(f.Name) > 300 || len(f.Owner) > 100 || strings.ContainsAny(f.Owner+f.Name, "\x00\r\n") ||
		strings.Contains(f.Owner, "/") || strings.Contains("/"+f.Name+"/", "/../") || strings.Contains("/"+f.Name+"/", "/./") {
		return nil, nil, errors.New("bad file name")
	}
	ext := strings.ToLower(path.Ext(f.Name))
	if !supported(f.Name) {
		return nil, nil, errUnsupported
	}
	p := "/api/files/" + escPath(f.Owner) + "/" + escPath(f.Name)
	// Größe zuerst per HEAD prüfen, damit große Dateien nie im Speicher landen
	h := s.raw(r, http.MethodHead, p)
	if h.Code != 200 {
		return nil, nil, errors.New("file not found or no access: " + f.Name)
	}
	limit := int64(maxFileBytes)
	if imgExt[ext] != "" {
		limit = 4 << 20
	}
	if n := h.Header().Get("Content-Length"); n != "" {
		var sz int64
		fmt.Sscan(n, &sz)
		if sz > limit {
			return nil, nil, fmt.Errorf("file too large (max %d MB): %s", limit>>20, f.Name)
		}
	}
	g := s.raw(r, http.MethodGet, p)
	if g.Code != 200 {
		return nil, nil, errors.New("file not found or no access: " + f.Name)
	}
	b := g.Body.Bytes()
	if int64(len(b)) > limit {
		return nil, nil, fmt.Errorf("file too large (max %d MB): %s", limit>>20, f.Name)
	}
	label := f.Name
	if f.Owner != "" {
		label = f.Owner + "/" + f.Name
	}
	if mt := imgExt[ext]; mt != "" {
		if !vision {
			return nil, nil, errors.New("images are switched off: " + f.Name)
		}
		return nil, &image{mime: mt, b64: base64.StdEncoding.EncodeToString(b)}, nil
	}
	var txt string
	switch ext {
	case ".docx":
		ps, err := conv.FromDOCX(b)
		if err != nil {
			return nil, nil, errors.New("cannot read " + f.Name)
		}
		txt = strings.Join(ps, "\n")
	case ".cstext":
		ps, err := conv.FromCSText(b)
		if err != nil {
			return nil, nil, errors.New("cannot read " + f.Name)
		}
		txt = strings.Join(ps, "\n")
	case ".xlsx":
		c, err := conv.FromXLSX(b)
		if err != nil {
			return nil, nil, errors.New("cannot read " + f.Name)
		}
		txt = string(conv.ToCSV(c))
	case ".cscalc":
		c, err := conv.FromCSCalc(b)
		if err != nil {
			return nil, nil, errors.New("cannot read " + f.Name)
		}
		txt = string(conv.ToCSV(c))
	default:
		if !utf8.Valid(b) || strings.ContainsRune(string(b[:min(len(b), 8192)]), 0) {
			return nil, nil, errUnsupported
		}
		txt = string(b)
	}
	return &fileText{Label: label, Text: txt}, nil, nil
}

// readDoc liest ein Calc-/Text-Dokument (Export als CSV bzw. Text) mit den Rechten des Fragenden.
func (s *Svc) readDoc(r *http.Request, id string, known map[string]docRow) (*fileText, error) {
	d, ok := known[id]
	if !ok {
		return nil, errors.New("document not found or no access")
	}
	format := "txt"
	if d.Type == "sheet" {
		format = "csv"
	}
	g := s.raw(r, http.MethodGet, "/api/docs/"+url.PathEscape(id)+"/export?format="+format)
	if g.Code != 200 {
		return nil, errors.New("document not available: " + d.Name)
	}
	return &fileText{Label: d.Name, Text: g.Body.String()}, nil
}

type docRow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Owner string `json:"owner"`
}

// attachments baut den FILE-Block (Text) und liefert Bilder aus Dateien.
func (s *Svc) attachments(r *http.Request, c Config, fs []fileRef, docs []string) (string, []image, error) {
	if len(fs)+len(docs) == 0 {
		return "", nil, nil
	}
	if c.Files == "no" {
		return "", nil, errors.New("reading files is switched off")
	}
	if len(fs)+len(docs) > maxItems {
		return "", nil, fmt.Errorf("at most %d files or documents", maxItems)
	}
	var items []*fileText
	var imgs []image
	for _, f := range fs {
		t, im, err := s.readFile(r, f, c.Vision != "no")
		if err != nil {
			return "", nil, err
		}
		if im != nil {
			imgs = append(imgs, *im)
		} else {
			items = append(items, t)
		}
	}
	if len(docs) > 0 {
		var list []docRow
		if err := s.get(r, "/api/docs", &list); err != nil {
			return "", nil, errors.New("documents not available")
		}
		known := map[string]docRow{}
		for _, d := range list {
			known[d.ID] = d
		}
		for _, id := range docs {
			t, err := s.readDoc(r, id, known)
			if err != nil {
				return "", nil, err
			}
			items = append(items, t)
		}
	}
	var sb strings.Builder
	left := maxTotalChars
	for _, t := range items {
		txt := strings.ReplaceAll(strings.ReplaceAll(t.Text, "<<<", "<<"), ">>>", ">>")
		txt = strings.ToValidUTF8(strings.ReplaceAll(txt, "\x00", ""), "")
		n := maxFileChars
		if left < n {
			n = left
		}
		rs := []rune(txt)
		note := ""
		if len(rs) > n {
			rs = rs[:max(n, 0)]
			note = "\n[... truncated: the file is longer than shown ...]"
		}
		left -= len(rs)
		fmt.Fprintf(&sb, "\n<<<FILE name=%q\n%s%s\nFILE>>>", clean(t.Label, 200), string(rs), note)
	}
	return sb.String(), imgs, nil
}

// sources: Auswahlliste fürs Widget - nur Dateien, die ausgewertet werden können (Rechte wie in der Oberfläche).
func (s *Svc) sources(w http.ResponseWriter, r *http.Request) {
	var fl struct {
		Own    []fileRow `json:"own"`
		Shared []fileRow `json:"shared"`
	}
	out := struct {
		Files []fileRow `json:"files"`
		Docs  []docRow  `json:"docs"`
	}{[]fileRow{}, []docRow{}}
	if s.get(r, "/api/files", &fl) == nil {
		for _, f := range append(fl.Own, fl.Shared...) {
			if supported(f.Name) && f.Size <= 4<<20 && len(out.Files) < 300 {
				out.Files = append(out.Files, f)
			}
		}
	}
	var dl []docRow
	if s.get(r, "/api/docs", &dl) == nil {
		for _, d := range dl {
			if len(out.Docs) < 300 {
				out.Docs = append(out.Docs, d)
			}
		}
	}
	json.NewEncoder(w).Encode(out)
}

type fileRow struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
	Size  int64  `json:"size"`
}
