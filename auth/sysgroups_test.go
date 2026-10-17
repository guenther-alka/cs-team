package auth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestSysOnlyAdmins: globale Admins legt und aendert nur der Sysadmin; globale Admins verwalten alle anderen Konten weiter.
func TestSysOnlyAdmins(t *testing.T) {
	e := newOIDCEnv(t)
	ctx := context.Background()
	call := func(tok, method, path, body string) (int, string) {
		w := webReq(e.mux, method, path, body, tok, nil)
		return w.Code, w.Body.String()
	}
	root := e.login(t, "root")
	for _, n := range []string{"adm2", "adm3"} {
		if c, b := call(root, "POST", "/api/users", `{"name":"`+n+`","password":"Passwort-`+n+`-77x","admin":true}`); c != 200 {
			t.Fatalf("Sysadmin legt Admin %s an: %d %s", n, c, b)
		}
	}
	adm2 := e.login(t, "adm2")
	if c, _ := call(adm2, "POST", "/api/users", `{"name":"neu","password":"Passwort-neu-77x","admin":true}`); c != 403 {
		t.Fatalf("globaler Admin legt Admin an: %d", c)
	}
	if c, b := call(adm2, "POST", "/api/users", `{"name":"normal","password":"Passwort-normal-77x"}`); c != 200 {
		t.Fatalf("globaler Admin legt normales Konto an: %d %s", c, b)
	}
	for _, x := range []struct{ m, p, b string }{
		{"POST", "/api/users/adm3/flags", `{"disabled":true}`},
		{"POST", "/api/users/adm3/flags", `{"admin":false}`},
		{"POST", "/api/users/anna/flags", `{"admin":true}`},
		{"POST", "/api/users/adm3/password", `{"password":"Neues-Passwort-77x"}`},
		{"POST", "/api/users/adm3/2fa/reset", ``},
		{"POST", "/api/users/adm3/contact", `{"mail":"x@example.org"}`},
		{"DELETE", "/api/users/adm3", ``},
		{"POST", "/api/users/root/password", `{"password":"Neues-Passwort-77x"}`},
		{"POST", "/api/users/root/flags", `{"disabled":true}`},
	} {
		if c, b := call(adm2, x.m, x.p, x.b); c != 403 {
			t.Fatalf("globaler Admin: %s %s -> %d %s (erwartet 403)", x.m, x.p, c, b)
		}
	}
	// alle anderen Konten verwaltet er weiter, das eigene auch
	if c, b := call(adm2, "POST", "/api/users/anna/password", `{"password":"Neues-Passwort-77x"}`); c != 200 {
		t.Fatalf("Passwort eines normalen Kontos: %d %s", c, b)
	}
	if c, b := call(adm2, "POST", "/api/users/anna/flags", `{"disabled":true}`); c != 200 {
		t.Fatalf("normales Konto sperren: %d %s", c, b)
	}
	if c, b := call(adm2, "POST", "/api/users/adm2/password", `{"password":"Eigenes-Passwort-77x"}`); c != 200 {
		t.Fatalf("eigenes Passwort: %d %s", c, b)
	}
	// der Sysadmin darf alles
	if c, b := call(root, "POST", "/api/users/adm3/password", `{"password":"Neues-Passwort-77x"}`); c != 200 {
		t.Fatalf("Sysadmin setzt Admin-Passwort: %d %s", c, b)
	}
	if c, b := call(root, "POST", "/api/users/adm3/flags", `{"admin":false}`); c != 200 {
		t.Fatalf("Sysadmin entzieht Admin-Recht: %d %s", c, b)
	}
	if u, _ := e.a.get(ctx, "adm3"); u.Admin {
		t.Fatal("Admin-Recht nicht entzogen")
	}
}

// TestSystemGroups: globaladmins folgt dem Admin-Recht, groupadmins den Admin-Listen der Gruppen.
func TestSystemGroups(t *testing.T) {
	e := newOIDCEnv(t)
	ctx := context.Background()
	if err := e.a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(tok, method, path, body string) (int, string) {
		w := webReq(e.mux, method, path, body, tok, nil)
		return w.Code, w.Body.String()
	}
	type grp struct {
		Name    string
		Members []string
		Admins  []string
		Manage  bool
	}
	get := func(tok, name string) grp {
		t.Helper()
		_, b := call(tok, "GET", "/api/groups", "")
		var l []grp
		if err := json.Unmarshal([]byte(b), &l); err != nil {
			t.Fatal(err, b)
		}
		for _, g := range l {
			if g.Name == name {
				return g
			}
		}
		t.Fatalf("Gruppe %s fehlt: %s", name, b)
		return grp{}
	}
	has := func(l []string, n string) bool { return contains(l, n) }
	root := e.login(t, "root")
	if c, b := call(root, "POST", "/api/users", `{"name":"adm2","password":"Passwort-adm2-77x","admin":true}`); c != 200 {
		t.Fatal(c, b)
	}
	adm2 := e.login(t, "adm2")
	if g := get(root, AdminGroup); !has(g.Members, "root") || !has(g.Members, "adm2") || has(g.Members, "anna") || !has(g.Admins, "root") {
		t.Fatalf("globaladmins: %+v", g)
	}
	// Mitglieder aufnehmen/entfernen = Admin-Recht: nur der Sysadmin
	if c, _ := call(adm2, "POST", "/api/groups/"+AdminGroup+"/members", `{"add":["anna"]}`); c != 403 {
		t.Fatalf("globaler Admin nimmt in globaladmins auf: %d", c)
	}
	if c, b := call(root, "POST", "/api/groups/"+AdminGroup+"/members", `{"add":["anna"]}`); c != 200 {
		t.Fatalf("Sysadmin nimmt auf: %d %s", c, b)
	}
	if u, _ := e.a.get(ctx, "anna"); !u.Admin || contains(u.Groups, AdminGroup) {
		t.Fatalf("anna: Admin=%v Groups=%v", u.Admin, u.Groups)
	}
	if c, b := call(root, "POST", "/api/groups/"+AdminGroup+"/members", `{"remove":["anna"]}`); c != 200 {
		t.Fatalf("Sysadmin entfernt: %d %s", c, b)
	}
	if u, _ := e.a.get(ctx, "anna"); u.Admin {
		t.Fatal("anna ist noch Admin")
	}
	if c, _ := call(root, "POST", "/api/groups/"+AdminGroup+"/members", `{"remove":["root"]}`); c != 403 {
		t.Fatalf("Sysadmin aus globaladmins entfernen: %d", c)
	}
	for _, n := range []string{AdminGroup, GAdminGroup} {
		if c, _ := call(root, "DELETE", "/api/groups/"+n, ""); c != 400 {
			t.Fatalf("Systemgruppe %s loeschen: %d", n, c)
		}
	}
	// die Systemgruppen stehen nie in der Handliste
	if c, b := call(root, "POST", "/api/users/carl/groups", `{"groups":["alluser","`+AdminGroup+`","`+GAdminGroup+`"]}`); c != 200 {
		t.Fatal(c, b)
	}
	if u, _ := e.a.get(ctx, "carl"); contains(u.Groups, AdminGroup) || contains(u.Groups, GAdminGroup) {
		t.Fatalf("carl: %v", u.Groups)
	}
	// groupadmins: Mitglieder = Eintraege in Admin-Listen; globale Admins legen sie an (Feld der Gruppe) und duerfen sie entfernen
	if c, b := call(adm2, "POST", "/api/groups", `{"name":"k1","areas":["files"],"admins":["anna"]}`); c != 200 {
		t.Fatalf("Gruppe mit Gruppen-Admin: %d %s", c, b)
	}
	if g := get(root, GAdminGroup); !has(g.Members, "anna") || has(g.Members, "carl") {
		t.Fatalf("groupadmins: %+v", g)
	}
	if c, _ := call(adm2, "POST", "/api/groups/"+GAdminGroup+"/members", `{"add":["carl"]}`); c != 400 {
		t.Fatalf("Aufnahme in groupadmins: %d", c)
	}
	anna := e.login(t, "anna")
	if c, _ := call(anna, "POST", "/api/groups/"+GAdminGroup+"/members", `{"remove":["anna"]}`); c == 200 {
		t.Fatal("Gruppen-Admin entfernt sich selbst aus groupadmins")
	}
	if c, b := call(adm2, "POST", "/api/groups/"+GAdminGroup+"/members", `{"remove":["anna"]}`); c != 200 {
		t.Fatalf("globaler Admin entfernt Gruppen-Admin: %d %s", c, b)
	}
	if g := get(root, "k1"); has(g.Admins, "anna") {
		t.Fatalf("k1 hat noch Admin anna: %+v", g)
	}
	if strings.Contains(strings.Join(effGroups(mustGet(t, e.a, "anna")), ","), GAdminGroup) {
		t.Fatal("anna ist noch Mitglied von groupadmins")
	}
}

func mustGet(t *testing.T, a *Auth, n string) Account {
	t.Helper()
	u, ok := a.get(context.Background(), n)
	if !ok {
		t.Fatalf("Konto %s fehlt", n)
	}
	return u
}

// TestGroupComment: die Beschreibung einer Gruppe aendern globale Admins und Gruppen-Admins der Gruppe; alle sehen sie.
func TestGroupComment(t *testing.T) {
	e := newOIDCEnv(t)
	call := func(tok, method, path, body string) (int, string) {
		w := webReq(e.mux, method, path, body, tok, nil)
		return w.Code, w.Body.String()
	}
	root := e.login(t, "root")
	if c, b := call(root, "POST", "/api/groups", `{"name":"kurs23","areas":["files"],"admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := call(root, "POST", "/api/users/carl/groups", `{"groups":["alluser","kurs23"]}`); c != 200 {
		t.Fatal(c, b)
	}
	anna, carl := e.login(t, "anna"), e.login(t, "carl")
	if c, b := call(anna, "POST", "/api/groups/kurs23/comment", `{"comment":"3D Einführung Prof xyz"}`); c != 200 {
		t.Fatalf("Gruppen-Admin setzt Beschreibung: %d %s", c, b)
	}
	if c, _ := call(carl, "POST", "/api/groups/kurs23/comment", `{"comment":"x"}`); c != 403 {
		t.Fatalf("Mitglied setzt Beschreibung: %d", c)
	}
	if c, _ := call(anna, "POST", "/api/groups/kurs23/comment", `{"comment":"`+strings.Repeat("x", 201)+`"}`); c != 400 {
		t.Fatalf("zu lang: %d", c)
	}
	if c, _ := call(anna, "POST", "/api/groups/kurs23/comment", `{"comment":"a\nb"}`); c != 400 {
		t.Fatalf("Zeilenumbruch: %d", c)
	}
	_, b := call(carl, "GET", "/api/groups", "")
	if !strings.Contains(b, `"comment":"3D Einführung Prof xyz"`) {
		t.Fatalf("Mitglied sieht die Beschreibung nicht: %s", b)
	}
	// Rolle in /api/users: Gruppen-Admin sieht die Gruppen, die er verwaltet
	_, b = call(root, "GET", "/api/users", "")
	if !strings.Contains(b, `"name":"anna"`) || !strings.Contains(b, `"adminOf":["kurs23"]`) {
		t.Fatalf("adminOf fehlt: %s", b)
	}
}
