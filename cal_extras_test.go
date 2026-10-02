package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type putRes struct {
	File  string
	Mails int
}

func mustPut(t *testing.T, do func(m, p, b string) (int, string), m, path, body string) putRes {
	t.Helper()
	c, b := do(m, path, body)
	if c != 200 {
		t.Fatal(m, path, c, b)
	}
	var r putRes
	json.Unmarshal([]byte(b), &r)
	return r
}

// icsOfMail liefert die Kalenderdatei und die Methode aus einer empfangenen Einladungsmail.
func icsOfMail(t *testing.T, m fakeMail) (ics, method string) {
	t.Helper()
	i := strings.Index(m.data, "Content-Type: text/calendar")
	if i < 0 {
		t.Fatal("kein text/calendar:", m.data)
	}
	rest := m.data[i:]
	line := rest[:strings.Index(rest, "\r\n")]
	if j := strings.Index(line, "method="); j >= 0 {
		method = line[j+7:]
	}
	k := strings.Index(rest, "\r\n\r\n")
	body := rest[k+4:]
	body = body[:strings.Index(body, "\r\n--")]
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n"), method
}

func wantMail(t *testing.T, ch chan fakeMail) fakeMail {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("keine Mail")
	}
	return fakeMail{}
}

func noMail(t *testing.T, ch chan fakeMail) {
	t.Helper()
	select {
	case m := <-ch:
		t.Fatal("unerwartete Mail an", m.rcpt)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCalAlarm(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	r := mustPut(t, do, "POST", "/api/cal/default/events", `{"summary":"Zahnarzt","start":"2027-03-15T08:00:00Z","alarm":15}`)
	_, ics := do("GET", "/dav/anna/cal/default/"+r.File, "")
	for _, w := range []string{"BEGIN:VALARM", "ACTION:DISPLAY", "TRIGGER:-PT15M", "END:VALARM"} {
		if !strings.Contains(ics, w) {
			t.Fatalf("%q fehlt:\n%s", w, ics)
		}
	}
	ev := listEv(t, do, "2027-03-01", "2027-04-01")
	if len(ev) != 1 || ev[0].Alarm == nil || *ev[0].Alarm != 15 {
		t.Fatalf("%+v", ev)
	}
	// ohne Angabe bleibt die Erinnerung erhalten, 1 Tag und 0 Minuten, dann entfernen
	mustPut(t, do, "PUT", "/api/cal/default/events/"+r.File, `{"summary":"Zahnarzt 2","start":"2027-03-15T08:00:00Z"}`)
	if ev = listEv(t, do, "2027-03-01", "2027-04-01"); ev[0].Alarm == nil || *ev[0].Alarm != 15 {
		t.Fatal("Erinnerung verloren", ev[0].Alarm)
	}
	mustPut(t, do, "PUT", "/api/cal/default/events/"+r.File, `{"summary":"Zahnarzt 2","start":"2027-03-15T08:00:00Z","alarm":1440}`)
	if _, ics = do("GET", "/dav/anna/cal/default/"+r.File, ""); !strings.Contains(ics, "-P1D") {
		t.Fatal(ics)
	}
	mustPut(t, do, "PUT", "/api/cal/default/events/"+r.File, `{"summary":"Zahnarzt 2","start":"2027-03-15T08:00:00Z","alarm":0}`)
	if ev = listEv(t, do, "2027-03-01", "2027-04-01"); ev[0].Alarm == nil || *ev[0].Alarm != 0 {
		t.Fatal("0 Minuten", ev[0].Alarm)
	}
	mustPut(t, do, "PUT", "/api/cal/default/events/"+r.File, `{"summary":"Zahnarzt 2","start":"2027-03-15T08:00:00Z","alarm":-1}`)
	if _, ics = do("GET", "/dav/anna/cal/default/"+r.File, ""); strings.Contains(ics, "VALARM") {
		t.Fatal(ics)
	}
	if c, _ := do("POST", "/api/cal/default/events", `{"summary":"x","start":"2027-03-15T08:00:00Z","alarm":999999}`); c != 400 {
		t.Fatal("zu großer Wert", c)
	}
	// fremde Erinnerung (Thunderbird: -PT30M mit RELATED) wird gelesen
	ext := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:al1@t\r\nDTSTAMP:20270101T000000Z\r\nDTSTART:20270320T100000Z\r\nDTEND:20270320T110000Z\r\nSUMMARY:Ext\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:x\r\nTRIGGER;RELATED=START:-PT30M\r\nEND:VALARM\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if resp, b := rawReq(t, srv.URL, "anna", "PUT", "/dav/anna/cal/default/al1.ics", ext, "Content-Type", "text/calendar"); resp.StatusCode/100 != 2 {
		t.Fatal(resp.StatusCode, b)
	}
	found := false
	for _, e := range listEv(t, do, "2027-03-01", "2027-04-01") {
		if e.Summary == "Ext" {
			found = true
			if e.Alarm == nil || *e.Alarm != 30 {
				t.Fatal("Ext-Erinnerung", e.Alarm)
			}
		}
	}
	if !found {
		t.Fatal("Ext fehlt")
	}
}

func TestCalAttendeesInvite(t *testing.T) {
	host, port, got := smtpServer(t)
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	// ohne Mailversand: Teilnehmer werden gespeichert, aber nichts verschickt
	if c, b := do("POST", "/api/users/bob/contact", `{"mail":"bob@example.com"}`); c != 200 {
		t.Fatal(c, b)
	}
	r := mustPut(t, do, "POST", "/api/cal/default/events", `{"summary":"Plan","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","att":["bob","ext@example.org"]}`)
	if r.Mails != 0 {
		t.Fatal("Mails ohne SMTP:", r.Mails)
	}
	noMail(t, got)
	ev := listEv(t, do, "2027-03-01", "2027-04-01")
	if len(ev) != 1 || len(ev[0].Att) != 2 || ev[0].Att[0].Name != "bob" || ev[0].Att[0].Mail != "bob@example.com" || ev[0].Att[1].Mail != "ext@example.org" || ev[0].Att[0].Status != "NEEDS-ACTION" || ev[0].Org != "anna" {
		t.Fatalf("%+v", ev)
	}
	_, ics := do("GET", "/dav/anna/cal/default/"+r.File, "")
	for _, w := range []string{"ATTENDEE;", "mailto:bob@example.com", "PARTSTAT=NEEDS-ACTION", "ORGANIZER"} {
		if !strings.Contains(ics, w) {
			t.Fatalf("%q fehlt:\n%s", w, ics)
		}
	}
	do("DELETE", "/api/cal/default/events/"+r.File, "")
	// Fehlerfälle
	for _, bad := range []string{`["gibtsnicht"]`, `["x@"]`, `["a b@example.org"]`} {
		if c, _ := do("POST", "/api/cal/default/events", `{"summary":"x","start":"2027-03-15T08:00:00Z","att":`+bad+`}`); c != 400 {
			t.Fatal("Teilnehmer", bad, c)
		}
	}
	// mit Mailversand
	if c, b := do("POST", "/api/settings", `{"host":"`+host+`","port":"`+port+`","tls":"none","from":"cs-team@example.org","public":"https://team.example.org:9004","private":true}`); c != 200 {
		t.Fatal(c, b)
	}
	r = mustPut(t, do, "POST", "/api/cal/default/events", `{"summary":"Plan","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","location":"Raum 1","att":["bob","ext@example.org"]}`)
	if r.Mails != 2 {
		t.Fatal("Mails:", r.Mails)
	}
	m := wantMail(t, got)
	if len(m.rcpt) != 2 {
		t.Fatal(m.rcpt)
	}
	ics, method := icsOfMail(t, m)
	if method != "REQUEST" || !strings.Contains(ics, "METHOD:REQUEST") || !strings.Contains(ics, "SUMMARY:Plan") || !strings.Contains(ics, "mailto:ext@example.org") {
		t.Fatal(method, ics)
	}
	noMail(t, got)
	// Änderung der Zeit: bleibende Teilnehmer bekommen eine Aktualisierung (SEQUENCE steigt)
	mustPut(t, do, "PUT", "/api/cal/default/events/"+r.File, `{"summary":"Plan","start":"2027-03-15T10:00:00Z","end":"2027-03-15T11:00:00Z","location":"Raum 1"}`)
	m = wantMail(t, got)
	ics, method = icsOfMail(t, m)
	if method != "REQUEST" || len(m.rcpt) != 2 || !strings.Contains(ics, "SEQUENCE:1") || !strings.Contains(ics, "T100000Z") {
		t.Fatal(method, m.rcpt, ics)
	}
	// nur Erinnerung geändert: keine Mail
	mustPut(t, do, "PUT", "/api/cal/default/events/"+r.File, `{"summary":"Plan","start":"2027-03-15T10:00:00Z","end":"2027-03-15T11:00:00Z","location":"Raum 1","alarm":10}`)
	noMail(t, got)
	// Teilnehmer entfernen und hinzufügen: Absage an ext, Einladung an anna-fremde neue
	mustPut(t, do, "PUT", "/api/cal/default/events/"+r.File, `{"summary":"Plan","start":"2027-03-15T10:00:00Z","end":"2027-03-15T11:00:00Z","location":"Raum 1","att":["bob","neu@example.org"]}`)
	seen := map[string]string{}
	for i := 0; i < 2; i++ {
		m = wantMail(t, got)
		_, method = icsOfMail(t, m)
		seen[method] = strings.Join(m.rcpt, ",")
	}
	if !strings.Contains(seen["REQUEST"], "neu@example.org") || seen["CANCEL"] != "ext@example.org" {
		t.Fatal(seen)
	}
	noMail(t, got)
	// Teilnehmer ohne Änderung der Liste übernehmen: nichts neu verschicken
	ev = listEv(t, do, "2027-03-01", "2027-04-01")
	if len(ev[0].Att) != 2 {
		t.Fatalf("%+v", ev[0].Att)
	}
	// Löschen: Absage an alle
	do("DELETE", "/api/cal/default/events/"+r.File, "")
	m = wantMail(t, got)
	ics, method = icsOfMail(t, m)
	if method != "CANCEL" || len(m.rcpt) != 2 || !strings.Contains(ics, "STATUS:CANCELLED") {
		t.Fatal(method, m.rcpt, ics)
	}
}

func TestCalFollowing(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	mk := func(rule string) string {
		return mustPut(t, do, "POST", "/api/cal/default/events", `{"summary":"Jour fixe","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","alarm":10,"rule":`+rule+`}`).File
	}
	sums := func() []string {
		var out []string
		for _, e := range listEv(t, do, "2027-03-01", "2027-06-01") {
			out = append(out, e.Start[:10]+" "+e.Start[11:16]+" "+e.Summary)
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("\n got: %q\nwant: %q", got, want)
		}
	}
	f := mk(`{"freq":"WEEKLY","count":6}`)
	// ab 29.03. (3. Termin): neue Uhrzeit und Titel; Rest der Serie = 4 Termine
	r := mustPut(t, do, "PUT", "/api/cal/default/events/"+f, `{"summary":"Jour fixe neu","start":"2027-03-29T10:00:00Z","end":"2027-03-29T11:00:00Z","scope":"following","rid":"2027-03-29T08:00:00Z","rule":{"freq":"WEEKLY","count":6}}`)
	if r.File == f || r.File == "" {
		t.Fatal("neue Serie braucht eine neue Datei", r)
	}
	eq(sums(), "2027-03-15 08:00 Jour fixe", "2027-03-22 08:00 Jour fixe",
		"2027-03-29 10:00 Jour fixe neu", "2027-04-05 10:00 Jour fixe neu", "2027-04-12 10:00 Jour fixe neu", "2027-04-19 10:00 Jour fixe neu")
	ev := listEv(t, do, "2027-03-01", "2027-06-01")
	if ev[0].Rule == nil || ev[0].Rule.Until == "" || ev[0].Rule.Count != 0 {
		t.Fatalf("alter Teil muss mit UNTIL enden: %+v", ev[0].Rule)
	}
	if ev[2].Rule == nil || ev[2].Rule.Count != 4 || ev[2].Alarm == nil || *ev[2].Alarm != 10 {
		t.Fatalf("neue Serie: %+v %v", ev[2].Rule, ev[2].Alarm)
	}
	// "dieser und folgende" löschen: ab 12.04. der neuen Serie
	if c, b := do("DELETE", "/api/cal/default/events/"+r.File+"?scope=following&rid=2027-04-12T10:00:00Z", ""); c != 200 && c != 204 {
		t.Fatal(c, b)
	}
	eq(sums(), "2027-03-15 08:00 Jour fixe", "2027-03-22 08:00 Jour fixe", "2027-03-29 10:00 Jour fixe neu", "2027-04-05 10:00 Jour fixe neu")
	// Löschen ab dem ersten Termin löscht die ganze Serie
	if c, b := do("DELETE", "/api/cal/default/events/"+r.File+"?scope=following&rid=2027-03-29T10:00:00Z", ""); c != 200 && c != 204 {
		t.Fatal(c, b)
	}
	eq(sums(), "2027-03-15 08:00 Jour fixe", "2027-03-22 08:00 Jour fixe")
	// ab dem ersten Termin ändern = ganze Serie, gleiche Datei
	r = mustPut(t, do, "PUT", "/api/cal/default/events/"+f, `{"summary":"Alle","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","scope":"following","rid":"2027-03-15T08:00:00Z"}`)
	if r.File != f {
		t.Fatal("gleiche Datei erwartet", r)
	}
	eq(sums(), "2027-03-15 08:00 Alle", "2027-03-22 08:00 Alle")
	// unbefristete Serie: neue Serie bleibt unbefristet; Serie mit Ende (UNTIL) behält das Ende
	g := mk(`{"freq":"WEEKLY","until":"2027-04-30"}`)
	r = mustPut(t, do, "PUT", "/api/cal/default/events/"+g, `{"summary":"Spät","start":"2027-04-05T08:00:00Z","end":"2027-04-05T09:00:00Z","scope":"following","rid":"2027-04-05T08:00:00Z"}`)
	var spaet []string
	for _, e := range listEv(t, do, "2027-04-01", "2027-06-01") {
		if e.Summary == "Spät" {
			spaet = append(spaet, e.Start[:10])
		}
	}
	eq(spaet, "2027-04-05", "2027-04-12", "2027-04-19", "2027-04-26")
	// Einzeländerung vor dem Schnitt bleibt im alten Teil, danach entfällt
	h := mk(`{"freq":"DAILY","count":5}`)
	mustPut(t, do, "PUT", "/api/cal/default/events/"+h, `{"summary":"Tag 2 anders","start":"2027-03-16T08:00:00Z","end":"2027-03-16T09:00:00Z","scope":"one","rid":"2027-03-16T08:00:00Z"}`)
	mustPut(t, do, "PUT", "/api/cal/default/events/"+h, `{"summary":"Tag 4 anders","start":"2027-03-18T08:00:00Z","end":"2027-03-18T09:00:00Z","scope":"one","rid":"2027-03-18T08:00:00Z"}`)
	mustPut(t, do, "PUT", "/api/cal/default/events/"+h, `{"summary":"Neu","start":"2027-03-17T08:00:00Z","end":"2027-03-17T09:00:00Z","scope":"following","rid":"2027-03-17T08:00:00Z"}`)
	var days []string
	for _, e := range listEv(t, do, "2027-03-15", "2027-03-21") {
		if strings.HasPrefix(e.Summary, "Jour fixe") || strings.HasPrefix(e.Summary, "Tag") || e.Summary == "Neu" {
			days = append(days, e.Start[8:10]+" "+e.Summary)
		}
	}
	eq(days, "15 Jour fixe", "15 Jour fixe", "16 Tag 2 anders", "17 Neu", "18 Neu", "19 Neu")
}

func TestCalImportExport(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	mustPut(t, do, "POST", "/api/cal/default/events", `{"summary":"Eins","start":"2027-03-15T08:00:00Z","tz":"Europe/Berlin","rule":{"freq":"WEEKLY","count":3}}`)
	mustPut(t, do, "POST", "/api/cal/default/events", `{"summary":"Zwei","start":"2027-03-16T00:00:00Z","allDay":true}`)
	resp, ics := rawReq(t, srv.URL, "anna", "GET", "/api/cal/default/export.ics", "")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/calendar") || !strings.Contains(resp.Header.Get("Content-Disposition"), ".ics") {
		t.Fatal(resp.StatusCode, resp.Header)
	}
	if strings.Count(ics, "BEGIN:VEVENT") != 2 || strings.Count(ics, "BEGIN:VTIMEZONE") != 1 || !strings.Contains(ics, "SUMMARY:Zwei") {
		t.Fatal(ics)
	}
	// andere Benutzerin importiert die Datei in einen neuen Kalender
	if c, b := req(t, srv, "bob", "POST", "/api/cal", `{"name":"Kopie"}`); c != 200 {
		t.Fatal(c, b)
	}
	imp := func(user, kal, body string) (int, string) {
		resp, b := rawReq(t, srv.URL, user, "POST", "/api/cal/"+kal+"/import", body, "Content-Type", "text/calendar")
		return resp.StatusCode, b
	}
	if c, b := imp("bob", "kopie", ics); c != 200 || !strings.Contains(b, `"added":2`) || !strings.Contains(b, `"skipped":0`) {
		t.Fatal(c, b)
	}
	bob := func(m, p, b string) (int, string) { return req(t, srv, "bob", m, p, b) }
	l := listEvIn(t, bob, "kopie", "2027-03-01", "2027-04-30")
	if len(l) != 4 { // 3 Wochentermine + 1 ganztägig
		t.Fatalf("%+v", l)
	}
	// nochmal importieren: aktualisiert statt zu verdoppeln
	if c, b := imp("bob", "kopie", ics); c != 200 || !strings.Contains(b, `"updated":2`) || !strings.Contains(b, `"added":0`) {
		t.Fatal(c, b)
	}
	if l = listEvIn(t, bob, "kopie", "2027-03-01", "2027-04-30"); len(l) != 4 {
		t.Fatal("Dubletten", len(l))
	}
	// Termine der Quelle erneut in den eigenen Kalender: gleiche UID -> kein Duplikat
	if c, b := imp("anna", "default", ics); c != 200 || !strings.Contains(b, `"updated":2`) {
		t.Fatal(c, b)
	}
	if n := len(listEv(t, do, "2027-03-01", "2027-04-30")); n != 4 {
		t.Fatal("Dubletten im Original", n)
	}
	// Fehlerfälle: kein Kalender, leer, fremder/lesender Kalender
	if c, _ := imp("bob", "kopie", "kein kalender"); c != 400 {
		t.Fatal(c)
	}
	if c, _ := imp("bob", "kopie", "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nEND:VCALENDAR\r\n"); c != 400 {
		t.Fatal(c)
	}
	if c, _ := imp("bob", "default", ics); c != 200 {
		t.Fatal(c)
	}
	if c, _ := imp("bob", "anna~default", ics); c == 200 {
		t.Fatal("fremder Kalender")
	}
	// zu feine Wiederholung wird entschärft, Termin ohne UID bekommt eine
	bad := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\nDTSTAMP:20270101T000000Z\r\nDTSTART:20270320T100000Z\r\nRRULE:FREQ=SECONDLY\r\nSUMMARY:Flut\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if c, b := imp("bob", "kopie", bad); c != 200 || !strings.Contains(b, `"added":1`) {
		t.Fatal(c, b)
	}
	if c, _ := bob("GET", "/api/cal/kopie/events?from=2027-03-20&to=2027-03-21", ""); c != 200 {
		t.Fatal(c)
	}
}

func listEvIn(t *testing.T, do func(m, p, b string) (int, string), kal, from, to string) []calEv {
	t.Helper()
	c, b := do("GET", "/api/cal/"+kal+"/events?from="+from+"&to="+to, "")
	if c != 200 {
		t.Fatal(c, b)
	}
	var l []calEv
	json.Unmarshal([]byte(b), &l)
	return l
}
