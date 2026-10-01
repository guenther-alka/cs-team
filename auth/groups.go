package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"cs-team/store"
)

const (
	groupsKey     = "users/groups.json"
	DefaultGroup  = "alluser" // Standardgruppe: nicht löschbar, Gruppen-Admins = globale Admins
	legacyDefault = "users"   // frühere Bezeichnung (wird beim Start umbenannt)
)

// Bereiche, die eine Gruppe freischalten kann.
var Areas = []string{"cal", "calc", "text", "files"}

// Template: Vorlage für neue Gruppen (Bereiche, Gruppenordner, Gruppenkalender).
type Template struct {
	Areas, Read []string
	Folder, Cal string // Gruppenordner "", "ro", "rw"; Gruppenkalender "" (keiner), "ro", "rw"
}

var Templates = map[string]Template{
	"team":   {Areas: []string{"cal", "calc", "text", "files"}, Folder: "rw", Cal: "rw"},
	"klasse": {Areas: []string{"files"}, Read: []string{"cal", "calc", "text"}, Folder: "ro", Cal: "ro"},
}

// OnNewGroup: wird nach dem Anlegen einer Gruppe mit Gruppenkalender-Vorlage aufgerufen (main verbindet den Kalender).
var OnNewGroup func(ctx context.Context, group, calMode string)

var (
	ErrNoGroup      = errors.New("no such group")
	ErrBadArea      = errors.New("area: cal calc text files")
	ErrGroupUsed    = errors.New("group is the only group of a user")
	ErrLastGroup    = errors.New("at least one group must remain")
	ErrDefaultGroup = errors.New("the default group '" + DefaultGroup + "' cannot be deleted; its admins are the global admins")
	ErrNoGroups     = errors.New("user needs at least one group")
)

type Group struct {
	Areas  []string `json:"areas"`            // Bereiche mit Schreibrecht ("ändern")
	Read   []string `json:"read,omitempty"`   // Bereiche nur lesen
	Admins []string `json:"admins,omitempty"` // Gruppen-Admins: verwalten Mitglieder ihrer Gruppe
	Chat   string   `json:"chat,omitempty"`   // Gruppen-Chat: "" = member, "admin" (nur Admins schreiben), "off"
	Msg    string   `json:"msg,omitempty"`    // Nachrichten an die Gruppe: "" = admin, "member", "off"
	Tasks  string   `json:"tasks,omitempty"`  // wer Aufgaben der Gruppe anlegen darf: "" = member, "admin", "off"
	Chans  string   `json:"chans,omitempty"`  // wer weitere Chat-Kanäle anlegen darf: "" = admin, "member", "off" (niemand)
	Units  []string `json:"units,omitempty"`  // Organisationen (leer = "all")
	Folder string   `json:"folder,omitempty"` // Gruppenordner: "" keiner, "ro" Mitglieder lesen (Gruppen-Admins schreiben), "rw" Mitglieder lesen+schreiben
}

func defaultGroups() map[string]Group {
	return map[string]Group{DefaultGroup: {Areas: append([]string{}, Areas...)}}
}

func (a *Auth) loadGroups(ctx context.Context) (map[string]Group, bool) {
	b, _, err := a.st.Get(ctx, groupsKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return defaultGroups(), true
	case err != nil:
		return nil, false
	}
	g := map[string]Group{}
	if json.Unmarshal(b, &g) != nil || len(g) == 0 {
		return nil, false
	}
	return g, true
}

// GroupsOf: Gruppen eines Benutzers (für Freigaben); leer = Standardgruppe.
func GroupsOf(user string) []string {
	if std == nil {
		return nil
	}
	u, ok := std.get(context.Background(), user)
	if !ok {
		return nil
	}
	return effGroups(u)
}

// Allowed: steht der Benutzer in einer Freigabeliste? Einträge: "*" (alle), "g:<gruppe>" oder Benutzername.
func Allowed(list []string, user string) bool {
	var gs []string
	for _, e := range list {
		switch {
		case e == "*" || e == user:
			return true
		case strings.HasPrefix(e, "g:"):
			if gs == nil {
				gs = GroupsOf(user)
			}
			for _, g := range gs {
				if g == e[2:] || (e[2:] == legacyDefault && g == DefaultGroup) { // alte Freigaben an "users"
					return true
				}
			}
		}
	}
	return false
}

var ErrBadFolder = errors.New(`folder: "", "ro" or "rw"`)

// FolderAccess: Zugriff eines Benutzers auf den Ordner einer Gruppe (Files-Bereich "@<gruppe>").
// Mitglieder lesen; schreiben bei "rw" oder als Gruppen-Admin; globale Admins immer beides.
func FolderAccess(user, group string) (read, write bool) {
	if std == nil {
		return
	}
	u, ok := std.get(context.Background(), user)
	if !ok || u.Disabled {
		return
	}
	std.mu.Lock()
	g, okg := std.groups[group]
	std.mu.Unlock()
	if !okg || g.Folder == "" {
		return
	}
	if u.Admin {
		return true, true
	}
	if !contains(effGroups(u), group) {
		return
	}
	return true, g.Folder == "rw" || contains(g.Admins, user)
}

type FolderInfo struct {
	Name  string `json:"name"`
	Write bool   `json:"write"`
}

// FolderGroups: Gruppenordner, auf die der Benutzer zugreifen darf.
func FolderGroups(user string) []FolderInfo {
	out := []FolderInfo{}
	if std == nil {
		return out
	}
	std.mu.Lock()
	var names []string
	for n, g := range std.groups {
		if g.Folder != "" {
			names = append(names, n)
		}
	}
	std.mu.Unlock()
	sort.Strings(names)
	for _, n := range names {
		if r, w := FolderAccess(user, n); r {
			out = append(out, FolderInfo{n, w})
		}
	}
	return out
}

func (a *Auth) SetFolder(ctx context.Context, name, mode string) error {
	if mode != "" && mode != "ro" && mode != "rw" {
		return ErrBadFolder
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		g, ok := m[name]
		if !ok {
			return ErrNoGroup
		}
		g.Folder = mode
		m[name] = g
		return nil
	})
}

// adminOf: Gruppen, deren Gruppen-Admin der Benutzer ist.
func (a *Auth) adminOf(user string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for n, g := range a.groups {
		for _, x := range g.Admins {
			if x == user {
				out = append(out, n)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func AdminOf(ctx context.Context) []string { l, _ := ctx.Value(ctxAdminOf{}).([]string); return l }

// CanManage: globaler Admin oder Gruppen-Admin dieser Gruppe.
func CanManage(ctx context.Context, group string) bool {
	if IsAdmin(ctx) {
		return true
	}
	for _, g := range AdminOf(ctx) {
		if g == group {
			return true
		}
	}
	return false
}

func effGroups(u Account) []string {
	if len(u.Groups) == 0 {
		return []string{DefaultGroup}
	}
	return u.Groups
}

// areas: Vereinigung der Bereiche aller Gruppen des Benutzers (all = lesen oder ändern, wr = ändern); Admin darf alles.
func (a *Auth) areas(u Account) (all, wr map[string]bool) {
	all, wr = map[string]bool{}, map[string]bool{}
	if u.Admin {
		for _, r := range Areas {
			all[r], wr[r] = true, true
		}
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, g := range effGroups(u) {
		for _, r := range a.groups[g].Read {
			all[r] = true
		}
		for _, r := range a.groups[g].Areas {
			all[r], wr[r] = true, true
		}
	}
	return
}

// CanWrite: darf der angemeldete Benutzer im Bereich ändern (nicht nur lesen)?
func CanWrite(ctx context.Context, area string) bool {
	m, _ := ctx.Value(ctxWAreas{}).(map[string]bool)
	return m[area]
}

// WriteArea: dasselbe per Benutzername (für Rechteprüfungen ohne Request-Kontext).
func WriteArea(user, area string) bool {
	if std == nil {
		return false
	}
	u, ok := std.get(context.Background(), user)
	if !ok {
		return false
	}
	_, wr := std.areas(u)
	return wr[area]
}

// Can: darf der angemeldete Benutzer diesen Bereich nutzen?
func Can(ctx context.Context, area string) bool {
	m, _ := ctx.Value(ctxAreas{}).(map[string]bool)
	return m[area]
}

// Need: 403, wenn der Benutzer den Bereich nicht nutzen darf (hinter Wrap verwenden).
func Need(area string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Can(r.Context(), area) {
			http.Error(w, "no permission for "+area, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth) checkGroups(ctx context.Context, names []string) error {
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, n := range names {
		if _, ok := a.groups[n]; !ok {
			return ErrNoGroup
		}
	}
	return nil
}

func (a *Auth) mutateGroups(ctx context.Context, fn func(m map[string]Group) error) error {
	err := store.Update(ctx, a.st, groupsKey, func(cur []byte) ([]byte, error) {
		m := defaultGroups()
		if cur != nil {
			m = map[string]Group{}
			if err := json.Unmarshal(cur, &m); err != nil {
				return nil, err
			}
		}
		if err := fn(m); err != nil {
			return nil, err
		}
		return json.Marshal(m)
	})
	a.invalidate()
	return err
}

// SetGroup legt eine Gruppe an oder ändert ihre Bereiche.
func (a *Auth) SetGroup(ctx context.Context, name string, areas, read []string) error {
	if !validName.MatchString(name) {
		return ErrBadName
	}
	ok := map[string]bool{}
	for _, r := range Areas {
		ok[r] = true
	}
	norm := func(l []string, skip map[string]bool) ([]string, error) {
		seen := map[string]bool{}
		out := []string{}
		for _, r := range l {
			if !ok[r] {
				return nil, ErrBadArea
			}
			if !seen[r] && !skip[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
		sort.Strings(out)
		return out, nil
	}
	clean, err := norm(areas, nil)
	if err != nil {
		return err
	}
	edit := map[string]bool{}
	for _, r := range clean {
		edit[r] = true
	}
	ro, err := norm(read, edit) // "ändern" schließt "lesen" ein
	if err != nil {
		return err
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		g := m[name]
		g.Areas, g.Read = clean, ro
		if len(ro) == 0 {
			g.Read = nil
		}
		m[name] = g
		return nil
	})
}

func (a *Auth) DeleteGroup(ctx context.Context, name string) error {
	if name == DefaultGroup {
		return ErrDefaultGroup
	}
	g, _ := a.loadGroups(ctx)
	if _, ok := g[name]; !ok {
		return ErrNoGroup
	}
	if len(g) <= 1 {
		return ErrLastGroup
	}
	// Mitgliedschaften entfernen; Abbruch, wenn jemand sonst ohne Gruppe bliebe
	if err := a.mutate(ctx, func(m map[string]Account) error {
		for n, u := range m {
			cur := effGroups(u)
			keep := []string{}
			for _, x := range cur {
				if x != name {
					keep = append(keep, x)
				}
			}
			if len(keep) == len(cur) {
				continue
			}
			if len(keep) == 0 {
				return ErrGroupUsed
			}
			u.Groups = keep
			m[n] = u
		}
		return nil
	}); err != nil {
		return err
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error { delete(m, name); return nil })
}

// SetUserGroups ersetzt die Gruppen eines Benutzers (mindestens eine).
func (a *Auth) SetUserGroups(ctx context.Context, name string, groups []string) error {
	if len(groups) == 0 {
		return ErrNoGroups
	}
	if err := a.checkGroups(ctx, groups); err != nil {
		return err
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, g := range groups {
		if !seen[g] {
			seen[g] = true
			clean = append(clean, g)
		}
	}
	sort.Strings(clean)
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		u.Groups = clean
		m[name] = u
		return nil
	})
}

// Routes: Gruppenverwaltung (Liste für alle Angemeldeten, Ändern nur Admin).
func (a *Auth) groupRoutes(mux *http.ServeMux, adm func(http.HandlerFunc) http.Handler) {
	a.unitRoutes(mux, adm)
	mux.Handle("GET /api/groups", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.refresh(r.Context())
		type row struct {
			Name    string   `json:"name"`
			Areas   []string `json:"areas"`
			Read    []string `json:"read"`
			Admins  []string `json:"admins"`
			Folder  string   `json:"folder,omitempty"`
			Units   []string `json:"units"`
			Chat    string   `json:"chat"`
			Msg     string   `json:"msg"`
			Chans   string   `json:"chans"`
			Tasks   string   `json:"tasks"`
			Members []string `json:"members,omitempty"` // nur für Admin / Gruppen-Admin der Gruppe
			Manage  bool     `json:"manage,omitempty"`
		}
		a.mu.Lock()
		out := []row{}
		for n, g := range a.groups {
			rw := row{Name: n, Areas: g.Areas, Read: g.Read, Folder: g.Folder, Units: unitsOf(g), Chat: chatMode(g), Msg: msgMode(g), Chans: chansMode(g), Tasks: tasksMode(g), Admins: append([]string{}, g.Admins...), Manage: CanManage(r.Context(), n)}
			if n == DefaultGroup { // Gruppen-Admins der Standardgruppe = globale Admins
				rw.Admins = []string{}
				for un, u := range a.users {
					if u.Admin && !u.Disabled {
						rw.Admins = append(rw.Admins, un)
					}
				}
				sort.Strings(rw.Admins)
			}
			if rw.Manage {
				rw.Members = []string{}
				for un, u := range a.users {
					for _, x := range effGroups(u) {
						if x == n {
							rw.Members = append(rw.Members, un)
						}
					}
				}
				sort.Strings(rw.Members)
			}
			out = append(out, rw)
		}
		a.mu.Unlock()
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		json.NewEncoder(w).Encode(out)
	})))
	mux.Handle("POST /api/groups", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name      string
			Areas     []string
			Read      []string
			Folder    string
			Template  string
			Cal       string   // Gruppenkalender: "" keiner, "ro" Mitglieder lesen, "rw" Mitglieder dürfen eintragen
			Admins    []string // Gruppen-Admins (müssen existieren; werden auch Mitglied)
			Units     []string // Organisationen (müssen existieren; leer = all)
			ChatMode  string   `json:"chat"`  // member | admin | off (leer = member)
			MsgMode   string   `json:"msg"`   // member | admin | off (leer = admin)
			ChansMode string   `json:"chans"` // wer Kanäle anlegen darf: member | admin | off (leer = admin)
			TasksMode string   `json:"tasks"` // wer Aufgaben anlegen darf: member | admin | off (leer = member)
		}
		if !body(w, r, &in) {
			return
		}
		a.refresh(r.Context())
		a.mu.Lock()
		_, exists := a.groups[in.Name]
		for _, n := range in.Admins {
			if _, ok := a.users[n]; !ok && !exists {
				a.mu.Unlock()
				fail_(w, fmt.Errorf("%w: %s", ErrNoUser, n))
				return
			}
		}
		a.mu.Unlock()
		if exists {
			fail_(w, ErrExists)
			return
		}
		if in.Cal != "" && in.Cal != "ro" && in.Cal != "rw" {
			http.Error(w, `cal: "", "ro" or "rw"`, http.StatusBadRequest)
			return
		}
		tpl, hasTpl := Templates[in.Template]
		if in.Template != "" && !hasTpl {
			http.Error(w, "unknown template", http.StatusBadRequest)
			return
		}
		if hasTpl && in.Areas == nil && in.Read == nil { // Vorlage gilt, solange nichts Eigenes angegeben ist
			in.Areas, in.Read = tpl.Areas, tpl.Read
			if in.Folder == "" {
				in.Folder = tpl.Folder
			}
		}
		if in.Folder != "" && in.Folder != "ro" && in.Folder != "rw" {
			fail_(w, ErrBadFolder)
			return
		}
		for _, u := range in.Units {
			if !contains(a.loadUnits(r.Context()), u) {
				fail_(w, ErrNoUnit)
				return
			}
		}
		if err := a.SetGroup(r.Context(), in.Name, in.Areas, in.Read); err != nil {
			fail_(w, err)
			return
		}
		if in.ChatMode != "" || in.MsgMode != "" || in.ChansMode != "" || in.TasksMode != "" {
			if err := a.SetGroupModes(r.Context(), in.Name, nonEmpty(in.ChatMode), nonEmpty(in.MsgMode), nonEmpty(in.ChansMode), nonEmpty(in.TasksMode)); err != nil {
				fail_(w, err)
				return
			}
		}
		if len(in.Units) > 0 {
			if err := a.SetGroupUnits(r.Context(), in.Name, in.Units); err != nil {
				fail_(w, err)
				return
			}
		}
		if in.Folder != "" {
			if err := a.SetFolder(r.Context(), in.Name, in.Folder); err != nil {
				fail_(w, err)
				return
			}
		}
		if hasTpl && in.Cal == "" {
			in.Cal = tpl.Cal
		}
		if in.Cal != "" && OnNewGroup != nil {
			OnNewGroup(r.Context(), in.Name, in.Cal)
		}
		if len(in.Admins) > 0 {
			if err := a.SetMembers(r.Context(), in.Name, in.Admins, nil); err != nil {
				fail_(w, err)
				return
			}
			if err := a.SetGroupAdmins(r.Context(), in.Name, in.Admins); err != nil {
				fail_(w, err)
			}
		}
	}))
	mux.Handle("POST /api/groups/{name}", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Areas  []string
			Read   []string
			Folder *string
			Units  *[]string
			Chat   *string
			Msg    *string
			Chans  *string
			Tasks  *string
		}
		if !body(w, r, &in) {
			return
		}
		a.refresh(r.Context())
		a.mu.Lock()
		_, exists := a.groups[r.PathValue("name")]
		a.mu.Unlock()
		if !exists {
			fail_(w, ErrNoGroup)
			return
		}
		if in.Areas != nil {
			if err := a.SetGroup(r.Context(), r.PathValue("name"), in.Areas, in.Read); err != nil {
				fail_(w, err)
				return
			}
		}
		if in.Chat != nil || in.Msg != nil || in.Chans != nil || in.Tasks != nil {
			if err := a.SetGroupModes(r.Context(), r.PathValue("name"), in.Chat, in.Msg, in.Chans, in.Tasks); err != nil {
				fail_(w, err)
				return
			}
		}
		if in.Units != nil {
			if err := a.SetGroupUnits(r.Context(), r.PathValue("name"), *in.Units); err != nil {
				fail_(w, err)
				return
			}
		}
		if in.Folder != nil {
			if err := a.SetFolder(r.Context(), r.PathValue("name"), *in.Folder); err != nil {
				fail_(w, err)
			}
		}
	}))
	mux.Handle("DELETE /api/groups/{name}", adm(func(w http.ResponseWriter, r *http.Request) {
		if err := a.DeleteGroup(r.Context(), r.PathValue("name")); err != nil {
			fail_(w, err)
		}
	}))
	mux.Handle("POST /api/groups/{name}/members", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !CanManage(r.Context(), r.PathValue("name")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var in struct{ Add, Remove []string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in) != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if !IsAdmin(r.Context()) { // Gruppen-Admin: nur Konten hinzufügen, die ausschließlich in verwalteten Gruppen sind
			for _, n := range in.Add {
				if !a.manages(r.Context(), n) {
					http.Error(w, "forbidden: "+n+" belongs to groups you do not manage (ask a global admin)", http.StatusForbidden)
					return
				}
			}
		}
		if err := a.SetMembers(r.Context(), r.PathValue("name"), in.Add, in.Remove); err != nil {
			fail_(w, err)
		}
	})))
	mux.Handle("POST /api/groups/{name}/admins", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Admins []string }
		if !body(w, r, &in) {
			return
		}
		if err := a.SetGroupAdmins(r.Context(), r.PathValue("name"), in.Admins); err != nil {
			fail_(w, err)
		}
	}))
	mux.Handle("POST /api/users/{name}/groups", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Groups []string }
		if !body(w, r, &in) {
			return
		}
		if err := a.SetUserGroups(r.Context(), r.PathValue("name"), in.Groups); err != nil {
			fail_(w, err)
		}
	}))
}

func shares(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// manages: darf der Aufrufer Konto "target" verwalten (Passwort)? Admin: alle; Gruppen-Admin: Nicht-Admins seiner Gruppen.
func (a *Auth) manages(ctx context.Context, target string) bool {
	if IsAdmin(ctx) {
		return true
	}
	u, ok := a.get(ctx, target)
	return ok && !u.Admin && shares(effGroups(u), AdminOf(ctx)) && within(own(effGroups(u)), AdminOf(ctx))
}

// own: ohne die Standardgruppe (alluser = jeder; sie zählt nicht als "fremde" Gruppe).
func own(l []string) []string {
	var out []string
	for _, x := range l {
		if x != DefaultGroup {
			out = append(out, x)
		}
	}
	return out
}

// within: sind ALLE Gruppen in "l" auch in "allowed"? (Gruppen-Admin darf ein Konto nur verwalten, wenn er jede
// Gruppe des Kontos verwaltet - sonst käme er per Passwort-Reset an die Daten fremder Gruppen.)
func within(l, allowed []string) bool {
	for _, x := range l {
		if !contains(allowed, x) {
			return false
		}
	}
	return true
}

// SetMembers: Mitglieder einer Gruppe ändern (add/remove); jeder Benutzer behält mindestens eine Gruppe.
func (a *Auth) SetMembers(ctx context.Context, group string, add, remove []string) error {
	a.refresh(ctx)
	a.mu.Lock()
	_, ok := a.groups[group]
	a.mu.Unlock()
	if !ok {
		return ErrNoGroup
	}
	return a.mutate(ctx, func(m map[string]Account) error {
		for _, n := range add {
			u, ok := m[n]
			if !ok {
				return fmt.Errorf("%w: %s", ErrNoUser, n)
			}
			cur := effGroups(u)
			if !contains(cur, group) {
				u.Groups = append(append([]string{}, cur...), group)
				sort.Strings(u.Groups)
				m[n] = u
			}
		}
		for _, n := range remove {
			u, ok := m[n]
			if !ok {
				continue
			}
			var keep []string
			for _, x := range effGroups(u) {
				if x != group {
					keep = append(keep, x)
				}
			}
			if len(keep) == len(effGroups(u)) {
				continue
			}
			if len(keep) == 0 {
				return fmt.Errorf("%w: %s", ErrGroupUsed, n)
			}
			u.Groups = keep
			m[n] = u
		}
		return nil
	})
}

func contains(l []string, x string) bool {
	for _, y := range l {
		if y == x {
			return true
		}
	}
	return false
}

func (a *Auth) SetGroupAdmins(ctx context.Context, group string, admins []string) error {
	if group == DefaultGroup {
		return ErrDefaultGroup
	}
	a.refresh(ctx)
	a.mu.Lock()
	for _, n := range admins {
		if _, ok := a.users[n]; !ok {
			a.mu.Unlock()
			return fmt.Errorf("%w: %s", ErrNoUser, n)
		}
	}
	a.mu.Unlock()
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		g, ok := m[group]
		if !ok {
			return ErrNoGroup
		}
		g.Admins = uniq(admins)
		m[group] = g
		return nil
	})
}

func uniq(l []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range l {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

type ImportResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Errors  []string `json:"errors"`
}

// ImportCSV: Zeilen "name;passwort;gruppe1,gruppe2[;mail[;chat-url]]" (Trenner ; oder Tab, # = Kommentar).
// Globaler Admin: beliebige Gruppen (create=true legt unbekannte an, Bereich files); Gruppen-Admin: nur eigene Gruppen.
// Passwörter werden parallel gehasht, users.json wird nur einmal geschrieben.
type ImportOpts struct {
	Create, Update bool
	Template       string // Vorlage für neu angelegte Gruppen (leer: nur Bereich files)
}

func (a *Auth) ImportCSV(ctx context.Context, csv string, o ImportOpts) ImportResult {
	create, update := o.Create, o.Update
	type rowT struct {
		line        int
		name, pw, h string
		groups      []string
		mail, chat  string // optionale Felder 4 und 5; leer = unverändert
	}
	res := ImportResult{Errors: []string{}}
	a.refresh(ctx)
	a.mu.Lock()
	known := map[string]bool{}
	for g := range a.groups {
		known[g] = true
	}
	a.mu.Unlock()
	newGroups := map[string]bool{}
	var rows []*rowT
	for i, ln := range strings.Split(csv, "\n") {
		ln = strings.TrimSpace(strings.TrimPrefix(ln, "\ufeff"))
		if ln == "" || ln[0] == '#' {
			continue
		}
		sep := ";"
		if !strings.Contains(ln, ";") {
			sep = "\t"
		}
		f := strings.Split(ln, sep)
		if len(f) < 3 {
			res.Errors = append(res.Errors, fmt.Sprintf("line %d: name;password;groups expected", i+1))
			continue
		}
		for k := range f {
			f[k] = strings.Trim(strings.TrimSpace(f[k]), `"`) // Excel setzt Felder ggf. in Anführungszeichen
		}
		r := &rowT{line: i + 1, name: f[0], pw: f[1]}
		bad := ""
		if len(f) > 3 {
			r.mail = f[3]
		}
		if len(f) > 4 {
			r.chat = f[4]
		}
		if m, c, err := cleanContact(r.mail, r.chat); err != nil {
			bad = err.Error()
		} else {
			r.mail, r.chat = m, c
		}
		for _, g := range strings.Split(f[2], ",") {
			if g = strings.TrimSpace(g); g == "" {
				continue
			}
			switch {
			case !validName.MatchString(g):
				bad = "bad group name " + g
			case !IsAdmin(ctx) && !CanManage(ctx, g):
				bad = "not admin of group " + g
			case !known[g] && !(create && IsAdmin(ctx)):
				bad = "unknown group " + g
			default:
				r.groups = append(r.groups, g)
			}
		}
		switch {
		case bad != "":
			res.Errors = append(res.Errors, fmt.Sprintf("line %d %s: %s", r.line, r.name, bad))
		case len(r.groups) == 0:
			res.Errors = append(res.Errors, fmt.Sprintf("line %d %s: group required", r.line, r.name))
		case !validName.MatchString(r.name):
			res.Errors = append(res.Errors, fmt.Sprintf("line %d %s: %v", r.line, r.name, ErrBadName))
		case r.pw != "" && (len(r.pw) < minPass || len(r.pw) > maxPass): // leer = unverändert (nur vorhandene Benutzer)
			res.Errors = append(res.Errors, fmt.Sprintf("line %d %s: %v", r.line, r.name, ErrBadPass))
		default:
			for _, g := range r.groups {
				if !known[g] {
					newGroups[g] = true
				}
			}
			rows = append(rows, r)
		}
	}
	// parallel hashen
	var wg sync.WaitGroup
	ch := make(chan *rowT)
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range ch {
				if r.pw == "" {
					continue
				}
				h, err := bcrypt.GenerateFromPassword([]byte(r.pw), bcrypt.DefaultCost)
				if err == nil {
					r.h = string(h)
				}
			}
		}()
	}
	for _, r := range rows {
		ch <- r
	}
	close(ch)
	wg.Wait()
	if len(newGroups) > 0 {
		if err := a.mutateGroups(ctx, func(m map[string]Group) error {
			for g := range newGroups {
				if _, ok := m[g]; !ok {
					m[g] = groupFrom(o.Template)
				}
			}
			return nil
		}); err != nil {
			res.Errors = append(res.Errors, "groups: "+err.Error())
			return res
		}
		if t, ok := Templates[o.Template]; ok && t.Cal != "" && OnNewGroup != nil {
			for g := range newGroups {
				OnNewGroup(ctx, g, t.Cal)
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	base := len(res.Errors)
	err := a.mutate(ctx, func(m map[string]Account) error {
		res.Created, res.Updated, res.Errors = 0, 0, res.Errors[:base] // bei ETag-Retry neu zählen
		for _, r := range rows {
			g := uniq(r.groups)
			u, ex := m[r.name]
			switch {
			case ex && !update:
				res.Errors = append(res.Errors, fmt.Sprintf("line %d %s: exists", r.line, r.name))
			case ex:
				if !IsAdmin(ctx) {
					own := AdminOf(ctx)
					if u.Admin || !shares(effGroups(u), own) {
						res.Errors = append(res.Errors, fmt.Sprintf("line %d %s: not your user", r.line, r.name))
						continue
					}
					var keep []string // Gruppen außerhalb der eigenen bleiben unberührt
					for _, x := range effGroups(u) {
						if !contains(own, x) {
							keep = append(keep, x)
						}
					}
					g = uniq(append(keep, g...))
				}
				u.Groups = g
				if r.h != "" {
					u.Hash, u.Must = r.h, true
				}
				if r.mail != "" {
					u.Mail = r.mail
				}
				if r.chat != "" {
					u.Chat = r.chat
				}
				m[r.name] = u
				res.Updated++
			case r.h == "":
				res.Errors = append(res.Errors, fmt.Sprintf("line %d %s: password required for new user", r.line, r.name))
			default:
				m[r.name] = Account{Hash: r.h, Created: now, Groups: g, Must: true, Mail: r.mail, Chat: r.chat}
				res.Created++
			}
		}
		return nil
	})
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
	}
	return res
}

func groupFrom(tpl string) Group {
	t, ok := Templates[tpl]
	if !ok {
		return Group{Areas: []string{"files"}}
	}
	return Group{Areas: append([]string{}, t.Areas...), Read: append([]string{}, t.Read...), Folder: t.Folder}
}

func csvq(s string) string { // Excel-tauglich: Felder mit ; " oder Zeilenumbruch quoten
	if strings.ContainsAny(s, ";\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

func csvOut(w http.ResponseWriter, name string, lines []string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Write([]byte("\ufeff" + strings.Join(lines, "\r\n") + "\r\n")) // BOM: Excel erkennt UTF-8; Trenner ; wie in DE-Excel
}

func (a *Auth) exportRoutes(mux *http.ServeMux) {
	// Benutzer im Importformat name;;gruppen (Admin: alle, Gruppen-Admin: Mitglieder seiner Gruppen). Keine Passwörter.
	mux.Handle("GET /api/users/export", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.refresh(r.Context())
		ao := AdminOf(r.Context())
		if !IsAdmin(r.Context()) && len(ao) == 0 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		a.mu.Lock()
		var names []string
		for n, u := range a.users {
			if IsAdmin(r.Context()) || (!u.Admin && shares(effGroups(u), ao)) {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		lines := []string{"# name;password;groups;mail;chat-url  (empty = unchanged; mail and chat-url optional; new/changed passwords must be changed at first login)"}
		for _, n := range names {
			lines = append(lines, csvq(n)+";;"+csvq(strings.Join(effGroups(a.users[n]), ","))+";"+csvq(a.users[n].Mail)+";"+csvq(a.users[n].Chat))
		}
		a.mu.Unlock()
		csvOut(w, "users.csv", lines)
	})))
	// Gruppen (nur globaler Admin): group;areas;admins;members
	mux.Handle("GET /api/groups/export", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		a.refresh(r.Context())
		a.mu.Lock()
		mem := map[string][]string{}
		for un, u := range a.users {
			for _, g := range effGroups(u) {
				mem[g] = append(mem[g], un)
			}
		}
		var names []string
		for n := range a.groups {
			names = append(names, n)
		}
		sort.Strings(names)
		lines := []string{"group;edit;read;admins;members"}
		for _, n := range names {
			sort.Strings(mem[n])
			lines = append(lines, csvq(n)+";"+strings.Join(a.groups[n].Areas, ",")+";"+strings.Join(a.groups[n].Read, ",")+";"+csvq(strings.Join(a.groups[n].Admins, ","))+";"+csvq(strings.Join(mem[n], ",")))
		}
		a.mu.Unlock()
		csvOut(w, "groups.csv", lines)
	})))
}

// Migrate: einmalig beim Start. Die frühere Standardgruppe "users" heißt jetzt "alluser" (Gruppen + Mitgliedschaften);
// fehlt die Standardgruppe ganz, wird sie angelegt.
func (a *Auth) Migrate(ctx context.Context) error {
	renamed := false
	err := a.mutateGroups(ctx, func(m map[string]Group) error {
		if _, ok := m[DefaultGroup]; !ok {
			if g, old := m[legacyDefault]; old {
				m[DefaultGroup] = g
				delete(m, legacyDefault)
				renamed = true
			} else {
				m[DefaultGroup] = Group{Areas: append([]string{}, Areas...)}
			}
		}
		return nil
	})
	if err != nil || !renamed {
		return err
	}
	return a.mutate(ctx, func(m map[string]Account) error {
		for n, u := range m {
			for i, g := range u.Groups {
				if g == legacyDefault {
					u.Groups[i] = DefaultGroup
				}
			}
			m[n] = u
		}
		return nil
	})
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
