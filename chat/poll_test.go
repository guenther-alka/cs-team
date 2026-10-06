package chat

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"cs-team/store"
)

func mkPoll(t *testing.T, multi, anon bool) *Poll {
	t.Helper()
	p, err := newPoll(&pollIn{Q: "Welcher Tag?", Opts: []string{"Mo", "Di", "Mi"}, Multi: multi, Anon: anon}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPollValidate(t *testing.T) {
	now := time.Now()
	opts := func(n int) []string {
		var o []string
		for i := 0; i < n; i++ {
			o = append(o, strings.Repeat("x", i+1))
		}
		return o
	}
	for name, in := range map[string]*pollIn{
		"nil":            nil,
		"ohne Frage":     {Q: "  ", Opts: opts(2)},
		"Frage zu lang":  {Q: strings.Repeat("q", 201), Opts: opts(2)},
		"eine Option":    {Q: "q", Opts: opts(1)},
		"elf Optionen":   {Q: "q", Opts: opts(11)},
		"leere Option":   {Q: "q", Opts: []string{"a", " "}},
		"Option zu lang": {Q: "q", Opts: []string{"a", strings.Repeat("o", 101)}},
		"doppelt":        {Q: "q", Opts: []string{"a", "a"}},
		"Ende vorbei":    {Q: "q", Opts: opts(2), End: now.Add(-time.Minute).UnixMilli()},
		"Ende zu fern":   {Q: "q", Opts: opts(2), End: now.Add(400 * 24 * time.Hour).UnixMilli()},
	} {
		if _, err := newPoll(in, now); err == nil {
			t.Fatal("muss abgelehnt werden:", name)
		}
	}
	p, err := newPoll(&pollIn{Q: strings.Repeat("q", 200), Opts: opts(10), End: now.Add(time.Hour).UnixMilli()}, now)
	if err != nil || len(p.Cnt) != 10 || p.Votes == nil || p.Anon {
		t.Fatal("gueltig:", p, err)
	}
	if p, _ = newPoll(&pollIn{Q: "a\nb", Opts: []string{"x\ny", "z"}, Anon: true}, now); p.Q != "a b" || p.Opts[0] != "x y" || p.Votes != nil {
		t.Fatal("Zeilenumbrueche / anonym:", p)
	}
}

func TestPollVote(t *testing.T) {
	now := time.Now()
	p := mkPoll(t, false, false)
	if p.vote("anna", []int{0, 1}, now) == nil || p.vote("anna", []int{3}, now) == nil || p.vote("anna", []int{-1}, now) == nil || p.vote("anna", nil, now) == nil {
		t.Fatal("ungueltige Wahl muss scheitern (mehrere bei Einfachauswahl, ausserhalb, Rueckzug ohne Stimme)")
	}
	p.vote("anna", []int{0}, now)
	p.vote("bob", []int{0}, now)
	if p.N != 2 || p.Cnt[0] != 2 {
		t.Fatal("zwei Stimmen:", p)
	}
	p.vote("bob", []int{2}, now) // Stimme aendern
	if p.N != 2 || p.Cnt[0] != 1 || p.Cnt[2] != 1 || p.Votes["bob"][0] != 2 {
		t.Fatal("geaendert:", p)
	}
	if err := p.vote("bob", nil, now); err != nil || p.N != 1 || p.Cnt[2] != 0 || p.Votes["bob"] != nil { // Zuruecknehmen
		t.Fatal("zurueckgenommen:", p, err)
	}
	// Mehrfachauswahl
	m := mkPoll(t, true, false)
	if err := m.vote("anna", []int{2, 0}, now); err != nil || m.Cnt[0] != 1 || m.Cnt[2] != 1 || m.N != 1 {
		t.Fatal("Mehrfach:", m, err)
	}
	if m.vote("anna", []int{1, 1}, now) == nil {
		t.Fatal("doppelte Option")
	}
	m.vote("anna", []int{1}, now)
	if m.Cnt[0] != 0 || m.Cnt[1] != 1 || m.Cnt[2] != 0 || m.N != 1 {
		t.Fatal("Mehrfach geaendert:", m)
	}
}

func TestPollClosed(t *testing.T) {
	now := time.Now()
	p := mkPoll(t, false, false)
	p.End = now.Add(time.Minute).UnixMilli()
	if err := p.vote("anna", []int{0}, now); err != nil {
		t.Fatal(err)
	}
	if err := p.vote("bob", []int{0}, now.Add(2*time.Minute)); err != ErrPollClosed { // Ende erreicht
		t.Fatal("nach dem Ende:", err)
	}
	q := mkPoll(t, false, false)
	m := &Msg{By: "anna", Poll: q}
	if pollClose(m, "bob", false) || !pollClose(m, "anna", false) || m.Poll.vote("bob", []int{0}, now) != ErrPollClosed {
		t.Fatal("nur Ersteller/Admin schliessen; danach keine Stimmen")
	}
	m2 := &Msg{By: "anna", Poll: mkPoll(t, false, false)}
	if !pollClose(m2, "lehrer", true) || !m2.Poll.Closed {
		t.Fatal("Gruppen-Admin darf schliessen")
	}
}

// Anonymitaet: weder die Datei noch die Antwort enthalten eine Zuordnung Person -> Wahl.
func TestPollAnonLeak(t *testing.T) {
	now := time.Now()
	p := mkPoll(t, false, true)
	if err := p.vote("anna", []int{0}, now); err != nil {
		t.Fatal(err)
	}
	p.vote("bob", []int{1}, now)
	p.vote("carl", []int{1}, now)
	if p.vote("anna", []int{2}, now) != ErrPollVoted || p.vote("anna", nil, now) == nil {
		t.Fatal("anonym: keine Aenderung")
	}
	if p.N != 3 || p.Cnt[0] != 1 || p.Cnt[1] != 2 || p.Cnt[2] != 0 || p.Votes != nil {
		t.Fatal("Zaehler:", p)
	}
	b, _ := json.Marshal(Msg{ID: 1, By: "dora", T: p.Q, Poll: p})
	var raw map[string]any
	json.Unmarshal(b, &raw)
	pm := raw["poll"].(map[string]any)
	if _, ok := pm["votes"]; ok {
		t.Fatal("anonyme Umfrage darf kein votes-Feld haben:", string(b))
	}
	if strings.Join(p.Done, ",") != "anna,bob,carl" { // sortiert, ohne Wahl
		t.Fatal("Teilnehmer:", p.Done)
	}
	// benannt: Wahl ist sichtbar
	n := mkPoll(t, false, false)
	n.vote("anna", []int{1}, now)
	if b, _ := json.Marshal(n); !strings.Contains(string(b), `"votes":{"anna":[1]}`) {
		t.Fatal("benannt:", string(b))
	}
}

func zipChat(t *testing.T, s *Svc, user string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := s.ExportUser(context.Background(), user, zw); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	f, _ := zr.Open("chat.json")
	b, _ := io.ReadAll(f)
	return string(b)
}

func pollStore(t *testing.T) (*Svc, store.Store) {
	ctx := context.Background()
	st := store.NewMem()
	now := time.Now()
	named, anon := mkPoll(t, false, false), mkPoll(t, false, true)
	named.vote("carl", []int{1}, now)
	named.vote("anna", []int{0}, now)
	anon.vote("carl", []int{2}, now)
	anon.vote("anna", []int{0}, now)
	b, _ := json.Marshal([]Msg{
		{ID: 1, By: "anna", T: named.Q, Poll: named},
		{ID: 2, By: "bob", T: anon.Q, Poll: anon},
	})
	st.Put(ctx, chatKey("g1", "allgemein"), b, "")
	return New(st), st
}

func TestPollExport(t *testing.T) {
	s, _ := pollStore(t)
	out := zipChat(t, s, "carl")
	var r struct {
		Votes []exportVote `json:"votes"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Votes) != 2 {
		t.Fatal("zwei Teilnahmen erwartet:", out)
	}
	v := r.Votes[0]
	if v.Anonymous || len(v.Choice) != 1 || v.Choice[0] != "Di" {
		t.Fatal("benannte Wahl:", v)
	}
	a := r.Votes[1]
	if !a.Anonymous || !a.Participated || len(a.Choice) != 0 || strings.Contains(out, "Mi") {
		t.Fatal("anonym nur hat teilgenommen:", out)
	}
	if out := zipChat(t, s, "dora"); strings.Contains(out, "Welcher Tag") {
		t.Fatal("fremde Umfrage im Export:", out)
	}
}

func TestPollAnonymiseUser(t *testing.T) {
	ctx := context.Background()
	s, st := pollStore(t)
	if n := s.AnonCount(ctx, "carl"); n != 2 {
		t.Fatal("Vorschau:", n)
	}
	n, err := s.Anon(ctx, "carl", "geloescht")
	if err != nil || n != 2 {
		t.Fatal("anonymisiert:", n, err)
	}
	m := New(st).ch(ctx, "g1", "allgemein").msgs
	np, ap := m[0].Poll, m[1].Poll
	if _, ok := np.Votes["carl"]; ok || np.Votes["geloescht"][0] != 1 || np.Votes["anna"][0] != 0 || np.N != 2 || np.Cnt[1] != 1 || np.Cnt[0] != 1 {
		t.Fatal("benannt:", np)
	}
	if ap.done("carl") || !ap.done("geloescht") || ap.N != 2 || ap.Cnt[2] != 1 || ap.Votes != nil {
		t.Fatal("anonym:", ap)
	}
	raw, _, _ := st.Get(ctx, chatKey("g1", "allgemein"))
	if strings.Contains(string(raw), "carl") {
		t.Fatal("Name noch gespeichert")
	}
}

func TestPollPurge(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	old, fresh := mkPoll(t, false, false), mkPoll(t, false, true)
	b, _ := json.Marshal([]Msg{
		{ID: now.Add(-40 * 24 * time.Hour).UnixMicro(), By: "anna", T: old.Q, Poll: old},
		{ID: now.Add(-time.Hour).UnixMicro(), By: "anna", T: fresh.Q, Poll: fresh},
	})
	st.Put(ctx, chatKey("g1", "allgemein"), b, "")
	s := New(st)
	if n := s.purgeOld(ctx, "g1", 30, now); n != 1 {
		t.Fatal("alte Umfrage muss weg:", n)
	}
	if m := New(st).ch(ctx, "g1", "allgemein").msgs; len(m) != 1 || m[0].Poll == nil || !m[0].Poll.Anon {
		t.Fatal("Rest:", m)
	}
}
