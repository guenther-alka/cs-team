package auth

import (
	"context"
	"errors"
	"testing"

	"cs-team/store"
)

func TestSysAdminProtected(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	if got := a.SysAdmin(ctx); got != "anna" {
		t.Fatalf("Sysadmin nach Bootstrap: %q", got)
	}
	if err := a.AddUser(ctx, "bert", "bertgeheim1", true, nil); err != nil {
		t.Fatal(err)
	}
	no, yes := false, true
	if err := a.SetFlags(ctx, "anna", &no, nil); !errors.Is(err, ErrSysAdmin) {
		t.Errorf("Herabstufen: %v", err)
	}
	if err := a.SetFlags(ctx, "anna", nil, &yes); !errors.Is(err, ErrSysAdmin) {
		t.Errorf("Sperren: %v", err)
	}
	if err := a.DeleteUser(ctx, "anna"); !errors.Is(err, ErrSysAdmin) {
		t.Errorf("Loeschen: %v", err)
	}
	if err := a.SetFlags(ctx, "bert", nil, &yes); err != nil { // weitere Admins sind frei
		t.Errorf("anderen Admin sperren: %v", err)
	}
	if err := a.DeleteUser(ctx, "bert"); err != nil {
		t.Errorf("anderen Admin loeschen: %v", err)
	}
}

func TestEnsureAndSetSys(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	put := func(name string, u Account) {
		if err := a.mutate(ctx, func(m map[string]Account) error { m[name] = u; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	h, _ := hash("geheim1234")
	put("neu", Account{Hash: h, Admin: true, Created: "2026-05-01T00:00:00Z"})
	put("alt", Account{Hash: h, Admin: true, Created: "2025-01-01T00:00:00Z"})
	put("dirk@s.de", Account{Hash: h, Admin: true, Source: "dir", Created: "2020-01-01T00:00:00Z"})
	// Altbestand ohne Markierung: aeltestes aktives lokales Admin-Konto (das Verzeichniskonto zaehlt nicht)
	if err := a.EnsureSys(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got := a.SysAdmin(ctx); got != "alt" {
		t.Fatalf("Altbestand: %q", got)
	}
	// vorhandene Markierung bleibt, auch wenn CS_ADMIN_USER ein anderes Konto nennt
	if err := a.EnsureSys(ctx, "neu"); err != nil {
		t.Fatal(err)
	}
	if got := a.SysAdmin(ctx); got != "alt" {
		t.Fatalf("Markierung bleibt: %q", got)
	}
	if err := a.SetSys(ctx, "dirk@s.de"); !errors.Is(err, ErrSysLocal) {
		t.Errorf("Verzeichniskonto als Sysadmin: %v", err)
	}
	if err := a.SetSys(ctx, "gibtsnicht"); !errors.Is(err, ErrNoUser) {
		t.Errorf("unbekanntes Konto: %v", err)
	}
	if err := a.SetSys(ctx, "neu"); err != nil {
		t.Fatal(err)
	}
	if got := a.SysAdmin(ctx); got != "neu" {
		t.Fatalf("nach SetSys: %q", got)
	}
	if u, _ := a.get(ctx, "alt"); u.Sys {
		t.Errorf("altes Sysadmin-Konto behaelt die Markierung")
	}
	// ein gesperrtes oder herabgestuftes Sysadmin-Konto wird beim Start wieder hergestellt
	put("neu", Account{Hash: h, Created: "2026-05-01T00:00:00Z", Sys: true, Disabled: true})
	if err := a.EnsureSys(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if u, _ := a.get(ctx, "neu"); !u.Admin || u.Disabled || !u.Sys {
		t.Errorf("Wiederherstellung: %+v", u)
	}
}
