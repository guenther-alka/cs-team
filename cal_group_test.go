package main

import (
	"net/url"
	"strings"
	"testing"
)

// TestCalendarGroupMember (0.69.2): jedes Mitglied legt Gruppenkalender an (Standard rw: alle Mitglieder tragen ein),
// verwalten dürfen Ersteller und Gruppen-Admins; Umziehen persönlich <-> Gruppe samt Terminen.
func TestCalendarGroupMember(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string) (int, string) { return calDo(t, srv, user, pw, method, path, body) }
	do("anna", pa, "POST", "/api/groups", `{"name":"ag","areas":["cal"]}`)
	do("anna", pa, "POST", "/api/groups", `{"name":"zz","areas":["cal"]}`)
	do("anna", pa, "POST", "/api/users/import", "mia;miamiamia1;ag\nole;oleoleole1;ag\nkim;kimkimkim1;zz\n")

	// Anlegen durch ein Mitglied: Standard rw, Ersteller verwaltet, anderes Mitglied schreibt Termine, verwaltet aber nicht
	if c, b := do("mia", "miamiamia1", "POST", "/api/cal", `{"name":"AG Termine","scope":"group","group":"ag"}`); c != 200 {
		t.Fatalf("member create: %d %s", c, b)
	}
	mc := findCal(calList(t, srv, "mia", "miamiamia1"), func(c calOut) bool { return c.Name == "AG Termine" })
	if mc == nil || mc.Mode != "rw" || !mc.Manage || !mc.Write {
		t.Fatalf("creator: %+v", mc)
	}
	oc := findCal(calList(t, srv, "ole", "oleoleole1"), func(c calOut) bool { return c.Name == "AG Termine" })
	if oc == nil || !oc.Write || oc.Manage {
		t.Fatalf("other member: %+v", oc)
	}
	base := "/api/cal/" + url.PathEscape(mc.ID)
	if c, b := do("ole", "oleoleole1", "POST", base+"/events", `{"summary":"Treffen","start":"2026-12-01T10:00:00Z"}`); c != 200 {
		t.Fatalf("member writes event: %d %s", c, b)
	}
	if c, _ := do("ole", "oleoleole1", "PUT", base, `{"name":"Umbenannt"}`); c != 403 {
		t.Fatalf("other member must not edit calendar: %d", c)
	}
	// nur die Art (Termine/Plan) darf jedes Mitglied mit Schreibrecht umschalten, nicht zusammen mit anderen Feldern
	if c, b := do("ole", "oleoleole1", "PUT", base, `{"plan":true}`); c != 200 || !strings.Contains(b, `"plan":true`) {
		t.Fatalf("member switches plan: %d %s", c, b)
	}
	if c, _ := do("ole", "oleoleole1", "PUT", base, `{"plan":false,"name":"X"}`); c != 403 {
		t.Fatalf("plan + name by member: %d", c)
	}
	do("ole", "oleoleole1", "PUT", base, `{"plan":false}`)
	if c, _ := do("kim", "kimkimkim1", "POST", "/api/cal", `{"name":"X","scope":"group","group":"ag"}`); c != 403 {
		t.Fatalf("non-member create: %d", c)
	}
	if c, _ := do("mia", "miamiamia1", "POST", "/api/cal", `{"name":"Alle","scope":"group","group":"alluser"}`); c != 403 {
		t.Fatalf("member create in alluser: %d", c)
	}

	// Umziehen: persönlich -> Gruppe (Termine wandern mit, Freigabe rw) und zurück
	do("mia", "miamiamia1", "POST", "/api/cal", `{"name":"Privat"}`)
	if c, b := do("mia", "miamiamia1", "POST", "/api/cal/privat/events", `{"summary":"Umzug","start":"2026-12-03T10:00:00Z"}`); c != 200 {
		t.Fatalf("event: %d %s", c, b)
	}
	if c, _ := do("mia", "miamiamia1", "PUT", "/api/cal/privat", `{"scope":"group","group":"zz"}`); c != 403 {
		t.Fatalf("move to foreign group: %d", c)
	}
	if c, _ := do("mia", "miamiamia1", "PUT", "/api/cal/default", `{"scope":"group","group":"ag"}`); c != 400 {
		t.Fatalf("default calendar must not move: %d", c)
	}
	c, b := do("mia", "miamiamia1", "PUT", "/api/cal/privat", `{"scope":"group","group":"ag"}`)
	if c != 200 || !strings.Contains(b, `"id":"@ag~privat"`) || !strings.Contains(b, `"mode":"rw"`) {
		t.Fatalf("move to group: %d %s", c, b)
	}
	win := "?from=2026-11-01T00:00:00Z&to=2027-01-01T00:00:00Z"
	if c, b := do("ole", "oleoleole1", "GET", "/api/cal/@ag~privat/events"+win, ""); c != 200 || !strings.Contains(b, "Umzug") {
		t.Fatalf("group sees moved events: %d %s", c, b)
	}
	if c, _ := do("mia", "miamiamia1", "GET", "/api/cal/privat/events", ""); c != 404 {
		t.Fatalf("old personal calendar still there: %d", c)
	}
	c, b = do("mia", "miamiamia1", "PUT", "/api/cal/"+url.PathEscape("@ag~privat"), `{"scope":"user"}`)
	if c != 200 || !strings.Contains(b, `"id":"privat"`) {
		t.Fatalf("move back: %d %s", c, b)
	}
	if c, b := do("mia", "miamiamia1", "GET", "/api/cal/privat/events"+win, ""); c != 200 || !strings.Contains(b, "Umzug") {
		t.Fatalf("events back in personal: %d %s", c, b)
	}
	if c, _ := do("ole", "oleoleole1", "GET", "/api/cal/@ag~privat/events", ""); c != 404 {
		t.Fatalf("group calendar must be gone: %d", c)
	}
}
