package main

import (
	"cs-team/auth"
	"encoding/json"
	"strings"
	"testing"
)

func TestYearChange(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	mk := func(name string, groups ...string) {
		g, _ := json.Marshal(groups)
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+name+`","password":"passwort-`+name+`","groups":`+string(g)+`}`); c != 200 {
			t.Fatal(name, c, b)
		}
	}
	for _, g := range []string{"5a", "6a", "7a", "chor"} {
		req(t, srv, "anna", "POST", "/api/groups", `{"name":"`+g+`","areas":["files"]}`)
	}
	mk("lehrer", "alluser")
	req(t, srv, "anna", "POST", "/api/groups/5a/admins", `{"admins":["lehrer"]}`)
	req(t, srv, "anna", "POST", "/api/groups/5a/members", `{"add":["lehrer"]}`)
	mk("s1", "5a", "chor")
	mk("s2", "5a")
	mk("s3", "6a")
	mk("s4", "7a")
	groupsOf := func(u string) string {
		_, b := req(t, srv, "anna", "GET", "/api/users", "")
		var us []struct {
			Name     string
			Groups   []string
			Disabled bool
		}
		json.Unmarshal([]byte(b), &us)
		for _, x := range us {
			if x.Name == u {
				s := strings.Join(x.Groups, ",")
				if x.Disabled {
					s += "!"
				}
				return s
			}
		}
		return "?"
	}
	// Wiederholer: Gruppen-Admin darf für seine Gruppe, nur Mitglieder, nicht für fremde Gruppen
	if c, b := req(t, srv, "lehrer", "POST", "/api/groups/5a/stay", `{"stay":["s2","s3","nobody"]}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "lehrer", "POST", "/api/groups/6a/stay", `{"stay":["s3"]}`); c != 403 {
		t.Fatal("fremde Gruppe:", c)
	}
	if c, _ := req(t, srv, "s1", "POST", "/api/groups/5a/stay", `{"stay":["s1"]}`); c != 403 {
		t.Fatal("Mitglied:", c)
	}
	_, gb := req(t, srv, "lehrer", "GET", "/api/groups", "")
	if !strings.Contains(gb, `"stay":["s2"]`) {
		t.Fatal("Merkliste nur Mitglieder:", gb)
	}
	// nur globale Admins
	if c, _ := req(t, srv, "lehrer", "POST", "/api/routines/yearchange/preview", `{}`); c != 403 {
		t.Fatal("Vorschau nur Admin:", c)
	}
	type plan struct {
		Hash   string
		Moves  int
		Create []string
		Groups []struct {
			Group, To, Kind string
			Move, Stay      int
			Items           []struct {
				User        string
				Stay, Fixed bool
			}
		}
	}
	prev := func(p string) plan {
		c, b := req(t, srv, "anna", "POST", "/api/routines/yearchange/preview", p)
		if c != 200 {
			t.Fatal(c, b)
		}
		var pl plan
		json.Unmarshal([]byte(b), &pl)
		return pl
	}
	// ohne Abgangs-Regel: nur 5a->6a und 6a->7a, 7a bleibt
	pl := prev(`{}`)
	if pl.Moves != 2 || len(pl.Groups) != 3 || pl.Groups[2].Kind != "skip" {
		t.Fatalf("plan keep: %+v", pl)
	}
	if pl.Groups[0].Move != 1 || pl.Groups[0].Stay != 2 { // s1 wechselt; s2 (Wiederholer) und lehrer (Gruppen-Admin) bleiben
		t.Fatalf("5a: %+v", pl.Groups[0])
	}
	// Vorschau verändert nichts
	if groupsOf("s1") != "5a,chor" {
		t.Fatal("Vorschau hat etwas geändert:", groupsOf("s1"))
	}
	pl = prev(`{"leave":"archive","archive":"ehemalige"}`)
	if pl.Moves != 3 || len(pl.Create) != 1 || pl.Groups[2].Kind != "leave" || pl.Groups[2].To != "ehemalige" {
		t.Fatalf("plan archive: %+v", pl)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/routines/yearchange/preview", `{"leave":"archive","archive":"5a"}`); c != 400 {
		t.Fatal("Archivgruppe darf keine Jahrgangsgruppe sein:", c)
	}
	run := func(user, pass, hash string) (int, string) {
		return req(t, srv, user, "POST", "/api/routines/yearchange/run", `{"params":{"leave":"archive","archive":"ehemalige"},"hash":"`+hash+`","password":"`+pass+`"}`)
	}
	if c, _ := run("lehrer", "passwort-lehrer", pl.Hash); c != 403 {
		t.Fatal("run nur Admin:", c)
	}
	if c, _ := run("anna", "falsch", pl.Hash); c != 403 {
		t.Fatal("falsches Passwort:", c)
	}
	if c, _ := run("anna", "passwort-anna", "deadbeef"); c != 409 {
		t.Fatal("falsche Prüfsumme:", c)
	}
	if groupsOf("s1") != "5a,chor" {
		t.Fatal("nichts darf passiert sein:", groupsOf("s1"))
	}
	c, b := run("anna", "passwort-anna", pl.Hash)
	if c != 200 {
		t.Fatal(c, b)
	}
	var en struct{ ID string }
	json.Unmarshal([]byte(b), &en)
	for u, want := range map[string]string{"s1": "6a,chor", "s2": "5a", "s3": "7a", "s4": "ehemalige", "lehrer": "5a,alluser"} {
		if got := groupsOf(u); got != want {
			t.Fatalf("%s: %q, erwartet %q", u, got, want)
		}
	}
	_, gb = req(t, srv, "anna", "GET", "/api/groups", "")
	if !strings.Contains(gb, `"name":"ehemalige"`) || strings.Contains(gb, `"stay":["s2"]`) {
		t.Fatal("Archivgruppe angelegt, Merkliste geleert:", gb)
	}
	// zweiter Lauf mit alter Prüfsumme wird abgelehnt (Stand hat sich geändert)
	if c, _ := run("anna", "passwort-anna", pl.Hash); c != 409 {
		t.Fatal("alter Plan:", c)
	}
	_, lg := req(t, srv, "anna", "GET", "/api/routines/log", "")
	if !strings.Contains(lg, en.ID) {
		t.Fatal("Protokoll:", lg)
	}
	// Rückgängig
	if c, _ := req(t, srv, "anna", "POST", "/api/routines/undo/"+en.ID, `{"password":"falsch"}`); c != 403 {
		t.Fatal("undo Passwort:", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/routines/undo/"+en.ID, `{"password":"passwort-anna"}`); c != 200 {
		t.Fatal(c, b)
	}
	for u, want := range map[string]string{"s1": "5a,chor", "s2": "5a", "s3": "6a", "s4": "7a"} {
		if got := groupsOf(u); got != want {
			t.Fatalf("nach undo %s: %q, erwartet %q", u, got, want)
		}
	}
	_, gb = req(t, srv, "anna", "GET", "/api/groups", "")
	if !strings.Contains(gb, `"stay":["s2"]`) {
		t.Fatal("Merkliste wiederhergestellt:", gb)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/routines/undo/"+en.ID, `{"password":"passwort-anna"}`); c != 400 {
		t.Fatal("doppelt rückgängig:", c)
	}
	// Abgänger sperren
	pl = prev(`{"leave":"disable"}`)
	if c, b := req(t, srv, "anna", "POST", "/api/routines/yearchange/run", `{"params":{"leave":"disable"},"hash":"`+pl.Hash+`","password":"passwort-anna"}`); c != 200 {
		t.Fatal(c, b)
	}
	if groupsOf("s4") != "7a!" {
		t.Fatal("gesperrt:", groupsOf("s4"))
	}
	// Namen mit führenden Nullen und mehrstelligen Zahlen
	for in, want := range map[string]string{"5a": "6a", "09": "10", "klasse9b": "klasse10b", "x007": "x008", "a": ""} {
		_, ok := auth.NextName(in)
		got, _ := auth.NextName(in)
		if want == "" && ok || got != want {
			t.Fatalf("NextName(%q)=%q, erwartet %q", in, got, want)
		}
	}
}
