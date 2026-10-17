// Package news: Aushaenge (kurze Mitteilungen auf der Startseite "Heute", Block Neuigkeiten). Wer darf veroeffentlichen: globale Admins an alle
// oder an jede Gruppe, Gruppen-Admins an ihre Gruppen. Lesen: Aushaenge "an alle" jeder angemeldete Benutzer, Aushaenge einer Gruppe deren
// Mitglieder (und Verantwortliche). Loeschen: Autor, globale Admins, Gruppen-Admins der Gruppe. Kein Bearbeiten (loeschen und neu anlegen),
// optionales Ablaufdatum (leer = ohne Ablauf, "angepinnt"); abgelaufene Aushaenge sind nicht mehr sichtbar und werden nach 30 Tagen entfernt.
// "Ungelesen" merkt sich der Browser (localStorage), der Server speichert nichts je Leser.
package news

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cs-team/auth"
	"cs-team/store"
)

const (
	layout    = "2006-01-02"
	maxTitle  = 120
	maxText   = 4000
	maxActive = 50
	keepDays  = 30
)

type Item struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Group   string `json:"group,omitempty"` // leer = an alle
	By      string `json:"by"`
	Created int64  `json:"created"`         // Unix-Sekunden
	Until   string `json:"until,omitempty"` // letzter Tag der Anzeige (JJJJ-MM-TT), leer = ohne Ablauf
}

type view struct {
	Item
	Can bool `json:"can"` // darf loeschen
}

type Svc struct {
	St    store.Store
	Notify func(user, subj, text string) // Meldung an einen Benutzer (main: E-Mail/Chat-Adresse, Abwahl beachtet); nil = keine
	Base   func() string                 // oeffentliche Adresse fuer den Link in der Meldung (Einstellung zur Laufzeit)
	mu    sync.Mutex
	items map[string]*Item
}

func key(id string) string { return "news/" + id + ".json" }

func New(st store.Store) *Svc {
	s := &Svc{St: st, items: map[string]*Item{}}
	ctx := context.Background()
	infos, _ := st.List(ctx, "news/")
	for _, i := range infos {
		if b, _, err := st.Get(ctx, i.Key); err == nil {
			var it Item
			if json.Unmarshal(b, &it) == nil && it.ID != "" {
				s.items[it.ID] = &it
			}
		}
	}
	return s
}

func (s *Svc) save(it *Item) {
	b, _ := json.Marshal(it)
	s.St.Put(context.Background(), key(it.ID), b, "")
}

func (s *Svc) newID() string {
	n := time.Now().UnixMicro()
	for {
		id := strconv.FormatInt(n, 36)
		if _, ok := s.items[id]; !ok {
			return id
		}
		n++
	}
}

func visible(user string, it *Item) bool {
	if (it.Group == "" && !auth.IsGuest(user)) || user == it.By { // Aushaenge "an alle" sehen Gaeste nicht
		return true
	}
	return auth.IsMember(user, it.Group) || auth.IsGroupAdmin(user, it.Group) || auth.IsAdminUser(user)
}

func manage(user string, it *Item) bool {
	return user == it.By || auth.IsAdminUser(user) || (it.Group != "" && auth.IsGroupAdmin(user, it.Group))
}

func expired(it *Item, today string) bool { return it.Until != "" && it.Until < today }

// targets: an wen darf der Benutzer veroeffentlichen (all = an alle, groups = diese Gruppen).
func targets(ctx context.Context) (all bool, groups []string) {
	if auth.IsAdmin(ctx) {
		return true, auth.AllGroupNames()
	}
	return false, auth.AdminOf(ctx)
}

func contains(l []string, x string) bool {
	for _, v := range l {
		if v == x {
			return true
		}
	}
	return false
}

func (s *Svc) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	h := func(fn http.HandlerFunc) http.Handler { return wrap(http.HandlerFunc(fn)) }
	mux.Handle("GET /api/news", h(func(w http.ResponseWriter, r *http.Request) {
		me, now := auth.User(r.Context()), time.Now()
		today := now.Format(layout)
		all, groups := targets(r.Context())
		s.mu.Lock()
		out := []view{}
		for id, it := range s.items {
			if expired(it, today) {
				if d, err := time.Parse(layout, it.Until); err == nil && now.Sub(d) > keepDays*24*time.Hour { // lange abgelaufen: entfernen
					delete(s.items, id)
					s.St.Delete(context.Background(), key(id))
				}
				continue
			}
			if visible(me, it) {
				out = append(out, view{*it, manage(me, it)})
			}
		}
		s.mu.Unlock()
		sort.Slice(out, func(i, j int) bool { // neueste zuerst; gleiche Sekunde: die spaeter angelegte (Nummer = Mikrosekunden in Basis 36, gleich lang)
			if out[i].Created != out[j].Created {
				return out[i].Created > out[j].Created
			}
			return out[i].ID > out[j].ID
		})
		if groups == nil {
			groups = []string{}
		}
		json.NewEncoder(w).Encode(map[string]any{"items": out, "can": all || len(groups) > 0, "all": all, "groups": groups})
	}))
	mux.Handle("POST /api/news", h(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title, Text, Group, Until string
			Notify                    bool // Empfaenger zusaetzlich ueber ihre externen Adressen benachrichtigen
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		me, now := auth.User(r.Context()), time.Now()
		all, groups := targets(r.Context())
		if (in.Group == "" && !all) || (in.Group != "" && !all && !contains(groups, in.Group)) {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		if in.Group != "" {
			if _, ok := auth.GroupInfoOf(in.Group); !ok {
				http.Error(w, "no such group", http.StatusBadRequest)
				return
			}
		}
		in.Title, in.Text = strings.TrimSpace(in.Title), strings.TrimSpace(in.Text)
		if in.Title == "" || in.Text == "" || utf8.RuneCountInString(in.Title) > maxTitle || utf8.RuneCountInString(in.Text) > maxText {
			http.Error(w, "title and text required (max. 120 / 4000 characters)", http.StatusBadRequest)
			return
		}
		if in.Until != "" {
			d, err := time.Parse(layout, in.Until)
			if err != nil || in.Until < now.Format(layout) || d.After(now.AddDate(5, 0, 0)) {
				http.Error(w, "bad date", http.StatusBadRequest)
				return
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		active := 0
		for _, it := range s.items {
			if it.By == me && !expired(it, now.Format(layout)) {
				active++
			}
		}
		if active >= maxActive {
			http.Error(w, "too many active notices", http.StatusBadRequest)
			return
		}
		it := &Item{ID: s.newID(), Title: in.Title, Text: in.Text, Group: in.Group, By: me, Created: now.Unix(), Until: in.Until}
		s.items[it.ID] = it
		s.save(it)
		if in.Notify && s.Notify != nil {
			go s.tell(it)
		}
		json.NewEncoder(w).Encode(map[string]string{"id": it.ID})
	}))
	mux.Handle("DELETE /api/news/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		me := auth.User(r.Context())
		s.mu.Lock()
		defer s.mu.Unlock()
		it := s.items[r.PathValue("id")]
		if it == nil || !visible(me, it) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !manage(me, it) {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		delete(s.items, it.ID)
		s.St.Delete(context.Background(), key(it.ID))
	}))
}

// ---- Haken fuer Gruppen umbenennen und Benutzer loeschen (siehe main.go) ----

func (s *Svc) RenameGroup(ctx context.Context, from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.Group == from {
			it.Group = to
			s.save(it)
		}
	}
	return nil
}

func (s *Svc) Count(ctx context.Context, user string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, it := range s.items {
		if it.By == user {
			n++
		}
	}
	return n
}

func (s *Svc) Purge(ctx context.Context, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, it := range s.items {
		if it.By == user {
			delete(s.items, id)
			s.St.Delete(ctx, key(id))
		}
	}
	return nil
}

func (s *Svc) AnonCount(ctx context.Context, user string) int { return s.Count(ctx, user) }

func (s *Svc) Anon(ctx context.Context, user, repl string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, it := range s.items {
		if it.By == user {
			it.By = repl
			s.save(it)
			n++
		}
	}
	return n, nil
}

const maxTell = 500 // Empfaenger je Aushang

// tell: Meldung an die Empfaenger des Aushangs (alle oder die Mitglieder der Gruppe, ohne den Autor); wer keine Adresse hat oder Aushaenge abgewaehlt hat, bekommt nichts.
func (s *Svc) tell(it *Item) {
	var names []string
	if it.Group == "" {
		names = auth.ActiveUserNames()
	} else {
		for _, m := range auth.MembersOf(it.Group) {
			names = append(names, m.Name)
		}
	}
	text := it.By + ": " + it.Text
	if s.Base != nil {
		if b := s.Base(); b != "" {
			text += "\n\n" + b
		}
	}
	n := 0
	for _, u := range names {
		if u == it.By || (it.Group == "" && auth.IsGuest(u)) {
			continue
		}
		if n++; n > maxTell {
			return
		}
		s.Notify(u, "cs-team: "+it.Title, text)
		time.Sleep(20 * time.Millisecond) // Mailserver und Webhooks nicht fluten
	}
}