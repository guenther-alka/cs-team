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
	Scope       string `json:"scope"`            // user | group | unit | global
	Group       string `json:"group,omitempty"`  // bei scope=group
	Unit        string `json:"unit,omitempty"`   // bei scope=unit (Organisation)
	Mode        string `json:"mode,omitempty"`   // ro | rw (nur global/group/unit)
	Write       bool   `json:"write"`            // darf ich Termine ändern
	Manage      bool   `json:"manage"`           // darf ich den Kalender löschen
	URL         string `json:"url,omitempty"`    // Abo-URL (nur für Verwalter sichtbar)
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
	mux.Handle("PUT /api/cal/{kal}", f(b.apiEdit)) // Kalender bearbeiten/freigeben (nur Verantwortliche)
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
		out = append(out, row(c))
	}
	// Hierarchie von innen nach außen: eigene, Gruppe, Organisation, global - Abos (Internet-Kalender, nur lesbar)
	// stehen als eigene Gruppe am Ende.
	rank := map[string]int{"user": 0, "group": 1, "unit": 2, "global": 3}
	lvl := func(x calRow) int {
		if x.Sub {
			return 4
		}
		return rank[x.Scope]
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := lvl(out[i]), lvl(out[j])
		if a != b {
			return a < b
		}
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		if out[i].Unit != out[j].Unit {
			return out[i].Unit < out[j].Unit
		}
		return out[i].Name < out[j].Name
	})
	json.NewEncoder(w).Encode(out)
}

// row: ein Kalender als JSON für die Web-UI. Die Abo-URL sehen nur die Verantwortlichen.
func row(c *calRef) calRow {
	sc, g := scopeOf(c.owner)
	rw := calRow{ID: c.cid, Name: c.m.Name, Description: c.m.Description, Scope: sc, Mode: c.m.Mode,
		Write: c.write, Manage: c.manage && !(sc == "user" && c.kal == "default"), Sub: c.m.URL != "", Resource: c.m.Resource}
	switch sc {
	case "group":
		rw.Group = g
	case "unit":
		rw.Unit = g
	}
	if c.m.URL != "" {
		rw.Fetched, rw.SubErr = c.m.Fetched, c.m.Err
	}
	if c.manage {
		rw.URL = c.m.URL
	}
	return rw
}

// POST /api/cal {name, description, scope: user|group|unit|global, group/unit, mode: off|ro|rw, url}
// Termine ändern darf nur, wer den Kalender verantwortet (persönlich: der Benutzer; Gruppe: Gruppen-Admins,
// Organisation/global: globale Admins) - die Berechtigten lesen. "off" = noch nicht freigegeben (nur die
// Verantwortlichen sehen ihn), "rw" gibt das Ändern ausdrücklich für alle Berechtigten frei. Bearbeiten und
// Freigeben später: PUT /api/cal/{kal} (apiEdit).
func (b *Backend) apiCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, Description, Scope, Group, Unit, Mode, URL string
		Resource                                         bool
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
		owner, m.Mode = "@"+in.Group, "ro"
	case "unit":
		if !auth.IsAdmin(r.Context()) {
			http.Error(w, "only admins create organisation calendars", 403)
			return
		}
		if !auth.UnitExists(in.Unit) {
			http.Error(w, "no such organisation", 400)
			return
		}
		owner, m.Mode = "+"+in.Unit, "ro"
	default:
		http.Error(w, "bad scope", 400)
		return
	}
	if owner != me {
		switch in.Mode {
		case "":
		case "off", "ro", "rw":
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

// apiEdit: PUT /api/cal/{kal} - Kalender der eigenen Verantwortung bearbeiten: Name, Beschreibung, Freigabe
// ("off" nicht freigegeben, "ro" Berechtigte lesen, "rw" Berechtigte ändern) und Ressourcen-Filter; Abo-URL
// setzen/wechseln/beenden. Die Kalender-ID bleibt stabil (Link und Abo-Adresse ändern sich nicht), ein neues
// Feed-Ziel wird sofort abgerufen, Fehler dabei werden gemerkt (der Kalender bleibt bestehen).
func (b *Backend) apiEdit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name, Description, Mode, URL *string
		Resource                     *bool
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !c.manage { // nur die Verantwortlichen: Benutzer, Gruppen-Admins, globale Admins
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	m, newSub := c.m, ""
	if in.Mode != nil {
		mode := strings.ToLower(strings.TrimSpace(*in.Mode))
		if mode == "none" { // "none" und "off" sind gleichbedeutend: nicht freigegeben
			mode = "off"
		}
		if mode != "" && mode != "off" && mode != "ro" && mode != "rw" {
			http.Error(w, `mode: "off", "ro" or "rw"`, http.StatusBadRequest)
			return
		}
		if m.URL != "" && mode != "" { // Abos kommen vom Feed und sind immer nur lesbar
			http.Error(w, "subscription is always read-only", http.StatusBadRequest)
			return
		}
		m.Mode = mode
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 100 {
			http.Error(w, "name", http.StatusBadRequest)
			return
		}
		m.Name = n
	}
	if in.Description != nil {
		m.Description = strings.TrimSpace(*in.Description)
	}
	if in.Resource != nil && m.URL == "" {
		m.Resource = *in.Resource
	}
	if in.URL != nil {
		u := strings.TrimSpace(*in.URL)
		if u == "" { // Abo beenden: Feed-URL und die geholten Termine entfernen
			m.URL, m.Err, m.Fetched, m.Tried = "", "", 0, 0
			if infos, err := b.St.List(r.Context(), key(c.owner, c.kal, "")); err == nil {
				for _, i := range infos {
					if strings.HasSuffix(i.Key, ".ics") {
						b.St.Delete(r.Context(), i.Key)
					}
				}
			}
		} else {
			v, err := checkURL(u)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if v != m.URL {
				newSub = v
			}
			m.URL, m.Mode = v, "ro" // externe Abos sind immer nur lesbar
		}
	}
	mb, _ := json.Marshal(m)
	if _, err := b.St.Put(r.Context(), key(c.owner, c.kal, "_meta.json"), mb, ""); err != nil {
		http.Error(w, "save", http.StatusInternalServerError)
		return
	}
	subErr := ""
	if newSub != "" {
		if err := b.refresh(r.Context(), c.owner, c.kal, m); err != nil {
			b.subFailed(r.Context(), c.owner, c.kal, m, err) // Zustand merken, der Kalender bleibt bestehen
			subErr = feedErr(err)
		}
	}
	c, err = b.open(r.Context(), r.PathValue("kal")) // Rechte und Zustand nach dem Speichern neu bewerten
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rw := row(c)
	if subErr != "" {
		rw.SubErr = subErr
	}
	json.NewEncoder(w).Encode(rw)
}

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
