package tasks

import (
	"testing"
	"time"

	"cs-team/store"
)

func d(s string) time.Time { t, _ := time.Parse(layout, s); return t }

func TestNextDue(t *testing.T) {
	if got := addMonths(d("2026-01-31"), 1).Format(layout); got != "2026-02-28" {
		t.Fatal(got)
	}
	if got := addMonths(d("2024-02-29"), 12).Format(layout); got != "2025-02-28" {
		t.Fatal(got)
	}
	n, days := nextDue(d("2026-09-01"), "weekly", d("2026-09-30")) // verspätet abgenommen: nicht in die Vergangenheit
	if n.Format(layout) != "2026-10-06" || days != 35 {
		t.Fatal(n, days)
	}
	n, days = nextDue(d("2026-10-10"), "daily", d("2026-09-30")) // vorzeitig: einfach der nächste Tag
	if n.Format(layout) != "2026-10-11" || days != 1 {
		t.Fatal(n, days)
	}
}

func TestSpawnAndTick(t *testing.T) {
	s := New(store.NewMem())
	old := &Task{ID: "a1", Title: "Backup prüfen", By: "anna", Assignee: "bob", Status: "closed", Due: "2026-09-30", Repeat: "monthly",
		Miles: []Mile{{ID: 1, Text: "Entwurf", Due: "2026-09-20", Done: true, Nt: true}}}
	n := s.spawn(old, d("2026-09-30"))
	if n.Due != "2026-10-30" || n.Status != "open" || n.Assignee != "bob" || len(n.Miles) != 1 || n.Miles[0].Done || n.Miles[0].Due != "2026-10-20" || n.Link[0] != "a1" {
		t.Fatalf("%+v", n)
	}
	var sent []string
	s.Notify = func(user, subj, text string) { sent = append(sent, user) }
	n.Due = "2026-09-01"
	n.Miles[0].Due = "2026-09-02"
	s.Tick(d("2026-09-30"))
	if !n.DueNt || !n.Miles[0].Nt || len(n.Log) != 3 { // "from" + 2x "due"
		t.Fatalf("%+v", n)
	}
	s.Tick(d("2026-09-30")) // nur einmal
	if len(n.Log) != 3 {
		t.Fatal("doppelt gemeldet")
	}
	time.Sleep(50 * time.Millisecond)
	if len(sent) != 2 || sent[0] != "bob" {
		t.Fatal(sent)
	}
}
