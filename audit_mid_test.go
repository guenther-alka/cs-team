package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"cs-team/cal"
	"cs-team/doc"
)

// S-09: Webhook-Adressen (enthalten Tokens) sehen und ändern nur der Besitzer und globale Admins.
func TestChatURLPrivacy(t *testing.T) {
	srv, _ := setup(t) // anna = globaler Admin, bob = normaler Benutzer
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"k6","areas":["files"],"admins":["bob"]}`)
	if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"eva","password":"passwort-eva","groups":["k6"],"mail":"eva@example.com","chat":"https://ntfy.sh/eva-geheim"}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "bob", "POST", "/api/me/contact", `{"mail":"","chat":"https://ntfy.sh/bob-eigen"}`)

	// Liste: Gruppen-Admin sieht bei anderen nur "gesetzt", bei sich selbst die Adresse
	_, b := req(t, srv, "bob", "GET", "/api/users", "")
	if strings.Contains(b, "eva-geheim") || !strings.Contains(b, `"chatSet":true`) || !strings.Contains(b, "bob-eigen") {
		t.Fatal("Gruppen-Admin Liste:", b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/users", ""); !strings.Contains(b, "eva-geheim") {
		t.Fatal("globaler Admin muss die Adresse sehen:", b)
	}
	// Export: Gruppen-Admin ohne fremde Adressen, mit eigener
	_, b = req(t, srv, "bob", "GET", "/api/users/export", "")
	if strings.Contains(b, "eva-geheim") || !strings.Contains(b, "eva@example.com") || !strings.Contains(b, "bob-eigen") {
		t.Fatal("Export Gruppen-Admin:", b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/users/export", ""); !strings.Contains(b, "eva-geheim") {
		t.Fatal("Export globaler Admin:", b)
	}
	// Kontakt ändern: Gruppen-Admin nur E-Mail, Adresse bleibt; mit neuer Adresse 403
	if c, b := req(t, srv, "bob", "POST", "/api/users/eva/contact", `{"mail":"neu@example.com"}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/users/eva/contact", `{"mail":"neu@example.com","chat":"https://evil.example/x"}`); c != 403 {
		t.Fatal("fremde Adresse setzen:", c)
	}
	_, b = req(t, srv, "anna", "GET", "/api/users", "")
	if !strings.Contains(b, "eva-geheim") || !strings.Contains(b, "neu@example.com") {
		t.Fatal("E-Mail neu, Adresse unverändert erwartet:", b)
	}
	// Neuer Benutzer durch Gruppen-Admin: ohne Adresse ok, mit Adresse 403
	if c, _ := req(t, srv, "bob", "POST", "/api/users", `{"name":"fred","password":"passwort-fred","groups":["k6"],"chat":"https://ntfy.sh/fred"}`); c != 403 {
		t.Fatal("neu mit Adresse:", c)
	}
	if c, b := req(t, srv, "bob", "POST", "/api/users", `{"name":"fred","password":"passwort-fred","groups":["k6"]}`); c != 200 {
		t.Fatal(c, b)
	}
	// Import: Zeile mit fremder Adresse wird abgelehnt, ohne Adresse (und eigene) geht
	_, b = req(t, srv, "bob", "POST", "/api/users/import?update=1", "eva;;k6;;https://evil.example/x\nfred;;k6;fred@example.com;\nbob;;k6;;https://ntfy.sh/bob-neu\n")
	if !strings.Contains(b, "chat address only") || !strings.Contains(b, `"updated":2`) {
		t.Fatal("Import:", b)
	}
	_, b = req(t, srv, "anna", "GET", "/api/users/export", "")
	if strings.Contains(b, "evil.example") || !strings.Contains(b, "bob-neu") || !strings.Contains(b, "eva-geheim") {
		t.Fatal("Stand nach Import:", b)
	}
}

// K-05: Ressourcen-Kalender: gleichzeitige Buchungen, Serientermine, Meldung ohne Serverzeitzone.
func TestResourceRace(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, _ := req(t, srv, "anna", "POST", "/api/cal", `{"name":"Raum 102","resource":true,"scope":"global","mode":"rw"}`); c != 200 {
		t.Fatal("anlegen:", c)
	}
	const base = "/api/cal/_global~raum-102/events"
	add := func(user, s, e string) (int, string) {
		return req(t, srv, user, "POST", base, `{"summary":"Buchung","start":"`+s+`","end":"`+e+`"}`)
	}
	put := func(user, name, body string) (int, string) {
		r, _ := http.NewRequest("PUT", srv.URL+"/dav/"+user+"/cal/_global~raum-102/"+name+".ics", strings.NewReader(body))
		r.SetBasicAuth(user, "passwort-"+user)
		r.Header.Set("Content-Type", "text/calendar")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	ev := func(uid, extra string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\nBEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20260930T000000Z\r\nSUMMARY:Serie " + uid + "\r\n" + extra + "END:VEVENT\r\nEND:VCALENDAR\r\n"
	}

	cal.CheckDelay = 20 * time.Millisecond // Zeitfenster zwischen Prüfen und Speichern aufweiten
	defer func() { cal.CheckDelay = 0 }()
	// gleichzeitig dieselbe Zeit buchen: genau eine Buchung gewinnt
	var ok, conflict atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := "anna"
			if i%2 == 1 {
				u = "bob"
			}
			switch c, _ := add(u, "2026-10-05T08:00:00Z", "2026-10-05T09:00:00Z"); c {
			case 200:
				ok.Add(1)
			case 409:
				conflict.Add(1)
			default:
				t.Error("unerwartet:", c)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 1 || conflict.Load() != 11 {
		t.Fatal("Doppelbuchung: ok =", ok.Load(), "Konflikt =", conflict.Load())
	}

	cal.CheckDelay = 0
	// Serie wöchentlich dienstags 08:00-09:00 UTC (4x ab 06.10.), mit Ausnahme (20.10.) und verschobenem Vorkommen (27.10. -> 15:00)
	master := ev("serie1", "DTSTART:20261006T080000Z\r\nDTEND:20261006T090000Z\r\nRRULE:FREQ=WEEKLY;COUNT=4\r\nEXDATE:20261020T080000Z\r\n")
	if c, b := put("anna", "serie1", master); c != 201 && c != 204 && c != 200 {
		t.Fatal("Serie:", c, b)
	}
	moved := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//t//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:serie1\r\nDTSTAMP:20260930T000000Z\r\nSUMMARY:Serie serie1\r\nDTSTART:20261006T080000Z\r\nDTEND:20261006T090000Z\r\nRRULE:FREQ=WEEKLY;COUNT=4\r\nEXDATE:20261020T080000Z\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:serie1\r\nDTSTAMP:20260930T000000Z\r\nSUMMARY:Serie serie1 verschoben\r\nRECURRENCE-ID:20261027T080000Z\r\nDTSTART:20261027T150000Z\r\nDTEND:20261027T160000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if c, b := put("anna", "serie1", moved); c != 201 && c != 204 && c != 200 {
		t.Fatal("Serie mit Ausnahme:", c, b)
	}
	for _, x := range []struct {
		s, e string
		want int
	}{
		{"2026-10-13T08:30:00Z", "2026-10-13T09:30:00Z", 409}, // 2. Vorkommen
		{"2026-10-13T10:00:00Z", "2026-10-13T11:00:00Z", 200}, // dazwischen frei
		{"2026-10-20T08:30:00Z", "2026-10-20T09:30:00Z", 200}, // EXDATE: frei
		{"2026-10-27T08:30:00Z", "2026-10-27T09:30:00Z", 200}, // ursprüngliches Vorkommen verschoben: frei
		{"2026-10-27T15:30:00Z", "2026-10-27T16:30:00Z", 409}, // verschobenes Vorkommen belegt
		{"2026-11-03T08:00:00Z", "2026-11-03T09:00:00Z", 200}, // nach COUNT=4: frei
	} {
		if c, b := add("bob", x.s, x.e); c != x.want {
			t.Fatalf("%s: erwartet %d, bekommen %d (%s)", x.s, x.want, c, b)
		}
	}
	// Meldung: Zeit mit Zonenangabe (UTC), nicht in der Zeitzone des Servers
	if c, b := add("bob", "2026-10-13T08:15:00Z", "2026-10-13T08:45:00Z"); c != 409 || !strings.Contains(b, "08:00 - 09:00 UTC") || !strings.Contains(b, "13.10.") {
		t.Fatal("Meldung:", c, b)
	}
	// neue Serie gegen vorhandenen Einzeltermin: täglich 5x ab 02.11. 09:00; vorhanden: 04.11. 09:30
	if c, _ := add("bob", "2026-11-04T09:30:00Z", "2026-11-04T10:00:00Z"); c != 200 {
		t.Fatal("Einzeltermin")
	}
	daily := ev("serie2", "DTSTART:20261102T090000Z\r\nDTEND:20261102T100000Z\r\nRRULE:FREQ=DAILY;COUNT=5\r\n")
	if c, b := put("anna", "serie2", daily); c != 409 || !strings.Contains(b, "double booked") {
		t.Fatal("Serie gegen Einzeltermin:", c, b)
	}
	daily = ev("serie2", "DTSTART:20261102T110000Z\r\nDTEND:20261102T120000Z\r\nRRULE:FREQ=DAILY;COUNT=5\r\n")
	if c, b := put("anna", "serie2", daily); c != 201 && c != 204 && c != 200 {
		t.Fatal("freie Serie:", c, b)
	}
	// abgesagter Termin blockiert nicht
	canc := ev("abg", "DTSTART:20261110T080000Z\r\nDTEND:20261110T090000Z\r\nSTATUS:CANCELLED\r\n")
	if c, b := put("anna", "abg", canc); c != 201 && c != 204 && c != 200 {
		t.Fatal("abgesagt:", c, b)
	}
	if c, b := add("bob", "2026-11-10T08:00:00Z", "2026-11-10T09:00:00Z"); c != 200 {
		t.Fatal("abgesagter Termin blockiert:", c, b)
	}
}

// F4: Rechteentzug wirkt auf offene WebSockets (Freigabe geändert, Dokument gelöscht, Konto gesperrt).
func TestWSRevoke(t *testing.T) {
	srv, st := setup(t)
	defer srv.Close()
	doc.SetRecheck(100 * time.Millisecond)
	defer doc.SetRecheck(15 * time.Second)
	mk := func() string {
		_, b := req(t, srv, "anna", "POST", "/api/docs", `{"name":"t","type":"sheet"}`)
		var r struct{ ID string }
		json.Unmarshal([]byte(b), &r)
		return r.ID
	}
	closed := func(c *websocket.Conn) bool { // wird die Verbindung getrennt (statt weiterer Nachrichten)?
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return ctx.Err() == nil // Trennung, nicht Zeitablauf
			}
		}
	}
	id := mk()
	req(t, srv, "anna", "POST", "/api/docs/"+id+"/share", `{"read":[],"write":["bob"]}`)
	c := dial(t, srv, "bob", id)
	if m := read(t, c); m["t"] != "init" || m["rw"] != true {
		t.Fatal(m)
	}
	// Schreibrecht entzogen (nur noch lesen): Verbindung wird getrennt, die neue ist schreibgeschützt
	req(t, srv, "anna", "POST", "/api/docs/"+id+"/share", `{"read":["bob"],"write":[]}`)
	if !closed(c) {
		t.Fatal("Verbindung nach Rechteänderung nicht getrennt")
	}
	c = dial(t, srv, "bob", id)
	if m := read(t, c); m["rw"] == true {
		t.Fatal("bob darf nur lesen:", m)
	}
	// Lesen entzogen
	req(t, srv, "anna", "POST", "/api/docs/"+id+"/share", `{"read":[],"write":[]}`)
	if !closed(c) {
		t.Fatal("Verbindung nach Entzug nicht getrennt")
	}

	// Konto gesperrt (ohne share): die periodische Prüfung trennt
	req(t, srv, "anna", "POST", "/api/docs/"+id+"/share", `{"read":[],"write":["bob"]}`)
	c = dial(t, srv, "bob", id)
	read(t, c)
	if code, b := req(t, srv, "anna", "POST", "/api/users/bob/flags", `{"disabled":true}`); code != 200 {
		t.Fatal(code, b)
	}
	if !closed(c) {
		t.Fatal("gesperrtes Konto bleibt verbunden")
	}
	req(t, srv, "anna", "POST", "/api/users/bob/flags", `{"disabled":false}`)

	// Dokument gelöscht: Verbindung getrennt, der Snapshot wird nicht neu angelegt
	c = dial(t, srv, "bob", id)
	read(t, c)
	ctx := context.Background()
	c.Write(ctx, websocket.MessageText, []byte(`{"t":"set","k":"A1","v":"x"}`))
	time.Sleep(100 * time.Millisecond)
	if code, _ := req(t, srv, "anna", "DELETE", "/api/docs/"+id, ""); code != 200 {
		t.Fatal("löschen:", code)
	}
	if !closed(c) {
		t.Fatal("Verbindung nach Löschen nicht getrennt")
	}
	time.Sleep(2500 * time.Millisecond) // länger als der Speicher-Timer
	if infos, _ := st.List(ctx, "doc/"+id+"/"); len(infos) != 0 {
		t.Fatal("gelöschtes Dokument wurde neu geschrieben:", infos)
	}
}

// F8: Dateikontingent je Benutzer und Gruppenordner (Einstellungen, MB), gemeinsam für Web und WebDAV.
func TestQuota(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	blob := strings.Repeat("x", 600<<10) // 600 KB
	up := func(user, name, owner, data string) int {
		p := "/api/files?name=" + name
		if owner != "" {
			p += "&owner=" + owner
		}
		c, _ := req(t, srv, user, "POST", p, data)
		return c
	}
	// ohne Kontingent: unbegrenzt
	if c := up("bob", "a.bin", "", blob); c != 200 {
		t.Fatal("ohne Kontingent:", c)
	}
	// nur globale Admins stellen das Kontingent ein
	if c, _ := req(t, srv, "bob", "POST", "/api/settings/quota", `{"mb":1}`); c != 403 {
		t.Fatal("bob darf nicht:", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/settings/quota", `{"mb":-1}`); c != 400 {
		t.Fatal("negativ:", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/settings/quota", `{"mb":1}`); c != 200 {
		t.Fatal(c, b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/settings", ""); !strings.Contains(b, `"quotaMB":1`) {
		t.Fatal("GET settings:", b)
	}
	// bob belegt schon 600 KB: eine zweite Datei passt nicht mehr (507), Ersetzen der ersten schon
	if c := up("bob", "b.bin", "", blob); c != 507 {
		t.Fatal("zweite Datei:", c)
	}
	if c := up("bob", "a.bin", "", blob); c != 200 {
		t.Fatal("ersetzen:", c)
	}
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/c.bin", blob); c != 507 {
		t.Fatal("WebDAV:", c)
	}
	// Anzeige
	_, b := req(t, srv, "bob", "GET", "/api/files", "")
	var l struct {
		Quota, Used int64
	}
	json.Unmarshal([]byte(b), &l)
	if l.Quota != 1<<20 || l.Used != int64(len(blob)) {
		t.Fatal("quota/used:", l, b)
	}
	// anna hat ein eigenes Kontingent
	if c := up("anna", "a.bin", "", blob); c != 200 {
		t.Fatal("anna:", c)
	}
	// Gruppenordner: eigenes Kontingent, unabhängig von der eigenen Ablage
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"q1","areas":["files"],"folder":"rw"}`)
	req(t, srv, "anna", "POST", "/api/users/import?update=1", "bob;;q1\n")
	if c := up("bob", "g1.bin", "@q1", blob); c != 200 {
		t.Fatal("Gruppenordner:", c)
	}
	if c := up("bob", "g2.bin", "@q1", blob); c != 507 {
		t.Fatal("Gruppenordner voll:", c)
	}
	// Kontingent aus: wieder unbegrenzt
	req(t, srv, "anna", "POST", "/api/settings/quota", `{"mb":0}`)
	if c := up("bob", "b.bin", "", blob); c != 200 {
		t.Fatal("unbegrenzt:", c)
	}
	// parallele Uploads dürfen das Kontingent nicht überschreiten (Reservierung)
	req(t, srv, "anna", "POST", "/api/settings/quota", `{"mb":1}`)
	req(t, srv, "bob", "DELETE", "/api/files/bob/a.bin", "")
	req(t, srv, "bob", "DELETE", "/api/files/bob/b.bin", "")
	var ok int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if up("bob", "p"+string(rune('a'+i))+".bin", "", strings.Repeat("y", 400<<10)) == 200 {
				atomic.AddInt32(&ok, 1)
			}
		}(i)
	}
	wg.Wait()
	if ok != 2 {
		t.Fatal("parallel erlaubt:", ok, "(erwartet 2 von 400 KB bei 1 MB)")
	}
}
