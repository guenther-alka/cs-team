// Package tasks: Aufgaben (Ticketsystem light). Eine Aufgabe hat Auftraggeber, optional Gruppe, Bearbeiter (oder leer =
// "bitte bearbeiten"), Beteiligte, Status open -> doing -> done -> closed, Fälligkeit, Meilensteine (Text + Datum),
// einen Verlauf aus Kommentaren und Systemzeilen, Verweise auf andere Aufgaben und eine Wiederholung.
// Änderungen werden per WebSocket an alle Beteiligten geschoben (nur Server -> Client); Aktionen laufen über REST.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"cs-team/auth"
	"cs-team/store"
)

const (
	layout  = "2006-01-02"
	maxLog  = 500
	maxText = 4000
)

var reID = regexp.MustCompile(`^[0-9a-z]{1,16}$`)

type Mile struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
	Due  string `json:"due,omitempty"`
	Done bool   `json:"done,omitempty"`
	Nt   bool   `json:"nt,omitempty"` // Fälligkeit gemeldet
}

// Entry: Kommentar (Sys=false) oder Systemzeile (Sys=true: T = Code, A = Argumente; die Oberfläche übersetzt).
type Entry struct {
	ID  int64    `json:"id"`
	By  string   `json:"by"`
	T   string   `json:"t"`
	A   []string `json:"a,omitempty"`
	Sys bool     `json:"sys,omitempty"`
	Ed  bool     `json:"ed,omitempty"`
	Del bool     `json:"del,omitempty"`
}

type Task struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Desc     string   `json:"desc,omitempty"`
	By       string   `json:"by"`
	Group    string   `json:"group,omitempty"`
	Assignee string   `json:"assignee,omitempty"`
	Watch    []string `json:"watch,omitempty"`
	Req      bool     `json:"req,omitempty"` // Anfrage („Bitte bearbeiten“) eines Mitglieds
	Status   string   `json:"status"`        // open | doing | done | closed
	Prio     int      `json:"prio"`          // 0 niedrig, 1 normal, 2 hoch
	Due      string   `json:"due,omitempty"`
	Repeat   string   `json:"repeat,omitempty"` // daily | weekly | monthly | yearly
	Miles    []Mile   `json:"miles,omitempty"`
	Link     []string `json:"link,omitempty"`
	Created  int64    `json:"created"`
	Updated  int64    `json:"updated"` // ms
	Next     string   `json:"next,omitempty"`
	DueNt    bool     `json:"duent,omitempty"`
	Log      []Entry  `json:"log,omitempty"`
	N        int      `json:"n,omitempty"`    // nur Ansicht
	Last     *Entry   `json:"last,omitempty"` // nur Ansicht
}

type View struct {
	Task
	Own  bool `json:"own"`
	Take bool `json:"take"`
}

type client struct {
	user string
	out  chan []byte
}

type Svc struct {
	St     store.Store
	Notify func(user, subject, text string) // optional: E-Mail/Webhook
	Base   func() string                    // optional: öffentliche Adresse für Links in Benachrichtigungen
	mu     sync.Mutex
	tasks  map[string]*Task
	conns  map[*client]bool
}

func New(st store.Store) *Svc {
	s := &Svc{St: st, tasks: map[string]*Task{}, conns: map[*client]bool{}}
	ctx := context.Background()
	infos, _ := st.List(ctx, "tasks/")
	for _, i := range infos {
		if b, _, err := st.Get(ctx, i.Key); err == nil {
			var t Task
			if json.Unmarshal(b, &t) == nil && t.ID != "" {
				s.tasks[t.ID] = &t
			}
		}
	}
	return s
}

var (
	ErrForbidden = errors.New("not allowed")
	ErrNotFound  = errors.New("no such task")
	ErrBad       = errors.New("invalid input")
)

func key(id string) string { return "tasks/" + id + ".json" }
func contains(l []string, x string) bool {
	for _, v := range l {
		if v == x {
			return true
		}
	}
	return false
}

// ---- Rechte ----

func (s *Svc) canSee(user string, t *Task) bool {
	if user == t.By || user == t.Assignee || contains(t.Watch, user) {
		return true
	}
	if t.Group != "" {
		if info, ok := auth.GroupInfoOf(t.Group); ok && info.Tasks != auth.ModeOff && auth.IsMember(user, t.Group) {
			return true
		}
	}
	return auth.IsAdminUser(user)
}

func isOwner(user string, t *Task) bool {
	return user == t.By || auth.IsAdminUser(user) || (t.Group != "" && auth.IsGroupAdmin(user, t.Group))
}

func canCreate(user, group string) bool {
	ok, _ := createRight(user, group)
	return ok
}

// createRight: darf user in group Aufgaben anlegen, und ist es dann eine Anfrage ("Bitte bearbeiten": Mitglied in einer
// Gruppe mit Modus „nur Gruppen-Admins“, ohne Zuständigen, für alle Gruppenmitglieder sichtbar und übernehmbar)?
func createRight(user, group string) (ok, req bool) {
	if _, _, c := auth.ContactOf(user); !c {
		return false, false
	}
	if group == "" {
		return true, false
	}
	info, found := auth.GroupInfoOf(group)
	return rights(found, info.Tasks, auth.IsAdminUser(user), auth.IsMember(user, group), auth.IsGroupAdmin(user, group))
}

func rights(found bool, mode string, admin, member, gadmin bool) (ok, req bool) {
	if !found || mode == auth.ModeOff {
		return false, false
	}
	if admin { // globale Admins dürfen für jede Gruppe mit aktivierten Aufgaben anlegen
		return true, false
	}
	if !member {
		return false, false
	}
	if mode == auth.ModeMember || gadmin {
		return true, false
	}
	return true, true // Modus „nur Gruppen-Admins“: Mitglieder stellen eine Anfrage
}

func (s *Svc) view(user string, t *Task, full bool) View {
	c := *t
	c.N = len(t.Log)
	if !full {
		c.Log = nil
		if c.N > 0 {
			l := t.Log[c.N-1]
			c.Last = &l
		}
	}
	return View{Task: c, Own: isOwner(user, t), Take: t.Assignee == "" && t.Status == "open" && s.canSee(user, t)}
}

// ---- Validierung ----

func validDate(d string) bool {
	if d == "" {
		return true
	}
	_, err := time.Parse(layout, d)
	return err == nil
}

type meta struct {
	Title    string   `json:"title"`
	Desc     string   `json:"desc"`
	Group    string   `json:"group"`
	Assignee string   `json:"assignee"`
	Watch    []string `json:"watch"`
	Prio     int      `json:"prio"`
	Due      string   `json:"due"`
	Repeat   string   `json:"repeat"`
	Miles    []Mile   `json:"miles"`
	Link     []string `json:"link"`
}

func (s *Svc) check(user string, m *meta, group string) error {
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" || utf8.RuneCountInString(m.Title) > 200 || utf8.RuneCountInString(m.Desc) > 10000 {
		return ErrBad
	}
	if m.Prio < 0 || m.Prio > 2 || !validDate(m.Due) || len(m.Miles) > 30 || len(m.Watch) > 20 || len(m.Link) > 10 {
		return ErrBad
	}
	switch m.Repeat {
	case "", "daily", "weekly", "monthly", "yearly":
	default:
		return ErrBad
	}
	if m.Repeat != "" && m.Due == "" {
		return errors.New("repeat needs a due date")
	}
	for i := range m.Miles {
		m.Miles[i].Text = strings.TrimSpace(m.Miles[i].Text)
		if m.Miles[i].Text == "" || utf8.RuneCountInString(m.Miles[i].Text) > 200 || !validDate(m.Miles[i].Due) {
			return ErrBad
		}
	}
	for _, l := range m.Link {
		if !reID.MatchString(l) {
			return ErrBad
		}
	}
	peers := auth.PeersOf(user)
	okUser := func(n string) bool {
		if group != "" && !auth.IsMember(n, group) {
			return false
		}
		_, _, act := auth.ContactOf(n)
		return act && (auth.IsAdminUser(user) || contains(peers, n))
	}
	if m.Assignee != "" && !okUser(m.Assignee) {
		return errors.New("bad assignee")
	}
	seen := map[string]bool{}
	w := m.Watch[:0:0]
	for _, n := range m.Watch {
		if n == "" || seen[n] || n == m.Assignee {
			continue
		}
		seen[n] = true
		if _, _, act := auth.ContactOf(n); !act || !(auth.IsAdminUser(user) || contains(peers, n)) {
			return errors.New("bad participant")
		}
		w = append(w, n)
	}
	m.Watch = w
	return nil
}

// ---- Wiederholung ----

func addMonths(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, time.UTC)
}

func step(t time.Time, rep string) time.Time {
	switch rep {
	case "daily":
		return t.AddDate(0, 0, 1)
	case "weekly":
		return t.AddDate(0, 0, 7)
	case "monthly":
		return addMonths(t, 1)
	default:
		return addMonths(t, 12)
	}
}

// nextDue: nächster Termin nach base, nicht vor heute. Liefert auch die Verschiebung in Tagen.
func nextDue(base time.Time, rep string, today time.Time) (time.Time, int) {
	n := step(base, rep)
	for i := 0; i < 2000 && n.Before(today); i++ {
		n = step(n, rep)
	}
	return n, int(n.Sub(base).Hours() / 24)
}

// spawn legt nach der Abnahme die nächste Aufgabe an (einmal je Aufgabe).
func (s *Svc) spawn(old *Task, now time.Time) *Task {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	base := today
	if d, err := time.Parse(layout, old.Due); err == nil {
		base = d
	}
	nd, days := nextDue(base, old.Repeat, today)
	t := &Task{ID: s.newID(), Title: old.Title, Desc: old.Desc, By: old.By, Group: old.Group, Assignee: old.Assignee,
		Watch: append([]string{}, old.Watch...), Status: "open", Prio: old.Prio, Due: nd.Format(layout), Repeat: old.Repeat,
		Link: []string{old.ID}, Created: now.Unix(), Updated: now.UnixMilli()}
	for i, m := range old.Miles {
		nm := Mile{ID: i + 1, Text: m.Text}
		if d, err := time.Parse(layout, m.Due); err == nil {
			nm.Due = d.AddDate(0, 0, days).Format(layout)
		}
		t.Miles = append(t.Miles, nm)
	}
	sys(t, old.By, "from", old.ID)
	s.tasks[t.ID] = t
	return t
}

func (s *Svc) newID() string {
	n := time.Now().UnixMicro()
	for {
		id := strconv.FormatInt(n, 36)
		if _, ok := s.tasks[id]; !ok {
			return id
		}
		n++
	}
}

var lastEntry int64

func sys(t *Task, by, code string, args ...string) {
	addEntry(t, Entry{By: by, T: code, A: args, Sys: true})
}
func addEntry(t *Task, e Entry) {
	id := time.Now().UnixMicro()
	if id <= lastEntry {
		id = lastEntry + 1
	}
	lastEntry = id
	e.ID = id
	t.Log = append(t.Log, e)
	if len(t.Log) > maxLog {
		t.Log = t.Log[len(t.Log)-maxLog:]
	}
	t.Updated = time.Now().UnixMilli()
}

// ---- Speichern, Benachrichtigen ----

func (s *Svc) save(t *Task) {
	b, _ := json.Marshal(t)
	s.St.Put(context.Background(), key(t.ID), b, "")
}

func (s *Svc) push(t *Task) {
	for c := range s.conns {
		var m map[string]any
		if s.canSee(c.user, t) {
			m = map[string]any{"t": "upd", "task": s.view(c.user, t, false)}
		} else {
			m = map[string]any{"t": "del", "id": t.ID}
		}
		b, _ := json.Marshal(m)
		select {
		case c.out <- b:
		default:
		}
	}
}

func (s *Svc) pushDel(t *Task) {
	b, _ := json.Marshal(map[string]any{"t": "del", "id": t.ID})
	for c := range s.conns {
		select {
		case c.out <- b:
		default:
		}
	}
}

// ext: E-Mail/Webhook an einen Benutzer (nicht an den Auslöser), asynchron.
func (s *Svc) ext(actor, to string, t *Task, what string) {
	if s.Notify == nil || to == "" || to == actor {
		return
	}
	subj := "Aufgabe: " + t.Title
	text := what + "\n\n" + t.Title + " (#" + t.ID + ")"
	if t.Due != "" {
		text += "\nFällig: " + t.Due
	}
	if s.Base != nil {
		if b := s.Base(); b != "" {
			text += "\n" + b + "/#task/" + t.ID
		}
	}
	go s.Notify(to, subj, text)
}

// ---- Aktionen ----

func (s *Svc) Create(user string, m meta) (*Task, error) {
	ok, req := createRight(user, m.Group)
	if !ok {
		return nil, ErrForbidden
	}
	if req {
		m.Assignee = "" // Anfrage: übernehmen kann jedes Gruppenmitglied
	}
	if err := s.check(user, &m, m.Group); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cnt, now := 0, time.Now()
	for _, t := range s.tasks {
		if t.By == user && now.Unix()-t.Created < 60 {
			cnt++
		}
	}
	if cnt >= 30 {
		return nil, errors.New("too many tasks")
	}
	t := &Task{ID: s.newID(), Title: m.Title, Desc: m.Desc, By: user, Group: m.Group, Assignee: m.Assignee, Watch: m.Watch,
		Req: req, Status: "open", Prio: m.Prio, Due: m.Due, Repeat: m.Repeat, Link: m.Link, Created: now.Unix()}
	for i, ml := range m.Miles {
		t.Miles = append(t.Miles, Mile{ID: i + 1, Text: ml.Text, Due: ml.Due})
	}
	sys(t, user, "new")
	if req {
		sys(t, user, "req")
	}
	if t.Assignee != "" {
		sys(t, user, "assign", t.Assignee)
		s.ext(user, t.Assignee, t, user+" hat dir eine Aufgabe zugewiesen.")
	}
	s.tasks[t.ID] = t
	s.save(t)
	s.push(t)
	return t, nil
}

func (s *Svc) Update(user, id string, m meta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	if t == nil || !s.canSee(user, t) {
		return ErrNotFound
	}
	if !isOwner(user, t) {
		return ErrForbidden
	}
	m.Group = t.Group // Gruppe bleibt
	if t.Req && !auth.IsAdminUser(user) && !auth.IsGroupAdmin(user, t.Group) {
		m.Assignee = t.Assignee // Ersteller einer Anfrage ändert den Text, bestimmt aber keinen Zuständigen
	}
	if err := s.check(user, &m, t.Group); err != nil {
		return err
	}
	old := t.Assignee
	dueOld := t.Due
	t.Title, t.Desc, t.Prio, t.Due, t.Repeat, t.Watch, t.Link = m.Title, m.Desc, m.Prio, m.Due, m.Repeat, m.Watch, m.Link
	t.DueNt = t.DueNt && t.Due != "" && t.Due == dueOld // neues Datum: Fälligkeitsmeldung wieder möglich
	// Meilensteine: vorhandene IDs behalten Erledigt/Meldung, neue bekommen eine ID
	byID, next := map[int]Mile{}, 1
	for _, o := range t.Miles {
		byID[o.ID] = o
		if o.ID >= next {
			next = o.ID + 1
		}
	}
	var nm []Mile
	for _, ml := range m.Miles {
		if o, ok := byID[ml.ID]; ok && ml.ID > 0 {
			o.Nt = o.Nt && o.Due == ml.Due
			o.Text, o.Due = ml.Text, ml.Due
			nm = append(nm, o)
		} else {
			nm = append(nm, Mile{ID: next, Text: ml.Text, Due: ml.Due})
			next++
		}
	}
	t.Miles = nm
	sys(t, user, "edit")
	if m.Assignee != old {
		t.Assignee = m.Assignee
		if t.Assignee == "" && t.Status == "doing" {
			t.Status = "open"
		}
		sys(t, user, "assign", t.Assignee)
		s.ext(user, t.Assignee, t, user+" hat dir eine Aufgabe zugewiesen.")
	}
	s.save(t)
	s.push(t)
	return nil
}

type act struct {
	A      string `json:"a"`
	Status string `json:"status"`
	Mile   int    `json:"mile"`
	Done   bool   `json:"done"`
	Text   string `json:"text"`
	CID    int64  `json:"cid"`
}

func (s *Svc) Act(user, id string, in act) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	if t == nil || !s.canSee(user, t) {
		return nil, ErrNotFound
	}
	owner, assignee := isOwner(user, t), user == t.Assignee
	now := time.Now()
	switch in.A {
	case "take":
		if t.Assignee != "" || t.Status != "open" {
			return nil, ErrForbidden
		}
		t.Assignee, t.Status = user, "doing"
		sys(t, user, "take")
		s.ext(user, t.By, t, user+" hat die Aufgabe übernommen.")
	case "give":
		if !assignee && !owner || t.Assignee == "" || t.Status == "closed" {
			return nil, ErrForbidden
		}
		t.Assignee, t.Status = "", "open"
		sys(t, user, "give")
		s.ext(user, t.By, t, user+" hat die Aufgabe zurückgegeben.")
	case "status":
		st := in.Status
		if st != "open" && st != "doing" && st != "done" && st != "closed" {
			return nil, ErrBad
		}
		if !owner && !(assignee && (st == "doing" || st == "done") && t.Status != "closed") {
			return nil, ErrForbidden
		}
		if st == "doing" && t.Assignee == "" {
			return nil, errors.New("no assignee")
		}
		if st == "done" && owner && t.Assignee == user {
			st = "closed" // Auftraggeber erledigt selbst: gleich abgenommen
		}
		if st == t.Status {
			return t, nil
		}
		t.Status = st
		sys(t, user, "status", st)
		switch st {
		case "done":
			s.ext(user, t.By, t, user+" hat die Aufgabe erledigt – bitte abnehmen.")
		case "closed":
			s.ext(user, t.Assignee, t, "Die Aufgabe wurde abgenommen.")
			if t.Repeat != "" && t.Next == "" {
				n := s.spawn(t, now)
				t.Next = n.ID
				sys(t, user, "repeat", n.ID)
				s.save(n)
				s.push(n)
				s.ext(user, n.Assignee, n, "Wiederkehrende Aufgabe: neue Runde.")
			}
		}
	case "mile":
		if !owner && !assignee {
			return nil, ErrForbidden
		}
		found := false
		for i := range t.Miles {
			if t.Miles[i].ID == in.Mile {
				t.Miles[i].Done = in.Done
				d := "0"
				if in.Done {
					d = "1"
				}
				sys(t, user, "mile", t.Miles[i].Text, d)
				found = true
			}
		}
		if !found {
			return nil, ErrBad
		}
	case "comment":
		txt := strings.TrimSpace(in.Text)
		if txt == "" || utf8.RuneCountInString(txt) > maxText {
			return nil, ErrBad
		}
		addEntry(t, Entry{By: user, T: txt})
		if t.Assignee != user {
			s.ext(user, t.Assignee, t, user+" hat kommentiert:\n"+txt)
		}
		if t.By != user && t.By != t.Assignee {
			s.ext(user, t.By, t, user+" hat kommentiert:\n"+txt)
		}
	case "edit", "delc":
		var e *Entry
		for i := range t.Log {
			if t.Log[i].ID == in.CID && !t.Log[i].Sys && !t.Log[i].Del {
				e = &t.Log[i]
			}
		}
		if e == nil {
			return nil, ErrNotFound
		}
		if in.A == "edit" {
			txt := strings.TrimSpace(in.Text)
			if e.By != user || txt == "" || utf8.RuneCountInString(txt) > maxText {
				return nil, ErrForbidden
			}
			e.T, e.Ed = txt, true
		} else {
			if e.By != user && !owner {
				return nil, ErrForbidden
			}
			e.T, e.Del = "", true
		}
		t.Updated = now.UnixMilli()
	default:
		return nil, ErrBad
	}
	s.save(t)
	s.push(t)
	return t, nil
}

func (s *Svc) Delete(user, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	if t == nil || !s.canSee(user, t) {
		return ErrNotFound
	}
	if !isOwner(user, t) {
		return ErrForbidden
	}
	delete(s.tasks, id)
	s.St.Delete(context.Background(), key(id))
	s.pushDel(t)
	return nil
}

// Tick meldet fällige und überfällige Aufgaben/Meilensteine (einmal je Termin) im Verlauf und per E-Mail/Webhook.
func (s *Svc) Tick(now time.Time) {
	today := now.Format(layout)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.Status == "done" || t.Status == "closed" {
			continue
		}
		who := t.Assignee
		if who == "" {
			who = t.By
		}
		changed := false
		if t.Due != "" && t.Due <= today && !t.DueNt {
			t.DueNt, changed = true, true
			sys(t, "", "due", "")
			s.ext("", who, t, "Die Aufgabe ist fällig (bis "+t.Due+").")
		}
		for i := range t.Miles {
			m := &t.Miles[i]
			if !m.Done && m.Due != "" && m.Due <= today && !m.Nt {
				m.Nt, changed = true, true
				sys(t, "", "due", m.Text)
				s.ext("", who, t, "Meilenstein fällig: "+m.Text+" (bis "+m.Due+")")
			}
		}
		if changed {
			s.save(t)
			s.push(t)
		}
	}
}

// Start: stündliche Prüfung im Hintergrund.
func (s *Svc) Start(ctx context.Context) {
	go func() {
		time.Sleep(30 * time.Second)
		s.Tick(time.Now())
		tk := time.NewTicker(time.Hour)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				s.Tick(time.Now())
			}
		}
	}()
}

// ---- HTTP ----

func (s *Svc) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	h := func(fn http.HandlerFunc) http.Handler { return wrap(http.HandlerFunc(fn)) }
	fail := func(w http.ResponseWriter, err error) {
		code := 400
		switch err {
		case ErrForbidden:
			code = 403
		case ErrNotFound:
			code = 404
		}
		http.Error(w, err.Error(), code)
	}
	out := func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }
	mux.Handle("GET /api/tasks", h(func(w http.ResponseWriter, r *http.Request) {
		me := auth.User(r.Context())
		s.mu.Lock()
		list := []View{}
		for _, t := range s.tasks {
			if s.canSee(me, t) {
				list = append(list, s.view(me, t, false))
			}
		}
		s.mu.Unlock()
		sort.Slice(list, func(i, j int) bool { return list[i].Updated > list[j].Updated })
		type gr struct {
			Name string `json:"name"`
			Req  bool   `json:"req,omitempty"`
		}
		groups := []gr{}
		cand := auth.GroupsOf(me)
		if auth.IsAdminUser(me) {
			cand = cand[:0:0]
			cand = append(cand, auth.AllGroupNames()...)
		}
		for _, g := range cand {
			if ok, req := createRight(me, g); ok {
				groups = append(groups, gr{g, req})
			}
		}
		out(w, map[string]any{"tasks": list, "people": auth.PeersOf(me), "groups": groups})
	}))
	mux.Handle("GET /api/tasks/ws", h(s.ws))
	mux.Handle("GET /api/tasks/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		me := auth.User(r.Context())
		s.mu.Lock()
		defer s.mu.Unlock()
		t := s.tasks[r.PathValue("id")]
		if t == nil || !s.canSee(me, t) {
			http.Error(w, "not found", 404)
			return
		}
		v := s.view(me, t, true)
		// Verweise: nur sichtbare Aufgaben mit Titel
		type lk struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		}
		var links []lk
		for _, id := range t.Link {
			if o := s.tasks[id]; o != nil && s.canSee(me, o) {
				links = append(links, lk{o.ID, o.Title, o.Status})
			}
		}
		out(w, map[string]any{"task": v, "links": links})
	}))
	mux.Handle("POST /api/tasks", h(func(w http.ResponseWriter, r *http.Request) {
		var m meta
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&m) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		t, err := s.Create(auth.User(r.Context()), m)
		if err != nil {
			fail(w, err)
			return
		}
		out(w, map[string]string{"id": t.ID})
	}))
	mux.Handle("POST /api/tasks/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		var m meta
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&m) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.Update(auth.User(r.Context()), r.PathValue("id"), m); err != nil {
			fail(w, err)
		}
	}))
	mux.Handle("POST /api/tasks/{id}/act", h(func(w http.ResponseWriter, r *http.Request) {
		var in act
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if _, err := s.Act(auth.User(r.Context()), r.PathValue("id"), in); err != nil {
			fail(w, err)
		}
	}))
	mux.Handle("DELETE /api/tasks/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		if err := s.Delete(auth.User(r.Context()), r.PathValue("id")); err != nil {
			fail(w, err)
		}
	}))
}

func (s *Svc) ws(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 10)
	c := &client{user: me, out: make(chan []byte, 256)}
	s.mu.Lock()
	s.conns[c] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.conns[c] {
			delete(s.conns, c)
			close(c.out)
		}
		s.mu.Unlock()
	}()
	ctx := r.Context()
	go func() {
		for b := range c.out {
			if conn.Write(ctx, websocket.MessageText, b) != nil {
				return
			}
		}
		conn.Close(websocket.StatusPolicyViolation, "resync")
	}()
	for { // nur Verbindung halten
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
	}
}
