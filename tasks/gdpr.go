package tasks

import (
	"archive/zip"
	"context"
	"encoding/json"
	"log"
	"sort"
	"time"
)

// Datenschutz: Namen gelöschter Konten ersetzen, Auskunft (Export) und Aufbewahrung abgeschlossener Aufgaben.

const maxExportTasks = 2000 // neueste Aufgaben in der Datenauskunft

// mentions: kommt der Name als Auftraggeber, Bearbeiter, Beteiligter oder im Verlauf vor?
func (t *Task) mentions(user string) bool {
	if t.By == user || t.Assignee == user || contains(t.Watch, user) {
		return true
	}
	for _, e := range t.Log {
		if e.By == user || contains(e.A, user) {
			return true
		}
	}
	return false
}

func swap(l []string, user, repl string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range l {
		if x == user {
			x = repl
		}
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// AnonCount: Zahl der Aufgaben, in denen der Name vorkommt (Vorschau beim Löschen eines Benutzers).
func (s *Svc) AnonCount(ctx context.Context, user string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.tasks {
		if t.mentions(user) {
			n++
		}
	}
	return n
}

// Anon ersetzt den Namen in Auftraggeber, Bearbeiter, Beteiligten und im Verlauf (Kommentare, Systemzeilen) durch repl.
func (s *Svc) Anon(ctx context.Context, user, repl string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.tasks {
		if !t.mentions(user) {
			continue
		}
		if t.By == user {
			t.By = repl
		}
		if t.Assignee == user {
			t.Assignee = repl
		}
		t.Watch = swap(t.Watch, user, repl)
		for i := range t.Log {
			e := &t.Log[i]
			if e.By == user {
				e.By = repl
			}
			if len(e.A) > 0 {
				e.A = swap(e.A, user, repl)
			}
		}
		s.save(t)
		s.push(t)
		n++
	}
	return n, nil
}

// ExportUser schreibt tasks.json: Aufgaben, die der Benutzer angelegt hat oder bearbeitet (mit Verlauf), Datenauskunft Art. 15/20.
// Der Verlauf kann Kommentare anderer Beteiligter dieser Aufgaben enthalten.
func (s *Svc) ExportUser(ctx context.Context, user string, zw *zip.Writer) error {
	s.mu.Lock()
	var list []Task
	for _, t := range s.tasks {
		if t.By == user || t.Assignee == user {
			list = append(list, *t)
		}
	}
	s.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Created > list[j].Created })
	trunc := len(list) > maxExportTasks
	if trunc {
		list = list[:maxExportTasks]
	}
	if list == nil {
		list = []Task{}
	}
	w, err := zw.Create("tasks.json")
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(map[string]any{"count": len(list), "truncated": trunc, "tasks": list}, "", " ")
	_, err = w.Write(b)
	return err
}

// PurgeClosed löscht abgeschlossene Aufgaben (Status closed), die seit mehr als days Tagen nicht geändert wurden. Liefert die Zahl.
func (s *Svc) PurgeClosed(days int, now time.Time) int {
	if days <= 0 {
		return 0
	}
	cut := now.Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, t := range s.tasks {
		if t.Status != "closed" || t.Updated >= cut {
			continue
		}
		delete(s.tasks, id)
		s.St.Delete(context.Background(), key(id))
		s.pushDel(t)
		n++
	}
	return n
}

// hourly: Fälligkeitsmeldungen und Aufbewahrung (globale Einstellung ClosedDays, 0 = aus).
func (s *Svc) hourly(now time.Time) {
	s.Tick(now)
	if s.ClosedDays == nil {
		return
	}
	if d := s.ClosedDays(); d > 0 {
		if n := s.PurgeClosed(d, now); n > 0 {
			log.Printf("retention: closed tasks deleted %d (older than %d days)", n, d)
		}
	}
}
