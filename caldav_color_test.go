package main

import (
	"strings"
	"testing"
)

const pfColor = `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:" xmlns:ic="http://apple.com/ns/ical/"><d:prop><d:displayname/><ic:calendar-color/></d:prop></d:propfind>`

// TestCalDAVColor (0.69.2): PROPFIND liefert die in cs-team gewählte Kalenderfarbe als Apple calendar-color
// (DAVx5, Thunderbird, Apple Kalender); ohne gespeicherte Farbe die automatische Farbe der Web-UI.
func TestCalDAVColor(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	pa := "passwort-anna"
	calDo(t, srv, "anna", pa, "POST", "/api/cal", `{"name":"Bunt"}`)
	calDo(t, srv, "anna", pa, "PUT", "/api/cal/bunt", `{"color":"#a855f7"}`)
	resp, b := rawReq(t, srv.URL, "anna", "PROPFIND", "/dav/anna/cal/", pfColor, "Depth", "1", "Content-Type", "text/xml")
	if resp.StatusCode != 207 {
		t.Fatalf("propfind: %d %s", resp.StatusCode, b)
	}
	i := strings.Index(b, "/dav/anna/cal/bunt/")
	if i < 0 {
		t.Fatalf("calendar missing: %s", b)
	}
	rest := b[i:]
	if j := strings.Index(rest, "</"+respTag(b)+">"); j > 0 {
		rest = rest[:j]
	}
	if !strings.Contains(rest, "#A855F7FF") || strings.Contains(rest, "404") {
		t.Fatalf("color not delivered for bunt: %s", rest)
	}
	// Standardkalender ohne Farbe: automatische Farbe, ebenfalls 200
	i = strings.Index(b, "/dav/anna/cal/default/")
	if i < 0 {
		t.Fatalf("default missing: %s", b)
	}
	rest = b[i:]
	if j := strings.Index(rest, "</"+respTag(b)+">"); j > 0 {
		rest = rest[:j]
	}
	if !strings.Contains(rest, "calendar-color") || strings.Contains(rest, "404") {
		t.Fatalf("auto color for default: %s", rest)
	}
}

// respTag: Name des response-Elements mit Präfix, wie es in der Antwort steht (z. B. "D:response" oder "response").
func respTag(b string) string {
	if i := strings.Index(b, ":response>"); i > 0 {
		j := strings.LastIndex(b[:i], "<")
		return strings.TrimPrefix(b[j+1:i], "/") + ":response"
	}
	return "response"
}
