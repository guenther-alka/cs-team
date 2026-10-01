package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestUserPurge(t *testing.T) {
	srv, st := setup(t)
	defer srv.Close()
	ctx := context.Background()
	if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"carl","password":"passwort-carl","groups":["alluser"]}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k6","areas":["files"],"admins":["carl","anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	// Daten von carl: Datei, Dokument (für bob freigegeben), Termin
	if c, b := req(t, srv, "carl", "POST", "/api/files?name=carl.txt", "inhalt"); c != 200 {
		t.Fatal(c, b)
	}
	_, b := req(t, srv, "carl", "POST", "/api/docs", `{"name":"cdoc","type":"sheet"}`)
	var d struct{ ID string }
	json.Unmarshal([]byte(b), &d)
	req(t, srv, "carl", "POST", "/api/docs/"+d.ID+"/share", `{"read":["bob"],"write":[]}`)
	if c, b := req(t, srv, "carl", "PUT", "/dav/carl/cal/default/e1.ics", ics, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatal(c, b)
	}
	// fremde Objekte, die für carl freigegeben sind
	req(t, srv, "anna", "POST", "/api/files?name=anna.txt", "x")
	req(t, srv, "anna", "POST", "/api/filesshare/anna/anna.txt", `{"read":["carl"],"write":["carl"]}`)
	_, b = req(t, srv, "anna", "POST", "/api/docs", `{"name":"adoc","type":"text"}`)
	var ad struct{ ID string }
	json.Unmarshal([]byte(b), &ad)
	req(t, srv, "anna", "POST", "/api/docs/"+ad.ID+"/share", `{"read":["carl","bob"],"write":["carl"]}`)

	if c, _ := req(t, srv, "bob", "GET", "/api/users/carl/delete/preview", ""); c != 403 {
		t.Fatal("nur Admin:", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/users/anna/delete", `{"password":"passwort-anna","noSnapshot":true}`); c != 400 {
		t.Fatal("eigenes Konto:", c)
	}
	c, b := req(t, srv, "anna", "GET", "/api/users/carl/delete/preview", "")
	var pv struct {
		Counts   map[string]int
		Snapshot string
	}
	json.Unmarshal([]byte(b), &pv)
	if c != 200 || pv.Counts["files"] != 1 || pv.Counts["documents"] != 1 || pv.Counts["calendar"] < 1 || pv.Snapshot != "none" {
		t.Fatal("Vorschau:", c, b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/users/carl/delete", `{"password":"falsch","noSnapshot":true}`); c != 403 {
		t.Fatal("falsches Passwort:", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/users/carl/delete", `{"password":"passwort-anna"}`); c != 400 {
		t.Fatal("ohne Snapshot nur ausdrücklich:", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/users/carl/delete", `{"password":"passwort-anna","noSnapshot":true}`); c != 200 {
		t.Fatal("löschen:", c, b)
	}
	if c, _ := req(t, srv, "carl", "GET", "/api/me", ""); c != 401 {
		t.Fatal("Konto noch vorhanden:", c)
	}
	// Daten weg, Verweise bereinigt
	for _, pre := range []string{"files/carl/", "filesmeta/carl/", "cal/carl/", "doc/" + d.ID + "/"} {
		if l, _ := st.List(ctx, pre); len(l) != 0 {
			t.Fatalf("%s nicht gelöscht: %d Objekte", pre, len(l))
		}
	}
	if _, g := req(t, srv, "anna", "GET", "/api/groups", ""); strings.Contains(g, `"carl"`) {
		t.Fatal("carl noch Gruppen-Admin:", g)
	}
	if _, m := req(t, srv, "anna", "GET", "/api/files", ""); strings.Contains(m, "carl") {
		t.Fatal("Freigabe an carl noch da:", m)
	}
	mb, _, _ := st.Get(ctx, "doc/"+ad.ID+"/meta.json")
	if strings.Contains(string(mb), "carl") || !strings.Contains(string(mb), "bob") {
		t.Fatal("Dokumentfreigabe:", string(mb))
	}
	// Neuanlegen des Namens erbt nichts
	req(t, srv, "anna", "POST", "/api/users", `{"name":"carl","password":"passwort-carl","groups":["alluser"]}`)
	_, g := req(t, srv, "anna", "GET", "/api/groups", "")
	var gl []struct {
		Name   string
		Admins []string
	}
	json.Unmarshal([]byte(g), &gl)
	for _, x := range gl {
		for _, a := range x.Admins {
			if a == "carl" {
				t.Fatal("neuer carl erbt Gruppen-Admin in", x.Name)
			}
		}
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/groups", `{"name":"users","areas":["files"]}`); c != 400 {
		t.Fatal("Gruppe users muss abgelehnt werden:", c)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	r, _ := http.NewRequest("GET", srv.URL+"/", nil)
	r.SetBasicAuth("anna", "passwort-anna")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for k, want := range map[string]string{"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer"} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q", k, got)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "object-src 'none'") {
		t.Errorf("CSP: %q", csp)
	}
}

func TestFineRRuleRefused(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	bad := strings.Replace(ics, "SUMMARY:Test", "SUMMARY:Test\r\nRRULE:FREQ=SECONDLY", 1)
	if c, _ := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/r1.ics", bad, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 400 {
		t.Fatalf("FREQ=SECONDLY muss 400 sein: %d", c)
	}
	ok := strings.Replace(ics, "SUMMARY:Test", "SUMMARY:Test\r\nRRULE:FREQ=WEEKLY;COUNT=3", 1)
	if c, _ := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/r2.ics", ok, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatalf("wöchentlich muss gehen: %d", c)
	}
}
