package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"cs-team/store"
)

func day(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }

func TestGuestAccounts(t *testing.T) {
	ctx := context.Background()
	a := New(store.NewMem())
	const pw = "Gast-Passwort-77x"
	if err := a.AddUser(ctx, "bob", pw, false, []string{"k5"}); err != nil && !errors.Is(err, ErrNoGroup) {
		t.Log("bob vor der Gruppe:", err)
	}
	if err := a.mutateGroups(ctx, func(m map[string]Group) error {
		m["k5"] = Group{Areas: []string{"files"}}
		m["alle5"] = Group{Areas: []string{"files"}, Sub: []string{"k5"}} // alle in k5 sind auch in alle5 (abgeleitet)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "bob", pw, false, []string{"k5"}); err != nil && !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}

	// Pflichtangaben: Datum (heute bis 400 Tage), nicht die Standardgruppe
	for _, tc := range []struct{ exp, what string }{{"", "ohne Datum"}, {"morgen", "kein Datum"}, {day(-1), "gestern"}, {day(401), "zu weit"}} {
		if err := a.AddGuest(ctx, "gast", pw, []string{"k5"}, tc.exp); !errors.Is(err, ErrGuest) {
			t.Fatalf("%s: %v", tc.what, err)
		}
	}
	if err := a.AddGuest(ctx, "gast", pw, []string{DefaultGroup}, day(5)); !errors.Is(err, ErrGuest) {
		t.Fatal("Standardgruppe:", err)
	}
	if _, ok := a.get(ctx, "gast"); ok {
		t.Fatal("nach Fehlern darf kein Konto entstehen")
	}

	// anlegen: immer Mitglied der Gruppe "guests" (wird restriktiv angelegt), dazu die genannten Gruppen
	if err := a.AddGuest(ctx, "gast", pw, []string{"k5"}, day(10)); err != nil {
		t.Fatal(err)
	}
	u, ok := a.get(ctx, "gast")
	if !ok || !u.Guest || u.Expires != day(10) || u.Admin || u.Disabled {
		t.Fatalf("%+v", u)
	}
	if g := effGroups(u); len(g) != 2 || g[0] != GuestGroup || g[1] != "k5" {
		t.Fatalf("Gruppen: %v", g)
	}
	if gg := a.groups[GuestGroup]; len(gg.Areas) != 0 || gg.Chat != ModeOff {
		t.Fatalf("Gruppe guests ist nicht restriktiv: %+v", gg)
	}
	if !IsGuest("gast") || IsGuest("bob") || GuestOf("gast") != day(10) || GuestOf("bob") != "" {
		t.Fatal("IsGuest/GuestOf")
	}
	if _, ok := a.verify(ctx, "gast", pw); !ok {
		t.Fatal("Anmeldung des Gastes")
	}
	// ein weiterer Gast: die Gruppe guests gibt es schon, "guests" in der Liste wird nicht doppelt eingetragen
	if err := a.AddGuest(ctx, "gast2", pw, []string{GuestGroup}, day(3)); err != nil {
		t.Fatal(err)
	}
	if u2, _ := a.get(ctx, "gast2"); len(effGroups(u2)) != 1 || effGroups(u2)[0] != GuestGroup {
		t.Fatalf("gast2: %v", effGroups(u2))
	}

	// keine abgeleiteten Mitgliedschaften (bob in k5 -> alle5, der Gast nicht), nie Gruppen-Admin
	if !IsMember("bob", "alle5") || IsMember("gast", "alle5") || !IsMember("gast", "k5") {
		t.Fatalf("Mitgliedschaften: bob/alle5=%v gast/alle5=%v gast/k5=%v", IsMember("bob", "alle5"), IsMember("gast", "alle5"), IsMember("gast", "k5"))
	}
	if err := a.mutateGroups(ctx, func(m map[string]Group) error { g := m["k5"]; g.Admins = []string{"gast", "bob"}; m["k5"] = g; return nil }); err != nil {
		t.Fatal(err)
	}
	if IsGroupAdmin("gast", "k5") || !IsGroupAdmin("bob", "k5") || len(a.adminOf("gast")) != 0 {
		t.Fatal("ein Gast darf nie Gruppen-Admin sein")
	}

	// Ablauf: abgelaufen = gesperrt (keine Anmeldung, keine Mitgliedschaft, nicht in den Listen), ohne dass etwas gespeichert wird
	if err := a.mutate(ctx, func(m map[string]Account) error { g := m["gast"]; g.Expires = day(-1); m["gast"] = g; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.verify(ctx, "gast", pw); ok {
		t.Fatal("abgelaufener Gast meldet sich an")
	}
	if IsMember("gast", "k5") || IsMember("gast", GuestGroup) {
		t.Fatal("abgelaufener Gast ist noch Mitglied")
	}
	for _, m := range MembersOf("k5") {
		if m.Name == "gast" {
			t.Fatal("abgelaufener Gast in der Mitgliederliste")
		}
	}
	for _, n := range ActiveUserNames() {
		if n == "gast" {
			t.Fatal("abgelaufener Gast unter den aktiven Konten")
		}
	}
	if _, ok := a.verify(ctx, "gast2", pw); !ok {
		t.Fatal("ein anderer Gast ist nicht betroffen")
	}
	var raw map[string]Account
	if b, _, err := a.st.Get(ctx, usersKey); err == nil {
		raw = map[string]Account{}
		if json.Unmarshal(b, &raw) != nil || raw["gast"].Disabled {
			t.Fatal("der Ablauf darf nichts als gesperrt speichern")
		}
	}

	// verlaengern: nur Gaeste, nur gueltige Daten
	if err := a.SetGuestExpiry(ctx, "gast", day(-1)); !errors.Is(err, ErrGuest) {
		t.Fatal("Datum in der Vergangenheit:", err)
	}
	if err := a.SetGuestExpiry(ctx, "bob", day(5)); !errors.Is(err, ErrNoUser) {
		t.Fatal("normales Konto ist kein Gast:", err)
	}
	if err := a.SetGuestExpiry(ctx, "gast", day(30)); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.verify(ctx, "gast", pw); !ok || !IsMember("gast", "k5") {
		t.Fatal("verlaengerter Gast ist wieder aktiv")
	}
}
