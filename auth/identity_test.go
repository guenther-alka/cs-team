package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"cs-team/store"
)

// fakeDir: Verzeichnisprüfung mit festen Benutzern (Tests ohne LDAP).
type fakeDir struct {
	users map[string]string  // Anmeldename -> Passwort
	info  map[string]DirUser // Zusatzangaben (Gruppen, E-Mail)
	err   error              // feste Störung (Verzeichnis weg)
	calls int
}

func (f *fakeDir) CheckDir(ctx context.Context, user, pass string) (DirUser, error) {
	f.calls++
	if f.err != nil {
		return DirUser{}, f.err
	}
	if p, ok := f.users[user]; !ok || p != pass {
		return DirUser{}, ErrBadDir
	}
	du := f.info[user]
	du.Name = user
	return du, nil
}

// putCount: Speicher, der Schreibzugriffe auf users.json zählt (das Spiegelkonto soll nicht bei jeder Anmeldung
// neu geschrieben werden).
type putCount struct {
	store.Store
	puts int
}

func (p *putCount) Put(ctx context.Context, key string, b []byte, cond string) (string, error) {
	if key == usersKey {
		p.puts++
	}
	return p.Store.Put(ctx, key, b, cond)
}

func TestParseLogin(t *testing.T) {
	id := Identity{Realm: "local.de"}
	for _, c := range []struct {
		in    string
		key   string
		realm string
		local bool
		err   error
	}{
		{"anna", "anna", RealmLocal, true, nil},
		{"Anna", "anna", RealmLocal, true, nil},
		{"anna@local", "anna", RealmLocal, true, nil},
		{"anna@local.de", "anna@local.de", "local.de", false, nil},
		{" ANNA@LOCAL.DE ", "anna@local.de", "local.de", false, nil},
		{"anna@fremd.de", "anna@fremd.de", "fremd.de", false, ErrBadRealm},
		{"anna@", "anna@", "", false, ErrBadRealm},
		{"@local.de", "", "", false, ErrBadName},
		{"", "", "", false, ErrBadName},
		{"a b", "", "", false, ErrBadName},
		{"..@local.de", "", "", false, ErrBadName},
	} {
		got, err := parseLogin(c.in, id)
		if !errors.Is(err, c.err) {
			t.Errorf("%q: Fehler %v, erwartet %v", c.in, err, c.err)
			continue
		}
		if err != nil {
			continue
		}
		if got.Key != c.key || got.Realm != c.realm || got.Local != c.local {
			t.Errorf("%q: %+v", c.in, got)
		}
		if got.Name == "" || got.Source == "" {
			t.Errorf("%q: Name/Quelle fehlt: %+v", c.in, got)
		}
	}
}

func TestIdentityFields(t *testing.T) {
	id := Identity{Mode: "dir", Realm: "Local.DE", AdmitGroups: []string{" lehrer ", "", "schueler"}, LocalGroup: " teaching "}
	if id.DirRealm() != "local.de" || id.HomeRealm() != "local.de" || id.DisplayRealm() != "local.de" {
		t.Fatalf("Namensraum: %s %s %s", id.DirRealm(), id.HomeRealm(), id.DisplayRealm())
	}
	if !id.ValidRealm("LOCAL.DE") || !id.ValidRealm("local") || id.ValidRealm("fremd.de") || id.ValidRealm("") {
		t.Fatal("ValidRealm")
	}
	if got := id.AdmitGroupNames(); len(got) != 2 || got[0] != "lehrer" || got[1] != "schueler" {
		t.Fatalf("Aufnahmegruppen: %v", got)
	}
	if id.Group() != "teaching" || !id.DirOK() || id.LocalOK() || id.ModeName() != "directory only" || id.Unencrypted() {
		t.Fatal("Verzeichnis-Einstellung")
	}
	if got := (Identity{Base: "DC=Local, DC=de"}).DirRealm(); got != "local.de" {
		t.Fatalf("Namensraum aus der Suchbasis: %q", got)
	}
	if got := (Identity{}).HomeRealm(); got != RealmLocal {
		t.Fatalf("ohne Verzeichnis: %q", got)
	}
	if got := (Identity{Realm: "local.de", DefaultRealm: "Schule.DE"}).DisplayRealm(); got != "schule.de" {
		t.Fatalf("Anzeige-Namensraum: %q", got)
	}
	for _, c := range []struct {
		id   Identity
		ok   bool
		name string
	}{
		{Identity{}, true, "local accounts"},
		{Identity{Mode: "local"}, true, "local accounts"},
		{Identity{Mode: "mixed", Realm: "local.de"}, true, "directory + local"},
		{Identity{Mode: "dir", Realm: "local.de"}, false, "directory only"},
		{Identity{Mode: "dir", Realm: "local.de", AllowLocal: true}, true, "directory only"},
	} {
		if c.id.LocalOK() != c.ok || c.id.ModeName() != c.name {
			t.Fatalf("%+v: LocalOK=%v %s", c.id, c.id.LocalOK(), c.id.ModeName())
		}
	}
	if !(Identity{URL: "ldap://dc:389"}).Unencrypted() {
		t.Fatal("ldap:// gilt als unverschlüsselt")
	}
	if (Identity{URL: "ldap://dc:389", StartTLS: true}).Unencrypted() || (Identity{URL: "ldaps://dc:636"}).Unencrypted() {
		t.Fatal("übertragen verschlüsselt")
	}
	if (Identity{Realm: "local.de"}).Group() != DefaultGroup {
		t.Fatal("Standardgruppe")
	}
}

func TestAdmitted(t *testing.T) {
	du := DirUser{Groups: []string{"CN=Lehrer,OU=Schule", "cn=alluser,ou=gruppen"}}
	if !admitted(Identity{}, du) {
		t.Fatal("ohne Aufnahmegruppen: alle")
	}
	for _, w := range []string{"lehrer", "LEHRER", "alluser", "CN=Lehrer,OU=Schule"} {
		if !admitted(Identity{AdmitGroups: []string{w}}, du) {
			t.Fatalf("%s", w)
		}
	}
	if admitted(Identity{AdmitGroups: []string{"schueler"}}, du) {
		t.Fatal("nicht aufgenommen")
	}
	if admitted(Identity{AdmitGroups: []string{"lehrer"}}, DirUser{}) {
		t.Fatal("ohne Gruppen im Verzeichnis")
	}
	if cn("lehrer") != "lehrer" || cn("CN=Lehrer,OU=Schule") != "Lehrer" || cn(" uid=anna ") != "anna" {
		t.Fatalf("cn: %q %q %q", cn("lehrer"), cn("CN=Lehrer,OU=Schule"), cn(" uid=anna "))
	}
}

// wrapCall: eine Anfrage mit Basic Auth durch die Anmeldung schicken.
func wrapCall(h http.Handler, user, pass, ip string) (int, string) {
	r := httptest.NewRequest("GET", "/x", nil)
	r.SetBasicAuth(user, pass)
	r.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func TestDirLogin(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	st := &putCount{Store: store.NewMem()}
	a := New(st)
	dir := &fakeDir{users: map[string]string{"anna": "geheim123"},
		info: map[string]DirUser{"anna": {Groups: []string{"CN=Lehrer,OU=Schule"}, Mail: "anna@schule.de"}}}
	a.SetDirChecker(dir)
	a.SetIdentitySource(fixedIdentity{Mode: "dir", Realm: "local.de", AdmitGroups: []string{"lehrer"}, LocalGroup: "teaching"})
	h := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok " + User(r.Context()))) }))

	if code, body := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.5"); code != 200 || body != "ok anna@local.de" {
		t.Fatalf("Anmeldung: %d %q", code, body)
	}
	u, ok := a.get(ctx, "anna@local.de")
	if !ok || u.Source != "dir" || u.Realm != "local.de" || u.Hash != "!" || u.Mail != "anna@schule.de" ||
		len(u.Groups) != 1 || u.Groups[0] != "teaching" || u.Admin || u.Must || u.Disabled {
		t.Fatalf("Spiegelkonto: %+v", u)
	}
	if st.puts != 1 {
		t.Fatalf("Schreibzugriffe auf users.json: %d", st.puts)
	}
	// zweite Anmeldung: aus dem Zwischenspeicher - keine zweite Verzeichnisprüfung, kein zweites Schreiben
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.5"); code != 200 || dir.calls != 1 {
		t.Fatalf("Zwischenspeicher: %d, Prüfungen %d", code, dir.calls)
	}
	if st.puts != 1 {
		t.Fatalf("Schreibzugriffe nach Cache-Treffer: %d", st.puts)
	}
	// Verzeichnisbenutzer ohne Namensraum: bei Mode "dir" keine lokale Anmeldung
	if code, _ := wrapCall(h, "anna", "geheim123", "192.0.2.5"); code != 403 {
		t.Fatalf("lokale Anmeldung bei Mode dir: %d", code)
	}
	if code, _ := wrapCall(h, "anna@local.de", "falsch123", "192.0.2.5"); code != 401 {
		t.Fatalf("falsches Passwort: %d", code)
	}
	if code, _ := wrapCall(h, "anna@fremd.de", "geheim123", "192.0.2.5"); code != 401 {
		t.Fatalf("unbekannter Namensraum: %d", code)
	}
	if code, _ := wrapCall(h, "anna@local", "geheim123", "192.0.2.5"); code != 403 {
		t.Fatalf("@local ist eine lokale Anmeldung (abgeschaltet): %d", code)
	}
}

func TestDirAdmitAndDown(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	dir := &fakeDir{users: map[string]string{"anna": "geheim123"},
		info: map[string]DirUser{"anna": {Groups: []string{"CN=Schueler,OU=Schule"}}}}
	a := New(store.NewMem())
	a.SetDirChecker(dir)
	a.SetIdentitySource(fixedIdentity{Realm: "local.de", AdmitGroups: []string{"lehrer"}})
	id := loginID{Key: "anna@local.de", Name: "anna", Realm: "local.de", Source: "dir"}
	if _, err := a.verifyDir(ctx, id, "geheim123"); !errors.Is(err, ErrNotAdmit) {
		t.Fatalf("Aufnahme: %v", err)
	}
	if u, ok := a.get(ctx, "anna@local.de"); ok {
		t.Fatalf("Konto trotz Ablehnung angelegt: %+v", u)
	}
	// ohne Aufnahmegruppen ist jeder gültige Benutzer erlaubt
	a.SetIdentitySource(fixedIdentity{Realm: "local.de"})
	if _, err := a.verifyDir(ctx, id, "geheim123"); err != nil {
		t.Fatalf("ohne Aufnahmegruppen: %v", err)
	}
	// Verzeichnis nicht erreichbar: Anmeldung nicht möglich, kein Konto
	down := New(store.NewMem())
	down.SetDirChecker(&fakeDir{err: ErrDirDown})
	down.SetIdentitySource(fixedIdentity{Realm: "local.de"})
	if _, err := down.verifyDir(ctx, id, "geheim123"); !errors.Is(err, ErrDirDown) {
		t.Fatalf("Verzeichnis weg: %v", err)
	}
	if _, ok := down.get(ctx, "anna@local.de"); ok {
		t.Fatal("Konto trotz nicht erreichbarem Verzeichnis")
	}
	// leeres Passwort wird nie ans Verzeichnis gegeben
	before := dir.calls
	if _, err := a.verifyDir(ctx, id, ""); !errors.Is(err, ErrBadDir) {
		t.Fatalf("leeres Passwort: %v", err)
	}
	if dir.calls != before {
		t.Fatalf("Prüfung mit leerem Passwort: %d statt %d", dir.calls, before)
	}
}

func TestLocalLoginUnchanged(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "root", "rootrootroot"); err != nil {
		t.Fatal(err)
	}
	h := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(User(r.Context()))) }))
	if code, body := wrapCall(h, "root", "rootrootroot", "192.0.2.9"); code != 200 || body != "root" {
		t.Fatalf("ohne Namensraum: %d %q", code, body)
	}
	if code, body := wrapCall(h, "ROOT@LOCAL", "rootrootroot", "192.0.2.9"); code != 200 || body != "root" {
		t.Fatalf("@local: %d %q", code, body)
	}
	if code, _ := wrapCall(h, "root@local.de", "rootrootroot", "192.0.2.9"); code != 401 {
		t.Fatalf("unbekannter Namensraum ohne Verzeichnis: %d", code)
	}
	// Mode "mixed": Verzeichnis und lokale Konten nebeneinander
	m := New(store.NewMem())
	if err := m.Bootstrap(ctx, "root", "rootrootroot"); err != nil {
		t.Fatal(err)
	}
	m.SetIdentitySource(fixedIdentity{Mode: "mixed", Realm: "local.de", AllowLocal: true})
	hm := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if code, _ := wrapCall(hm, "root", "rootrootroot", "192.0.2.10"); code != 200 {
		t.Fatalf("Mode mixed, lokale Anmeldung: %d", code)
	}
}

func TestMeDirFields(t *testing.T) {
	ForceChange = false
	a := New(store.NewMem())
	a.SetDirChecker(&fakeDir{users: map[string]string{"anna": "geheim123"}})
	a.SetIdentitySource(fixedIdentity{Mode: "dir", Realm: "local.de", LocalGroup: "teaching", AllowLocal: true})
	mux := http.NewServeMux()
	a.Routes(mux)
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.SetBasicAuth("anna@local.de", "geheim123")
	r.RemoteAddr = "192.0.2.11:1234"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("/api/me: %d %s", w.Code, w.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["realm"] != "local.de" || m["source"] != "dir" || m["name"] != "anna@local.de" ||
		m["idRealm"] != "local.de" || m["idMode"] != "directory only" || m["allowLocal"] != true {
		t.Fatalf("/api/me: %v", m)
	}
	if gs, _ := m["groups"].([]any); len(gs) != 1 || gs[0] != "teaching" {
		t.Fatalf("Gruppen: %v", m["groups"])
	}
}
