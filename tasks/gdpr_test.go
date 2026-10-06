package tasks

import (
	"context"
	"testing"
	"time"

	"cs-team/store"
)

// Aufbewahrung abgeschlossener Aufgaben mit festem "jetzt": nur "closed" und nur ältere als N Tage.
func TestPurgeClosed(t *testing.T) {
	st := store.NewMem()
	s := New(st)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ms := func(days int) int64 { return now.AddDate(0, 0, -days).UnixMilli() }
	mk := func(id, status string, days int) {
		t := &Task{ID: id, Title: id, By: "anna", Status: status, Updated: ms(days)}
		s.tasks[id] = t
		s.save(t)
	}
	mk("alt", "closed", 200)
	mk("neu", "closed", 10)
	mk("offen", "open", 400)
	mk("fertig", "done", 400)
	if n := s.PurgeClosed(0, now); n != 0 {
		t.Fatal("0 = unbegrenzt:", n)
	}
	if n := s.PurgeClosed(180, now); n != 1 {
		t.Fatal("gelöscht:", n)
	}
	if s.tasks["alt"] != nil || s.tasks["neu"] == nil || s.tasks["offen"] == nil || s.tasks["fertig"] == nil {
		t.Fatal("falsche Aufgaben gelöscht")
	}
	if _, _, err := st.Get(context.Background(), key("alt")); err == nil {
		t.Fatal("im Speicher geblieben")
	}
	s.ClosedDays = func() int { return 5 }
	s.hourly(now)
	if s.tasks["neu"] != nil || s.tasks["offen"] == nil {
		t.Fatal("stündlicher Lauf:", len(s.tasks))
	}
}

func TestAnonTasks(t *testing.T) {
	st := store.NewMem()
	s := New(st)
	tk := &Task{ID: "a1", Title: "T", By: "carl", Assignee: "bob", Watch: []string{"carl", "dora"}, Status: "open",
		Log: []Entry{{ID: 1, By: "carl", T: "new", Sys: true}, {ID: 2, By: "bob", T: "assign", A: []string{"carl"}, Sys: true}, {ID: 3, By: "bob", T: "hallo"}}}
	other := &Task{ID: "a2", Title: "U", By: "anna", Status: "open"}
	s.tasks["a1"], s.tasks["a2"] = tk, other
	ctx := context.Background()
	if n := s.AnonCount(ctx, "carl"); n != 1 {
		t.Fatal("Vorschau:", n)
	}
	if n, err := s.Anon(ctx, "carl", "gelöscht"); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	if tk.By != "gelöscht" || tk.Watch[0] != "gelöscht" || tk.Watch[1] != "dora" || tk.Log[0].By != "gelöscht" || tk.Log[1].A[0] != "gelöscht" || tk.Log[2].T != "hallo" || tk.Assignee != "bob" {
		t.Fatalf("%+v", tk)
	}
	if s.AnonCount(ctx, "carl") != 0 || other.By != "anna" {
		t.Fatal("Rest")
	}
	b, _, _ := st.Get(ctx, key("a1")) // gespeichert
	if len(b) == 0 || string(b) == "" {
		t.Fatal("nicht gespeichert")
	}
}
