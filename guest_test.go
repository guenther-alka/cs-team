package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func dayStr(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }

// Gast-Konten mit Ablauf: anlegen (Admin, Gruppen-Admin), immer in der Gruppe "guests", Einschraenkungen, Fristverlaengerung.
func TestGuestAccountsAPI(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	for _, g := range []string{`{"name":"k5","areas":["files"],"chat":"member","chans":"member","msg":"member","admins":["bob"]}`, `{"name":"k6","areas":["files"],"admins":["anna"]}`} {
		if c, b := req(t, srv, "anna", "POST", "/api/groups", g); c != 200 {
			t.Fatal(c, b)
		}
	}
	mk := func(who, name string, extra string) (int, string) {
		return req(t, srv, who, "POST", "/api/users", `{"name":"`+name+`","password":"passwort-`+name+`","guest":true`+extra+`}`)
	}

	// 1. Pflichtangaben
	for _, tc := range []struct{ extra, what string }{
		{``, "ohne Datum"},
		{`,"expires":"` + dayStr(-1) + `"`, "gestern"},
		{`,"expires":"` + dayStr(500) + `"`, "zu weit"},
		{`,"expires":"morgen"`, "kein Datum"},
		{`,"expires":"` + dayStr(5) + `","admin":true`, "Gast mit Admin-Recht"},
		{`,"expires":"` + dayStr(5) + `","groups":["alluser"]`, "Standardgruppe"},
	} {
		if c, b := mk("anna", "gx", tc.extra); c != 400 {
			t.Fatalf("%s: %d %s", tc.what, c, b)
		}
	}
	// 2. globaler Admin legt einen Gast an: nur Gruppe guests
	if c, b := mk("anna", "gast1", `,"expires":"`+dayStr(7)+`"`); c != 200 {
		t.Fatal(c, b)
	}
	if c, _ := mk("anna", "gast1", `,"expires":"`+dayStr(7)+`"`); c != 409 {
		t.Fatal("doppelt:", c)
	}
	_, ub := req(t, srv, "anna", "GET", "/api/users", "")
	var rows []struct {
		Name    string
		Guest   bool
		Expires string
		Groups  []string
	}
	json.Unmarshal([]byte(ub), &rows)
	found := false
	for _, r := range rows {
		if r.Name == "gast1" {
			found = true
			if !r.Guest || r.Expires != dayStr(7) || strings.Join(r.Groups, ",") != "guests" {
				t.Fatalf("Benutzerliste: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("Gast fehlt in der Benutzerliste")
	}
	if _, gb := req(t, srv, "anna", "GET", "/api/groups", ""); !strings.Contains(gb, `"name":"guests"`) {
		t.Fatalf("Gruppe guests wurde nicht angelegt: %s", gb)
	}

	// 3. Gruppen-Admin bob: nur mit seinen Gruppen (dazu automatisch guests), sonst verboten
	if c, _ := mk("bob", "gast2", `,"expires":"`+dayStr(7)+`"`); c != 403 {
		t.Fatal("ohne Gruppen:", c)
	}
	if c, _ := mk("bob", "gast2", `,"expires":"`+dayStr(7)+`","groups":["k6"]`); c != 403 {
		t.Fatal("fremde Gruppe:", c)
	}
	if c, b := mk("bob", "gast2", `,"expires":"`+dayStr(7)+`","groups":["k5"]`); c != 200 {
		t.Fatal(c, b)
	}
	// 4. der Gast aus Sicht des Gastes: eigene Angaben, keine Bereiche, nichts "an alle", kein KI-Assistent
	_, me := req(t, srv, "gast2", "GET", "/api/me", "")
	var mj struct {
		Guest   bool
		Expires string
		Admin   bool
		Groups  []string
	}
	if json.Unmarshal([]byte(me), &mj) != nil || !mj.Guest || mj.Expires != dayStr(7) || mj.Admin {
		t.Fatalf("/api/me des Gastes: %s", me)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/news", `{"title":"Intern","text":"nur fuer Mitglieder"}`); c != 200 {
		t.Fatal("Aushang an alle")
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/news", `{"title":"Klasse","text":"fuer k5","group":"k5"}`); c != 200 {
		t.Fatal("Aushang k5")
	}
	_, nb := req(t, srv, "gast2", "GET", "/api/news", "")
	if strings.Contains(nb, "Intern") || !strings.Contains(nb, "Klasse") {
		t.Fatalf("Gast sieht Aushang an alle bzw. nicht den der eigenen Gruppe: %s", nb)
	}
	_, nb1 := req(t, srv, "gast1", "GET", "/api/news", "")
	if strings.Contains(nb1, "Intern") || strings.Contains(nb1, "Klasse") {
		t.Fatalf("gast1 (nur guests) sieht Aushaenge: %s", nb1)
	}
	if c, _ := req(t, srv, "gast2", "POST", "/api/ai/chat", `{"messages":[{"role":"user","text":"hallo"}]}`); c != 403 {
		t.Fatal("KI fuer Gast:", c)
	}
	if c, _ := req(t, srv, "gast2", "POST", "/api/ai/translate", `{"text":"x","lang":"en"}`); c != 403 {
		t.Fatal("Uebersetzen fuer Gast:", c)
	}
	// 5. ein Gast ist nie Gruppen-Admin, auch wenn er in der Liste steht
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k9","areas":["files"],"admins":["gast1"]}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "gast1", "POST", "/api/groups/k9/dm", `{"mode":"member"}`); c != 403 {
		t.Fatal("Gast verwaltet eine Gruppe:", c)
	}
	if c, _ := req(t, srv, "gast1", "POST", "/api/users", `{"name":"x1","password":"passwort-x1","guest":true,"expires":"`+dayStr(3)+`","groups":["k9"]}`); c != 403 {
		t.Fatal("Gast legt Konten an:", c)
	}

	// 6. Frist aendern: Gruppen-Admin nur fuer Gaeste seiner Gruppen, globale Admins fuer alle; nur gueltige Daten; nur Gaeste
	exp := func(who, name, d string) int {
		c, _ := req(t, srv, who, "POST", "/api/users/"+name+"/expires", `{"expires":"`+d+`"}`)
		return c
	}
	if c := exp("bob", "gast2", dayStr(30)); c != 200 {
		t.Fatal("Gruppen-Admin verlaengert:", c)
	}
	if c := exp("bob", "gast1", dayStr(30)); c != 403 {
		t.Fatal("Gast ausserhalb der Gruppen:", c)
	}
	if c := exp("anna", "gast1", dayStr(30)); c != 200 {
		t.Fatal("globaler Admin:", c)
	}
	if c := exp("anna", "gast1", dayStr(-2)); c != 400 {
		t.Fatal("Datum in der Vergangenheit:", c)
	}
	if c := exp("anna", "bob", dayStr(30)); c != 404 {
		t.Fatal("normales Konto ist kein Gast:", c)
	}
	if c := exp("gast2", "gast2", dayStr(90)); c != 403 {
		t.Fatal("Gast verlaengert sich selbst:", c)
	}
	_, me = req(t, srv, "gast2", "GET", "/api/me", "")
	if !strings.Contains(me, `"expires":"`+dayStr(30)+`"`) {
		t.Fatalf("neue Frist: %s", me)
	}
}
