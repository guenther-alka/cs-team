package cal

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"cs-team/auth"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

type calRow struct {
	ID          string `json:"id"` // cid: eigene "<kal>", sonst "<owner>~<kal>"
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope"`           // user | global | group
	Group       string `json:"group,omitempty"` // bei scope=group
	Mode        string `json:"mode,omitempty"`  // ro | rw (nur global/group)
	Write       bool   `json:"write"`           // darf ich Termine ändern
	Manage      bool   `json:"manage"`          // darf ich den Kalender löschen
	URL         string `json:"url,omitempty"`   // Abo-URL (nur für Verwalter sichtbar)
	Resource    bool   `json:"resource,omitempty"`
	Sub         bool   `json:"sub,omitempty"`     // Abo-Kalender (Internet-Feed)
	Fetched     int64  `json:"fetched,omitempty"` // Abo: letzter erfolgreicher Abruf (Unix)
	SubErr      string `json:"suberr,omitempty"`  // Abo: Fehler des letzten Versuchs
}

// Routes: einfache JSON-API für die Web-UI (CalDAV-Clients nutzen /dav/).
func (b *Backend) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	f := func(fn http.HandlerFunc) http.Handler { return wrap(auth.Need("cal", fn)) }
	mux.Handle("GET /api/cal", f(b.apiList))
	mux.Handle("POST /api/cal", f(b.apiCreate))
	mux.Handle("DELETE /api/cal/{kal}", f(b.apiDelete))
	mux.Handle("POST /api/cal/{kal}/refresh", f(b.apiRefresh))
	mux.Handle("GET /api/cal/{kal}/export.ics", f(b.apiExport))
	mux.Handle("POST /api/cal/{kal}/import", f(b.apiImport))
	mux.Handle("GET /api/cal/{kal}/events", f(b.apiEvents))
	mux.Handle("POST /api/cal/{kal}/events", f(b.apiAddEvent))
	mux.Handle("PUT /api/cal/{kal}/events/{file}", f(b.apiPutEvent))
	mux.Handle("DELETE /api/cal/{kal}/events/{file}", f(b.apiDelEvent))
}

func (b *Backend) apiList(w http.ResponseWriter, r *http.Request) {
	l, err := b.list(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	out := []calRow{}
	for _, c := range l {
		sc, g := scopeOf(c.owner)
		out = append(out, calRow{ID: c.cid, Name: c.m.Name, Description: c.m.Description, Scope: sc, Group: g, Mode: c.m.Mode,
			Write: c.write, Manage: c.manage && !(sc == "user" && c.kal == "default"), Sub: c.m.URL != "", Resource: c.m.Resource})
		if c.m.URL != "" {
			out[len(out)-1].Fetched, out[len(out)-1].SubErr = c.m.Fetched, c.m.Err
		}
		if c.manage {
			out[len(out)-1].URL = c.m.URL
		}
	}
	rank := map[string]int{"global": 0, "group": 1, "user": 2}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return rank[out[i].Scope] < rank[out[j].Scope]
		}
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	json.NewEncoder(w).Encode(out)
}

// POST /api/cal {name, description, scope: user|global|group, group, mode: ro|rw}
func (b *Backend) apiCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, Description, Scope, Group, Mode, URL string
		Resource                                   bool
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" {
		http.Error(w, "name required", 400)
		return
	}
	me := auth.User(r.Context())
	m := meta{Name: strings.TrimSpace(in.Name), Description: in.Description}
	m.Resource = in.Resource && strings.TrimSpace(in.URL) == ""
	if strings.TrimSpace(in.URL) != "" {
		u, err := checkURL(in.URL)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		m.URL = u
	}
	owner := me
	switch in.Scope {
	case "", "user":
	case "global":
		if !auth.IsAdmin(r.Context()) {
			http.Error(w, "only admins create global calendars", 403)
			return
		}
		owner, m.Mode = globalOwner, "ro"
	case "group":
		if !auth.CanManage(r.Context(), in.Group) {
			http.Error(w, "not admin of group", 403)
			return
		}
		owner, m.Mode = "@"+in.Group, "rw"
	default:
		http.Error(w, "bad scope", 400)
		return
	}
	if owner != me {
		switch in.Mode {
		case "":
		case "ro", "rw":
			m.Mode = in.Mode
		default:
			http.Error(w, "bad mode", 400)
			return
		}
	}
	if !auth.CanWrite(r.Context(), "cal") {
		http.Error(w, "calendar area is read-only for you", http.StatusForbidden)
		return
	}
	id := slug(in.Name)
	if id == "" || (owner == me && id == "default") {
		id += hex4()
	}
	if err := b.createIn(r.Context(), owner, id, m); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if m.URL != "" { // Abo: sofort holen; bei Fehler den Kalender wieder entfernen
		if err := b.refresh(r.Context(), owner, id, m); err != nil {
			infos, _ := b.St.List(r.Context(), key(owner, id, ""))
			for _, i := range infos {
				b.St.Delete(r.Context(), i.Key)
			}
			http.Error(w, feedErr(err), http.StatusBadGateway)
			return
		}
	}
	json.NewEncoder(w).Encode(calRow{ID: mkCID(me, owner, id), Name: m.Name})
}

func hex4() string { b := make([]byte, 3); rand.Read(b); return hex.EncodeToString(b) }

func (b *Backend) apiDelete(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if !c.manage {
		http.Error(w, "forbidden", 403)
		return
	}
	if c.owner == me && c.kal == "default" {
		http.Error(w, "cannot delete", 400)
		return
	}
	if err := b.DeleteCalendarObject(r.Context(), davPath(me, c.cid, "")); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// feedErr: Fehlertext für den Browser ohne Resolver-/Netzwerkdetails (nur Art des Fehlers).
func feedErr(err error) string {
	m := err.Error()
	switch {
	case strings.Contains(m, "address not allowed"):
		return "feed: address not allowed"
	case strings.HasPrefix(m, "feed:"), strings.Contains(m, "too large"), strings.Contains(m, "too many redirects"):
		return m
	}
	return "feed: not reachable or not a calendar"
}
