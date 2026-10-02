package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCalVTimezone(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	mk := func(tz string) string {
		c, b := do("POST", "/api/cal/default/events", `{"summary":"Zone","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","tz":"`+tz+`","rule":{"freq":"WEEKLY","count":3}}`)
		if c != 200 {
			t.Fatal(tz, c, b)
		}
		var f struct{ File string }
		json.Unmarshal([]byte(b), &f)
		_, ics := do("GET", "/dav/anna/cal/default/"+f.File, "")
		return strings.ReplaceAll(ics, "\r\n", "\n")
	}
	has := func(zone, ics string, wants ...string) {
		for _, w := range wants {
			if !strings.Contains(ics, w) {
				t.Fatalf("%s: %q fehlt in\n%s", zone, w, ics)
			}
		}
	}
	// Berlin: Sommerzeit letzter Sonntag im März, Normalzeit letzter Sonntag im Oktober
	ics := mk("Europe/Berlin")
	has("Berlin", ics, "BEGIN:VTIMEZONE\nTZID:Europe/Berlin", "BEGIN:DAYLIGHT", "TZOFFSETFROM:+0100", "TZOFFSETTO:+0200", "RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=-1SU",
		"BEGIN:STANDARD", "TZOFFSETFROM:+0200", "TZOFFSETTO:+0100", "RRULE:FREQ=YEARLY;BYMONTH=10;BYDAY=-1SU", "DTSTART:20070325T020000", "DTSTART:20071028T030000")
	if strings.Index(ics, "BEGIN:VTIMEZONE") > strings.Index(ics, "BEGIN:VEVENT") {
		t.Fatal("VTIMEZONE muss vor dem Termin stehen")
	}
	// New York: zweiter Sonntag im März, erster Sonntag im November
	has("NY", mk("America/New_York"), "TZID:America/New_York", "TZOFFSETFROM:-0500", "TZOFFSETTO:-0400", "RRULE:FREQ=YEARLY;BYMONTH=3;BYDAY=2SU", "RRULE:FREQ=YEARLY;BYMONTH=11;BYDAY=1SU")
	// Indien: keine Umstellung -> ein Block ohne Regel
	ics = mk("Asia/Kolkata")
	has("Indien", ics, "TZID:Asia/Kolkata", "BEGIN:STANDARD", "TZOFFSETTO:+0530")
	if strings.Contains(ics, "BEGIN:DAYLIGHT") || strings.Contains(ics[strings.Index(ics, "BEGIN:VTIMEZONE"):strings.Index(ics, "END:VTIMEZONE")], "RRULE") {
		t.Fatal("Indien ohne Regel:", ics)
	}
	// UTC und ganztägig: keine VTIMEZONE
	c, b := do("POST", "/api/cal/default/events", `{"summary":"U","start":"2027-03-15T08:00:00Z"}`)
	var f struct{ File string }
	json.Unmarshal([]byte(b), &f)
	if _, u := do("GET", "/dav/anna/cal/default/"+f.File, ""); c != 200 || strings.Contains(u, "VTIMEZONE") {
		t.Fatal("UTC:", c, u)
	}
	// Bearbeiten ändert nichts an der Definition (kein Doppeleintrag), Anzeige bleibt richtig
	ics = mk("Europe/Berlin")
	if strings.Count(ics, "BEGIN:VTIMEZONE") != 1 {
		t.Fatal("doppelt:", ics)
	}
}
