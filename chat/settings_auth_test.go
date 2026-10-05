package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cs-team/auth"
	"cs-team/store"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func intPtr(n int) *int       { return &n }

func TestSettingsIdentityEnv(t *testing.T) {
	s := NewSettings(store.NewMem(), SMTP{}, false)
	s.EnvIdentity = auth.Identity{Mode: "mixed", Realm: "local.de", AdmitGroups: []string{"lehrer"},
		LocalGroup: "teaching", URL: "ldap://192.168.2.124:389", Base: "DC=local,DC=de", CacheDays: 14}
	id := s.Identity()
	if id.Mode != "mixed" || id.Realm != "local.de" || id.DirRealm() != "local.de" || id.LocalGroup != "teaching" ||
		len(id.AdmitGroups) != 1 || id.AdmitGroups[0] != "lehrer" || id.CacheDays != 14 || !id.DirOK() || !id.LocalOK() {
		t.Fatalf("Vorgabe: %+v", id)
	}
}

func TestSettingsIdentitySet(t *testing.T) {
	st := store.NewMem()
	s := NewSettings(st, SMTP{}, false)
	s.EnvIdentity = auth.Identity{Realm: "local.de", AdmitGroups: []string{"lehrer"}, CacheDays: 14, Base: "DC=local,DC=de"}
	if err := s.setAuth(authIn{URL: "ldaps://dc.schule.de:636", BindDN: "cn=svc,dc=schule,dc=de",
		BindPW: strPtr("geheim"), AllowLocal: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	id := s.Identity()
	if id.Mode != "" || id.Realm != "local.de" || id.DefaultRealm != "" || id.URL != "ldaps://dc.schule.de:636" ||
		id.BindDN != "cn=svc,dc=schule,dc=de" || id.BindPW != "geheim" || id.AllowLocal || id.CacheDays != 14 ||
		len(id.AdmitGroups) != 1 || id.DirRealm() != "local.de" || id.Unencrypted() {
		t.Fatalf("gespeichert: %+v", id)
	}
	// gespeicherte Werte überleben einen Neustart, Nichtgesetztes bleibt Vorgabe
	s2 := NewSettings(st, SMTP{}, false)
	if id2 := s2.Identity(); id2.URL != "ldaps://dc.schule.de:636" || id2.BindPW != "geheim" || id2.Realm != "" {
		t.Fatalf("nach dem Laden: %+v", id2)
	}
	// Adresse geändert: das Passwort des Dienstkontos wird nicht mitgenommen
	if err := s.setAuth(authIn{URL: "ldaps://dc2.schule.de:636", BindDN: "cn=svc,dc=schule,dc=de"}); err != nil {
		t.Fatal(err)
	}
	if id := s.Identity(); id.BindPW != "" {
		t.Fatalf("Passwort nach Adresswechsel: %+v", id)
	}
	// Modus "dir" ohne Verzeichnisadresse und ohne Namensraum wird abgelehnt
	fresh := NewSettings(store.NewMem(), SMTP{}, false)
	if err := fresh.setAuth(authIn{Mode: "dir"}); err == nil {
		t.Fatal("Mode dir ohne Adresse und Namensraum")
	}
}

func TestSettingsIdentityBad(t *testing.T) {
	for _, c := range []struct {
		name string
		in   authIn
	}{
		{"Modus", authIn{Mode: "beides", Realm: "local.de", URL: "ldap://dc:389"}},
		{"Namensraum", authIn{Realm: "Local DE"}},
		{"Anzeige-Namensraum", authIn{DefaultRealm: "local/DE"}},
		{"Adresse", authIn{URL: "http://dc:389"}},
		{"Adresse mit Leerzeichen", authIn{URL: "ldap://dc 1:389"}},
		{"StartTLS mit ldaps", authIn{URL: "ldaps://dc:636", StartTLS: boolPtr(true)}},
		{"Gruppe", authIn{LocalGroup: "sehr/komisch"}},
		{"Aufnahmegruppe", authIn{AdmitGroups: []string{strings.Repeat("x", 65)}}},
		{"Aufnahmegruppen", authIn{AdmitGroups: make([]string, 33)}},
		{"Zwischenspeicher", authIn{CacheDays: intPtr(400)}},
	} {
		s := NewSettings(store.NewMem(), SMTP{}, false)
		if err := s.setAuth(c.in); err == nil {
			t.Fatalf("%s: erwartet Fehler", c.name)
		}
	}
}

func TestSettingsAuthRoute(t *testing.T) {
	st := store.NewMem()
	auth.ForceChange = false
	a := auth.New(st)
	if err := a.Bootstrap(context.Background(), "root", "rootrootroot"); err != nil {
		t.Fatal(err)
	}
	s := NewSettings(st, SMTP{}, false)
	mux := http.NewServeMux()
	s.Routes(mux, a.Wrap, &Mailer{St: st, Cfg: s})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		r.SetBasicAuth("root", "rootrootroot")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	put := `{"Mode":"dir","Realm":"Local.DE","URL":"ldap://192.168.2.124:389","Base":"DC=local,DC=de",
		"AdmitGroups":["lehrer"],"LocalGroup":"teaching","BindDN":"cn=svc,DC=local,DC=de","BindPW":"geheim","AllowLocal":false}`
	if w := call("POST", "/api/settings/auth", put); w.Code != 200 {
		t.Fatalf("POST: %d %s", w.Code, w.Body.String())
	}
	id := s.Identity()
	if id.Mode != "dir" || id.Realm != "local.de" || id.URL != "ldap://192.168.2.124:389" || id.BindPW != "geheim" ||
		id.BindDN != "cn=svc,DC=local,DC=de" || len(id.AdmitGroups) != 1 || id.AdmitGroups[0] != "lehrer" ||
		id.LocalGroup != "teaching" || id.AllowLocal || !id.DirOK() || id.LocalOK() || !id.Unencrypted() {
		t.Fatalf("gespeichert: %+v", id)
	}
	w := call("GET", "/api/settings", "")
	if w.Code != 200 {
		t.Fatalf("GET: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	ids, _ := got["identity"].(map[string]any)
	if ids == nil || ids["mode"] != "dir" || ids["realm"] != "local.de" || ids["bindPwSet"] != true {
		t.Fatalf("GET: %v", got)
	}
	eff, _ := ids["effective"].(map[string]any)
	if eff == nil || eff["realm"] != "local.de" || eff["displayRealm"] != "local.de" || eff["group"] != "teaching" ||
		eff["allowLocal"] != false || eff["unencrypted"] != true || eff["mode"] != "directory only" {
		t.Fatalf("wirksam: %v", eff)
	}
	// ungültiger Wert: 400 und die gespeicherte Einstellung bleibt stehen
	if w := call("POST", "/api/settings/auth", `{"Mode":"dir","Realm":"local.de","URL":"ftp://dc:389"}`); w.Code != 400 {
		t.Fatalf("ungültig: %d", w.Code)
	}
	if id := s.Identity(); id.URL != "ldap://192.168.2.124:389" || id.BindPW != "geheim" {
		t.Fatalf("nach Fehler: %+v", id)
	}
	// ohne gültige Anmeldung: 401
	r := httptest.NewRequest("POST", "/api/settings/auth", strings.NewReader(put))
	r.SetBasicAuth("anna", "geheim123")
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, r)
	if w2.Code != 401 {
		t.Fatalf("ohne gültige Anmeldung: %d", w2.Code)
	}
}
