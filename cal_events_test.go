package main

import (
	"encoding/json"
	"strings"
	"testing"
)

type calEv struct {
	UID, File, Summary, Location, Desc, Start, End, TZ, Rid string
	AllDay, Float, Rec, Ovr                                 bool
	Alarm                                                   *int
	Org                                                     string
	Att                                                     []struct{ Name, Mail, Status string }
	Rule                                                    *struct {
		Freq            string
		Interval, Count int
		Until           string
		Simple          bool
	}
}

func listEv(t *testing.T, srvReq func(method, path, body string) (int, string), from, to string) []calEv {
	t.Helper()
	c, b := srvReq("GET", "/api/cal/default/events?from="+from+"&to="+to, "")
	if c != 200 {
		t.Fatal("events:", c, b)
	}
	var l []calEv
	if err := json.Unmarshal([]byte(b), &l); err != nil {
		t.Fatal(err, b)
	}
	return l
}

const berlinSeries = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:ser1@t\r\nDTSTAMP:20270101T000000Z\r\n" +
	"DTSTART;TZID=Europe/Berlin:20270315T090000\r\nDTEND;TZID=Europe/Berlin:20270315T100000\r\nRRULE:FREQ=WEEKLY;COUNT=6\r\nEXDATE;TZID=Europe/Berlin:20270405T090000\r\nSUMMARY:Jour fixe\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:ser1@t\r\nDTSTAMP:20270101T000000Z\r\nRECURRENCE-ID;TZID=Europe/Berlin:20270329T090000\r\nDTSTART;TZID=Europe/Berlin:20270329T140000\r\nDTEND;TZID=Europe/Berlin:20270329T150000\r\nSUMMARY:Jour fixe (verschoben)\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestCalSeriesDisplay(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	if c, b := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/s1.ics", berlinSeries, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatal(c, b)
	}
	l := listEv(t, do, "2027-03-01", "2027-05-01")
	// 6 Vorkommen: 15.3., 22.3., 29.3. (verschoben), 5.4. (EXDATE), 12.4., 19.4. -> 5 Einträge
	if len(l) != 5 {
		t.Fatalf("Vorkommen: %d %+v", len(l), l)
	}
	want := []string{"2027-03-15T08:00:00Z", "2027-03-22T08:00:00Z", "2027-03-29T12:00:00Z", "2027-04-12T07:00:00Z", "2027-04-19T07:00:00Z"} // Sommerzeit ab 28.3.
	for i, w := range want {
		if l[i].Start != w {
			t.Fatalf("%d: %s, erwartet %s", i, l[i].Start, w)
		}
		if !l[i].Rec || l[i].Rid == "" || l[i].TZ != "Europe/Berlin" || l[i].Rule == nil || l[i].Rule.Freq != "WEEKLY" || !l[i].Rule.Simple || l[i].Rule.Count != 6 {
			t.Fatalf("%d: Serieninfo %+v", i, l[i])
		}
	}
	if !l[2].Ovr || l[2].Summary != "Jour fixe (verschoben)" || l[0].Ovr {
		t.Fatal("Einzeltermin:", l[2])
	}
	// Zeitraum begrenzt
	if l := listEv(t, do, "2027-03-20", "2027-03-30"); len(l) != 2 {
		t.Fatal("Zeitraum:", l)
	}
	if c, _ := do("GET", "/api/cal/default/events?from=2027-05-01&to=2027-04-01", ""); c != 400 {
		t.Fatal("falscher Zeitraum:", c)
	}
}

func TestCalZones(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	one := func(uid, dtstart string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20270101T000000Z\r\n" + dtstart + "\r\nSUMMARY:" + uid + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	}
	put := func(f, s string) {
		if c, b := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/"+f+".ics", s, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
			t.Fatal(c, b)
		}
	}
	put("win", one("win", "DTSTART;TZID=W. Europe Standard Time:20270720T090000\r\nDTEND;TZID=W. Europe Standard Time:20270720T100000"))
	put("flo", one("flo", "DTSTART:20270720T090000\r\nDTEND:20270720T100000"))
	put("unk", one("unk", "DTSTART;TZID=Mars/Olympus:20270720T090000\r\nDTEND;TZID=Mars/Olympus:20270720T100000"))
	put("utc", one("utc", "DTSTART:20270720T090000Z\r\nDTEND:20270720T100000Z"))
	put("day", one("day", "DTSTART;VALUE=DATE:20270720\r\nDTEND;VALUE=DATE:20270721"))
	m := map[string]calEv{}
	for _, e := range listEv(t, do, "2027-07-01", "2027-08-01") {
		m[e.UID] = e
	}
	if len(m) != 5 {
		t.Fatal("Anzahl:", m)
	}
	if e := m["win"]; e.Start != "2027-07-20T07:00:00Z" || e.TZ != "Europe/Berlin" || e.Float {
		t.Fatal("Windows-Zone:", e)
	}
	if e := m["flo"]; e.Start != "2027-07-20T09:00:00" || !e.Float || e.TZ != "" {
		t.Fatal("schwebend:", e)
	}
	if e := m["unk"]; e.Start != "2027-07-20T09:00:00" || !e.Float {
		t.Fatal("unbekannte Zone:", e)
	}
	if e := m["utc"]; e.Start != "2027-07-20T09:00:00Z" || e.Float {
		t.Fatal("UTC:", e)
	}
	if e := m["day"]; !e.AllDay || e.Start != "2027-07-20T00:00:00Z" {
		t.Fatal("ganztägig:", e)
	}
}

func TestCalEdit(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	// Anlegen mit Zeitzone und Wiederholung: Montag 15.3.2027 09:00 Berlin = 08:00Z, wöchentlich 4x
	body := `{"summary":"Probe","location":"Raum 1","description":"Notizen","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","tz":"Europe/Berlin","rule":{"freq":"WEEKLY","count":4}}`
	c, b := do("POST", "/api/cal/default/events", body)
	if c != 200 {
		t.Fatal(c, b)
	}
	var f struct{ File string }
	json.Unmarshal([]byte(b), &f)
	// gespeichert mit TZID und RRULE
	_, ics := do("GET", "/dav/anna/cal/default/"+f.File, "")
	if !strings.Contains(ics, "TZID=Europe/Berlin:20270315T090000") || !strings.Contains(ics, "RRULE:FREQ=WEEKLY;COUNT=4") {
		t.Fatal("ICS:", ics)
	}
	l := listEv(t, do, "2027-03-01", "2027-05-01")
	if len(l) != 4 || l[2].Start != "2027-03-29T07:00:00Z" || l[3].Desc != "Notizen" {
		t.Fatalf("Liste: %+v", l)
	}
	// ungültige / zu feine Regeln
	for _, r := range []string{`{"freq":"HOURLY"}`, `{"freq":"DAILY","count":5,"until":"2027-04-01"}`, `{"freq":"DAILY","interval":500}`, `{"freq":"DAILY","count":100000}`} {
		if c, _ := do("POST", "/api/cal/default/events", `{"summary":"x","start":"2027-03-15T08:00:00Z","tz":"Europe/Berlin","rule":`+r+`}`); c != 400 {
			t.Fatal("Regel", r, "->", c)
		}
	}
	if c, _ := do("POST", "/api/cal/default/events", `{"summary":"x","start":"2027-03-15T08:00:00Z","tz":"Mars/Olympus"}`); c != 400 {
		t.Fatal("Zeitzone:", c)
	}
	edit := func(extra string) (int, string) {
		return do("PUT", "/api/cal/default/events/"+f.File, `{"summary":"Probe neu","location":"Raum 2","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","tz":"Europe/Berlin"`+extra+`}`)
	}
	// nur dieses Vorkommen (22.3.) auf 14:00 Berlin verlegen
	rid := l[1].Rid
	if c, b := do("PUT", "/api/cal/default/events/"+f.File, `{"summary":"Probe (spaeter)","start":"2027-03-22T13:00:00Z","end":"2027-03-22T14:00:00Z","tz":"Europe/Berlin","scope":"one","rid":"`+rid+`"}`); c != 200 {
		t.Fatal(c, b)
	}
	l = listEv(t, do, "2027-03-01", "2027-05-01")
	if len(l) != 4 || l[1].Summary != "Probe (spaeter)" || l[1].Start != "2027-03-22T13:00:00Z" || !l[1].Ovr || l[0].Summary != "Probe" {
		t.Fatalf("nach Einzeländerung: %+v", l)
	}
	// nochmals dasselbe Vorkommen ändern ersetzt den Einzeltermin (kein Duplikat)
	if c, b := do("PUT", "/api/cal/default/events/"+f.File, `{"summary":"Probe (nochmal)","start":"2027-03-22T13:00:00Z","end":"2027-03-22T14:00:00Z","tz":"Europe/Berlin","scope":"one","rid":"`+rid+`"}`); c != 200 {
		t.Fatal(c, b)
	}
	if l = listEv(t, do, "2027-03-01", "2027-05-01"); len(l) != 4 || l[1].Summary != "Probe (nochmal)" {
		t.Fatalf("Duplikat? %+v", l)
	}
	// ein Vorkommen, das es nicht gibt
	if c, _ := do("PUT", "/api/cal/default/events/"+f.File, `{"summary":"x","start":"2027-03-23T08:00:00Z","tz":"Europe/Berlin","scope":"one","rid":"2027-03-23T08:00:00Z"}`); c != 404 {
		t.Fatal("falsches Vorkommen:", c)
	}
	// ganze Serie: nur Text ändern - Einzeltermin bleibt erhalten
	if c, b := edit(""); c != 200 {
		t.Fatal(c, b)
	}
	l = listEv(t, do, "2027-03-01", "2027-05-01")
	if len(l) != 4 || l[0].Summary != "Probe neu" || l[0].Location != "Raum 2" || l[1].Summary != "Probe (nochmal)" {
		t.Fatalf("Text für alle: %+v", l)
	}
	// ein Vorkommen löschen (5.4. gibt es nicht mehr, 29.3. ja)
	if c, b := do("DELETE", "/api/cal/default/events/"+f.File+"?scope=one&rid="+l[2].Rid, ""); c != 200 {
		t.Fatal(c, b)
	}
	if l = listEv(t, do, "2027-03-01", "2027-05-01"); len(l) != 3 {
		t.Fatalf("nach Löschen eines Vorkommens: %+v", l)
	}
	// Beginn der Serie ändern: Einzeltermine und Ausnahmen entfallen
	if c, b := do("PUT", "/api/cal/default/events/"+f.File, `{"summary":"Probe neu","start":"2027-03-15T09:00:00Z","end":"2027-03-15T10:00:00Z","tz":"Europe/Berlin","rule":{"freq":"WEEKLY","count":4}}`); c != 200 {
		t.Fatal(c, b)
	}
	l = listEv(t, do, "2027-03-01", "2027-05-01")
	if len(l) != 4 || l[0].Start != "2027-03-15T09:00:00Z" || l[1].Ovr {
		t.Fatalf("Beginn geändert: %+v", l)
	}
	// Wiederholung entfernen -> Einzeltermin
	if c, b := do("PUT", "/api/cal/default/events/"+f.File, `{"summary":"Probe neu","start":"2027-03-15T09:00:00Z","end":"2027-03-15T10:00:00Z","tz":"Europe/Berlin","rule":{"freq":""}}`); c != 200 {
		t.Fatal(c, b)
	}
	if l = listEv(t, do, "2027-03-01", "2027-05-01"); len(l) != 1 || l[0].Rec {
		t.Fatalf("ohne Wiederholung: %+v", l)
	}
	// Einzeltermin löschen mit scope=one löscht das Objekt
	if c, b := do("DELETE", "/api/cal/default/events/"+f.File+"?scope=one&rid=x", ""); c != 200 {
		t.Fatal(c, b)
	}
	if l = listEv(t, do, "2027-03-01", "2027-05-01"); len(l) != 0 {
		t.Fatalf("nach Löschen: %+v", l)
	}
}

func TestCalEditKeepsForeign(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	// Serie aus Outlook (Windows-Zone, komplexe Regel): Text ändern lässt Zone und Regel unverändert
	s := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:o1@t\r\nDTSTAMP:20270101T000000Z\r\nDTSTART;TZID=W. Europe Standard Time:20270316T100000\r\nDTEND;TZID=W. Europe Standard Time:20270316T110000\r\nRRULE:FREQ=WEEKLY;BYDAY=TU,TH;COUNT=6\r\nSUMMARY:Outlook\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if c, b := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/o1.ics", s, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatal(c, b)
	}
	l := listEv(t, do, "2027-03-01", "2027-05-01")
	if len(l) != 6 || l[0].Rule == nil || l[0].Rule.Simple {
		t.Fatalf("komplexe Regel: %+v", l)
	}
	if c, b := do("PUT", "/api/cal/default/events/o1.ics", `{"summary":"Outlook neu","start":"2027-03-16T09:00:00Z","end":"2027-03-16T10:00:00Z","tz":"Europe/Berlin"}`); c != 200 {
		t.Fatal(c, b)
	}
	_, ics := do("GET", "/dav/anna/cal/default/o1.ics", "")
	if !strings.Contains(ics, "W. Europe Standard Time") || !strings.Contains(ics, "BYDAY=TU,TH") || !strings.Contains(ics, "Outlook neu") {
		t.Fatal("Zone/Regel verändert:", ics)
	}
	if l = listEv(t, do, "2027-03-01", "2027-05-01"); len(l) != 6 || l[0].Start != "2027-03-16T09:00:00Z" {
		t.Fatalf("danach: %+v", l)
	}
	// schwebender Termin: Text ändern behält die schwebende Zeit
	s2 := strings.NewReplacer("o1@t", "f1@t", "DTSTART;TZID=W. Europe Standard Time:", "DTSTART:", "DTEND;TZID=W. Europe Standard Time:", "DTEND:", "RRULE:FREQ=WEEKLY;BYDAY=TU,TH;COUNT=6\r\n", "").Replace(s)
	if c, b := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/f1.ics", s2, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatal(c, b)
	}
	if c, b := do("PUT", "/api/cal/default/events/f1.ics", `{"summary":"schwebend neu","start":"2027-03-16T09:00:00Z","end":"2027-03-16T10:00:00Z","tz":"Europe/Berlin"}`); c != 200 {
		t.Fatal(c, b)
	}
	_, ics = do("GET", "/dav/anna/cal/default/f1.ics", "")
	if !strings.Contains(ics, "DTSTART:20270316T100000\r\n") || strings.Contains(ics, "TZID") {
		t.Fatal("schwebend verändert:", ics)
	}
}

func TestCalEditRights(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	// globaler Kalender: Admin darf schreiben, bob nur lesen
	c, b := req(t, srv, "anna", "POST", "/api/cal", `{"name":"Team","scope":"global","mode":"ro"}`)
	if c != 200 {
		t.Fatal(c, b)
	}
	var cr struct{ ID string }
	json.Unmarshal([]byte(b), &cr)
	if c, b := req(t, srv, "anna", "POST", "/api/cal/"+cr.ID+"/events", `{"summary":"Fest","start":"2027-03-15T08:00:00Z","tz":"Europe/Berlin"}`); c != 200 {
		t.Fatal(c, b)
	}
	_, b = req(t, srv, "bob", "GET", "/api/cal/"+cr.ID+"/events?from=2027-03-01&to=2027-04-01", "")
	var l []calEv
	json.Unmarshal([]byte(b), &l)
	if len(l) != 1 {
		t.Fatal("bob liest:", b)
	}
	body := `{"summary":"gehackt","start":"2027-03-15T08:00:00Z","tz":"Europe/Berlin"}`
	if c, _ := req(t, srv, "bob", "PUT", "/api/cal/"+cr.ID+"/events/"+l[0].File, body); c != 403 {
		t.Fatal("bob PUT:", c)
	}
	if c, _ := req(t, srv, "bob", "DELETE", "/api/cal/"+cr.ID+"/events/"+l[0].File+"?scope=one&rid=x", ""); c != 403 {
		t.Fatal("bob DELETE one:", c)
	}
	if c, _ := req(t, srv, "bob", "DELETE", "/api/cal/"+cr.ID+"/events/"+l[0].File, ""); c != 403 {
		t.Fatal("bob DELETE:", c)
	}
	// fremder persönlicher Kalender bleibt unsichtbar
	if c, _ := req(t, srv, "bob", "PUT", "/api/cal/anna~default/events/x.ics", body); c != 404 {
		t.Fatal("fremder Kalender:", c)
	}
	// unbekannte Datei
	if c, _ := req(t, srv, "anna", "PUT", "/api/cal/"+cr.ID+"/events/nix.ics", body); c != 404 {
		t.Fatal("unbekannte Datei:", c)
	}
	if c, _ := req(t, srv, "anna", "PUT", "/api/cal/"+cr.ID+"/events/..%2Fx.ics", body); c != 400 && c != 404 {
		t.Fatal("Pfad:", c)
	}
}

func TestCalEditResource(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	c, b := req(t, srv, "anna", "POST", "/api/cal", `{"name":"Raum","scope":"global","mode":"rw","resource":true}`)
	if c != 200 {
		t.Fatal(c, b)
	}
	var cr struct{ ID string }
	json.Unmarshal([]byte(b), &cr)
	add := func(start, end string) (int, string) {
		return req(t, srv, "anna", "POST", "/api/cal/"+cr.ID+"/events", `{"summary":"B","start":"`+start+`","end":"`+end+`","tz":"Europe/Berlin"}`)
	}
	c, b = add("2027-03-15T08:00:00Z", "2027-03-15T09:00:00Z")
	if c != 200 {
		t.Fatal(c, b)
	}
	var a struct{ File string }
	json.Unmarshal([]byte(b), &a)
	if c, b = add("2027-03-15T09:00:00Z", "2027-03-15T10:00:00Z"); c != 200 {
		t.Fatal(c, b)
	}
	var b2 struct{ File string }
	json.Unmarshal([]byte(b), &b2)
	put := func(file, start, end string) int {
		c, _ := req(t, srv, "anna", "PUT", "/api/cal/"+cr.ID+"/events/"+file, `{"summary":"B","start":"`+start+`","end":"`+end+`","tz":"Europe/Berlin"}`)
		return c
	}
	if c := put(b2.File, "2027-03-15T08:30:00Z", "2027-03-15T09:30:00Z"); c != 409 {
		t.Fatal("Verschieben auf belegt:", c)
	}
	if c := put(b2.File, "2027-03-15T09:00:00Z", "2027-03-15T11:00:00Z"); c != 200 {
		t.Fatal("verlängern ohne Überschneidung:", c)
	}
	if c := put(a.File, "2027-03-15T08:00:00Z", "2027-03-15T09:30:00Z"); c != 409 {
		t.Fatal("verlängern in den Nachbarn:", c)
	}
}

// Ganze Serie an einem späteren Vorkommen bearbeiten oder verschieben (Drag & Drop): der Serienbeginn
// wird um den Abstand verschoben, nicht auf das Vorkommen gesetzt.
func TestCalSeriesShift(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	do := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	mk := func(body string) string {
		c, b := do("POST", "/api/cal/default/events", body)
		if c != 200 {
			t.Fatal(c, b)
		}
		var f struct{ File string }
		json.Unmarshal([]byte(b), &f)
		return f.File
	}
	starts := func() string {
		var out []string
		for _, e := range listEv(t, do, "2027-03-01", "2027-06-01") {
			out = append(out, e.Start)
		}
		return strings.Join(out, " ")
	}
	// Montag 15.3.2027 09:00 Berlin, wöchentlich 4x (28.3. Sommerzeit)
	f := mk(`{"summary":"Probe","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","tz":"Europe/Berlin","rule":{"freq":"WEEKLY","count":4}}`)
	l := listEv(t, do, "2027-03-01", "2027-06-01")
	// nur Titel ändern, am dritten Vorkommen (29.3.): Serienbeginn bleibt
	if c, b := do("PUT", "/api/cal/default/events/"+f, `{"summary":"Neu","start":"`+l[2].Start+`","end":"2027-03-29T08:00:00Z","tz":"Europe/Berlin","scope":"all","rid":"`+l[2].Rid+`"}`); c != 200 {
		t.Fatal(c, b)
	}
	if got := starts(); got != "2027-03-15T08:00:00Z 2027-03-22T08:00:00Z 2027-03-29T07:00:00Z 2027-04-05T07:00:00Z" {
		t.Fatal("Titel:", got)
	}
	// 2. Vorkommen (22.3.) um einen Tag verschieben: ganze Serie auf Dienstag
	if c, b := do("PUT", "/api/cal/default/events/"+f, `{"summary":"Neu","start":"2027-03-23T08:00:00Z","end":"2027-03-23T09:00:00Z","tz":"Europe/Berlin","scope":"all","rid":"`+l[1].Rid+`"}`); c != 200 {
		t.Fatal(c, b)
	}
	if got := starts(); got != "2027-03-16T08:00:00Z 2027-03-23T08:00:00Z 2027-03-30T07:00:00Z 2027-04-06T07:00:00Z" {
		t.Fatal("Tag:", got)
	}
	// 3. Vorkommen (30.3. 09:00 Berlin) auf 11:00 Berlin: alle Vorkommen 11:00 Ortszeit
	l = listEv(t, do, "2027-03-01", "2027-06-01")
	if c, b := do("PUT", "/api/cal/default/events/"+f, `{"summary":"Neu","start":"2027-03-30T09:00:00Z","end":"2027-03-30T10:00:00Z","tz":"Europe/Berlin","scope":"all","rid":"`+l[2].Rid+`"}`); c != 200 {
		t.Fatal(c, b)
	}
	if got := starts(); got != "2027-03-16T10:00:00Z 2027-03-23T10:00:00Z 2027-03-30T09:00:00Z 2027-04-06T09:00:00Z" {
		t.Fatal("Uhrzeit:", got)
	}
	// ganztägige Serie: 2. Tag auf den 4. verschieben -> Serie beginnt am 17.
	g := mk(`{"summary":"Tage","start":"2027-04-12T00:00:00Z","allDay":true,"rule":{"freq":"DAILY","count":3}}`)
	var days []calEv
	for _, e := range listEv(t, do, "2027-04-01", "2027-05-01") {
		if e.Summary == "Tage" {
			days = append(days, e)
		}
	}
	if len(days) != 3 {
		t.Fatalf("ganztägig: %+v", days)
	}
	if c, b := do("PUT", "/api/cal/default/events/"+g, `{"summary":"Tage","start":"2027-04-15T00:00:00Z","allDay":true,"scope":"all","rid":"`+days[1].Rid+`"}`); c != 200 {
		t.Fatal(c, b)
	}
	var got []string
	for _, e := range listEv(t, do, "2027-04-01", "2027-05-01") {
		if e.Summary == "Tage" {
			got = append(got, e.Start[:10])
		}
	}
	if strings.Join(got, " ") != "2027-04-14 2027-04-15 2027-04-16" {
		t.Fatal("ganztägig:", got)
	}
	// unbekanntes Vorkommen
	if c, _ := do("PUT", "/api/cal/default/events/"+f, `{"summary":"x","start":"2027-03-17T08:00:00Z","tz":"Europe/Berlin","scope":"all","rid":"2027-03-17T08:00:00Z"}`); c != 404 {
		t.Fatal("rid:", c)
	}
}
