// livetest: Funktionsprüfung einer laufenden cs-team-Instanz über HTTP/WebSocket (Windows, Linux, ...).
//
//	go run ./livetest -url http://127.0.0.1:9104 -admin admin:adminadmin1
//
// Legt eigene Testbenutzer mit Zufallsnamen an und räumt Dateien wieder auf; Ausgabe: PASS/FAIL je Prüfung, Exit-Code 1 bei Fehlern.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

var (
	base                 string
	adminU, adminP       string
	fails, passes        int
	httpc                = &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	started              = time.Now()
	sfx                  = strconv.FormatInt(time.Now().Unix()%1000000, 10)
	userB, passB, passB2 = "lt" + sfx, "start-passwort-1", "live-passwort-" + sfx
)

func ok(name string, cond bool, detail ...any) {
	if cond {
		passes++
		fmt.Printf("PASS  %s\n", name)
		return
	}
	fails++
	fmt.Printf("FAIL  %s  %v\n", name, detail)
}

func todo(name string, cond bool, detail ...any) {
	if cond {
		ok(name, true)
		return
	}
	fmt.Printf("TODO  %s  %v\n", name, detail)
}

func info(f string, a ...any) { fmt.Printf("INFO  "+f+"\n", a...) }

type resp struct {
	Code int
	H    http.Header
	B    []byte
}

func do(user, pass, method, path string, body []byte, hdr ...string) resp {
	r, _ := http.NewRequest(method, base+path, bytes.NewReader(body))
	r.SetBasicAuth(user, pass)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	x, err := httpc.Do(r)
	if err != nil {
		return resp{Code: -1, B: []byte(err.Error())}
	}
	defer x.Body.Close()
	b, _ := io.ReadAll(x.Body)
	return resp{x.StatusCode, x.Header, b}
}

func adm(method, path string, body string, hdr ...string) resp {
	return do(adminU, adminP, method, path, []byte(body), hdr...)
}
func usr(method, path string, body []byte, hdr ...string) resp {
	return do(userB, passB2, method, path, body, hdr...)
}

func dial(user, pass string) (*websocket.Conn, error) {
	h := http.Header{}
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth(user, pass)
	h.Set("Authorization", r.Header.Get("Authorization"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/chat/ws", &websocket.DialOptions{HTTPHeader: h})
	return c, err
}

func wsRead(c *websocket.Conn, d time.Duration) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		return nil
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func wsNext(c *websocket.Conn, typ string, d time.Duration) map[string]any {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if m := wsRead(c, time.Until(end)); m != nil && m["t"] == typ {
			return m
		}
	}
	return nil
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func main() {
	u := flag.String("url", "http://127.0.0.1:9004", "Basis-URL")
	a := flag.String("admin", "admin:admin", "Admin Benutzer:Passwort")
	slow := flag.Bool("slow", false, "auch die 35 s lange WebSocket-Ping-Prüfung")
	flag.Parse()
	base = strings.TrimRight(*u, "/")
	adminU, adminP, _ = strings.Cut(*a, ":")
	fmt.Println("== cs-team livetest", base, "==")

	// ---- Anmeldung und Benutzer
	r := adm("GET", "/api/me", "")
	ok("Admin-Anmeldung", r.Code == 200 && strings.Contains(string(r.B), `"admin":true`), r.Code, string(r.B))
	if r.Code != 200 {
		os.Exit(2)
	}
	ok("Falsches Passwort abgewiesen", do(adminU, "falsch-falsch", "GET", "/api/me", nil).Code == 401)
	r = adm("POST", "/api/users", js(map[string]any{"name": userB, "password": passB, "groups": []string{"alluser"}}))
	ok("Benutzer anlegen", r.Code == 200, r.Code, string(r.B))
	r = do(userB, passB, "GET", "/api/files", nil)
	ok("Startpasswort: Änderung erzwungen (403)", r.Code == 403 && r.H.Get("X-Must-Change") == "1", r.Code)
	r = do(userB, passB, "POST", "/api/me/password", []byte(js(map[string]string{"new": passB2})))
	ok("Passwort ändern", r.Code == 200, r.Code, string(r.B))
	ok("Anmeldung mit neuem Passwort", usr("GET", "/api/me", nil).Code == 200)

	// ---- Dateien: Hochladen, Streaming, Range, 304
	big := make([]byte, 6<<20+123)
	rand.Read(big)
	sum := sha256.Sum256(big)
	t0 := time.Now()
	r = usr("POST", "/api/files?name=big.bin", big)
	ok("Upload 6 MB", r.Code == 200, r.Code, string(r.B))
	info("Upload %.1f MB/s", float64(len(big))/1e6/time.Since(t0).Seconds())
	r = usr("GET", "/api/files/"+userB+"/big.bin", nil)
	s2 := sha256.Sum256(r.B)
	ok("Download unverändert (SHA-256)", r.Code == 200 && s2 == sum, r.Code, len(r.B))
	et := r.H.Get("ETag")
	ok("ETag und Accept-Ranges", et != "" && r.H.Get("Accept-Ranges") == "bytes", r.H)
	r = usr("GET", "/api/files/"+userB+"/big.bin", nil, "Range", "bytes=1000000-1000999")
	ok("Range 206 Inhalt", r.Code == 206 && bytes.Equal(r.B, big[1000000:1001000]) && r.H.Get("Content-Range") == "bytes 1000000-1000999/"+strconv.Itoa(len(big)), r.Code, r.H.Get("Content-Range"))
	r = usr("GET", "/api/files/"+userB+"/big.bin", nil, "Range", "bytes=-100")
	ok("Range Suffix", r.Code == 206 && bytes.Equal(r.B, big[len(big)-100:]), r.Code)
	r = usr("GET", "/api/files/"+userB+"/big.bin", nil, "Range", "bytes="+strconv.Itoa(len(big))+"-")
	ok("Range hinter dem Ende: 416", r.Code == 416, r.Code)
	r = usr("GET", "/api/files/"+userB+"/big.bin", nil, "If-None-Match", et)
	ok("If-None-Match: 304", r.Code == 304 && len(r.B) == 0, r.Code)
	r = adm("GET", "/api/files/"+userB+"/big.bin", "", "If-None-Match", et)
	ok("Fremder Benutzer: 404 statt 304", r.Code == 404, r.Code)
	r = usr("POST", "/api/files?name=ordner/klein.txt", []byte("hallo welt äöü"))
	ok("Upload Unterordner", r.Code == 200, r.Code, string(r.B))

	// ---- WebDAV (aktueller Stand)
	dav := func(m, p string, b []byte, h ...string) resp { return usr(m, "/webdav"+p, b, h...) }
	r = dav("PUT", "/dav.txt", []byte("0123456789"))
	ok("WebDAV PUT", r.Code == 201 || r.Code == 204, r.Code)
	r = dav("GET", "/dav.txt", nil)
	ok("WebDAV GET", r.Code == 200 && string(r.B) == "0123456789", r.Code, string(r.B))
	r = dav("GET", "/dav.txt", nil, "Range", "bytes=2-4")
	todo("WebDAV Range", r.Code == 206 && string(r.B) == "234", r.Code, string(r.B))
	r = dav("PROPFIND", "/", nil, "Depth", "1")
	ok("WebDAV PROPFIND", r.Code == 207 && strings.Contains(string(r.B), "dav.txt"), r.Code)
	r = dav("OPTIONS", "/", nil)
	ok("WebDAV OPTIONS: DAV-Klasse 2", strings.Contains(r.H.Get("Dav"), "2") && strings.Contains(r.H.Get("Allow"), "LOCK"), r.H.Get("Dav"), r.H.Get("Allow"))
	lk := `<?xml version="1.0"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner>live</D:owner></D:lockinfo>`
	r = dav("LOCK", "/dav.txt", []byte(lk), "Timeout", "Second-60", "Content-Type", "text/xml")
	tok := r.H.Get("Lock-Token")
	ok("WebDAV LOCK", r.Code == 200 && tok != "", r.Code, tok)
	r = dav("PUT", "/dav.txt", []byte("0123456789x"))
	ok("WebDAV PUT durch Sperrenden", r.Code == 201 || r.Code == 204, r.Code)
	r = dav("LOCK", "/dav.txt", nil, "If", "("+tok+")", "Timeout", "Second-120")
	ok("WebDAV LOCK erneuern", r.Code == 200, r.Code)
	r = dav("UNLOCK", "/dav.txt", nil, "Lock-Token", tok)
	ok("WebDAV UNLOCK", r.Code == 204, r.Code)
	r = dav("LOCK", "/neu-gesperrt.docx", []byte(lk), "Content-Type", "text/xml")
	ok("WebDAV LOCK neue Datei (lock-null)", r.Code == 201, r.Code)
	dav("UNLOCK", "/neu-gesperrt.docx", nil, "Lock-Token", r.H.Get("Lock-Token"))
	dav("DELETE", "/neu-gesperrt.docx", nil)
	r = dav("MKCOL", "/neu", nil)
	info("WebDAV MKCOL -> %d", r.Code)
	r = dav("MOVE", "/dav.txt", nil, "Destination", base+"/webdav/dav2.txt")
	ok("WebDAV MOVE", r.Code == 201 || r.Code == 204, r.Code)
	r = dav("DELETE", "/dav2.txt", nil)
	ok("WebDAV DELETE", r.Code == 204, r.Code)

	// ---- Papierkorb
	trashItems := func() []struct{ ID, Owner, Name string } {
		var t struct {
			Days  int
			Items []struct{ ID, Owner, Name string }
		}
		json.Unmarshal(usr("GET", "/api/trash", nil).B, &t)
		return t.Items
	}
	var tid string
	for _, it := range trashItems() {
		if it.Name == "dav2.txt" {
			tid = it.ID
		}
	}
	ok("Papierkorb: gelöschte Datei (WebDAV) ist dort", tid != "")
	r = usr("GET", "/webdav/dav2.txt", nil)
	ok("Gelöschte Datei ist weg", r.Code == 404, r.Code)
	r = usr("POST", "/api/trash/"+userB+"/"+tid+"/restore", nil)
	ok("Papierkorb: Wiederherstellen", r.Code == 200, r.Code, string(r.B))
	r = usr("GET", "/webdav/dav2.txt", nil)
	ok("Wiederhergestellte Datei lesbar", r.Code == 200 && string(r.B) == "0123456789x", r.Code, string(r.B))
	r = usr("DELETE", "/api/files/"+userB+"/dav2.txt", nil)
	ok("Löschen im Browser", r.Code == 200, r.Code)
	r = usr("DELETE", "/api/trash", nil)
	ok("Papierkorb leeren", r.Code == 200, r.Code, string(r.B))
	ok("Papierkorb danach leer", len(trashItems()) == 0)

	// ---- Kontingent
	r = adm("POST", "/api/settings/quota", `{"mb":8}`)
	ok("Kontingent setzen", r.Code == 200, r.Code)
	r = usr("POST", "/api/files?name=zweite.bin", big)
	ok("Kontingent: 507", r.Code == 507, r.Code)
	adm("POST", "/api/settings/quota", `{"mb":0}`)

	// ---- Kalender (Serie über die Sommerzeit, Bearbeiten nur eines Vorkommens)
	ev := `{"summary":"Jour fixe","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","tz":"Europe/Berlin","rule":{"freq":"WEEKLY","interval":1,"count":4}}`
	r = usr("POST", "/api/cal/default/events", []byte(ev))
	ok("Serientermin anlegen", r.Code == 200 || r.Code == 201, r.Code, string(r.B))
	r = usr("GET", "/api/cal/default/events?from=2027-03-01&to=2027-05-01", nil)
	var evs []map[string]any
	json.Unmarshal(r.B, &evs)
	ok("Serie: 4 Vorkommen", len(evs) == 4, len(evs), string(r.B))
	if len(evs) == 4 {
		ok("Sommerzeit: 3. Vorkommen 07:00Z", evs[3]["start"] == "2027-04-05T07:00:00Z" && evs[0]["start"] == "2027-03-15T08:00:00Z", evs[0]["start"], evs[3]["start"])
		put := js(map[string]any{"summary": "Jour fixe (verschoben)", "start": "2027-03-22T13:00:00Z", "end": "2027-03-22T14:00:00Z", "tz": "Europe/Berlin", "scope": "one", "rid": evs[1]["rid"]})
		r = usr("PUT", "/api/cal/default/events/"+fmt.Sprint(evs[1]["file"]), []byte(put))
		ok("Einzelnes Vorkommen ändern", r.Code == 200, r.Code, string(r.B))
		r = usr("DELETE", "/api/cal/default/events/"+fmt.Sprint(evs[1]["file"])+"?scope=all", nil)
		ok("Serie löschen", r.Code == 200 || r.Code == 204, r.Code)
	}
	// Erinnerung, Teilnehmer, "dieser und folgende", Export/Import, VTIMEZONE
	r = usr("POST", "/api/cal/default/events", []byte(`{"summary":"Live 0.15","start":"2027-03-15T08:00:00Z","end":"2027-03-15T09:00:00Z","tz":"Europe/Berlin","alarm":15,"att":["`+adminU+`","extern@live.invalid"],"rule":{"freq":"WEEKLY","count":4}}`))
	var cr struct{ File string }
	json.Unmarshal(r.B, &cr)
	ok("Termin mit Erinnerung und Teilnehmern", r.Code == 200 && cr.File != "", r.Code, string(r.B))
	r = usr("GET", "/api/cal/default/export.ics", nil)
	ics := string(r.B)
	ok("Export: VALARM, ATTENDEE, VTIMEZONE", r.Code == 200 && strings.Contains(ics, "BEGIN:VALARM") && strings.Contains(ics, "extern@live.invalid") && strings.Contains(ics, "BEGIN:VTIMEZONE"), r.Code)
	r = usr("PUT", "/api/cal/default/events/"+cr.File, []byte(`{"summary":"Live 0.15 neu","start":"2027-03-29T09:00:00Z","end":"2027-03-29T10:00:00Z","tz":"Europe/Berlin","scope":"following","rid":"2027-03-29T07:00:00Z"}`))
	ok("Dieser und folgende ändern", r.Code == 200, r.Code, string(r.B))
	r = usr("GET", "/api/cal/default/events?from=2027-03-01&to=2027-05-01", nil)
	var l2 []map[string]any
	json.Unmarshal(r.B, &l2)
	neu := 0
	for _, e := range l2 {
		if e["summary"] == "Live 0.15 neu" {
			neu++
		}
	}
	ok("Serie geteilt: 2 alte + 2 neue", len(l2) == 4 && neu == 2, len(l2), neu)
	r = usr("POST", "/api/cal/default/import", []byte(ics), "Content-Type", "text/calendar")
	ok("Import (bekannte UID: aktualisiert)", r.Code == 200 && strings.Contains(string(r.B), `"skipped":0`), r.Code, string(r.B))
	r = usr("DELETE", "/api/cal/default/events/"+cr.File+"?scope=all", nil)
	for _, e := range l2 {
		if e["summary"] == "Live 0.15 neu" {
			usr("DELETE", "/api/cal/default/events/"+fmt.Sprint(e["file"])+"?scope=all", nil)
			break
		}
	}
	// 0.15.3: ganze Serie von einem späteren Vorkommen aus verschieben (rid + scope=all): Serienbeginn wandert mit
	r = usr("POST", "/api/cal/default/events", []byte(`{"summary":"Live 0.15.3","start":"2027-06-07T08:00:00Z","end":"2027-06-07T09:00:00Z","tz":"Europe/Berlin","rule":{"freq":"WEEKLY","count":3}}`))
	var sr struct{ File string }
	json.Unmarshal(r.B, &sr)
	ok("Serie anlegen (0.15.3)", r.Code == 200 && sr.File != "", r.Code, string(r.B))
	r = usr("PUT", "/api/cal/default/events/"+sr.File, []byte(`{"summary":"Live 0.15.3","start":"2027-06-15T08:00:00Z","end":"2027-06-15T09:00:00Z","tz":"Europe/Berlin","scope":"all","rid":"2027-06-14T08:00:00Z"}`))
	ok("Serie vom 2. Vorkommen um einen Tag verschieben", r.Code == 200, r.Code, string(r.B))
	r = usr("GET", "/api/cal/default/events?from=2027-06-01&to=2027-07-01", nil)
	var l3 []map[string]any
	json.Unmarshal(r.B, &l3)
	st := ""
	for _, e := range l3 {
		if e["summary"] == "Live 0.15.3" {
			st += fmt.Sprint(e["start"]) + " "
		}
	}
	ok("Serie: Dienstage statt Montage, nichts verloren", st == "2027-06-08T08:00:00Z 2027-06-15T08:00:00Z 2027-06-22T08:00:00Z ", st)
	usr("DELETE", "/api/cal/default/events/"+sr.File+"?scope=all", nil)
	// Belegungsübersicht (nur Admin)
	r = usr("GET", "/api/filesusage", nil)
	ok("Belegung: Benutzer gesperrt (403)", r.Code == 403, r.Code)
	r = adm("GET", "/api/filesusage", "")
	ok("Belegung: Admin sieht Benutzer", r.Code == 200 && strings.Contains(string(r.B), userB), r.Code)

	// ---- KI-Widget: Konfiguration schaltet das Widget (ohne Provider-Aufruf)
	r = usr("GET", "/api/ai/config", nil)
	ok("KI aus: enabled=false", strings.Contains(string(r.B), `"enabled":false`), string(r.B))
	r = adm("POST", "/api/ai/settings", `{"mode":"provider","provider":"anthropic","model":"test-modell"}`)
	ok("KI einstellen", r.Code == 200, r.Code, string(r.B))
	ok("KI an: enabled=true (sofort, ohne Neustart)", strings.Contains(string(usr("GET", "/api/ai/config", nil).B), `"enabled":true`))
	adm("POST", "/api/ai/settings", `{"mode":"off"}`)

	// ---- Chat über WebSocket
	adm("POST", "/api/groups", `{"name":"live`+sfx+`","areas":["files"],"chat":"member","chans":"member","admins":["`+adminU+`"]}`)
	adm("POST", "/api/groups/live"+sfx+"/members", `{"add":["`+userB+`"]}`)
	ca, err := dial(adminU, adminP)
	ok("WebSocket verbinden (Admin)", err == nil, err)
	cb, err2 := dial(userB, passB2)
	ok("WebSocket verbinden (Benutzer)", err2 == nil, err2)
	if err == nil && err2 == nil {
		defer ca.CloseNow()
		defer cb.CloseNow()
		ok("hello", wsNext(ca, "hello", 5*time.Second) != nil && wsNext(cb, "hello", 5*time.Second) != nil)
		g := "live" + sfx
		ca.Write(context.Background(), websocket.MessageText, []byte(js(map[string]any{"t": "send", "g": g, "c": "allgemein", "text": "Grüße aus dem Livetest ✓"})))
		m := wsNext(cb, "msg", 5*time.Second)
		ok("Nachricht live zugestellt", m != nil, m)
		if m != nil {
			id := m["m"].(map[string]any)["id"]
			cb.Write(context.Background(), websocket.MessageText, []byte(js(map[string]any{"t": "react", "g": g, "c": "allgemein", "id": id, "e": "👍"})))
			ok("Reaktion live zugestellt", wsNext(ca, "upd", 5*time.Second) != nil)
			t1 := time.Now()
			for i := 0; i < 25; i++ {
				cb.Write(context.Background(), websocket.MessageText, []byte(js(map[string]any{"t": "react", "g": g, "c": "allgemein", "id": id, "e": "❤️"})))
			}
			lim := false
			for time.Since(t1) < 8*time.Second && !lim {
				x := wsRead(cb, 2*time.Second)
				if x == nil {
					break
				}
				lim = x["t"] == "err" && strings.Contains(fmt.Sprint(x["m"]), "too many")
			}
			ok("Tempolimit bei Reaktionen", lim)
		}
		// Verbindungslimit: 8 weitere Verbindungen des Benutzers, die erste (cb) muss weichen
		var extra []*websocket.Conn
		for i := 0; i < 8; i++ {
			c, e := dial(userB, passB2)
			if e == nil {
				extra = append(extra, c)
			}
		}
		closed := false
		for k := 0; k < 12 && !closed; k++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _, e := cb.Read(ctx)
			closed = e != nil && ctx.Err() == nil // Fehler ohne Zeitüberschreitung = Gegenseite hat geschlossen
			cancel()
		}
		ok("Verbindungslimit: älteste Verbindung geschlossen", closed)
		for _, c := range extra {
			c.CloseNow()
		}
		if *slow {
			info("Ping-Prüfung: 35 s warten, Verbindung muss offen bleiben")
			time.Sleep(35 * time.Second)
			ca.Write(context.Background(), websocket.MessageText, []byte(js(map[string]any{"t": "send", "g": g, "c": "allgemein", "text": "nach 35 s"})))
			ok("Verbindung nach 35 s noch offen", wsNext(ca, "msg", 5*time.Second) != nil)
		}
	}

	// ---- Aufräumen
	usr("DELETE", "/api/files/"+userB+"/big.bin", nil)
	usr("DELETE", "/api/files/"+userB+"/ordner/klein.txt", nil)
	adm("DELETE", "/api/groups/live"+sfx, "")
	adm("DELETE", "/api/users/"+userB, "")
	_ = hex.EncodeToString
	fmt.Printf("== %d bestanden, %d fehlgeschlagen (%.1f s) ==\n", passes, fails, time.Since(started).Seconds())
	if fails > 0 {
		os.Exit(1)
	}
}
