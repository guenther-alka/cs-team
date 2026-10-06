package auth

import (
	"context"
	"errors"
	"testing"

	"cs-team/store"
)

// Trivialpasswörter werden abgelehnt (Anlegen, Ändern, Zurücksetzen laufen alle über hash()).
func TestWeakPassword(t *testing.T) {
	for _, p := range []string{"12345678", "Password", "PASSWORT1", "aaaaaaaa", "ÄÄÄÄÄÄÄÄ", "Willkommen"} {
		if !weakPass(p) {
			t.Errorf("%q müsste als zu einfach gelten", p)
		}
		if _, err := hash(p); !errors.Is(err, ErrWeakPass) {
			t.Errorf("hash(%q): %v", p, err)
		}
	}
	for _, p := range []string{"rootrootroot", "Schule2026!", "anna-geheim", "a1b2c3d4"} {
		if weakPass(p) {
			t.Errorf("%q ist nicht trivial", p)
		}
	}
	if _, err := hash("kurz"); !errors.Is(err, ErrBadPass) {
		t.Errorf("zu kurz: %v", err)
	}
}

// Das Startpasswort aus Umgebung (Bootstrap) und Kommandozeile (SetUser) muss beim ersten Login geändert werden.
func TestStartPasswordMustChange(t *testing.T) {
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "root", "rootrootroot"); err != nil {
		t.Fatal(err)
	}
	if u, _ := a.get(ctx, "root"); !u.Must {
		t.Fatal("Bootstrap-Admin ohne Änderungspflicht")
	}
	if err := a.SetPassword(ctx, "root", "meingeheimes1"); err != nil {
		t.Fatal(err)
	}
	if u, _ := a.get(ctx, "root"); u.Must {
		t.Fatal("Änderungspflicht nach Passwortwechsel noch gesetzt")
	}
	if err := a.SetUser(ctx, "root", "cli-passwort-1", false); err != nil { // CLI setzt Passwort zurück
		t.Fatal(err)
	}
	if u, _ := a.get(ctx, "root"); !u.Must {
		t.Fatal("CLI-Reset ohne Änderungspflicht")
	}
	if err := a.SetUser(ctx, "neu", "cli-passwort-2", false); err != nil {
		t.Fatal(err)
	}
	if u, _ := a.get(ctx, "neu"); !u.Must {
		t.Fatal("CLI-Neuanlage ohne Änderungspflicht")
	}
	if err := a.SetUser(ctx, "schwach", "12345678", false); !errors.Is(err, ErrWeakPass) {
		t.Fatalf("schwaches CLI-Passwort: %v", err)
	}
}
