package files

import (
	"context"
	"io"
	"testing"
	"time"

	"cs-team/store"
)

// Öffentliche Links mit Ablauf (0.60): abgelaufen = 404 wie unbekannt, unbegrenzt geht, verlängern, Bereinigung, Obergrenze.
func TestPublicLinkExpiry(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	max := 0
	s := &Svc{St: st, Max: 1 << 20, PubMax: func() int { return max }}
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := s.saveMeta(ctx, &Meta{Name: n, Owner: "bob", Size: 5, Type: "text/plain", Mod: time.Now().UnixNano()}); err != nil {
			t.Fatal(err)
		}
		st.Put(ctx, dataKey("bob", n), []byte("hallo"), "")
	}
	d := func(n int) *int { return &n }
	share := func(name string, days *int) *Meta {
		m, err := s.Share(ctx, "bob", "bob", name, nil, nil, true, days)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	read := func(tok string) error {
		_, rc, err := s.Public(ctx, tok)
		if err == nil {
			io.Copy(io.Discard, rc)
			rc.Close()
		}
		return err
	}
	// 7 Tage: gültig, Ablauf gesetzt
	a := share("a.txt", d(7))
	if a.Exp < time.Now().Add(6*24*time.Hour).Unix() || a.Exp > time.Now().Add(8*24*time.Hour).Unix() {
		t.Fatal("Ablauf 7 Tage:", a.Exp)
	}
	if err := read(a.Token); err != nil {
		t.Fatal("gültiger Link:", err)
	}
	// unbegrenzt: kein Ablauf, funktioniert; alter Link ohne Feld (Exp 0) ebenfalls
	b := share("b.txt", d(0))
	if b.Exp != 0 || read(b.Token) != nil {
		t.Fatal("unbegrenzt:", b.Exp)
	}
	// abgelaufen: wie ein unbekannter Token
	a.Exp = time.Now().Unix() - 1
	if err := s.saveMeta(ctx, a); err != nil {
		t.Fatal(err)
	}
	s.invalidate()
	if err := read(a.Token); err != ErrNotFound {
		t.Fatal("abgelaufener Link muss ErrNotFound liefern:", err)
	}
	// Verlängern: derselbe Link gilt wieder
	a2 := share("a.txt", d(30))
	if a2.Token != a.Token || a2.Exp < time.Now().Add(29*24*time.Hour).Unix() {
		t.Fatal("Verlängerung:", a2.Token == a.Token, a2.Exp)
	}
	if err := read(a.Token); err != nil {
		t.Fatal("verlängerter Link:", err)
	}
	// ohne Angabe (nil) bleibt die Frist unverändert
	if a3 := share("a.txt", nil); a3.Exp != a2.Exp {
		t.Fatal("nil ändert die Frist:", a3.Exp, a2.Exp)
	}
	// Bereinigung: abgelaufene Links verschwinden samt Token, gültige bleiben
	c := share("c.txt", d(1))
	c.Exp = time.Now().Unix() - 10
	s.saveMeta(ctx, c)
	s.invalidate()
	if n := s.PurgeLinks(ctx); n != 1 {
		t.Fatal("PurgeLinks:", n)
	}
	if _, _, err := st.Get(ctx, tokKey(c.Token)); err == nil {
		t.Fatal("Token des abgelaufenen Links noch da")
	}
	m, _ := s.Meta(ctx, "bob", "c.txt")
	if m.Token != "" || m.Exp != 0 {
		t.Fatal("Metadaten nach Bereinigung:", m.Token, m.Exp)
	}
	if read(a.Token) != nil || read(b.Token) != nil {
		t.Fatal("gültige Links durch Bereinigung verloren")
	}
	// Obergrenze: 10 Tage; unbegrenzt und längere Wünsche werden auf 10 Tage gekürzt, kürzere bleiben
	max = 10
	for _, w := range []struct{ days, want int }{{0, 10}, {30, 10}, {3, 3}} {
		m := share("c.txt", d(w.days))
		got := (m.Exp - time.Now().Unix() + 3600) / 86400
		if int(got) != w.want {
			t.Fatalf("Obergrenze: Wunsch %d Tage ergibt %d (erwartet %d)", w.days, got, w.want)
		}
	}
	// neuer Link ohne Angabe unter Obergrenze bekommt die Obergrenze
	s.Share(ctx, "bob", "bob", "c.txt", nil, nil, false, nil)
	if m := share("c.txt", nil); m.Exp == 0 {
		t.Fatal("neuer Link ohne Angabe unter Obergrenze muss ablaufen")
	}
	// widerrufen: Token und Frist weg
	mm, _ := s.Share(ctx, "bob", "bob", "c.txt", nil, nil, false, nil)
	if mm.Token != "" || mm.Exp != 0 {
		t.Fatal("Widerruf:", mm.Token, mm.Exp)
	}
}
