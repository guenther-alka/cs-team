package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"cs-team/store"
)

// Aufbewahrung mit festem "jetzt": alte Nachrichten (und ihre Anhänge) werden gelöscht, neuere bleiben, 0 = nichts.
func TestPurgeOld(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixMicro() }
	day := 24 * time.Hour
	msgs := []Msg{
		{ID: ago(40 * day), By: "anna", T: "uralt", Att: &Att{ID: "f1", Name: "a.txt"}},
		{ID: ago(31 * day), By: "bob", T: "alt"},
		{ID: ago(29 * day), By: "bob", T: "neu genug"},
		{ID: ago(time.Hour), By: "anna", T: "heute"},
	}
	b, _ := json.Marshal(msgs)
	st.Put(ctx, chatKey("g1", "allgemein"), b, "")
	st.Put(ctx, fileKey("g1", "allgemein", "f1"), []byte("x"), "")
	st.Put(ctx, listKey("g1"), []byte(`{"extra":{"by":"anna","created":1}}`), "")
	b2, _ := json.Marshal([]Msg{{ID: ago(100 * day), By: "bob", T: "alt im Extra-Kanal"}, {ID: ago(day), By: "bob", T: "neu"}})
	st.Put(ctx, chatKey("g1", "extra"), b2, "")
	st.Put(ctx, chatKey("g2", "allgemein"), b, "") // andere Gruppe: unberührt

	s := New(st)
	if n := s.purgeOld(ctx, "g1", 0, now); n != 0 {
		t.Fatal("0 Tage = unbegrenzt:", n)
	}
	if n := s.purgeOld(ctx, "g1", 30, now); n != 3 {
		t.Fatal("gelöscht:", n)
	}
	if n := s.purgeOld(ctx, "g1", 30, now); n != 0 {
		t.Fatal("zweiter Lauf:", n)
	}
	// Neustart: der gekürzte Verlauf ist gespeichert, Anhang weg, andere Gruppe unberührt
	s2 := New(st)
	c := s2.ch(ctx, "g1", "allgemein")
	if len(c.msgs) != 2 || c.msgs[0].T != "neu genug" {
		t.Fatalf("Verlauf: %+v", c.msgs)
	}
	if _, _, err := st.Get(ctx, fileKey("g1", "allgemein", "f1")); err == nil {
		t.Fatal("Anhang nicht gelöscht")
	}
	if e := s2.ch(ctx, "g1", "extra"); len(e.msgs) != 1 || e.msgs[0].T != "neu" {
		t.Fatalf("Extra-Kanal: %+v", e.msgs)
	}
	if o := s2.ch(ctx, "g2", "allgemein"); len(o.msgs) != 4 {
		t.Fatal("andere Gruppe verändert:", len(o.msgs))
	}
	// späterer Zeitpunkt: auch die übrigen werden fällig
	if n := s2.purgeOld(ctx, "g1", 30, now.Add(60*day)); n != 3 {
		t.Fatal("später:", n)
	}
}

func TestAnon(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	b, _ := json.Marshal([]Msg{
		{ID: 1, By: "carl", T: "von carl", Re: map[string][]string{"👍": {"carl", "anna"}}},
		{ID: 2, By: "anna", T: "von anna", Re: map[string][]string{"👍": {"carl"}, "❤️": {"bob"}}},
		{ID: 3, By: "anna", T: "ohne"},
	})
	st.Put(ctx, chatKey("g1", "allgemein"), b, "")
	st.Put(ctx, listKey("g1"), []byte(`{"extra":{"by":"carl","created":1}}`), "")
	st.Put(ctx, logKey, []byte(`[{"ts":1,"by":"carl","group":"g1","subject":"s"},{"ts":2,"by":"anna","group":"g1","subject":"t"}]`), "")
	s := New(st)
	if n := s.AnonCount(ctx, "carl"); n != 4 { // 2 Nachrichten + Kanal + Protokoll
		t.Fatal("Vorschau:", n)
	}
	n, err := s.Anon(ctx, "carl", "gelöscht")
	if err != nil || n != 4 {
		t.Fatal("anonymisiert:", n, err)
	}
	if s.AnonCount(ctx, "carl") != 0 {
		t.Fatal("Name noch da")
	}
	s2 := New(st)
	m := s2.ch(ctx, "g1", "allgemein").msgs
	if m[0].By != "gelöscht" || m[0].T != "von carl" || m[1].Re["👍"][0] != "gelöscht" || m[1].By != "anna" || len(m[0].Re["👍"]) != 2 {
		t.Fatalf("Nachrichten: %+v", m)
	}
	if l := s2.list(ctx, "g1"); l["extra"].By != "gelöscht" {
		t.Fatal("Kanalliste:", l)
	}
	raw, _, _ := st.Get(ctx, logKey)
	var lg []logEntry
	json.Unmarshal(raw, &lg)
	if lg[0].By != "gelöscht" || lg[1].By != "anna" {
		t.Fatal("Protokoll:", lg)
	}
}
