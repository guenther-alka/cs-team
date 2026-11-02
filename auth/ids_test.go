package auth

// Feste Kennungen (ids.go): Vergabe, keine Wiederverwendung, Heilung des Zaehlers, Import, Kurzname.

import (
	"context"
	"errors"
	"testing"

	"cs-team/store"
)

func idsAuth(t *testing.T) (*Auth, context.Context, store.Store) {
	t.Helper()
	ForceChange = false
	ctx := context.Background()
	st := store.NewMem()
	a := New(st)
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	return a, ctx, st
}

func uidOf(t *testing.T, a *Auth, ctx context.Context, n string) int {
	t.Helper()
	a.refresh(ctx)
	u, ok := a.get(ctx, n)
	if !ok {
		t.Fatalf("Konto %s fehlt", n)
	}
	return u.Uid
}

func gidOf(a *Auth, ctx context.Context, n string) int {
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.groups[n].Gid
}

func TestIDsAssigned(t *testing.T) {
	a, ctx, _ := idsAuth(t)
	for _, n := range []string{"bert", "carla"} {
		if err := a.AddUser(ctx, n, "geheimgeheim1", false, nil); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int]bool{}
	for _, n := range []string{"anna", "bert", "carla"} {
		u := uidOf(t, a, ctx, n)
		if u < firstID || seen[u] {
			t.Fatalf("%s: uid %d ungueltig oder doppelt", n, u)
		}
		seen[u] = true
	}
	if err := a.EnsureIDs(ctx); err != nil { // Start nach dem Update: Standardgruppen bekommen ihre Kennung
		t.Fatal(err)
	}
	if g := gidOf(a, ctx, DefaultGroup); g < firstID {
		t.Fatalf("Standardgruppe: gid %d", g)
	}
}

func TestIDsNotReused(t *testing.T) {
	a, ctx, _ := idsAuth(t)
	if err := a.AddUser(ctx, "bert", "geheimgeheim1", false, nil); err != nil {
		t.Fatal(err)
	}
	old := uidOf(t, a, ctx, "bert")
	if err := a.DeleteUser(ctx, "bert"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "bert", "geheimgeheim1", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := uidOf(t, a, ctx, "bert"); n <= old {
		t.Fatalf("uid wiederverwendet: alt %d neu %d", old, n)
	}
	if err := a.SetGroup(ctx, "klasse5a", []string{"files"}, nil); err != nil {
		t.Fatal(err)
	}
	og := gidOf(a, ctx, "klasse5a")
	if err := a.DeleteGroup(ctx, "klasse5a"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroup(ctx, "klasse5a", []string{"files"}, nil); err != nil {
		t.Fatal(err)
	}
	if n := gidOf(a, ctx, "klasse5a"); n <= og {
		t.Fatalf("gid wiederverwendet: alt %d neu %d", og, n)
	}
}

func TestIDsHealLostCounter(t *testing.T) {
	a, ctx, st := idsAuth(t)
	if err := a.AddUser(ctx, "bert", "geheimgeheim1", false, nil); err != nil {
		t.Fatal(err)
	}
	top := uidOf(t, a, ctx, "bert")
	if err := st.Delete(ctx, idsKey); err != nil { // Zaehler verloren
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "carla", "geheimgeheim1", false, nil); err != nil {
		t.Fatal(err)
	}
	if n := uidOf(t, a, ctx, "carla"); n <= top {
		t.Fatalf("uid %d kollidiert mit vorhandenen (hoechste %d)", n, top)
	}
}

func TestIDsImportKeepsTargetIDs(t *testing.T) {
	a, ctx, _ := idsAuth(t)
	ctx = adminCtx(ctx, "anna")
	if err := a.AddUser(ctx, "bert", "geheimgeheim1", false, nil); err != nil {
		t.Fatal(err)
	}
	bert := uidOf(t, a, ctx, "bert")
	d := BulkDump{Users: map[string]Account{
		"bert":  {Uid: 5, Groups: []string{DefaultGroup}},
		"dora":  {Uid: 7, Groups: []string{DefaultGroup}},
	}}
	res := a.ImportBulk(ctx, d, BulkImportOpts{Create: true, Update: true, GenPW: true})
	if len(res.Errors) > 0 {
		t.Fatal(res.Errors)
	}
	if n := uidOf(t, a, ctx, "bert"); n != bert {
		t.Fatalf("vorhandenes Konto: uid %d statt %d", n, bert)
	}
	if n := uidOf(t, a, ctx, "dora"); n < firstID {
		t.Fatalf("neues Konto uebernahm die Kennung aus dem Export: %d", n)
	}
}

func TestGroupShortAndExport(t *testing.T) {
	a, ctx, _ := idsAuth(t)
	for _, g := range []string{"klasse5a", "klasse5b"} {
		if err := a.SetGroup(ctx, g, []string{"files"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.SetGroupShort(ctx, "klasse5a", "k5a"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroupShort(ctx, "klasse5b", "k5a"); !errors.Is(err, ErrShortTaken) {
		t.Fatalf("doppelter Kurzname: %v", err)
	}
	for _, bad := range []string{"Gross", "zu-lang-12345", "a b", "ueü"} {
		if err := a.SetGroupShort(ctx, "klasse5b", bad); !errors.Is(err, ErrBadShort) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if err := a.SetGroupShort(ctx, "klasse5a", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroupExport(ctx, "klasse5a", true); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroupExport(ctx, AdminGroup, true); !errors.Is(err, ErrNoExport) {
		t.Fatalf("Systemgruppe exportierbar: %v", err)
	}
}
