package main

import (
	"strings"
	"testing"
)

// MenÃ¼ Nachricht: welche Gruppen sieht wer? Globale Admins alle (auch alluser), Gruppen-Admins ihre (auch ohne Mitglied),
// Mitglieder nur bei "member"; alluser nie fÃ¼r andere; Gruppen mit "Nachricht senden: aus" fÃ¼r niemanden.
func TestMessageGroupsByRole(t *testing.T) {
	srv, _ := setup(t) // anna = globaler Admin, bob = normaler Benutzer
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"gadm","areas":["files"],"msg":"admin","admins":["bob"]}`)
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"gmem","areas":["files"],"msg":"member","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/gmem/members", `{"add":["bob"]}`)
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"goff","areas":["files"],"msg":"off","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/goff/members", `{"add":["bob"]}`)
	has := func(user, g string) bool {
		_, b := req(t, srv, user, "GET", "/api/message/groups", "")
		return strings.Contains(b, `"name":"`+g+`"`)
	}
	for _, g := range []string{"alluser", "gadm", "gmem"} {
		if !has("anna", g) {
			t.Fatal("globaler Admin muss", g, "sehen")
		}
	}
	if has("anna", "goff") {
		t.Fatal("Gruppe mit Nachricht aus darf nicht erscheinen")
	}
	if !has("bob", "gadm") || !has("bob", "gmem") {
		t.Fatal("Gruppen-Admin sieht seine Gruppe, Mitglied sieht member-Gruppe")
	}
	if has("bob", "alluser") || has("bob", "goff") {
		t.Fatal("alluser nur fuer globale Admins, aus fuer alle")
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/message", `{"group":"alluser","body":"hi","chat":true}`); c != 403 {
		t.Fatal("alluser darf bob nicht beschicken", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/message", `{"group":"gadm","body":"hi","hook":true}`); c != 200 {
		t.Fatal("globaler Admin darf an jede Gruppe", c, b)
	}
}

