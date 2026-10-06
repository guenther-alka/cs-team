package auth

// Mitgliederlisten und Zugehörigkeit nach der KISS-Regel 0.55: ein Eintrag ist ein cs-team-Konto, eine
// cs-team-Untergruppe ("@name") oder eine Verzeichnisgruppe ("DOMAENE\gruppe"); Verzeichnis-Einträge darf nur der
// globale Admin setzen. Mitgliedschaft aus dem Verzeichnis/über Untergruppen wird beim Laden berechnet (Account.Member).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cs-team/store"
)

// TestEntryKind: Einträge werden nach Vorrang erkannt (Stern, "@", Domäne, Konto, Gruppe, sonst Verzeichnis).
func TestEntryKind(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroup(ctx, "klasse5a", []string{"files"}, nil); err != nil {
		t.Fatal(err)
	}
	a.refresh(ctx)
	for _, c := range []struct {
		in   string
		kind int
		name string
	}{
		{"", entBad, ""},
		{"*", entBad, "*"}, // nur in Freigabelisten
		{"@", entBad, "@"},
		{"@Klasse5A", entSub, "klasse5a"},
		{"klasse5a", entSub, "klasse5a"}, // vorhandene cs-team-Gruppe
		{"anna", entAccount, "anna"},
		{"ANNA", entDir, "ANNA"},                        // Kontoschlüssel sind kleingeschrieben
		{`SCHULE\Lehrer`, entDir, `SCHULE\Lehrer`},      // Domäne: eindeutig Verzeichnis
		{"CN=Lehrer,OU=Schule", entDir, "CN=Lehrer,OU=Schule"},
		{"lehrer", entDir, "lehrer"}, // unbekannter Name: Verzeichnisgruppe (nachsichtig)
	} {
		kind, name := a.entryKind(c.in)
		if kind != c.kind || name != c.name {
			t.Errorf("%q: Art %d Name %q (erwartet %d %q)", c.in, kind, name, c.kind, c.name)
		}
	}
}

// TestSetMembersEntries: Konten landen in der Handliste, Untergruppen in Group.Sub, Verzeichnisgruppen in Group.Dir.
func TestSetMembersEntries(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "bob", "bobgeheim1", false, []string{DefaultGroup}); err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"klasse5a", "lehrer"} {
		if err := a.SetGroup(ctx, g, []string{"files"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.SetMembers(ctx, "klasse5a", []string{"bob", "@lehrer", `SCHULE\schueler`, "grundschule"}, nil); err != nil {
		t.Fatal(err)
	}
	g, _ := a.loadGroups(ctx)
	if !contains(g["klasse5a"].Sub, "@lehrer") || len(g["klasse5a"].Sub) != 1 {
		t.Fatalf("Untergruppen: %v", g["klasse5a"].Sub)
	}
	if !contains(g["klasse5a"].Dir, `SCHULE\schueler`) || !contains(g["klasse5a"].Dir, "grundschule") || len(g["klasse5a"].Dir) != 2 {
		t.Fatalf("Verzeichnisgruppen: %v", g["klasse5a"].Dir)
	}
	a.refresh(ctx)
	if u, ok := a.get(ctx, "bob"); !ok || !contains(effGroups(u), "klasse5a") {
		t.Fatalf("bob ist kein Mitglied: %+v", u)
	}
	// Entfernen trifft den richtigen Eintrag (Groß-/Kleinschreibung und "@"-Form sind gleichwertig)
	if err := a.SetMembers(ctx, "klasse5a", nil, []string{"@LEHRER", `schule\SCHUELER`}); err != nil {
		t.Fatal(err)
	}
	if g, _ = a.loadGroups(ctx); len(g["klasse5a"].Sub) != 0 || len(g["klasse5a"].Dir) != 1 {
		t.Fatalf("nach dem Entfernen: sub=%v dir=%v", g["klasse5a"].Sub, g["klasse5a"].Dir)
	}
	if err := a.SetMembers(ctx, "klasse5a", []string{"*"}, nil); !errors.Is(err, ErrBadMember) {
		t.Fatalf("Stern in der Mitgliederliste: %v", err)
	}
	if err := a.SetMembers(ctx, "gibtsnicht", []string{"bob"}, nil); !errors.Is(err, ErrNoGroup) {
		t.Fatalf("unbekannte Gruppe: %v", err)
	}
}

// TestResolveMembers: Verzeichnisgruppen und Untergruppen (auch mehrstufig und mit Schleife) werden aufgelöst.
func TestResolveMembers(t *testing.T) {
	us := map[string]Account{
		"anna": {Groups: []string{DefaultGroup}},
		"carl": {Groups: []string{"x"}},
		"dirk": {Source: "dir", Groups: []string{DefaultGroup}, DirGroups: []string{"lehrer"}},
	}
	gs := map[string]Group{
		DefaultGroup: {Areas: []string{"files"}},
		"x":          {Sub: []string{"@y"}},
		"y":          {Sub: []string{"x"}}, // Schleife: endet nach höchstens len(gs) Runden
		"z":          {Dir: []string{`schule.de\lehrer`}},
		"w":          {Sub: []string{"@z"}},
	}
	got := resolveMembers(us, gs)
	if m := strings.Join(got["carl"].Member, ","); m != "y" {
		t.Errorf("carl (Schleife): %q", m)
	}
	if m := strings.Join(got["dirk"].Member, ","); m != "w,z" {
		t.Errorf("dirk (Verzeichnisgruppe + Untergruppe): %q", m)
	}
	if len(got["anna"].Member) != 0 {
		t.Errorf("anna: %v", got["anna"].Member)
	}
}

// TestDirMembership: Zugehörigkeit aus dem Verzeichnis (0.55): der Eintrag "DOMAENE\gruppe" in der Gruppenliste
// entscheidet, wer Mitglied ist; Untergruppen ("@name") ziehen die Mitglieder mit.
func TestDirMembership(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"klasse5a", "lehrerteam"} {
		if err := a.SetGroup(ctx, g, []string{"files"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.AddUser(ctx, "carl", "carlgeheim1", false, []string{"klasse5a"}); err != nil {
		t.Fatal(err)
	}
	a.SetDirChecker(&fakeDir{users: map[string]string{"dirk": "geheim123"},
		info: map[string]DirUser{"dirk": {Groups: []string{"CN=Lehrer,OU=Schule", "Grundschule"}}}})
	a.SetIdentitySource(fixedIdentity{Realm: "local.de"})
	if err := a.SetMembers(ctx, "klasse5a", []string{`schule.de\lehrer`}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.SetMembers(ctx, "lehrerteam", []string{"@klasse5a"}, nil); err != nil {
		t.Fatal(err)
	}
	id := loginID{Key: "dirk@local.de", Name: "dirk", Realm: "local.de", Source: "dir"}
	if _, err := a.verifyDir(ctx, id, "geheim123"); err != nil {
		t.Fatal(err)
	}
	u, ok := a.get(ctx, "dirk@local.de")
	if !ok || strings.Join(u.DirGroups, ",") != "grundschule,lehrer" {
		t.Fatalf("Verzeichnisgruppen des Kontos: %+v", u.DirGroups)
	}
	if u.Source != "dir" || strings.Join(u.Groups, ",") != DefaultGroup {
		t.Fatalf("Spiegelkonto: %+v", u)
	}
	for _, g := range []string{DefaultGroup, "klasse5a", "lehrerteam"} {
		if !IsMember("dirk@local.de", g) {
			t.Errorf("dirk ist nicht in %s: %v", g, GroupsOf("dirk@local.de"))
		}
	}
	// lokales Konto carl: Handliste klasse5a und darüber Mitglied der Untergruppe lehrerteam
	if !IsMember("carl", "lehrerteam") {
		t.Errorf("carl ist nicht in lehrerteam: %v", GroupsOf("carl"))
	}
}


// TestMembersDirRouteOnlyGlobalAdmin: Verzeichnis-Einträge setzt nur der globale Admin (Empfehlung (c), 0.55);
// Untergruppen darf auch der Gruppen-Admin eintragen.
func TestMembersDirRouteOnlyGlobalAdmin(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroup(ctx, "klasse5a", []string{"files"}, nil); err != nil {
		t.Fatal(err)
	}
	// zweite Gruppe als Untergruppe (eine Gruppe kann sich nicht selbst enthalten, siehe unten)
	if err := a.SetGroup(ctx, "grundschule", []string{"files"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "bob", "bobgeheim1", false, []string{"klasse5a"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroupAdmins(ctx, "klasse5a", []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.groupRoutes(mux, func(fn http.HandlerFunc) http.Handler { return a.Wrap(fn) })
	call := func(user, pass, body string) int {
		r := httptest.NewRequest("POST", "/api/groups/klasse5a/members", strings.NewReader(body))
		r.SetBasicAuth(user, pass)
		r.RemoteAddr = "192.0.2.30:1234"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if code := call("bob", "bobgeheim1", `{"add":["schule.de\\lehrer"]}`); code != 403 {
		t.Fatalf("Gruppen-Admin mit Verzeichnisgruppe: %d", code)
	}
	if code := call("bob", "bobgeheim1", `{"add":["@grundschule"]}`); code != 200 {
		t.Fatalf("Gruppen-Admin mit Untergruppe: %d", code)
	}
	if code := call("bob", "bobgeheim1", `{"add":["@klasse5a"]}`); code != 400 {
		t.Fatalf("Gruppe darf sich nicht selbst enthalten: %d", code)
	}
	if code := call("anna", "annageheim1", `{"add":["schule.de\\lehrer"]}`); code != 200 {
		t.Fatalf("globaler Admin mit Verzeichnisgruppe: %d", code)
	}
	if g, _ := a.loadGroups(ctx); !contains(g["klasse5a"].Dir, `schule.de\lehrer`) {
		t.Fatalf("Verzeichnisgruppe nicht gespeichert: %v", g["klasse5a"].Dir)
	}
}


// TestDirAccountRoles: globale Admins und Gruppen-Admins duerfen Verzeichniskonten sein; die Rolle wird immer lokal
// vergeben (Account.Admin bzw. Gruppe in groups.json). Den Notfallzugang haelt das Sysadmin-Konto (sysadmin_test.go).
func TestDirAccountRoles(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "anna", "annageheim1"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetGroup(ctx, "klasse5a", []string{"files"}, nil); err != nil {
		t.Fatal(err)
	}
	a.SetDirChecker(&fakeDir{users: map[string]string{"dirk": "geheim123"}, info: map[string]DirUser{"dirk": {}}})
	a.SetIdentitySource(fixedIdentity{Realm: "local.de"})
	id := loginID{Key: "dirk@local.de", Name: "dirk", Realm: "local.de", Source: "dir"}
	if _, err := a.verifyDir(ctx, id, "geheim123"); err != nil {
		t.Fatal(err)
	}
	yes := true
	if err := a.SetFlags(ctx, "dirk@local.de", &yes, nil); err != nil { // weitere globale Admins duerfen Verzeichniskonten sein (0.57.1)
		t.Errorf("globaler Admin fuer Verzeichniskonto: %v", err)
	}
	if err := a.SetGroupAdmins(ctx, "klasse5a", []string{"dirk@local.de"}); err != nil {
		t.Fatalf("Gruppen-Admin fuer Verzeichniskonto: %v", err)
	}
	a.refresh(ctx)
	if l := a.adminOf("dirk@local.de"); len(l) != 1 || l[0] != "klasse5a" {
		t.Errorf("adminOf Verzeichniskonto: %v", l)
	}
	if err := a.SetGroupAdmins(ctx, "klasse5a", []string{"gibtsnicht@local.de"}); !errors.Is(err, ErrNoUser) {
		t.Errorf("unbekanntes Konto: %v", err)
	}
}
