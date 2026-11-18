package auth

import (
	"context"
	"testing"
	"time"
)

// Verzeichnisanbindung abgeschaltet (0.72): Spiegelkonten melden sich bis zum Ablauf der Frist aus dem Zwischenspeicher an.
func TestDirSwitchedOff(t *testing.T) {
	ctx := context.Background()
	dir := &fakeDir{users: map[string]string{"anna": "geheim123"}}
	a, h, _ := offlineSetup(t, 20, dir)
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.20"); code != 200 {
		t.Fatalf("Anmeldung mit Verzeichnis: %d", code)
	}
	// Anbindung abschalten: kein Namensraum, keine Adresse, Zwischenspeicher bleibt 20 Tage
	off := fixedIdentity{CacheDays: 20}
	a.SetIdentitySource(off)
	a.dropDirCache(ctx, Identity(off)) // wie beim Start: der Abdruck darf ohne Verzeichnis nicht verschwinden
	if u, _ := a.get(ctx, "anna"); u.Hash == "" || u.Hash == "!" {
		t.Fatalf("Abdruck nach dem Abschalten entfernt: %+v", u)
	}
	dir.err = ErrDirDown // das Verzeichnis wird nicht mehr gefragt; falls doch, wuerde es nicht antworten
	clearLogins(a)
	if code, body := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.20"); code != 200 || body != "anna" {
		t.Fatalf("Anmeldung nach dem Abschalten: %d %q", code, body)
	}
	clearLogins(a)
	if code, _ := wrapCall(h, "anna@local.de", "falsch123", "192.0.2.21"); code == 200 {
		t.Fatal("falsches Passwort angenommen")
	}
	clearLogins(a)
	if code, _ := wrapCall(h, "bert@local.de", "geheim123", "192.0.2.22"); code == 200 {
		t.Fatal("unbekanntes Spiegelkonto angenommen")
	}
	// Frist abgelaufen
	err := a.mutate(ctx, func(m map[string]Account) error {
		u := m["anna"]
		u.DirSeen = time.Now().Add(-21 * 24 * time.Hour).UTC().Format(time.RFC3339)
		m["anna"] = u
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	clearLogins(a)
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.23"); code == 200 {
		t.Fatal("abgelaufener Zwischenspeicher angenommen")
	}
	// Zwischenspeicher aus: Abdruck wird entfernt
	a.dropDirCache(ctx, Identity{CacheDays: 0})
	if u, _ := a.get(ctx, "anna"); u.Hash != "!" {
		t.Fatalf("Abdruck bei abgeschaltetem Zwischenspeicher nicht entfernt: %+v", u)
	}
}

func TestConvertDir(t *testing.T) {
	ctx := context.Background()
	dir := &fakeDir{users: map[string]string{"anna": "geheim123"}}
	a, h, _ := offlineSetup(t, 20, dir)
	if err := a.mutateGroups(ctx, func(m map[string]Group) error { m["lehrer"] = Group{}; return nil }); err != nil {
		t.Fatal(err)
	}
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.30"); code != 200 {
		t.Fatalf("Anmeldung: %d", code)
	}
	err := a.mutate(ctx, func(m map[string]Account) error {
		m["bert"] = Account{Hash: "!", Realm: "local.de", Source: "dir", Groups: []string{"lehrer"}}
		m["carl@schule"] = Account{Hash: "!", Realm: "schule", Source: "dir", Groups: []string{DefaultGroup}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.ConvertDir(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Converted) != 1 || res.Converted[0] != "anna" || len(res.NoPw) != 1 || res.NoPw[0] != "bert" ||
		len(res.Skipped) != 1 || res.Skipped[0] != "carl@schule" {
		t.Fatalf("Ergebnis: %+v", res)
	}
	u, _ := a.get(ctx, "anna")
	if u.Source != "" || u.Realm != "" || u.DirSeen != "" || u.Hash == "" || u.Hash == "!" || u.Must {
		t.Fatalf("anna nicht sauber umgewandelt: %+v", u)
	}
	if b, _ := a.get(ctx, "bert"); b.Source != "" || b.Hash != "" || !b.Must || len(b.Groups) != 1 || b.Groups[0] != "lehrer" {
		t.Fatalf("bert: %+v", b)
	}
	if c, _ := a.get(ctx, "carl@schule"); c.Source != "dir" {
		t.Fatalf("carl haette bleiben muessen: %+v", c)
	}
	// anna meldet sich jetzt als lokales Konto an, auch ganz ohne Verzeichnis und ohne Zwischenspeicher
	a.SetIdentitySource(fixedIdentity{})
	dir.err = ErrDirDown
	clearLogins(a)
	if code, body := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.31"); code != 200 || body != "anna" {
		t.Fatalf("Anmeldung als lokales Konto: %d %q", code, body)
	}
}

// 0.72: Spiegelkonten bekommen OS-Konten (ExportDataOS, nicht im LDAP-Export), ihr Passwort geht bei der Anmeldung an
// OnPlain (online und aus dem Zwischenspeicher); keine neuen Konten mit Systemnamen.
func TestDirPlainAndSysNames(t *testing.T) {
	ctx := context.Background()
	dir := &fakeDir{users: map[string]string{"anna": "geheim123", "root": "geheim123"}}
	a, h, _ := offlineSetup(t, 20, dir)
	var got []string
	a.OnPlain = func(n, p string) { got = append(got, n+":"+p) }
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.40"); code != 200 {
		t.Fatalf("Anmeldung: %d", code)
	}
	if len(got) != 1 || got[0] != "anna:geheim123" {
		t.Fatalf("OnPlain online: %v", got)
	}
	dir.err = ErrDirDown
	clearLogins(a)
	a.cache = map[[32]byte]cacheEnt{}
	if code, _ := wrapCall(h, "anna", "geheim123", "192.0.2.41"); code != 200 {
		t.Fatalf("Zwischenspeicher: %d", code)
	}
	if len(got) != 2 || got[1] != "anna:geheim123" {
		t.Fatalf("OnPlain Zwischenspeicher: %v", got)
	}
	a.mutate(ctx, func(m map[string]Account) error { u := m["anna"]; u.Uid = 10050; m["anna"] = u; return nil })
	inOS, inLDAP := false, false
	us, _ := a.ExportDataOS(ctx)
	for _, u := range us {
		inOS = inOS || u.Name == "anna"
	}
	us, _ = a.ExportData(ctx)
	for _, u := range us {
		inLDAP = inLDAP || u.Name == "anna"
	}
	if !inOS || inLDAP {
		t.Fatalf("Export: OS %v, LDAP %v", inOS, inLDAP)
	}
	// Systemnamen: kein neues Spiegelkonto, kein neues lokales Konto; Adressen und admin bleiben erlaubt
	dir.err = nil
	clearLogins(a)
	if code, _ := wrapCall(h, "root@local.de", "geheim123", "192.0.2.42"); code == 200 {
		t.Fatal("Spiegelkonto root angelegt")
	}
	if _, ok := a.get(ctx, "root"); ok {
		t.Fatal("Konto root vorhanden")
	}
	if err := a.AddUser(ctx, "administrator", "passwort-123x", false, nil); err != ErrSysName {
		t.Fatalf("AddUser administrator: %v", err)
	}
	if !SysName("Root") || SysName("admin") || SysName("root@firma.de") {
		t.Fatal("SysName")
	}
}
