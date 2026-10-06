package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// calOut: ein Kalender aus GET /api/cal (siehe cal.calRow).
type calOut struct {
	ID, Name, Scope, Group, Unit, Mode, URL string
	Write, Manage, Sub, Resource            bool
}

func findCal(l []calOut, pred func(calOut) bool) *calOut {
	for i := range l {
		if pred(l[i]) {
			return &l[i]
		}
	}
	return nil
}

// calDo: Anfrage als Benutzer (Basic Auth) an den Testserver.
func calDo(t *testing.T, srv *httptest.Server, user, pw, method, path, body string, hdr ...string) (int, string) {
	t.Helper()
	r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	r.SetBasicAuth(user, pw)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// calList: GET /api/cal eines Benutzers, ausgewertet als calRow-Liste.
func calList(t *testing.T, srv *httptest.Server, user, pw string) []calOut {
	t.Helper()
	c, b := calDo(t, srv, user, pw, "GET", "/api/cal", "")
	if c != 200 {
		t.Fatalf("GET /api/cal (%s): %d %s", user, c, b)
	}
	var out []calOut
	if err := json.Unmarshal([]byte(b), &out); err != nil {
		t.Fatalf("Liste (%s): %v %s", user, err, b)
	}
	return out
}

// TestCalendarScope: hierarchische Kalender - wer sieht, wer ändert, wer gibt frei.
//
//	globaler Kalender: globale Admins legen an, ändern und geben frei (alle lesen)
//	Gruppenkalender:   Gruppen-Admins (auch ohne Mitgliedschaft) ändern und geben frei (Mitglieder lesen)
//	Organisation:      globale Admins;  Abos: jeder für sich, für die Gruppe der Gruppen-Admin - immer nur lesbar
func TestCalendarScope(t *testing.T) {
	feed := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\nUID:g1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART;VALUE=DATE:20261224\r\nSUMMARY:Betriebsferien\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, feed) }))
	defer fs.Close()
	t.Setenv("CS_ICS_PRIVATE", "1") // Testfeed liegt auf localhost
	srv, _ := setup(t)
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string) (int, string) {
		return calDo(t, srv, user, pw, method, path, body)
	}
	cals := func(user, pw string) []calOut {
		return calList(t, srv, user, pw)
	}
	ev := `{"summary":"Probe","start":"2026-12-01T10:00:00Z"}`
	// Gruppe 8b (Vorlage team: Bereiche + Gruppenkalender "rw") mit bea als Mitglied; carl und eve gehören
	// in die Gruppe lehrer; eve wird zusätzlich Gruppen-Admin von 8b, ohne Mitglied zu sein.
	do("anna", pa, "POST", "/api/users/import?create=1&template=team", "bea;beageheim1;8b\ncarl;carlgeheim1;lehrer\neve;evegeheim1;lehrer\n")
	if c, b := do("anna", pa, "POST", "/api/groups/8b/admins", `{"admins":["eve"]}`); c != 200 {
		t.Fatalf("Gruppen-Admins setzen: %d %s", c, b)
	}

	// Gruppenkalender: der Gruppen-Admin ohne Mitgliedschaft sieht und ändert ihn, Außenstehende nicht.
	eveList := cals("eve", "evegeheim1")
	grp := findCal(eveList, func(c calOut) bool { return c.Group == "8b" })
	if grp == nil || !grp.Write || !grp.Manage {
		t.Fatalf("Gruppen-Admin ohne Mitgliedschaft: %+v", eveList)
	}
	if findCal(cals("carl", "carlgeheim1"), func(c calOut) bool { return c.Group == "8b" }) != nil {
		t.Fatal("Außenstehende sehen den Gruppenkalender")
	}
	beaList := cals("bea", "beageheim1")
	beaGrp := findCal(beaList, func(c calOut) bool { return c.Group == "8b" })
	if beaGrp == nil || !beaGrp.Write || beaGrp.Mode != "rw" || beaGrp.Manage {
		t.Fatalf("Mitglied im Gruppenkalender (Vorlage rw): %+v", beaList)
	}
	base := "/api/cal/" + url.PathEscape(beaGrp.ID)
	if c, b := do("bea", "beageheim1", "POST", base+"/events", ev); c != 200 {
		t.Fatalf("Mitglied schreibt im rw-Kalender: %d %s", c, b)
	}
	// Freigabe "ro": Mitglieder lesen nur noch, der Gruppen-Admin schreibt weiter.
	if c, b := do("eve", "evegeheim1", "PUT", base, `{"mode":"ro"}`); c != 200 {
		t.Fatalf("Freigabe ro: %d %s", c, b)
	}
	if c, _ := do("bea", "beageheim1", "POST", base+"/events", ev); c != 403 {
		t.Fatalf("Mitglied schreibt trotz ro: %d", c)
	}
	if c, b := do("eve", "evegeheim1", "POST", base+"/events", `{"summary":"Leitung","start":"2026-12-02T10:00:00Z"}`); c != 200 {
		t.Fatalf("Gruppen-Admin schreibt bei ro: %d %s", c, b)
	}
	// Freigabe "off": nur die Verantwortlichen sehen den Kalender (Entwurf).
	if c, b := do("eve", "evegeheim1", "PUT", base, `{"mode":"off"}`); c != 200 {
		t.Fatalf("Freigabe off: %d %s", c, b)
	}
	if findCal(cals("bea", "beageheim1"), func(c calOut) bool { return c.ID == beaGrp.ID }) != nil {
		t.Fatal("nicht freigegebener Gruppenkalender ist für Mitglieder sichtbar")
	}
	if findCal(cals("eve", "evegeheim1"), func(c calOut) bool { return c.ID == beaGrp.ID }) == nil {
		t.Fatal("Gruppen-Admin muss den nicht freigegebenen Kalender sehen")
	}
	// Wieder freigeben und umbenennen.
	if c, b := do("eve", "evegeheim1", "PUT", base, `{"mode":"rw","name":"Klasse 8b"}`); c != 200 {
		t.Fatalf("Freigabe rw: %d %s", c, b)
	}
	if g := findCal(cals("bea", "beageheim1"), func(c calOut) bool { return c.ID == beaGrp.ID }); g == nil || !g.Write || g.Name != "Klasse 8b" {
		t.Fatalf("nach dem Freigeben: %+v", g)
	}

	// Globale Kalender: nur globale Admins, Freigabe wirkt für alle Benutzer.
	if c, b := do("bob", "passwort-bob", "POST", "/api/cal", `{"name":"Firmenfeiertage","scope":"global"}`); c != 403 {
		t.Fatalf("globaler Kalender als Benutzer: %d %s", c, b)
	}
	if c, b := do("anna", pa, "POST", "/api/cal", `{"name":"Firmenfeiertage","scope":"global","mode":"off"}`); c != 200 {
		t.Fatalf("globaler Kalender: %d %s", c, b)
	}
	glob := findCal(cals("anna", pa), func(c calOut) bool { return c.Scope == "global" && c.Name == "Firmenfeiertage" })
	if glob == nil || !glob.Write || !glob.Manage || glob.Mode != "off" {
		t.Fatalf("globaler Kalender (Admin): %+v", glob)
	}
	if findCal(cals("bob", "passwort-bob"), func(c calOut) bool { return c.Name == "Firmenfeiertage" }) != nil {
		t.Fatal("nicht freigegebener globaler Kalender ist für Benutzer sichtbar")
	}
	if c, b := do("anna", pa, "PUT", "/api/cal/"+url.PathEscape(glob.ID), `{"mode":"ro"}`); c != 200 {
		t.Fatalf("globale Freigabe ro: %d %s", c, b)
	}
	gb := findCal(cals("bob", "passwort-bob"), func(c calOut) bool { return c.Name == "Firmenfeiertage" })
	if gb == nil || gb.Write || gb.Mode != "ro" || gb.Manage {
		t.Fatalf("globaler Kalender nach Freigabe (Benutzer): %+v", gb)
	}
	dav := "/api/cal/" + url.PathEscape(gb.ID)
	if c, _ := do("bob", "passwort-bob", "POST", dav+"/events", ev); c != 403 {
		t.Fatalf("Benutzer schreibt im globalen ro-Kalender: %d", c)
	}
	if c, _ := do("bob", "passwort-bob", "PUT", dav, `{"mode":"ro"}`); c != 403 {
		t.Fatalf("Benutzer ändert den globalen Kalender: %d", c)
	}
	if c, b := do("anna", pa, "PUT", "/api/cal/"+url.PathEscape(glob.ID), `{"mode":"rw"}`); c != 200 {
		t.Fatalf("globale Freigabe rw: %d %s", c, b)
	}
	if c, b := do("bob", "passwort-bob", "POST", dav+"/events", ev); c != 200 {
		t.Fatalf("Benutzer schreibt im globalen rw-Kalender: %d %s", c, b)
	}

	// Organisation "schule": globale Admins legen sie an und geben ihren Kalender frei; sichtbar wird er
	// für die Mitglieder der Gruppen dieser Organisation ("all" ist keine echte Zuordnung).
	if c, b := do("anna", pa, "POST", "/api/units", `{"name":"schule"}`); c != 200 {
		t.Fatalf("Organisation anlegen: %d %s", c, b)
	}
	if c, b := do("anna", pa, "POST", "/api/groups/8b", `{"units":["schule"]}`); c != 200 {
		t.Fatalf("Gruppe der Organisation zuordnen: %d %s", c, b)
	}
	if c, b := do("anna", pa, "POST", "/api/cal", `{"name":"Schulferien","scope":"unit","unit":"schule","mode":"off"}`); c != 200 {
		t.Fatalf("Organisationskalender: %d %s", c, b)
	}
	unit := findCal(cals("anna", pa), func(c calOut) bool { return c.Scope == "unit" && c.Name == "Schulferien" })
	if unit == nil || unit.Unit != "schule" || !unit.Manage {
		t.Fatalf("Organisationskalender: %+v", unit)
	}
	if findCal(cals("bea", "beageheim1"), func(c calOut) bool { return c.Name == "Schulferien" }) != nil {
		t.Fatal("nicht freigegebener Organisationskalender ist sichtbar")
	}
	if c, b := do("anna", pa, "PUT", "/api/cal/"+url.PathEscape(unit.ID), `{"mode":"ro"}`); c != 200 {
		t.Fatalf("Organisations-Freigabe: %d %s", c, b)
	}
	if u := findCal(cals("bea", "beageheim1"), func(c calOut) bool { return c.Name == "Schulferien" }); u == nil || u.Write || u.Manage || u.Unit != "schule" {
		t.Fatalf("Organisationskalender nach Freigabe: %+v", u)
	}
	if findCal(cals("carl", "carlgeheim1"), func(c calOut) bool { return c.Name == "Schulferien" }) != nil {
		t.Fatal("Organisationskalender ist für andere Organisationen sichtbar")
	}

	// Eigene Internet-Abos: jeder Benutzer darf für sich einen nur lesbaren Kalender abonnieren.
	if c, b := do("bob", "passwort-bob", "POST", "/api/cal", `{"name":"Feiertage","url":"`+fs.URL+`/f.ics"}`); c != 200 {
		t.Fatalf("persönliches Abo: %d %s", c, b)
	}
	sub := findCal(cals("bob", "passwort-bob"), func(c calOut) bool { return c.Name == "Feiertage" })
	if sub == nil || !sub.Sub || sub.Write || !sub.Manage || sub.URL == "" {
		t.Fatalf("persönliches Abo: %+v", sub)
	}
	if _, e := do("bob", "passwort-bob", "GET", "/api/cal/"+url.PathEscape(sub.ID)+"/events", ""); !strings.Contains(e, "Betriebsferien") {
		t.Fatalf("Abo-Termine: %s", e)
	}
	if findCal(cals("anna", pa), func(c calOut) bool { return c.Name == "Feiertage" }) != nil {
		t.Fatal("fremdes Abo ist sichtbar")
	}
	// Bereitstellen für die Gruppe: der Gruppen-Admin abonniert für die Gruppe, Mitglieder lesen nur.
	if c, b := do("eve", "evegeheim1", "POST", "/api/cal", `{"name":"Ferien 8b","scope":"group","group":"8b","url":"`+fs.URL+`/f.ics"}`); c != 200 {
		t.Fatalf("Gruppen-Abo: %d %s", c, b)
	}
	gs := findCal(cals("bea", "beageheim1"), func(c calOut) bool { return c.Name == "Ferien 8b" })
	if gs == nil || !gs.Sub || gs.Write || gs.Group != "8b" {
		t.Fatalf("Gruppen-Abo für Mitglieder: %+v", gs)
	}
	gsub := "/api/cal/" + url.PathEscape(gs.ID)
	if c, _ := do("bea", "beageheim1", "POST", gsub+"/events", ev); c != 403 {
		t.Fatalf("Mitglied schreibt im Abo: %d", c)
	}
	// Abo umstellen (neue Quelle) und beenden (URL leeren): Termine folgen, Kalender bleibt bestehen.
	if c, b := do("eve", "evegeheim1", "PUT", gsub, `{"url":"`+fs.URL+`/f.ics","description":"Ferien"}`); c != 200 {
		t.Fatalf("Abo ändern: %d %s", c, b)
	}
	if c, b := do("eve", "evegeheim1", "PUT", gsub, `{"url":""}`); c != 200 {
		t.Fatalf("Abo beenden: %d %s", c, b)
	}
	if _, e := do("bea", "beageheim1", "GET", gsub+"/events", ""); strings.Contains(e, "Betriebsferien") {
		t.Fatalf("Termine nach dem Abo-Ende: %s", e)
	}
	if g := findCal(cals("bea", "beageheim1"), func(c calOut) bool { return c.Name == "Ferien 8b" }); g == nil || g.Sub {
		t.Fatalf("Kalender nach dem Abo-Ende: %+v", g)
	}
}

// TestCalDAVScope: CalDAV-Clients (Thunderbird, iOS, DAVx5) sehen genau die Kalender, die der Benutzer
// sehen darf - eigene, Gruppe, Organisation, global, Abos - und schreiben nur dort, wo es die Freigabe erlaubt.
func TestCalDAVScope(t *testing.T) {
	feed := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\nUID:g1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART;VALUE=DATE:20261224\r\nSUMMARY:Betriebsferien\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, feed) }))
	defer fs.Close()
	t.Setenv("CS_ICS_PRIVATE", "1") // Testfeed liegt auf localhost
	srv, _ := setup(t)
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string, hdr ...string) (int, string) {
		return calDo(t, srv, user, pw, method, path, body, hdr...)
	}
	propfind := func(user, pw, path string) (int, string) {
		return do(user, pw, "PROPFIND", path, "", "Depth", "1")
	}
	const evICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\nUID:t1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20261202T100000Z\r\nSUMMARY:Probe\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	// Gruppe 8b (Vorlage team: Gruppenkalender "rw") mit bea als Mitglied; globaler Kalender zunaechst als Entwurf.
	if c, b := do("anna", pa, "POST", "/api/users/import?create=1&template=team", "bea;beageheim1;8b\n"); c != 200 {
		t.Fatalf("Benutzer anlegen: %d %s", c, b)
	}
	if c, b := do("anna", pa, "POST", "/api/cal", `{"name":"Schule","scope":"global","mode":"off"}`); c != 200 {
		t.Fatalf("globaler Kalender: %d %s", c, b)
	}
	glob := findCal(calList(t, srv, "anna", pa), func(c calOut) bool { return c.Scope == "global" && c.Name == "Schule" })
	grp := findCal(calList(t, srv, "bea", "beageheim1"), func(c calOut) bool { return c.Group == "8b" })
	if glob == nil || grp == nil {
		t.Fatalf("Kalender nicht gefunden: %+v %+v", glob, grp)
	}

	// Kalender-Home (Thunderbird fragt hier mit Depth 1 ab): Entwurf fehlt, Gruppenkalender ist dabei.
	if c, b := propfind("bea", "beageheim1", "/dav/bea/cal/"); c != 207 || !strings.Contains(b, grp.ID+"/") || strings.Contains(b, glob.ID+"/") {
		t.Fatalf("PROPFIND mit Entwurf: %d %s", c, b)
	}
	if c, _ := propfind("bea", "beageheim1", "/dav/bea/cal/"+glob.ID+"/"); c != 404 {
		t.Fatalf("Entwurf direkt per CalDAV: %d", c)
	}
	// Mitglied schreibt im Gruppenkalender (Vorlage rw) und liest das Objekt wieder.
	if c, b := do("bea", "beageheim1", "PUT", "/dav/bea/cal/"+grp.ID+"/t1.ics", evICS, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatalf("CalDAV-PUT im Gruppenkalender: %d %s", c, b)
	}
	if c, b := do("bea", "beageheim1", "GET", "/dav/bea/cal/"+grp.ID+"/t1.ics", ""); c != 200 || !strings.Contains(b, "SUMMARY:Probe") {
		t.Fatalf("CalDAV-GET im Gruppenkalender: %d %s", c, b)
	}

	// Freigabe ro: der globale Kalender erscheint, Schreiben lehnt der Server ab.
	if c, b := do("anna", pa, "PUT", "/api/cal/"+url.PathEscape(glob.ID), `{"mode":"ro"}`); c != 200 {
		t.Fatalf("Freigabe ro: %d %s", c, b)
	}
	if c, b := propfind("bea", "beageheim1", "/dav/bea/cal/"); c != 207 || !strings.Contains(b, glob.ID+"/") {
		t.Fatalf("PROPFIND nach Freigabe ro: %d %s", c, b)
	}
	if c, _ := do("bea", "beageheim1", "PUT", "/dav/bea/cal/"+glob.ID+"/x.ics", evICS, "Content-Type", "text/calendar"); c != 403 {
		t.Fatalf("CalDAV-PUT im ro-Kalender: %d", c)
	}
	// Freigabe rw: Schreiben erlaubt (Termin erscheint danach auch im Export).
	if c, b := do("anna", pa, "PUT", "/api/cal/"+url.PathEscape(glob.ID), `{"mode":"rw"}`); c != 200 {
		t.Fatalf("Freigabe rw: %d %s", c, b)
	}
	if c, b := do("bea", "beageheim1", "PUT", "/dav/bea/cal/"+glob.ID+"/x.ics", evICS, "Content-Type", "text/calendar"); c != 201 && c != 204 {
		t.Fatalf("CalDAV-PUT im rw-Kalender: %d %s", c, b)
	}
	if c, b := do("bea", "beageheim1", "GET", "/api/cal/"+url.PathEscape(glob.ID)+"/export.ics", ""); c != 200 || !strings.Contains(b, "SUMMARY:Probe") {
		t.Fatalf("Export nach CalDAV-PUT: %d %s", c, b)
	}

	// Abo (Internet-Kalender): erscheint als Kalender, ist per CalDAV aber nur lesbar.
	if c, b := do("bea", "beageheim1", "POST", "/api/cal", `{"name":"Feiertage","url":"`+fs.URL+`/f.ics"}`); c != 200 {
		t.Fatalf("Abo anlegen: %d %s", c, b)
	}
	sub := findCal(calList(t, srv, "bea", "beageheim1"), func(c calOut) bool { return c.Name == "Feiertage" })
	if sub == nil {
		t.Fatal("Abo fehlt in der Kalenderliste")
	}
	if c, b := propfind("bea", "beageheim1", "/dav/bea/cal/"); c != 207 || !strings.Contains(b, sub.ID+"/") {
		t.Fatalf("Abo im PROPFIND: %d %s", c, b)
	}
	if c, _ := do("bea", "beageheim1", "PUT", "/dav/bea/cal/"+sub.ID+"/y.ics", evICS, "Content-Type", "text/calendar"); c != 403 {
		t.Fatalf("CalDAV-PUT im Abo: %d", c)
	}
}

// TestCalDAVOwnCalendar: die eigenen Kalender gehören dem Benutzer - auch per CalDAV.
//
//	eigener Kalender: schreiben, lesen, löschen (unabhängig von einer Freigabe; die gilt nur für andere)
//	zweiter eigener Kalender: per MKCOL anlegen (MKCALENDAR kann der WebDAV-Server nicht: 405)
//	eigenes Abo (URL): nur lesbar, Termine kommen vom Feed
//	Bereich "Kalender" nur lesend (Vorlage klasse): auch die eigenen Kalender sind gesperrt
func TestCalDAVOwnCalendar(t *testing.T) {
	t.Setenv("CS_ICS_PRIVATE", "1") // Testfeed liegt auf localhost
	srv, _ := setup(t)
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string, hdr ...string) (int, string) {
		return calDo(t, srv, user, pw, method, path, body, hdr...)
	}
	const evICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\nUID:o1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART:20261202T100000Z\r\nSUMMARY:Probe\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	// Eigener Kalender "default" (wird beim ersten Zugriff automatisch angelegt).
	if c, b := do("anna", pa, "PUT", "/dav/anna/cal/default/a.ics", evICS, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatalf("CalDAV-PUT im eigenen Kalender: %d %s", c, b)
	}
	if c, b := do("anna", pa, "GET", "/dav/anna/cal/default/a.ics", ""); c != 200 || !strings.Contains(b, "SUMMARY:Probe") {
		t.Fatalf("CalDAV-GET im eigenen Kalender: %d %s", c, b)
	}
	if c, _ := do("anna", pa, "DELETE", "/dav/anna/cal/default/a.ics", ""); c != 204 {
		t.Fatalf("CalDAV-DELETE im eigenen Kalender: %d", c)
	}

	// Zweiter eigener Kalender: MKCOL mit resourcetype "calendar" legt ihn an, ein zweites Mal gibt es 409.
	mkcol := `<d:mkcol xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:set><d:prop><d:resourcetype>` +
		`<d:collection/><c:calendar/></d:resourcetype><d:displayname>Privat</d:displayname></d:prop></d:set></d:mkcol>`
	if c, b := do("anna", pa, "MKCOL", "/dav/anna/cal/privat/", mkcol, "Content-Type", "application/xml"); c != 201 {
		t.Fatalf("MKCOL eigener Kalender: %d %s", c, b)
	}
	if c, b := do("anna", pa, "MKCOL", "/dav/anna/cal/privat/", mkcol, "Content-Type", "application/xml"); c != 409 {
		t.Fatalf("MKCOL zweimal: %d %s", c, b)
	}
	if c, _ := do("anna", pa, "MKCALENDAR", "/dav/anna/cal/privat2/", mkcol, "Content-Type", "application/xml"); c != 405 {
		t.Fatalf("MKCALENDAR (kennt der WebDAV-Server nicht): %d", c)
	}
	if c, b := do("anna", pa, "PROPFIND", "/dav/anna/cal/", "", "Depth", "1"); c != 207 || !strings.Contains(b, "privat/") {
		t.Fatalf("PROPFIND Kalender-Home: %d %s", c, b)
	}
	if c, b := do("anna", pa, "PUT", "/dav/anna/cal/privat/b.ics", evICS, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatalf("CalDAV-PUT im zweiten eigenen Kalender: %d %s", c, b)
	}

	// Eigenes Abo: erscheint als Kalender, ist per CalDAV aber nur lesbar.
	feed := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\nUID:f1\r\nDTSTAMP:20260101T000000Z\r\n" +
		"DTSTART;VALUE=DATE:20261224\r\nSUMMARY:Betriebsferien\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, feed) }))
	defer fs.Close()
	if c, b := do("anna", pa, "POST", "/api/cal", `{"name":"Feiertage","url":"`+fs.URL+`/f.ics"}`); c != 200 {
		t.Fatalf("eigenes Abo anlegen: %d %s", c, b)
	}
	sub := findCal(calList(t, srv, "anna", pa), func(c calOut) bool { return c.Name == "Feiertage" })
	if sub == nil || !sub.Sub || sub.Write || !sub.Manage {
		t.Fatalf("eigenes Abo: %+v", sub)
	}
	if c, _ := do("anna", pa, "PUT", "/dav/anna/cal/"+sub.ID+"/c.ics", evICS, "Content-Type", "text/calendar"); c != 403 {
		t.Fatalf("CalDAV-PUT im eigenen Abo: %d", c)
	}

	// Bereich "Kalender" nur lesend (Vorlage klasse): der eigene Kalender ist lesbar, aber gesperrt.
	if c, b := do("anna", pa, "POST", "/api/users/import?create=1&template=klasse", "carl;carlgeheim1;klasse\n"); c != 200 {
		t.Fatalf("Benutzer anlegen: %d %s", c, b)
	}
	if c, b := do("carl", "carlgeheim1", "PROPFIND", "/dav/carl/cal/", "", "Depth", "1"); c != 207 {
		t.Fatalf("PROPFIND bei Bereich nur lesend: %d %s", c, b)
	}
	if c, _ := do("carl", "carlgeheim1", "PUT", "/dav/carl/cal/default/x.ics", evICS, "Content-Type", "text/calendar"); c != 403 {
		t.Fatalf("CalDAV-PUT bei Bereich nur lesend: %d", c)
	}
	if c, b := do("carl", "carlgeheim1", "POST", "/api/cal", `{"name":"Eigenes"}`); c != 403 {
		t.Fatalf("Kalender anlegen bei Bereich nur lesend: %d %s", c, b)
	}
}

// grpOut: die für die Gruppen-Einstellungen wichtigen Felder aus GET /api/groups.
type grpOut struct {
	Name, Cal string
	Areas     []string
}

// groupCal: Freigabe des Gruppenkalenders aus GET /api/groups ("" = kein Gruppenkalender).
func groupCal(t *testing.T, srv *httptest.Server, user, pw, name string) string {
	t.Helper()
	c, b := calDo(t, srv, user, pw, "GET", "/api/groups", "")
	if c != 200 {
		t.Fatalf("GET /api/groups (%s): %d %s", user, c, b)
	}
	var out []grpOut
	if err := json.Unmarshal([]byte(b), &out); err != nil {
		t.Fatalf("Gruppenliste (%s): %v %s", user, err, b)
	}
	for _, g := range out {
		if g.Name == name {
			return g.Cal
		}
	}
	t.Fatalf("Gruppe %s nicht in der Liste: %s", name, b)
	return ""
}

// TestGroupCalendarRelease: Gruppenkalender aus den Gruppen-Einstellungen freigeben, ändern und entfernen
// (POST /api/groups/{name} mit "cal"). Nur globale Admins; "off" ist der Entwurf der Verantwortlichen; entfernen
// geht nur, solange keine Termine darin liegen.
func TestGroupCalendarRelease(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string) (int, string) {
		return calDo(t, srv, user, pw, method, path, body)
	}
	grpCal := func(user, pw string) *calOut {
		return findCal(calList(t, srv, user, pw), func(c calOut) bool { return c.Group == "projekt" })
	}
	ev := `{"summary":"Probe","start":"2026-12-01T10:00:00Z"}`
	// Gruppe "projekt" (Bereich Kalender) mit bob als Gruppen-Admin und Mitglied; carl ist einfaches Mitglied.
	if c, b := do("anna", pa, "POST", "/api/groups", `{"name":"projekt","areas":["cal"],"admins":["bob"]}`); c != 200 {
		t.Fatalf("Gruppe anlegen: %d %s", c, b)
	}
	if c, b := do("anna", pa, "POST", "/api/users", `{"name":"carl","password":"passwort-carl","groups":["projekt"]}`); c != 200 {
		t.Fatalf("Mitglied anlegen: %d %s", c, b)
	}
	// Vorher gibt es keinen Gruppenkalender: die Einstellungen zeigen keinen, die Mitglieder sehen keinen.
	if m := groupCal(t, srv, "anna", pa, "projekt"); m != "" {
		t.Fatalf("Gruppenkalender ohne Freigabe: %q", m)
	}
	if grpCal("carl", "passwort-carl") != nil {
		t.Fatal("Gruppenkalender ohne Freigabe ist für Mitglieder sichtbar")
	}
	// Die Gruppe ändert nur ein globaler Admin (ein Gruppen-Admin nicht), und nur mit gültigem Wert.
	if c, _ := do("bob", "passwort-bob", "POST", "/api/groups/projekt", `{"cal":"rw"}`); c != 403 {
		t.Fatalf("Gruppen-Admin gibt den Gruppenkalender frei: %d", c)
	}
	if c, _ := do("anna", pa, "POST", "/api/groups/projekt", `{"cal":"xx"}`); c != 400 {
		t.Fatalf("ungültige Freigabe: %d", c)
	}
	// "rw": der Gruppenkalender entsteht, die Mitglieder dürfen eintragen.
	if c, b := do("anna", pa, "POST", "/api/groups/projekt", `{"cal":"rw"}`); c != 200 {
		t.Fatalf("Gruppenkalender freigeben: %d %s", c, b)
	}
	if m := groupCal(t, srv, "anna", pa, "projekt"); m != "rw" {
		t.Fatalf("Freigabe in den Einstellungen: %q", m)
	}
	g := grpCal("carl", "passwort-carl")
	if g == nil || !g.Write || g.Mode != "rw" || g.Manage {
		t.Fatalf("Gruppenkalender für Mitglieder: %+v", g)
	}
	base := "/api/cal/" + url.PathEscape(g.ID)
	if c, b := do("carl", "passwort-carl", "POST", base+"/events", ev); c != 200 {
		t.Fatalf("Mitglied trägt ein: %d %s", c, b)
	}
	// "ro": die Mitglieder lesen nur, der Gruppen-Admin trägt weiter ein.
	if c, b := do("anna", pa, "POST", "/api/groups/projekt", `{"cal":"ro"}`); c != 200 {
		t.Fatalf("Freigabe ro: %d %s", c, b)
	}
	if g := grpCal("carl", "passwort-carl"); g == nil || g.Write || g.Mode != "ro" {
		t.Fatalf("Mitglied nach ro: %+v", g)
	}
	if c, _ := do("carl", "passwort-carl", "POST", base+"/events", ev); c != 403 {
		t.Fatalf("Mitglied trägt bei ro ein: %d", c)
	}
	if g := grpCal("bob", "passwort-bob"); g == nil || !g.Write || !g.Manage {
		t.Fatalf("Gruppen-Admin nach ro: %+v", g)
	}
	// "off": Entwurf - nur die Verantwortlichen sehen den Kalender, Termine bleiben erhalten.
	if c, b := do("anna", pa, "POST", "/api/groups/projekt", `{"cal":"off"}`); c != 200 {
		t.Fatalf("Freigabe off: %d %s", c, b)
	}
	if m := groupCal(t, srv, "anna", pa, "projekt"); m != "off" {
		t.Fatalf("Entwurf in den Einstellungen: %q", m)
	}
	if grpCal("carl", "passwort-carl") != nil {
		t.Fatal("Entwurf ist für Mitglieder sichtbar")
	}
	if grpCal("bob", "passwort-bob") == nil {
		t.Fatal("Gruppen-Admin sieht den Entwurf nicht")
	}
	// Entfernen: mit Terminen abgelehnt (409), sonst verschwindet der Kalender.
	if c, b := do("anna", pa, "POST", "/api/groups/projekt", `{"cal":""}`); c != 409 || !strings.Contains(b, "not empty") {
		t.Fatalf("Entfernen mit Terminen: %d %s", c, b)
	}
	c, b := do("bob", "passwort-bob", "GET", base+"/events?from=2026-01-01&to=2027-01-01", "")
	if c != 200 {
		t.Fatalf("Termine lesen: %d %s", c, b)
	}
	var evs []struct{ File string }
	if err := json.Unmarshal([]byte(b), &evs); err != nil || len(evs) != 1 {
		t.Fatalf("Termine: %v %s", err, b)
	}
	if c, b := do("bob", "passwort-bob", "DELETE", base+"/events/"+url.PathEscape(evs[0].File), ""); c != 200 && c != 204 {
		t.Fatalf("Termin löschen: %d %s", c, b)
	}
	if c, b := do("anna", pa, "POST", "/api/groups/projekt", `{"cal":""}`); c != 200 {
		t.Fatalf("Gruppenkalender entfernen: %d %s", c, b)
	}
	if m := groupCal(t, srv, "anna", pa, "projekt"); m != "" {
		t.Fatalf("Gruppenkalender nach dem Entfernen: %q", m)
	}
	if grpCal("bob", "passwort-bob") != nil {
		t.Fatal("entfernter Gruppenkalender ist noch sichtbar")
	}
	// Wieder freigeben: der Kalender entsteht neu (leer) und ist für die Mitglieder wieder da.
	if c, b := do("anna", pa, "POST", "/api/groups/projekt", `{"cal":"rw"}`); c != 200 {
		t.Fatalf("neu freigeben: %d %s", c, b)
	}
	if g := grpCal("carl", "passwort-carl"); g == nil || !g.Write || g.Mode != "rw" {
		t.Fatalf("nach dem Neuanlegen: %+v", g)
	}
}
