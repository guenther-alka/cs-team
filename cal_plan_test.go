package main

import (
	"strings"
	"testing"
)

// TestCalendarPlan (0.69.2): Plan-Kalender (persönlich/Gruppe), Einträge werden ganztägig auf den Quartalsanfang
// eingerastet, Wiederholung nur jährlich; global/Abo als Plan abgelehnt.
func TestCalendarPlan(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	pa := "passwort-anna"
	do := func(method, path, body string) (int, string) { return calDo(t, srv, "anna", pa, method, path, body) }
	if c, b := do("POST", "/api/cal", `{"name":"Jahresplan","plan":true}`); c != 200 {
		t.Fatalf("create plan: %d %s", c, b)
	}
	if c, _ := do("POST", "/api/cal", `{"name":"GPlan","scope":"global","plan":true}`); c != 400 {
		t.Fatalf("global plan must be refused: %d", c)
	}
	if _, b := do("GET", "/api/cal", ""); !strings.Contains(b, `"plan":true`) {
		t.Fatalf("plan flag in list: %s", b)
	}
	// Art umschalten: Termine -> Plan und zurück (bearbeiten), globaler Kalender nie
	do("POST", "/api/cal", `{"name":"Wechsel"}`)
	if c, b := do("PUT", "/api/cal/wechsel", `{"plan":true}`); c != 200 || !strings.Contains(b, `"plan":true`) {
		t.Fatalf("switch to plan: %d %s", c, b)
	}
	if c, b := do("PUT", "/api/cal/wechsel", `{"plan":false}`); c != 200 || strings.Contains(b, `"plan":true`) {
		t.Fatalf("switch back: %d %s", c, b)
	}
	do("POST", "/api/cal", `{"name":"Glob","scope":"global"}`)
	if c, _ := do("PUT", "/api/cal/_global~glob", `{"plan":true}`); c != 400 {
		t.Fatalf("global calendar must not become plan: %d", c)
	}
	// Termin mitten im Mai mit Uhrzeit und monatlicher Regel -> 1. April ganztägig, jährlich
	if c, b := do("POST", "/api/cal/jahresplan/events", `{"summary":"Projekt A","start":"2027-05-17T10:00:00Z","rule":{"freq":"MONTHLY","count":3}}`); c != 200 {
		t.Fatalf("add plan entry: %d %s", c, b)
	}
	_, b := do("GET", "/api/cal/jahresplan/events?from=2027-01-01T00:00:00Z&to=2029-01-01T00:00:00Z", "")
	if !strings.Contains(b, `"start":"2027-04-01T00:00:00Z"`) || !strings.Contains(b, `"end":"2027-07-01T00:00:00Z"`) ||
		!strings.Contains(b, `"start":"2028-04-01T00:00:00Z"`) || !strings.Contains(b, `"freq":"YEARLY"`) || !strings.Contains(b, `"allDay":true`) {
		t.Fatalf("plan entry not snapped to quarter / yearly: %s", b)
	}
	// Notenkonferenz: Q1, halbjährlich -> 1.1. und 1.7. jedes Jahr
	if c, b := do("POST", "/api/cal/jahresplan/events", `{"summary":"Notenkonferenz","start":"2027-02-10T00:00:00Z","allDay":true,"rule":{"freq":"MONTHLY","interval":6}}`); c != 200 {
		t.Fatalf("add half-yearly: %d %s", c, b)
	}
	_, b = do("GET", "/api/cal/jahresplan/events?from=2027-01-01T00:00:00Z&to=2028-01-01T00:00:00Z", "")
	if !strings.Contains(b, `"summary":"Notenkonferenz","start":"2027-01-01T00:00:00Z"`) || !strings.Contains(b, `"summary":"Notenkonferenz","start":"2027-07-01T00:00:00Z"`) ||
		strings.Contains(b, `"summary":"Notenkonferenz","start":"2027-04-01`) {
		t.Fatalf("half-yearly plan entry: %s", b)
	}
	// alle 2 Jahre bleibt erhalten
	if c, b := do("POST", "/api/cal/jahresplan/events", `{"summary":"Wahl","start":"2027-10-05T00:00:00Z","allDay":true,"rule":{"freq":"YEARLY","interval":2}}`); c != 200 {
		t.Fatalf("add 2-yearly: %d %s", c, b)
	}
	_, b = do("GET", "/api/cal/jahresplan/events?from=2027-09-01T00:00:00Z&to=2029-12-31T00:00:00Z", "") // Fenster < 800 Tage (maxWindow)
	if !strings.Contains(b, `"summary":"Wahl","start":"2027-10-01T00:00:00Z"`) || !strings.Contains(b, `"summary":"Wahl","start":"2029-10-01T00:00:00Z"`) ||
		strings.Contains(b, `"summary":"Wahl","start":"2028-10-01`) {
		t.Fatalf("2-yearly plan entry: %s", b)
	}
}
