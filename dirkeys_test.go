package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"cs-team/auth"
	"cs-team/store"
)

// 0.72: alte Spiegelkonten "name@realm" werden beim Start zu "name" (MigrateDirKeys); alle Daten ziehen mit:
// persoenliche Dateien samt Freigaben, Dokumente, Kalender, Chat, Aufgaben, Gruppen-Admin-Eintraege. Ist der Name belegt,
// bleibt das alte Konto.
func TestMigrateDirKeys(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	auth.ForceChange = false
	a := auth.New(st)
	for _, u := range []string{"anna", "bob"} {
		if err := a.SetUser(ctx, u, "passwort-"+u, u == "anna"); err != nil {
			t.Fatal(err)
		}
		a.SetPassword(ctx, u, "passwort-"+u)
	}
	a.EnsureSys(ctx, "anna")
	srv := httptest.NewServer(routes(st, a))
	defer srv.Close()

	// Konten mit Adressnamen anlegen (lokal), Daten erzeugen, danach im Speicher zu alten Spiegelkonten machen
	const old, dup = "carl@local.de", "bob@local.de"
	for _, n := range []string{old, dup} {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
		a.SetPassword(ctx, n, "passwort-"+n)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k9","areas":["files","cal"],"admins":["`+old+`"],"msg":"member","folder":"rw"}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups/k9/members", `{"add":["bob","`+old+`"]}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, old, "POST", "/api/files?name=notiz.txt", "hallo"); c != 200 {
		t.Fatal("Datei:", c, b)
	}
	if c, b := req(t, srv, "bob", "POST", "/api/files?name=geteilt.txt", "fuer carl"); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "bob", "POST", "/api/filesshare/bob/geteilt.txt", `{"read":["`+old+`"]}`)
	if c, b := req(t, srv, old, "POST", "/api/message", `{"group":"k9","body":"Gruss von carl","chat":true}`); c != 200 {
		t.Fatal("Chat:", c, b)
	}
	_, b := req(t, srv, "bob", "POST", "/api/tasks", `{"title":"Aufgabe fuer carl","group":"k9","assignee":"`+old+`"}`)
	tid := taskID(t, b)
	if c, b := req(t, srv, old, "PUT", "/dav/"+old+"/cal/default/m1.ics", ics, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatal("Kalender:", c, b)
	}
	_, b = req(t, srv, old, "POST", "/api/docs", `{"name":"plan","type":"sheet"}`)
	var dj struct{ ID string }
	json.Unmarshal([]byte(b), &dj)

	err := store.Update(ctx, st, "users/users.json", func(cur []byte) ([]byte, error) {
		var m map[string]map[string]any
		json.Unmarshal(cur, &m)
		for _, n := range []string{old, dup} {
			m[n]["source"], m[n]["realm"], m[n]["hash"] = "dir", "local.de", "!"
		}
		return json.Marshal(m)
	})
	if err != nil {
		t.Fatal(err)
	}

	a2 := auth.New(st) // frischer Stand (wie nach einem Neustart); die Module bleiben dieselben
	r := a2.MigrateDirKeys(ctx)
	if len(r.Renamed) != 1 || r.Renamed[0] != old+" -> carl" || len(r.Kept) != 1 || r.Kept[0] != dup {
		t.Fatalf("Ergebnis: %+v", r)
	}
	raw, _, _ := st.Get(ctx, "users/users.json")
	var users map[string]auth.Account
	json.Unmarshal(raw, &users)
	if c, ok := users["carl"]; !ok || c.Source != "dir" || c.Realm != "local.de" || c.Uid == 0 {
		t.Fatalf("Konto carl: %+v %v", c, ok)
	}
	if _, ok := users[old]; ok {
		t.Fatal("altes Konto noch da")
	}
	if _, ok := users[dup]; !ok {
		t.Fatal("belegter Name: altes Konto muss bleiben")
	}
	if l, _ := st.List(ctx, "filesmeta/carl/"); len(l) == 0 {
		t.Fatal("persoenliche Dateien nicht umgezogen")
	}
	if l, _ := st.List(ctx, "files/"+old+"/"); len(l) != 0 {
		t.Fatal("alte Dateien noch da")
	}
	if b, _, _ := st.Get(ctx, "filesmeta/carl/notiz.txt.json"); !strings.Contains(string(b), `"owner":"carl"`) {
		t.Fatal("Besitzer:", string(b))
	}
	if b, _, _ := st.Get(ctx, "files/carl/notiz.txt"); string(b) != "hallo" {
		t.Fatal("Inhalt:", string(b))
	}
	if b, _, _ := st.Get(ctx, "filesmeta/bob/geteilt.txt.json"); !strings.Contains(string(b), `"carl"`) || strings.Contains(string(b), old) {
		t.Fatal("Freigabe:", string(b))
	}
	if l, _ := st.List(ctx, "cal/carl/"); len(l) == 0 {
		t.Fatal("Kalender nicht umgezogen")
	}
	if l, _ := st.List(ctx, "cal/"+old+"/"); len(l) != 0 {
		t.Fatal("alter Kalender noch da")
	}
	if b, _, _ := st.Get(ctx, "doc/"+dj.ID+"/meta.json"); !strings.Contains(string(b), `"owner":"carl"`) {
		t.Fatal("Dokument:", string(b))
	}
	if b, _, _ := st.Get(ctx, "users/groups.json"); strings.Contains(string(b), old) || !strings.Contains(string(b), `"carl"`) {
		t.Fatal("Gruppen-Admins:", string(b))
	}
	_, ch := req(t, srv, "bob", "GET", "/api/chat/k9/allgemein/history", "")
	if strings.Contains(ch, old) || !strings.Contains(ch, `"carl"`) {
		t.Fatal("Chat:", ch)
	}
	_, tb := req(t, srv, "bob", "GET", "/api/tasks/"+tid, "")
	if strings.Contains(tb, old) || !strings.Contains(tb, `"assignee":"carl"`) {
		t.Fatal("Aufgabe:", tb)
	}
	if r := a2.MigrateDirKeys(ctx); len(r.Renamed) != 0 { // zweiter Lauf: nichts mehr zu tun (das belegte bleibt)
		t.Fatalf("zweiter Lauf: %+v", r)
	}
}
