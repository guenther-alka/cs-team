package files

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"cs-team/auth"
)

func status(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrDenied):
		return http.StatusForbidden
	case errors.Is(err, ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrBadName), errors.Is(err, ErrExists):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func fail(w http.ResponseWriter, err error) { http.Error(w, err.Error(), status(err)) }

type row struct {
	Name  string   `json:"name"`
	Owner string   `json:"owner"`
	Size  int64    `json:"size"`
	Type  string   `json:"type"`
	Mod   int64    `json:"mod"`
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
	Pub   string   `json:"pub,omitempty"` // öffentlicher Link (nur für den Owner sichtbar)
	RW    bool     `json:"rw"`
	View  bool     `json:"view"` // im Browser anzeigbar
}

func toRow(m Meta, user string) row {
	_, w := m.Level(user)
	r := row{m.Name, m.Owner, m.Size, m.Type, m.Mod, nil, nil, "", w, InlineOK(m.Type)}
	if _, isG := GroupOf(m.Owner); m.Owner == user || (isG && w) {
		if !isG {
			r.Read, r.Write = m.Read, m.Write
		}
		if m.Token != "" {
			r.Pub = "/pub/" + m.Token
		}
	}
	return r
}

// Routes: JSON-API (mit Login), WebDAV (mit Login) und öffentliche Links (ohne Login).
func (s *Svc) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	f := func(fn http.HandlerFunc) http.Handler { return wrap(auth.Need("files", fn)) }
	mux.Handle("GET /api/files", f(s.list))
	mux.Handle("POST /api/files", f(s.upload))
	mux.Handle("GET /api/files/{owner}/{name...}", f(s.download))
	mux.Handle("DELETE /api/files/{owner}/{name...}", f(s.remove)) // ?dir=1 löscht einen Ordner samt Inhalt
	mux.Handle("POST /api/filesshare/{owner}/{name...}", f(s.share))
	mux.Handle("POST /api/filesdir", f(s.mkdir)) // ?owner=&name=<ordnerpfad>
	mux.Handle("POST /api/filesmove", f(s.move)) // ?owner=&from=&to=[&dir=1][&copy=1]
	mux.Handle("GET /pub/{token}", http.HandlerFunc(s.public))
	mux.Handle("/webdav/", wrap(auth.Need("files", s.WebDAV())))
	mux.Handle("/webdav", wrap(http.RedirectHandler("/webdav/", http.StatusMovedPermanently)))
}

func (s *Svc) list(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	own, shared, err := s.List(r.Context(), me)
	if err != nil {
		fail(w, err)
		return
	}
	out := struct {
		Own     []row             `json:"own"`
		Shared  []row             `json:"shared"`
		Folders []auth.FolderInfo `json:"folders"` // Gruppenordner, auf die ich zugreifen darf
		MaxMB   int64             `json:"maxMB"`
	}{[]row{}, []row{}, auth.FolderGroups(me), s.Max >> 20}
	for _, m := range own {
		out.Own = append(out.Own, toRow(m, me))
	}
	for _, m := range shared {
		out.Shared = append(out.Shared, toRow(m, me))
	}
	json.NewEncoder(w).Encode(out)
}

// POST /api/files?name=x  (Body = Dateiinhalt). Gleicher Name ersetzt die Datei.
func (s *Svc) upload(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	body := http.MaxBytesReader(w, r.Body, s.Max+1)
	owner := me
	if o := r.URL.Query().Get("owner"); o != "" { // Upload in einen Gruppenordner: ?owner=@gruppe
		if _, ok := GroupOf(o); !ok {
			http.Error(w, "bad owner", http.StatusBadRequest)
			return
		}
		owner = o
	}
	m, err := s.Put(r.Context(), me, owner, r.URL.Query().Get("name"), body, r.ContentLength)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			err = ErrTooLarge
		}
		fail(w, err)
		return
	}
	json.NewEncoder(w).Encode(toRow(*m, me))
}

func serve(w http.ResponseWriter, r *http.Request, m *Meta, rc io.ReadCloser) {
	defer rc.Close()
	disp := "attachment"
	if InlineOK(m.Type) && r.URL.Query().Get("dl") != "1" {
		disp = "inline"
	}
	w.Header().Set("Content-Type", m.Type)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": Base(m.Name)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(m.Size, 10))
	w.Header().Set("ETag", `"`+m.ETag()+`"`)
	if r.Method != http.MethodHead {
		io.Copy(w, rc)
	}
}

func (s *Svc) download(w http.ResponseWriter, r *http.Request) {
	m, rc, err := s.Open(r.Context(), auth.User(r.Context()), r.PathValue("owner"), r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	serve(w, r, m, rc)
}

func (s *Svc) public(w http.ResponseWriter, r *http.Request) {
	m, rc, err := s.Public(r.Context(), r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serve(w, r, m, rc)
}

func (s *Svc) ownerParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	me := auth.User(r.Context())
	o := r.URL.Query().Get("owner")
	if o == "" {
		return me, true
	}
	if o != me {
		if _, ok := GroupOf(o); !ok {
			http.Error(w, "bad owner", http.StatusBadRequest)
			return "", false
		}
	}
	return o, true
}

func (s *Svc) mkdir(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownerParam(w, r)
	if !ok {
		return
	}
	if err := s.Mkdir(r.Context(), auth.User(r.Context()), o, r.URL.Query().Get("name")); err != nil {
		fail(w, err)
	}
}

func (s *Svc) move(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownerParam(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if err := s.MoveTo(r.Context(), auth.User(r.Context()), o, q.Get("from"), q.Get("to"), q.Get("dir") == "1", q.Get("copy") != "1", false); err != nil {
		fail(w, err)
	}
}

func (s *Svc) remove(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("dir") == "1" {
		if err := s.RemoveDir(r.Context(), auth.User(r.Context()), r.PathValue("owner"), r.PathValue("name")); err != nil {
			fail(w, err)
		}
		return
	}
	if err := s.Remove(r.Context(), auth.User(r.Context()), r.PathValue("owner"), r.PathValue("name")); err != nil {
		fail(w, err)
	}
}

func (s *Svc) share(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Read, Write []string
		Public      bool
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	me := auth.User(r.Context())
	m, err := s.Share(r.Context(), me, r.PathValue("owner"), r.PathValue("name"), in.Read, in.Write, in.Public)
	if err != nil {
		fail(w, err)
		return
	}
	json.NewEncoder(w).Encode(toRow(*m, me))
}
