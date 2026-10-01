package main

import (
	"context"
	"cs-team/auth"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
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
		return req(t, srv, user, "POST", "/api/routines/yearchange/run", `{"params":{"leave":"archive","archive":"ehemalige"},"hash":"`+hash+`","password":"`+pass+`","noSnapshot":true}`)
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
	if c, b := req(t, srv, "anna", "POST", "/api/routines/yearchange/run", `{"params":{"leave":"disable"},"hash":"`+pl.Hash+`","password":"passwort-anna","noSnapshot":true}`); c != 200 {
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

func TestYearRename(t *testing.T) {
	srv, st := setup(t)
	defer srv.Close()
	ctx := context.Background()
	for _, g := range []string{"5a", "6a", "7a"} {
		if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"`+g+`","areas":["files"],"folder":"rw","cal":"rw","tasks":"member"}`); c != 200 {
			t.Fatal(g, c, b)
		}
	}
	mk := func(name string, groups ...string) {
		g, _ := json.Marshal(groups)
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+name+`","password":"passwort-`+name+`","groups":`+string(g)+`}`); c != 200 {
			t.Fatal(name, c, b)
		}
	}
	mk("lehrer", "alluser")
	req(t, srv, "anna", "POST", "/api/groups/5a/members", `{"add":["lehrer"]}`)
	req(t, srv, "anna", "POST", "/api/groups/5a/admins", `{"admins":["lehrer"]}`)
	mk("s1", "5a")
	mk("s2", "5a")
	mk("s3", "6a")
	mk("s4", "7a")
	req(t, srv, "lehrer", "POST", "/api/groups/5a/stay", `{"stay":["s2"]}`)
	// Daten der Gruppen: Gruppenordner, Freigabe an die Gruppe, Chat, Aufgabe
	if c, b := req(t, srv, "s1", "POST", "/api/files?owner=@5a&name=a.txt", "AAA"); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "s3", "POST", "/api/files?owner=@6a&name=b.txt", "BBB")
	req(t, srv, "s4", "POST", "/api/files?owner=@7a&name=c.txt", "CCC")
	req(t, srv, "s1", "POST", "/api/files?name=priv.txt", "PRIV")
	req(t, srv, "s1", "POST", "/api/filesshare/s1/priv.txt", `{"read":["g:5a"]}`)
	st.Put(ctx, "chat/5a/allgemein.json", []byte(`[{"id":1,"by":"s1","t":"hallo5a"}]`), "")
	st.Put(ctx, "chat/6a/allgemein.json", []byte(`[{"id":1,"by":"s3","t":"hallo6a"}]`), "")
	if c, b := req(t, srv, "s1", "POST", "/api/tasks", `{"title":"T5","group":"5a"}`); c != 200 {
		t.Fatal(c, b)
	}
	year := time.Now().Year()
	arch := "ehem-7a-" + strconv.Itoa(year)
	params := `{"mode":"rename","leave":"archive","carry":{"cal":true,"chat":true,"tasks":true}}`
	type plan struct {
		Hash   string
		Moves  int
		Repeat int
		Create []string
		Pairs  []struct{ From, To string }
		Groups []struct{ Group, Kind string }
	}
	c, b := req(t, srv, "anna", "POST", "/api/routines/yearchange/preview", params)
	var pl plan
	json.Unmarshal([]byte(b), &pl)
	if c != 200 || len(pl.Pairs) != 3 || pl.Pairs[0].To != arch || pl.Pairs[1].From != "6a" || pl.Pairs[2].To != "6a" || len(pl.Create) != 1 || pl.Create[0] != "5a" || pl.Repeat != 1 {
		t.Fatalf("plan: %d %s", c, b)
	}
	groupsOf := func(u string) string {
		_, b := req(t, srv, "anna", "GET", "/api/users", "")
		var us []struct {
			Name   string
			Groups []string
		}
		json.Unmarshal([]byte(b), &us)
		for _, x := range us {
			if x.Name == u {
				return strings.Join(x.Groups, ",")
			}
		}
		return "?"
	}
	has := func(key string) bool { _, _, err := st.Get(ctx, key); return err == nil }
	run := func(h string) (int, string) {
		return req(t, srv, "anna", "POST", "/api/routines/yearchange/run", `{"params":`+params+`,"hash":"`+h+`","password":"passwort-anna","noSnapshot":true}`)
	}
	c, b = run(pl.Hash)
	if c != 200 {
		t.Fatal(c, b)
	}
	var en struct{ ID string }
	json.Unmarshal([]byte(b), &en)
	for u, want := range map[string]string{"s1": "6a", "s2": "5a", "s3": "7a", "s4": arch, "lehrer": "6a,alluser"} {
		if got := groupsOf(u); got != want {
			t.Fatalf("%s: %q, erwartet %q", u, got, want)
		}
	}
	if c, b := req(t, srv, "s1", "GET", "/api/files/@6a/a.txt", ""); c != 200 || b != "AAA" {
		t.Fatalf("Gruppenordner wandert mit: %d %q", c, b)
	}
	if c, b := req(t, srv, "s3", "GET", "/api/files/@7a/b.txt", ""); c != 200 || b != "BBB" {
		t.Fatalf("6a -> 7a: %d %q", c, b)
	}
	if c, _ := req(t, srv, "s2", "GET", "/api/files/@5a/a.txt", ""); c != 404 && c != 403 {
		t.Fatal("neue 5a ist leer:", c)
	}
	if c, b := req(t, srv, "s4", "GET", "/api/files/@"+arch+"/c.txt", ""); c != 200 || b != "CCC" {
		t.Fatalf("Abgang: %d %q", c, b)
	}
	_, lst := req(t, srv, "s1", "GET", "/api/files", "")
	if !strings.Contains(lst, `"g:6a"`) || strings.Contains(lst, `"g:5a"`) {
		t.Fatal("Freigabe folgt der Gruppe:", lst)
	}
	if !has("chat/6a/allgemein.json") || !has("chat/7a/allgemein.json") || has("chat/5a/allgemein.json") {
		t.Fatal("Chat wandert mit")
	}
	if !has("cal/@6a/gruppe/_meta.json") || !has("cal/@5a/gruppe/_meta.json") {
		t.Fatal("Kalender wandert mit, neue Gruppe bekommt einen neuen")
	}
	_, tb := req(t, srv, "s1", "GET", "/api/tasks", "")
	if !strings.Contains(tb, `"group":"6a"`) {
		t.Fatal("Aufgabe:", tb)
	}
	_, gb := req(t, srv, "anna", "GET", "/api/groups", "")
	var gs []struct {
		Name   string
		Admins []string
		Stay   []string
	}
	json.Unmarshal([]byte(gb), &gs)
	adm := map[string]string{}
	for _, g := range gs {
		adm[g.Name] = strings.Join(g.Admins, ",")
		if len(g.Stay) > 0 {
			t.Fatal("Merkliste geleert:", g.Name)
		}
	}
	if adm["6a"] != "lehrer" || adm["5a"] != "" {
		t.Fatalf("Gruppen-Admins wandern mit: %v", adm)
	}
	// Rückgängig
	if c, b := req(t, srv, "anna", "POST", "/api/routines/undo/"+en.ID, `{"password":"passwort-anna"}`); c != 200 {
		t.Fatal(c, b)
	}
	for u, want := range map[string]string{"s1": "5a", "s2": "5a", "s3": "6a", "s4": "7a", "lehrer": "5a,alluser"} {
		if got := groupsOf(u); got != want {
			t.Fatalf("nach undo %s: %q, erwartet %q", u, got, want)
		}
	}
	if c, b := req(t, srv, "s1", "GET", "/api/files/@5a/a.txt", ""); c != 200 || b != "AAA" {
		t.Fatalf("undo Datei: %d %q", c, b)
	}
	if !has("chat/5a/allgemein.json") || has("chat/6a/allgemein.json") && !strings.Contains(string(func() []byte { x, _, _ := st.Get(ctx, "chat/6a/allgemein.json"); return x }()), "hallo6a") {
		t.Fatal("undo Chat")
	}
	_, tb = req(t, srv, "s1", "GET", "/api/tasks", "")
	if !strings.Contains(tb, `"group":"5a"`) {
		t.Fatal("undo Aufgabe:", tb)
	}
	// zweiter Lauf, dann Daten in der neuen Gruppe: Rückgängig wird abgelehnt
	c, b = req(t, srv, "anna", "POST", "/api/routines/yearchange/preview", params)
	json.Unmarshal([]byte(b), &pl)
	if c, b := run(pl.Hash); c != 200 {
		t.Fatal(c, b)
	}
	var en2 struct{ ID string }
	_, lg := req(t, srv, "anna", "GET", "/api/routines/log", "")
	var logs []struct{ ID string }
	json.Unmarshal([]byte(lg), &logs)
	en2.ID = logs[len(logs)-1].ID
	if c, b := req(t, srv, "s2", "POST", "/api/files?owner=@5a&name=neu.txt", "NEU"); c != 200 {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/routines/undo/"+en2.ID, `{"password":"passwort-anna"}`); c != 400 {
		t.Fatal("neue Gruppe mit Daten: Rückgängig muss abgelehnt werden:", c)
	}
	// blockiert: Abgang unverändert -> kein Umbenennen möglich
	c, b = req(t, srv, "anna", "POST", "/api/routines/yearchange/preview", `{"mode":"rename","leave":"keep"}`)
	var pk plan
	json.Unmarshal([]byte(b), &pk)
	if c != 200 || pk.Moves != 0 || !strings.Contains(b, `"blocked"`) {
		t.Fatalf("keep: %d %s", c, b)
	}
}

func TestYearSnapshotHook(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	defer func() { auth.SnapshotCmd = "" }()
	for _, g := range []string{"5a", "6a"} {
		req(t, srv, "anna", "POST", "/api/groups", `{"name":"`+g+`","areas":["files"]}`)
	}
	req(t, srv, "anna", "POST", "/api/users", `{"name":"s1","password":"passwort-s1","groups":["5a"]}`)
	_, b := req(t, srv, "anna", "POST", "/api/routines/yearchange/preview", `{}`)
	var pl struct{ Hash string }
	json.Unmarshal([]byte(b), &pl)
	run := func() (int, string) {
		return req(t, srv, "anna", "POST", "/api/routines/yearchange/run", `{"params":{},"hash":"`+pl.Hash+`","password":"passwort-anna"}`)
	}
	if c, b := run(); c != 400 || !strings.Contains(b, "noSnapshot") {
		t.Fatal("ohne Snapshot nur mit ausdrücklicher Bestätigung:", c, b)
	}
	auth.SnapshotCmd = "exit 1"
	if c, b := run(); c != 400 || !strings.Contains(b, "snapshot") {
		t.Fatal("fehlgeschlagener Snapshot muss den Lauf verhindern:", c, b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/users", ""); !strings.Contains(b, `"5a"`) {
		t.Fatal("nichts darf geändert sein:", b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/routines/info", ""); !strings.Contains(b, `"snapshot":"cmd"`) {
		t.Fatal(b)
	}
	auth.SnapshotCmd = "echo {id}"
	if c, b := run(); c != 200 {
		t.Fatal(c, b)
	}
}
