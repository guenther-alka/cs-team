package tasks

// Klassenaufgaben: Eine freie, offene Gruppenaufgabe wird mit "an alle Mitglieder verteilen" zur Klassenaufgabe. Jedes Mitglied
// (ausser dem Auftraggeber) bekommt eine eigene, private Kopie: sie sehen nur Auftraggeber, Bearbeiter, Beteiligte, Gruppen-Admins
// und globale Admins, nicht die anderen Mitglieder. Abgabe = "Erledigt melden" (mit Kommentar), Rueckgabe = "Wieder oeffnen" oder
// "Abnehmen" durch den Auftraggeber. Die Klassenaufgabe selbst (Fortschritt "abgegeben von gesamt") ist ebenfalls nicht fuer alle sichtbar.

import (
	"errors"
	"sort"
	"strconv"
	"time"

	"cs-team/auth"
)

const maxClass = 200

type kid struct {
	ID       string `json:"id"`
	Assignee string `json:"assignee"`
	Status   string `json:"status"`
}

// kidsOf: die Kopien einer Klassenaufgabe fuer ihre Verantwortlichen (s.mu muss gehalten werden).
func (s *Svc) kidsOf(user string, t *Task) []kid {
	if !t.Class || !isOwner(user, t) {
		return nil
	}
	out := []kid{}
	for _, id := range t.Kids {
		if k := s.tasks[id]; k != nil {
			out = append(out, kid{k.ID, k.Assignee, k.Status})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Assignee < out[j].Assignee })
	return out
}

// Distribute verteilt die Gruppenaufgabe id an alle Mitglieder der Gruppe (ohne den Auftraggeber). Liefert die Zahl der Kopien.
func (s *Svc) Distribute(user, id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tasks[id]
	if t == nil || !s.canSee(user, t) {
		return 0, ErrNotFound
	}
	if !isOwner(user, t) {
		return 0, ErrForbidden
	}
	if t.Group == "" || t.Class || t.Priv || t.Req || t.Assignee != "" || t.Status != "open" {
		return 0, errors.New("only a free, open group task can be distributed")
	}
	if ok, req := createRight(user, t.Group); !ok || req {
		return 0, ErrForbidden
	}
	var names []string
	for _, m := range auth.MembersOf(t.Group) {
		if m.Name != t.By && m.Name != user {
			names = append(names, m.Name)
		}
	}
	if len(names) == 0 {
		return 0, errors.New("the group has no other members")
	}
	if len(names) > maxClass {
		return 0, errors.New("too many members")
	}
	now := time.Now()
	ids := make([]string, 0, len(names))
	for _, n := range names {
		k := &Task{ID: s.newID(), Title: t.Title, Desc: t.Desc, By: t.By, Group: t.Group, Assignee: n, Status: "open", Prio: t.Prio,
			Due: t.Due, Labels: append([]string{}, t.Labels...), Priv: true, Of: t.ID, Created: now.Unix()}
		for i, m := range t.Miles {
			k.Miles = append(k.Miles, Mile{ID: i + 1, Text: m.Text, Due: m.Due})
		}
		sys(k, user, "new")
		sys(k, user, "assign", n)
		s.tasks[k.ID] = k
		s.save(k)
		s.push(k)
		s.ext(user, n, k, user+" hat dir eine Aufgabe zugewiesen.")
		ids = append(ids, k.ID)
	}
	t.Class, t.Priv, t.Kids, t.Repeat = true, true, ids, ""
	sys(t, user, "dist", strconv.Itoa(len(ids)))
	s.save(t)
	s.push(t)
	return len(ids), nil
}
