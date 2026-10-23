package share

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"cs-team/auth"
	"cs-team/files"
)

func who(r *http.Request) Who {
	u := auth.User(r.Context())
	return Who{User: u, Groups: auth.GroupsOf(u), Admin: auth.IsAdmin(r.Context()), Guest: auth.IsGuest(u)}
}

func status(err error) int {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrNotFile), errors.Is(err, files.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, files.ErrDenied):
		return http.StatusForbidden
	case errors.Is(err, files.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, files.ErrQuota):
		return http.StatusInsufficientStorage
	case errors.Is(err, files.ErrBadName), errors.Is(err, files.ErrExists):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func fail(w http.ResponseWriter, err error) { http.Error(w, err.Error(), status(err)) }

// Routes: nur lesende API (mit Login, Bereich "files") plus der Import in den eigenen Bereich. Es gibt keine Schreib-API fuer share.
func (s *Svc) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	f := func(fn http.HandlerFunc) http.Handler { return wrap(auth.Need("files", fn)) }
	mux.Handle("GET /api/share", f(s.list))        // ?path=<ordner>; {"enabled":false} wenn es share nicht gibt
	mux.Handle("GET /api/share/file", f(s.file))   // ?path=<datei>[&dl=1]
	mux.Handle("POST /api/share/import", f(s.imp)) // ?path=<datei> -> {"name":...} (Kopie in Meine Dateien)
}

func (s *Svc) list(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Enabled bool    `json:"enabled"`
		Path    string  `json:"path"`
		Entries []Entry `json:"entries"`
	}{s.Enabled(), r.URL.Query().Get("path"), []Entry{}}
	if out.Enabled {
		es, err := s.List(who(r), out.Path)
		if err != nil {
			fail(w, err)
			return
		}
		out.Entries = es
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

func (s *Svc) file(w http.ResponseWriter, r *http.Request) {
	f, fi, err := s.Open(who(r), r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(fi.Name())))
	if ct == "" {
		ct = "application/octet-stream"
	}
	disp := "attachment" // nur harmlose Typen im Browser anzeigen (HTML/SVG/JS nie aus unserem Origin)
	if files.InlineOK(ct) && r.URL.Query().Get("dl") != "1" {
		disp = "inline"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": fi.Name()}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func (s *Svc) imp(w http.ResponseWriter, r *http.Request) {
	name, err := s.Import(r.Context(), who(r), r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"name": name})
}
