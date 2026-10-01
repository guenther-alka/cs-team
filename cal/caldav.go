// Package cal: CalDAV-Backend auf S3. Ein Termin = ein Objekt (cal/<user>/<kalender>/<name>.ics).
// URL-Schema (Prefix /dav):  /dav/<user>/  /dav/<user>/cal/  /dav/<user>/cal/<kalender>/  .../<name>.ics
package cal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"cs-team/auth"
	"cs-team/store"
)

const Prefix = "/dav"

type Backend struct{ St store.Store }

type calendarT = caldav.Calendar

// Kalender-Bereiche (owner im Schlüssel cal/<owner>/<kalender>/...):
//
//	<benutzer>   persönlicher Kalender (nur der Benutzer)
//	_global      globale Kalender (alle lesen; schreiben Admins, bei Mode "rw" alle)
//	@<gruppe>    Gruppenkalender (Mitglieder lesen; schreiben Gruppen-Admins, bei Mode "rw" alle Mitglieder)
//
// Kalender-ID in URLs (cid): eigene = "<kal>", sonst "<owner>~<kal>".
const globalOwner = "_global"

type meta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode,omitempty"`     // globale/Gruppenkalender: "ro" nur Admins schreiben, "rw" alle Berechtigten
	Resource    bool   `json:"resource,omitempty"` // Ressource: keine überschneidenden Termine
	URL         string `json:"url,omitempty"`      // Abo: ICS-Feed (Kalender ist dann nur lesbar)
	Fetched     int64  `json:"fetched,omitempty"`
}

func he(code int, msg string) error { return webdav.NewHTTPError(code, errors.New(msg)) }

// parse: /dav/<user>/cal/<cid>/<obj> -> (cid, obj); prüft, dass user == eingeloggter Benutzer.
func (b *Backend) parse(ctx context.Context, p string) (cid, obj string, err error) {
	p = strings.Trim(strings.TrimPrefix(path.Clean(p), Prefix), "/")
	parts := strings.Split(p, "/")
	if len(parts) < 1 || parts[0] == "" {
		return "", "", he(http.StatusNotFound, "no path")
	}
	if parts[0] != auth.User(ctx) {
		return "", "", he(http.StatusForbidden, "forbidden")
	}
	if len(parts) > 2 {
		cid = parts[2]
	}
	if len(parts) > 3 {
		obj = parts[3]
	}
	if strings.ContainsAny(cid+obj, `\:`) || cid == ".." || obj == ".." {
		return "", "", he(http.StatusBadRequest, "bad name")
	}
	return
}

func key(owner, kal, obj string) string { return fmt.Sprintf("cal/%s/%s/%s", owner, kal, obj) }
func davPath(user, cid, obj string) string {
	p := fmt.Sprintf("%s/%s/cal/%s/", Prefix, user, cid)
	if obj != "" {
		p += obj
	}
	return p
}

func mkCID(me, owner, kal string) string {
	if owner == me {
		return kal
	}
	return owner + "~" + kal
}

// calRef: aufgelöster Kalender mit Zugriffsrechten des angemeldeten Benutzers.
type calRef struct {
	owner, kal, cid string
	m               meta
	read, write     bool
	manage          bool // Kalender löschen
}

func contains(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

func scopeOf(owner string) (scope, group string) {
	switch {
	case owner == globalOwner:
		return "global", ""
	case strings.HasPrefix(owner, "@"):
		return "group", owner[1:]
	}
	return "user", ""
}

// access: Rechte des angemeldeten Benutzers auf einen Kalender-Besitzer.
func access(ctx context.Context, owner, mode string) (read, write, manage bool) {
	read, write, manage = access0(ctx, owner, mode)
	if !auth.CanWrite(ctx, "cal") { // bei Bereich "nur lesen" nie ändern
		write, manage = false, false
	}
	return
}

func access0(ctx context.Context, owner, mode string) (read, write, manage bool) {
	me := auth.User(ctx)
	adm := auth.IsAdmin(ctx)
	switch sc, g := scopeOf(owner); {
	case owner == me:
		return true, true, true
	case sc == "global":
		return true, adm || mode == "rw", adm
	case sc == "group":
		mgr := auth.CanManage(ctx, g)
		member := contains(auth.GroupsOf(me), g)
		rd := member || adm
		return rd, rd && (mgr || mode == "rw"), mgr
	}
	return false, false, false
}

func (b *Backend) open(ctx context.Context, cid string) (*calRef, error) {
	me := auth.User(ctx)
	owner, kal := me, cid
	if i := strings.Index(cid, "~"); i > 0 {
		owner, kal = cid[:i], cid[i+1:]
	}
	if kal == "" || strings.ContainsAny(kal, "~/") {
		return nil, he(http.StatusNotFound, "no calendar")
	}
	if owner != me && owner != globalOwner && !(strings.HasPrefix(owner, "@") && len(owner) > 1) {
		return nil, he(http.StatusNotFound, "no calendar") // fremde Benutzer nie preisgeben
	}
	if owner == me && kal == "default" {
		b.ensureDefault(ctx, me)
	}
	raw, _, err := b.St.Get(ctx, key(owner, kal, "_meta.json"))
	if errors.Is(err, store.ErrNotFound) {
		return nil, he(http.StatusNotFound, "no calendar")
	} else if err != nil {
		return nil, err
	}
	var m meta
	json.Unmarshal(raw, &m)
	if m.Name == "" {
		m.Name = kal
	}
	r, w, mg := access(ctx, owner, m.Mode)
	if m.URL != "" {
		w = false // Abo-Kalender: Termine kommen vom Feed
	}
	if !r {
		return nil, he(http.StatusNotFound, "no calendar")
	}
	return &calRef{owner, kal, mkCID(me, owner, kal), m, r, w, mg}, nil
}

func (b *Backend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	return fmt.Sprintf("%s/%s/", Prefix, auth.User(ctx)), nil
}
func (b *Backend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	return fmt.Sprintf("%s/%s/cal/", Prefix, auth.User(ctx)), nil
}

func (b *Backend) ensureDefault(ctx context.Context, user string) {
	m, _ := json.Marshal(meta{Name: "default"})
	b.St.Put(ctx, key(user, "default", "_meta.json"), m, "*") // Konflikt = existiert schon
}

// createIn: legt einen Kalender im angegebenen Bereich an (Rechte prüft der Aufrufer).
func (b *Backend) createIn(ctx context.Context, owner, kal string, m meta) error {
	if !auth.CanWrite(ctx, "cal") {
		return he(http.StatusForbidden, "read-only")
	}
	raw, _ := json.Marshal(m)
	if _, err := b.St.Put(ctx, key(owner, kal, "_meta.json"), raw, "*"); errors.Is(err, store.ErrConflict) {
		return he(http.StatusConflict, "exists")
	} else {
		return err
	}
}

// CreateCalendar (CalDAV MKCALENDAR): nur persönliche Kalender.
func (b *Backend) CreateCalendar(ctx context.Context, c *caldav.Calendar) error {
	cid, _, err := b.parse(ctx, c.Path)
	if err != nil || cid == "" || strings.Contains(cid, "~") {
		return he(http.StatusBadRequest, "bad calendar path")
	}
	return b.createIn(ctx, auth.User(ctx), cid, meta{Name: c.Name, Description: c.Description})
}

func (c *calRef) davCal(user string) *caldav.Calendar {
	return &caldav.Calendar{Path: davPath(user, c.cid, ""), Name: c.m.Name, Description: c.m.Description,
		SupportedComponentSet: []string{"VEVENT", "VTODO"}}
}

func (b *Backend) GetCalendar(ctx context.Context, p string) (*caldav.Calendar, error) {
	cid, _, err := b.parse(ctx, p)
	if err != nil || cid == "" {
		return nil, he(http.StatusNotFound, "no calendar")
	}
	c, err := b.open(ctx, cid)
	if err != nil {
		return nil, err
	}
	return c.davCal(auth.User(ctx)), nil
}

// list: alle Kalender, die der Benutzer sieht: eigene, globale und die seiner Gruppen.
func (b *Backend) list(ctx context.Context) ([]*calRef, error) {
	me := auth.User(ctx)
	b.ensureDefault(ctx, me)
	owners := []string{me, globalOwner}
	for _, g := range auth.GroupsOf(me) {
		owners = append(owners, "@"+g)
	}
	var out []*calRef
	for _, o := range owners {
		infos, err := b.St.List(ctx, "cal/"+o+"/")
		if err != nil {
			return nil, err
		}
		for _, i := range infos {
			if !strings.HasSuffix(i.Key, "/_meta.json") {
				continue
			}
			kal := strings.Split(i.Key, "/")[2]
			if c, err := b.open(ctx, mkCID(me, o, kal)); err == nil {
				out = append(out, c)
			}
		}
	}
	return out, nil
}

func (b *Backend) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	l, err := b.list(ctx)
	if err != nil {
		return nil, err
	}
	var out []caldav.Calendar
	for _, c := range l {
		out = append(out, *c.davCal(auth.User(ctx)))
	}
	return out, nil
}

func decode(data []byte) (*ical.Calendar, error) {
	return ical.NewDecoder(bytes.NewReader(data)).Decode()
}

func (b *Backend) object(ctx context.Context, cid string, i store.Info) (*caldav.CalendarObject, error) {
	data, etag, err := b.St.Get(ctx, i.Key)
	if err != nil {
		return nil, err
	}
	c, err := decode(data)
	if err != nil {
		return nil, err
	}
	return &caldav.CalendarObject{Path: davPath(auth.User(ctx), cid, path.Base(i.Key)), ModTime: i.ModTime,
		ContentLength: int64(len(data)), ETag: etag, Data: c}, nil
}

func (b *Backend) GetCalendarObject(ctx context.Context, p string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	cid, obj, err := b.parse(ctx, p)
	if err != nil || obj == "" {
		return nil, he(http.StatusNotFound, "not found")
	}
	c, err := b.open(ctx, cid)
	if err != nil {
		return nil, err
	}
	infos, err := b.St.List(ctx, key(c.owner, c.kal, obj))
	if err != nil || len(infos) == 0 {
		return nil, he(http.StatusNotFound, "not found")
	}
	return b.object(ctx, c.cid, infos[0])
}

func (b *Backend) ListCalendarObjects(ctx context.Context, p string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	cid, _, err := b.parse(ctx, p)
	if err != nil {
		return nil, err
	}
	c, err := b.open(ctx, cid)
	if err != nil {
		return nil, err
	}
	infos, err := b.St.List(ctx, key(c.owner, c.kal, ""))
	if err != nil {
		return nil, err
	}
	var out []caldav.CalendarObject // Skalierung: später Cache/Index statt N x Get
	for _, i := range infos {
		if !strings.HasSuffix(i.Key, ".ics") {
			continue
		}
		if o, err := b.object(ctx, c.cid, i); err == nil {
			out = append(out, *o)
		}
	}
	return out, nil
}

func (b *Backend) QueryCalendarObjects(ctx context.Context, p string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	all, err := b.ListCalendarObjects(ctx, p, &q.CompRequest)
	if err != nil {
		return nil, err
	}
	return caldav.Filter(q, all)
}

func (b *Backend) PutCalendarObject(ctx context.Context, p string, c *ical.Calendar, o *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	cid, obj, err := b.parse(ctx, p)
	if err != nil || obj == "" || !strings.HasSuffix(obj, ".ics") {
		return nil, he(http.StatusBadRequest, "bad object path")
	}
	ci, err := b.open(ctx, cid)
	if err != nil {
		return nil, err
	}
	if !ci.write {
		return nil, he(http.StatusForbidden, "read-only calendar")
	}
	if c != nil && hasFineRRule(c.Component) {
		return nil, he(http.StatusBadRequest, "RRULE: repeat more often than daily is not supported")
	}
	if ci.m.Resource { // Prüfung und Speichern in einem Zug (K-05)
		defer lockCal(ci.owner, ci.kal)()
	}
	if msg := b.conflict(ctx, ci, c, obj); msg != "" {
		return nil, he(http.StatusConflict, msg)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(c); err != nil {
		return nil, he(http.StatusBadRequest, err.Error())
	}
	cond := ""
	switch {
	case o != nil && o.IfNoneMatch.IsWildcard():
		cond = "*"
	case o != nil && o.IfMatch.IsSet():
		if cond, err = o.IfMatch.ETag(); err != nil {
			return nil, he(http.StatusBadRequest, "bad If-Match")
		}
	}
	etag, err := b.St.Put(ctx, key(ci.owner, ci.kal, obj), buf.Bytes(), cond)
	if errors.Is(err, store.ErrConflict) {
		return nil, he(http.StatusPreconditionFailed, "etag mismatch")
	} else if err != nil {
		return nil, err
	}
	return &caldav.CalendarObject{Path: davPath(auth.User(ctx), ci.cid, obj), ETag: etag, ContentLength: int64(buf.Len()), Data: c}, nil
}

func (b *Backend) DeleteCalendarObject(ctx context.Context, p string) error {
	cid, obj, err := b.parse(ctx, p)
	if err != nil {
		return err
	}
	ci, err := b.open(ctx, cid)
	if err != nil {
		return err
	}
	if obj == "" { // ganzer Kalender: alle Objekte löschen
		if !ci.manage {
			return he(http.StatusForbidden, "not allowed to delete calendar")
		}
		infos, _ := b.St.List(ctx, key(ci.owner, ci.kal, ""))
		for _, i := range infos {
			b.St.Delete(ctx, i.Key)
		}
		return nil
	}
	if !ci.write {
		return he(http.StatusForbidden, "read-only calendar")
	}
	return b.St.Delete(ctx, key(ci.owner, ci.kal, obj))
}

// maxDAV: größter CalDAV-Body (PUT/REPORT); ein Termin ist wenige KB groß.
const maxDAV = 1 << 20

func Handler(st store.Store) http.Handler {
	h := &caldav.Handler{Backend: &Backend{St: st}, Prefix: Prefix}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 {
			r.Body = http.MaxBytesReader(w, r.Body, maxDAV)
		}
		h.ServeHTTP(w, r)
	})
}

// NewGroupCalendar: legt den Gruppenkalender "gruppe" an (Vorlage bei neuer Gruppe); Fehler (z.B. existiert) sind unkritisch.
func (b *Backend) NewGroupCalendar(ctx context.Context, group, mode string) {
	if mode != "ro" && mode != "rw" {
		mode = "rw"
	}
	b.createIn(ctx, "@"+group, "gruppe", meta{Name: group, Mode: mode})
}
