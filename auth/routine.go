package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cs-team/store"
)

// Routinen für globale Admins (Stufe "Assistent"): Jahrgangswechsel.
// Ablauf: Vorschau (rechnet nur) -> Ausführen mit Passwort und Plan-Prüfsumme -> Sicherung -> Rückgängig.
// Die KI darf höchstens Parameter vorschlagen; ausgeführt wird nur, was der Server selbst als Vorschau berechnet hat.

const logKey = "users/routine-log.json"

// YearParams: Einstellungen des Jahrgangswechsels.
type YearParams struct {
	Groups  []string            `json:"groups"`  // leer = alle Gruppen mit Zahl im Namen
	Leave   string              `json:"leave"`   // Gruppen ohne Folgegruppe: ""/"keep" unverändert | "archive" | "disable" | "remove"
	Archive string              `json:"archive"` // Zielgruppe für Abgänger (leave=archive), wird bei Bedarf angelegt
	Stay    map[string][]string `json:"stay"`    // je Gruppe die bleibenden Benutzer (fehlt = Merkliste der Gruppe)

	Mode      string `json:"mode"`      // ""/"members": Mitglieder wechseln | "rename": Gruppe wird umbenannt (Daten wandern mit)
	Carry     Carry  `json:"carry"`     // Modus rename: was neben dem Gruppenordner mitwandert
	NewAdmins bool   `json:"newAdmins"` // Modus rename: Gruppen-Admins auch in die neu angelegte Gruppe
	Pattern   string `json:"pattern"`   // Modus rename: Name der Abgangsgruppe, {name} und {year}
}

// Carry: Bereiche, die beim Umbenennen mit der Gruppe wandern (Gruppenordner und Freigaben immer).
type Carry struct {
	Cal   bool `json:"cal"`
	Chat  bool `json:"chat"`
	Tasks bool `json:"tasks"`
}

type PlanItem struct {
	User  string `json:"user"`
	Stay  bool   `json:"stay"`
	Fixed bool   `json:"fixed,omitempty"` // bleibt immer (globaler Admin, Gruppen-Admin)
	Note  string `json:"note,omitempty"`
}

type PlanGroup struct {
	Group  string     `json:"group"`
	To     string     `json:"to,omitempty"`
	Kind   string     `json:"kind"` // move | leave | skip | rename | blocked
	Exists bool       `json:"exists"`
	Note   string     `json:"note,omitempty"`
	Items  []PlanItem `json:"items,omitempty"`
	Move   int        `json:"move"`
	Stay   int        `json:"stay"`
	Total  int        `json:"total"`
}

type Plan struct {
	Routine  string      `json:"routine"`
	Leave    string      `json:"leave"`
	Archive  string      `json:"archive,omitempty"`
	Groups   []PlanGroup `json:"groups"`
	Create   []string    `json:"create,omitempty"` // Gruppen, die angelegt werden
	Warnings []string    `json:"warnings,omitempty"`
	Moves    int         `json:"moves"`
	Hash     string      `json:"hash"`
	Mode     string      `json:"mode,omitempty"`
	Carry    Carry       `json:"carry"`
	Pairs    []Pair      `json:"pairs,omitempty"`
	Repeat   int         `json:"repeat,omitempty"`
	effects  map[string]effect
	newRec   map[string]Group
}

// Pair: Gruppe "From" heißt danach "To" (Reihenfolge = Ausführungsreihenfolge, höchste Stufe zuerst).
type Pair struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Archive bool   `json:"archive,omitempty"`
}

type effect struct {
	remove, add map[string]bool
	disable     bool
}

var reNum = regexp.MustCompile(`^(.*?)(\d{1,4})(\D.*)?$`)

// NextName: Folgegruppe "5a" -> "6a", "09" -> "10", "klasse9b" -> "klasse10b".
func NextName(n string) (string, bool) {
	m := reNum.FindStringSubmatch(n)
	if m == nil {
		return "", false
	}
	v, _ := strconv.Atoi(m[2])
	d := strconv.Itoa(v + 1)
	if m[2][0] == '0' && len(d) < len(m[2]) {
		d = strings.Repeat("0", len(m[2])-len(d)) + d
	}
	r := m[1] + d + m[3]
	return r, validName.MatchString(r)
}

func sortKey(n string) (string, int, string) {
	m := reNum.FindStringSubmatch(n)
	v, _ := strconv.Atoi(m[2])
	return m[1], v, m[3]
}

// planYear berechnet die Vorschau aus dem aktuellen Stand; verändert nichts.
func (a *Auth) planYear(ctx context.Context, p YearParams) (*Plan, error) {
	if p.Mode == "rename" {
		return a.planRename(ctx, p)
	}
	if p.Mode != "" && p.Mode != "members" {
		return nil, errors.New("mode: members | rename")
	}
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch p.Leave {
	case "", "keep", "archive", "disable", "remove":
	default:
		return nil, errors.New("leave: keep | archive | disable | remove")
	}
	if p.Leave == "" {
		p.Leave = "keep"
	}
	pl := &Plan{Routine: "yearchange", Leave: p.Leave, effects: map[string]effect{}}
	if p.Leave == "archive" {
		p.Archive = strings.TrimSpace(p.Archive)
		if !validName.MatchString(p.Archive) || p.Archive == DefaultGroup {
			return nil, errors.New("archive group: name a-z 0-9 . _ -")
		}
		pl.Archive = p.Archive
		if _, ok := a.groups[p.Archive]; !ok {
			pl.Create = append(pl.Create, p.Archive)
		}
	}
	sel := map[string]bool{}
	for _, g := range p.Groups {
		sel[g] = true
	}
	var names []string
	for n := range a.groups {
		if n == DefaultGroup || reNum.FindStringSubmatch(n) == nil || (len(sel) > 0 && !sel[n]) {
			continue
		}
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		p1, n1, s1 := sortKey(names[i])
		p2, n2, s2 := sortKey(names[j])
		if p1 != p2 {
			return p1 < p2
		}
		if s1 != s2 {
			return s1 < s2
		}
		return n1 < n2
	})
	for _, n := range names {
		if n == pl.Archive {
			return nil, errors.New("archive group must not be one of the groups of the year change: " + n)
		}
	}
	eff := func(u string) effect {
		e, ok := pl.effects[u]
		if !ok {
			e = effect{remove: map[string]bool{}, add: map[string]bool{}}
			pl.effects[u] = e
		}
		return e
	}
	for _, n := range names {
		g := a.groups[n]
		to, _ := NextName(n)
		_, exists := a.groups[to]
		pg := PlanGroup{Group: n, To: to, Exists: exists, Kind: "skip"}
		var members []string
		for un, u := range a.users {
			for _, x := range effGroups(u) {
				if x == n {
					members = append(members, un)
				}
			}
		}
		sort.Strings(members)
		pg.Total = len(members)
		switch {
		case exists:
			pg.Kind = "move"
		case p.Leave != "keep":
			pg.Kind = "leave"
			pg.To = ""
			if p.Leave == "archive" {
				pg.To = p.Archive
			}
		default:
			pg.To = ""
		}
		if pg.Kind == "skip" {
			pl.Groups = append(pl.Groups, pg)
			continue
		}
		stay := map[string]bool{}
		src := g.Stay
		if l, ok := p.Stay[n]; ok {
			src = l
		}
		isMember := map[string]bool{}
		for _, m := range members {
			isMember[m] = true
		}
		for _, s := range src {
			if isMember[s] {
				stay[s] = true
			} else if _, ok := p.Stay[n]; ok {
				pl.Warnings = append(pl.Warnings, n+": "+s+" is not a member (ignored)")
			}
		}
		admins := map[string]bool{}
		for _, x := range g.Admins {
			admins[x] = true
		}
		for _, m := range members {
			u := a.users[m]
			it := PlanItem{User: m, Stay: stay[m]}
			switch {
			case u.Admin:
				it.Stay, it.Fixed, it.Note = true, true, "admin"
			case admins[m]:
				it.Stay, it.Fixed, it.Note = true, true, "group admin"
			}
			if u.Disabled {
				it.Note = strings.TrimSpace(it.Note + " disabled")
			}
			pg.Items = append(pg.Items, it)
			if it.Stay {
				pg.Stay++
				continue
			}
			pg.Move++
			e := eff(m)
			switch {
			case pg.Kind == "move":
				e.remove[n], e.add[to] = true, true
			case p.Leave == "archive":
				e.remove[n], e.add[p.Archive] = true, true
			case p.Leave == "remove":
				e.remove[n] = true
			case p.Leave == "disable":
				e.disable = true
			}
			pl.effects[m] = e
		}
		pl.Moves += pg.Move
		pl.Groups = append(pl.Groups, pg)
	}
	if len(pl.Groups) == 0 {
		pl.Warnings = append(pl.Warnings, "no groups with a number in the name found")
	}
	b, _ := json.Marshal(pl)
	h := sha256.Sum256(b)
	pl.Hash = hex.EncodeToString(h[:8])
	return pl, nil
}

// finalGroups: neue Gruppenliste eines Kontos nach den Änderungen (leer = nur Standardgruppe).
func finalGroups(u Account, e effect) []string {
	set := map[string]bool{}
	for _, g := range effGroups(u) {
		if !e.remove[g] {
			set[g] = true
		}
	}
	for g := range e.add {
		set[g] = true
	}
	var out []string
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

type LogEntry struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Admin   string    `json:"admin"`
	Routine string    `json:"routine"`
	Summary string    `json:"summary"`
	Moved   int       `json:"moved"`
	Undone  bool      `json:"undone,omitempty"`
	Mode    string    `json:"mode,omitempty"`
	Pairs   []Pair    `json:"pairs,omitempty"`
	Created []string  `json:"created,omitempty"`
	Carry   Carry     `json:"carry"`
	Partial bool      `json:"partial,omitempty"`
}

type backup struct {
	Mode    string               `json:"mode,omitempty"`
	Users   map[string]userState `json:"users"`
	Stay    map[string][]string  `json:"stay"`
	Created []string             `json:"created,omitempty"`
}

type userState struct {
	Groups   []string `json:"groups,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

func (a *Auth) log(ctx context.Context) []LogEntry {
	var l []LogEntry
	if b, _, err := a.st.Get(ctx, logKey); err == nil {
		json.Unmarshal(b, &l)
	}
	return l
}

// applyYear führt einen zuvor berechneten Plan aus (Sicherung zuerst).
func (a *Auth) applyYear(ctx context.Context, admin string, pl *Plan) (*LogEntry, error) {
	if pl.Moves == 0 {
		return nil, errors.New("nothing to do")
	}
	id := time.Now().Format("20060102-150405")
	if err := runSnapshot(id); err != nil {
		return nil, fmt.Errorf("snapshot command failed, nothing was changed: %w", err)
	}
	if pl.Mode == "rename" {
		return a.applyRename(ctx, admin, pl, id)
	}
	bk := backup{Users: map[string]userState{}, Stay: map[string][]string{}, Created: pl.Create}
	a.refresh(ctx)
	a.mu.Lock()
	for u := range pl.effects {
		acc := a.users[u]
		bk.Users[u] = userState{Groups: append([]string{}, acc.Groups...), Disabled: acc.Disabled}
	}
	for _, g := range pl.Groups {
		if g.Kind != "skip" {
			bk.Stay[g.Group] = append([]string{}, a.groups[g.Group].Stay...)
		}
	}
	a.mu.Unlock()
	b, _ := json.Marshal(bk)
	if _, err := a.st.Put(ctx, "users/routine-"+id+".json", b, "*"); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	if err := a.mutateGroups(ctx, func(m map[string]Group) error {
		for _, c := range pl.Create {
			if _, ok := m[c]; !ok {
				m[c] = Group{Areas: []string{}}
			}
		}
		for _, g := range pl.Groups {
			if g.Kind == "skip" {
				continue
			}
			x := m[g.Group]
			x.Stay = nil
			m[g.Group] = x
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := a.mutate(ctx, func(m map[string]Account) error {
		for un, e := range pl.effects {
			u, ok := m[un]
			if !ok {
				continue
			}
			if e.disable {
				u.Disabled = true
			} else {
				u.Groups = finalGroups(u, e)
			}
			m[un] = u
		}
		return nil
	}); err != nil {
		return nil, err
	}
	en := LogEntry{ID: id, Time: time.Now(), Admin: admin, Routine: pl.Routine, Moved: pl.Moves,
		Summary: fmt.Sprintf("%d groups, %d accounts changed, leave=%s", len(pl.Groups), pl.Moves, pl.Leave)}
	return &en, a.appendLog(ctx, en)
}

func (a *Auth) appendLog(ctx context.Context, en LogEntry) error {
	return store.Update(ctx, a.st, logKey, func(cur []byte) ([]byte, error) {
		var l []LogEntry
		if cur != nil {
			json.Unmarshal(cur, &l)
		}
		l = append(l, en)
		if len(l) > 100 {
			l = l[len(l)-100:]
		}
		return json.Marshal(l)
	})
}

var errUndo = errors.New("only the latest routine that is not undone can be reverted")

// undo stellt Gruppen, Sperrstatus und Merklisten der betroffenen Konten aus der Sicherung wieder her.
func (a *Auth) undo(ctx context.Context, id string) error {
	l := a.log(ctx)
	idx := -1
	for i := len(l) - 1; i >= 0; i-- {
		if !l[i].Undone {
			idx = i
			break
		}
	}
	if idx < 0 || l[idx].ID != id {
		return errUndo
	}
	b, _, err := a.st.Get(ctx, "users/routine-"+id+".json")
	if err != nil {
		return err
	}
	var bk backup
	if json.Unmarshal(b, &bk) != nil {
		return errors.New("backup unreadable")
	}
	if l[idx].Mode == "rename" {
		return a.undoRename(ctx, l[idx], bk)
	}
	a.refresh(ctx)
	a.mu.Lock()
	have := map[string]bool{}
	for g := range a.groups {
		have[g] = true
	}
	a.mu.Unlock()
	if err := a.mutate(ctx, func(m map[string]Account) error {
		for un, st := range bk.Users {
			u, ok := m[un]
			if !ok {
				continue
			}
			var gs []string
			for _, g := range st.Groups {
				if have[g] {
					gs = append(gs, g)
				}
			}
			u.Groups, u.Disabled = gs, st.Disabled
			m[un] = u
		}
		return nil
	}); err != nil {
		return err
	}
	if err := a.mutateGroups(ctx, func(m map[string]Group) error {
		for g, s := range bk.Stay {
			if x, ok := m[g]; ok {
				x.Stay = s
				m[g] = x
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return store.Update(ctx, a.st, logKey, func(cur []byte) ([]byte, error) {
		var l []LogEntry
		json.Unmarshal(cur, &l)
		for i := range l {
			if l[i].ID == id {
				l[i].Undone = true
			}
		}
		return json.Marshal(l)
	})
}

// confirm: Passwort des angemeldeten Admins erneut prüfen (mit Sperre gegen Raten).
func (a *Auth) confirm(r *http.Request, pass string) (int, string) {
	name := User(r.Context())
	key := "act|" + name + "|" + a.ip(r)
	if a.locked(key) {
		return http.StatusTooManyRequests, "too many attempts"
	}
	if _, ok := a.verify(r.Context(), name, pass); !ok {
		a.failed(key)
		return http.StatusForbidden, "wrong password"
	}
	a.ok(key)
	return 0, ""
}

func (a *Auth) routineRoutes(mux *http.ServeMux, adm func(http.HandlerFunc) http.Handler) {
	dec := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v) != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return false
		}
		return true
	}
	mux.Handle("POST /api/routines/yearchange/preview", adm(func(w http.ResponseWriter, r *http.Request) {
		var p YearParams
		if !dec(w, r, &p) {
			return
		}
		pl, err := a.planYear(r.Context(), p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(pl)
	}))
	mux.Handle("POST /api/routines/yearchange/run", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Params   YearParams `json:"params"`
			Hash     string     `json:"hash"`
			Password string     `json:"password"`
			NoSnap   bool       `json:"noSnapshot"`
		}
		if !dec(w, r, &in) {
			return
		}
		if SnapshotMode() == "none" && !in.NoSnap {
			http.Error(w, "no snapshot possible - confirm to run without snapshot (noSnapshot) or set CS_SNAPSHOT_CMD", http.StatusBadRequest)
			return
		}
		if code, msg := a.confirm(r, in.Password); code != 0 {
			http.Error(w, msg, code)
			return
		}
		pl, err := a.planYear(r.Context(), in.Params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if in.Hash == "" || pl.Hash != in.Hash {
			http.Error(w, "the data changed since the preview - please check the preview again", http.StatusConflict)
			return
		}
		en, err := a.applyYear(r.Context(), User(r.Context()), pl)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("routine: yearchange by %s: %d accounts, backup %s", en.Admin, en.Moved, en.ID)
		json.NewEncoder(w).Encode(en)
	}))
	mux.Handle("GET /api/routines/info", adm(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"snapshot": SnapshotMode(), "dataset": SnapshotInfo})
	}))
	mux.Handle("GET /api/routines/log", adm(func(w http.ResponseWriter, r *http.Request) {
		l := a.log(r.Context())
		if l == nil {
			l = []LogEntry{}
		}
		json.NewEncoder(w).Encode(l)
	}))
	mux.Handle("POST /api/routines/undo/{id}", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Password string }
		if !dec(w, r, &in) {
			return
		}
		if code, msg := a.confirm(r, in.Password); code != 0 {
			http.Error(w, msg, code)
			return
		}
		if err := a.undo(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("routine: undo %s by %s", r.PathValue("id"), User(r.Context()))
	}))
	// Wiederholer-Merkliste: Gruppen-Admin der Gruppe oder globaler Admin
	mux.Handle("POST /api/groups/{name}/stay", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := r.PathValue("name")
		if !CanManage(r.Context(), n) || n == DefaultGroup {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var in struct{ Stay []string }
		if !dec(w, r, &in) {
			return
		}
		a.refresh(r.Context())
		a.mu.Lock()
		_, ok := a.groups[n]
		member := map[string]bool{}
		for un, u := range a.users {
			for _, x := range effGroups(u) {
				if x == n {
					member[un] = true
				}
			}
		}
		a.mu.Unlock()
		if !ok {
			fail_(w, ErrNoGroup)
			return
		}
		var out []string
		for _, s := range uniq(in.Stay) {
			if member[s] {
				out = append(out, s)
			}
		}
		sort.Strings(out)
		if err := a.mutateGroups(r.Context(), func(m map[string]Group) error {
			g := m[n]
			g.Stay = out
			m[n] = g
			return nil
		}); err != nil {
			fail_(w, err)
		}
	})))
}
