package files

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

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
	case errors.Is(err, ErrQuota):
		return http.StatusInsufficientStorage
	case errors.Is(err, ErrLocked):
		return http.StatusLocked
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
	mux.Handle("POST /api/filesdir", f(s.mkdir))                          // ?owner=&name=<ordnerpfad>
	mux.Handle("POST /api/filesmove", f(s.move))                          // ?owner=&from=&to=[&dir=1][&copy=1]
	mux.Handle("GET /api/trash", f(s.trashList))                          // Papierkorb: eigene und Gruppenordner, die ich verwalte
	mux.Handle("POST /api/trash/{owner}/{id}/restore", f(s.trashRestore)) // stellt wieder her, Antwort {"name":...}
	mux.Handle("DELETE /api/trash/{owner}/{id}", f(s.trashDelete))        // endgültig löschen
	mux.Handle("DELETE /api/trash", f(s.trashEmpty))                      // ?owner= leert den Papierkorb dieses Besitzers (Standard: eigener)
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
		Quota   int64             `json:"quota,omitempty"` // Kontingent in Bytes (0 = unbegrenzt)
		Used    int64             `json:"used"`            // belegt (eigener Bereich, einschließlich Papierkorb)
		Trash   int64             `json:"trash,omitempty"` // davon im Papierkorb
		Days    int               `json:"trashDays"`       // Aufbewahrung in Tagen (0 = kein Papierkorb)
	}{[]row{}, []row{}, auth.FolderGroups(me), s.Max >> 20, 0, 0, 0, s.trashDays()}
	if s.Quota != nil {
		out.Quota = s.Quota()
	}
	for _, m := range own {
		out.Own = append(out.Own, toRow(m, me))
		out.Used += m.Size
	}
	out.Trash = s.TrashUsed(r.Context(), me)
	out.Used += out.Trash
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
	etag := `"` + m.ETag() + `"`
	h := w.Header()
	h.Set("Content-Type", m.Type)
	h.Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": Base(m.Name)}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("ETag", etag)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "private, no-cache") // Browser fragt mit If-None-Match nach; die Zugriffsprüfung lief schon
	if etagMatch(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	start, n, partial, ok := byteRange(r.Header.Get("Range"), r.Header.Get("If-Range"), etag, m.Size)
	if !ok {
		h.Set("Content-Range", "bytes */"+strconv.FormatInt(m.Size, 10))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	h.Set("Content-Length", strconv.FormatInt(n, 10))
	if partial {
		h.Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(start+n-1, 10)+"/"+strconv.FormatInt(m.Size, 10))
		w.WriteHeader(http.StatusPartialContent)
	}
	if r.Method == http.MethodHead {
		return
	}
	if start > 0 {
		if _, err := io.CopyN(io.Discard, rc, start); err != nil {
			return
		}
	}
	io.CopyN(w, rc, n)
}

// etagMatch: If-None-Match (Liste, "*", schwache Form W/"..."), schwacher Vergleich.
func etagMatch(hdr, etag string) bool {
	hdr = strings.TrimSpace(hdr)
	if hdr == "" {
		return false
	}
	if hdr == "*" {
		return true
	}
	for _, p := range strings.Split(hdr, ",") {
		if strings.TrimPrefix(strings.TrimSpace(p), "W/") == etag {
			return true
		}
	}
	return false
}

// byteRange wertet einen einzelnen "bytes=a-b"-Bereich aus. partial=false: ganze Datei (kein/ungültiger Kopf, mehrere Bereiche
// oder If-Range passt nicht). ok=false: Bereich liegt außerhalb der Datei (416).
func byteRange(hdr, ifRange, etag string, size int64) (start, n int64, partial, ok bool) {
	whole := func() (int64, int64, bool, bool) { return 0, size, false, true }
	if !strings.HasPrefix(hdr, "bytes=") || strings.Contains(hdr, ",") {
		return whole()
	}
	if ifRange != "" && strings.TrimSpace(ifRange) != etag {
		return whole() // Datei hat sich geändert (oder Datumsform): ganze Datei senden
	}
	a, b, found := strings.Cut(strings.TrimSpace(hdr[len("bytes="):]), "-")
	if !found {
		return whole()
	}
	var from, to int64
	var err error
	switch {
	case a == "": // letzte n Bytes
		k, e := strconv.ParseInt(b, 10, 64)
		if e != nil || k <= 0 {
			return whole()
		}
		if size == 0 {
			return 0, 0, false, false
		}
		if k > size {
			k = size
		}
		from, to = size-k, size-1
	default:
		if from, err = strconv.ParseInt(a, 10, 64); err != nil || from < 0 {
			return whole()
		}
		to = size - 1
		if b != "" {
			if to, err = strconv.ParseInt(b, 10, 64); err != nil || to < from {
				return whole()
			}
		}
		if from >= size {
			return 0, 0, false, false
		}
		if to >= size {
			to = size - 1
		}
	}
	return from, to - from + 1, true, true
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

func (s *Svc) trashList(w http.ResponseWriter, r *http.Request) {
	items, err := s.Trash(r.Context(), auth.User(r.Context()))
	if err != nil {
		fail(w, err)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"days": s.trashDays(), "items": items})
}

func (s *Svc) trashRestore(w http.ResponseWriter, r *http.Request) {
	name, err := s.Restore(r.Context(), auth.User(r.Context()), r.PathValue("owner"), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"name": name})
}

func (s *Svc) trashDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.DeleteTrash(r.Context(), auth.User(r.Context()), r.PathValue("owner"), r.PathValue("id")); err != nil {
		fail(w, err)
	}
}

func (s *Svc) trashEmpty(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	owner := r.URL.Query().Get("owner")
	if owner == "" {
		owner = me
	}
	n, err := s.EmptyTrash(r.Context(), me, owner)
	if err != nil {
		fail(w, err)
		return
	}
	json.NewEncoder(w).Encode(map[string]int{"deleted": n})
}
