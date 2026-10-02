package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"cs-team/auth"
	"cs-team/doc"
	"cs-team/store"
)

const ics = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:e1\r\nDTSTAMP:20260930T100000Z\r\nDTSTART:20260930T120000Z\r\nDTEND:20260930T130000Z\r\nSUMMARY:Test\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func setup(t *testing.T) (*httptest.Server, store.Store) {
	var st store.Store = store.NewMem()
	if os.Getenv("CS_TEST_FS") == "1" { // dieselben Tests gegen den Ordner-Speicher
		f, err := store.NewFS(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		st = f
	}
	auth.ForceChange = false
	a := auth.New(st)
	ctx := context.Background()
	for _, u := range []string{"anna", "bob"} {
		if err := a.SetUser(ctx, u, "passwort-"+u, u == "anna"); err != nil {
			t.Fatal(err)
		}
	}
	return httptest.NewServer(routes(st, a)), st
}

func req(t *testing.T, srv *httptest.Server, user, method, path, body string, hdr ...string) (int, string) {
	r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	r.SetBasicAuth(user, "passwort-"+user)
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

func TestCalDAV(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, _ := req(t, srv, "anna", "PROPFIND", "/dav/anna/cal/", "", "Depth", "1"); c != 207 {
		t.Fatalf("propfind home: %d", c)
	}
	if c, b := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/e1.ics", ics, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatalf("put: %d %s", c, b)
	}
	if c, _ := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/e1.ics", ics, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 412 {
		t.Fatalf("second create must be 412, got %d", c)
	}
	if c, b := req(t, srv, "anna", "GET", "/dav/anna/cal/default/e1.ics", ""); c != 200 || !strings.Contains(b, "SUMMARY:Test") {
		t.Fatalf("get: %d %s", c, b)
	}
	q := `<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><d:getetag/><c:calendar-data/></d:prop><c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT"/></c:comp-filter></c:filter></c:calendar-query>`
	if c, b := req(t, srv, "anna", "REPORT", "/dav/anna/cal/default/", q, "Depth", "1", "Content-Type", "application/xml"); c != 207 || !strings.Contains(b, "e1") {
		t.Fatalf("report: %d %s", c, b)
	}
	if c, _ := req(t, srv, "bob", "GET", "/dav/anna/cal/default/e1.ics", ""); c == 200 {
		t.Fatalf("bob must not read anna calendar: %d", c)
	}
	if c, _ := req(t, srv, "anna", "DELETE", "/dav/anna/cal/default/e1.ics", ""); c != 204 {
		t.Fatalf("delete: %d", c)
	}
}

func dial(t *testing.T, srv *httptest.Server, user, id string) *websocket.Conn {
	h := http.Header{}
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth(user, "passwort-"+user)
	h.Set("Authorization", r.Header.Get("Authorization"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/"+id, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial %s: %v", user, err)
	}
	return c
}

func read(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func TestSheetLWW(t *testing.T) {
	srv, st := setup(t)
	defer srv.Close()
	_, b := req(t, srv, "anna", "POST", "/api/docs", `{"name":"t","type":"sheet"}`)
	var r struct{ ID string }
	json.Unmarshal([]byte(b), &r)
	// bob hat keinen Zugriff, bis geteilt wird
	if c, _ := req(t, srv, "bob", "GET", "/api/docs/"+r.ID+"/export", ""); c != 404 {
		t.Fatalf("bob before share: %d", c)
	}
	req(t, srv, "anna", "POST", "/api/docs/"+r.ID+"/share", `{"read":[],"write":["bob"]}`)

	ca, cb := dial(t, srv, "anna", r.ID), dial(t, srv, "bob", r.ID)
	defer ca.CloseNow()
	defer cb.CloseNow()
	read(t, ca)
	read(t, cb) // init
	ctx := context.Background()
	ca.Write(ctx, websocket.MessageText, []byte(`{"t":"set","k":"A1","v":"1"}`))
	read(t, ca)
	read(t, cb)
	cb.Write(ctx, websocket.MessageText, []byte(`{"t":"set","k":"A1","v":"2"}`)) // später -> gewinnt
	ma := read(t, ca)
	read(t, cb)
	if ma["item"].(map[string]any)["v"] != "2" {
		t.Fatalf("LWW: %v", ma)
	}
	// Snapshot nach Debounce in S3
	time.Sleep(2500 * time.Millisecond)
	raw, _, err := st.Get(ctx, "doc/"+r.ID+"/snapshot.json")
	if err != nil || !strings.Contains(string(raw), `"v":"2"`) {
		t.Fatalf("snapshot: %v %s", err, raw)
	}
	if c, csv := req(t, srv, "bob", "GET", "/api/docs/"+r.ID+"/export", ""); c != 200 || strings.TrimSpace(csv) != "2" {
		t.Fatalf("export: %d %q", c, csv)
	}
}

func TestMergeCommutes(t *testing.T) {
	a, b := doc.NewDoc(), doc.NewDoc()
	a.Apply("A1", doc.Item{V: "x", TS: 1, By: "a"})
	b.Apply("A1", doc.Item{V: "y", TS: 2, By: "b"})
	b.Apply("B1", doc.Item{V: "z", TS: 1, By: "b"})
	a2, b2 := doc.NewDoc(), doc.NewDoc()
	a2.Merge(a)
	a2.Merge(b)
	b2.Merge(b)
	b2.Merge(a)
	if a2.Items["A1"] != b2.Items["A1"] || a2.Items["A1"].V != "y" || len(a2.Items) != 2 {
		t.Fatal("merge not commutative")
	}
}

func reqp(t *testing.T, srv *httptest.Server, user, pw, method, path, body string) int {
	r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	r.SetBasicAuth(user, pw)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestUserAdmin(t *testing.T) {
	srv, _ := setup(t) // anna = Admin, bob = normal
	defer srv.Close()
	pa, pb := "passwort-anna", "passwort-bob"

	// Nur Admin darf verwalten
	if c := reqp(t, srv, "bob", pb, "POST", "/api/users", `{"name":"carl","password":"carlcarl1"}`); c != 403 {
		t.Fatalf("bob create: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users", `{"name":"carl","password":"carlcarl1"}`); c != 200 {
		t.Fatalf("anna create: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users", `{"name":"carl","password":"carlcarl1"}`); c != 409 {
		t.Fatalf("dup: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users", `{"name":"Bad Name","password":"carlcarl1"}`); c != 400 {
		t.Fatalf("bad name: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users", `{"name":"dora","password":"kurz"}`); c != 400 {
		t.Fatalf("short pw: %d", c)
	}
	if c := reqp(t, srv, "carl", "carlcarl1", "GET", "/api/me", ""); c != 200 {
		t.Fatalf("carl login: %d", c)
	}

	// Eigenes Passwort ändern
	if c := reqp(t, srv, "carl", "carlcarl1", "POST", "/api/me/password", `{"old":"falsch","new":"neues-passwort"}`); c != 403 {
		t.Fatalf("pw change wrong old: %d", c)
	}
	if c := reqp(t, srv, "carl", "carlcarl1", "POST", "/api/me/password", `{"old":"carlcarl1","new":"neues-passwort"}`); c != 200 {
		t.Fatalf("pw change: %d", c)
	}
	if c := reqp(t, srv, "carl", "carlcarl1", "GET", "/api/me", ""); c != 401 {
		t.Fatalf("old pw still valid: %d", c)
	}

	// Sperren / Entsperren
	reqp(t, srv, "anna", pa, "POST", "/api/users/carl/flags", `{"disabled":true}`)
	if c := reqp(t, srv, "carl", "neues-passwort", "GET", "/api/me", ""); c != 401 {
		t.Fatalf("disabled login: %d", c)
	}
	reqp(t, srv, "anna", pa, "POST", "/api/users/carl/flags", `{"disabled":false}`)
	if c := reqp(t, srv, "carl", "neues-passwort", "GET", "/api/me", ""); c != 200 {
		t.Fatalf("re-enabled login: %d", c)
	}

	// letzter Admin ist geschützt
	if c := reqp(t, srv, "anna", pa, "DELETE", "/api/users/anna", ""); c != 400 {
		t.Fatalf("delete last admin: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users/anna/flags", `{"disabled":true}`); c != 400 {
		t.Fatalf("disable last admin: %d", c)
	}

	// Löschen
	if c := reqp(t, srv, "anna", pa, "DELETE", "/api/users/carl?purge=1", ""); c != 200 {
		t.Fatalf("delete: %d", c)
	}
	if c := reqp(t, srv, "carl", "neues-passwort", "GET", "/api/me", ""); c != 401 {
		t.Fatalf("deleted login: %d", c)
	}
}

func TestLockout(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	for i := 0; i < 5; i++ {
		if c := reqp(t, srv, "bob", "falsch-falsch", "GET", "/api/me", ""); c != 401 {
			t.Fatalf("try %d: %d", i, c)
		}
	}
	if c := reqp(t, srv, "bob", "passwort-bob", "GET", "/api/me", ""); c != 429 {
		t.Fatalf("locked expected 429, got %d", c)
	}
	// anderer Benutzer von derselben IP ist nicht betroffen
	if c := reqp(t, srv, "anna", "passwort-anna", "GET", "/api/me", ""); c != 200 {
		t.Fatalf("anna: %d", c)
	}
}

func TestLegacyUsersJSON(t *testing.T) {
	st := store.NewMem()
	st.Put(context.Background(), "users/users.json", []byte(`{"old":"`+mustHash(t, "altes-passwort")+`"}`), "")
	srv := httptest.NewServer(routes(st, auth.New(st)))
	defer srv.Close()
	if c := reqp(t, srv, "old", "altes-passwort", "GET", "/api/me", ""); c != 200 {
		t.Fatalf("legacy login: %d", c)
	}
}

func TestBootstrapAdmin(t *testing.T) {
	var st store.Store = store.NewMem()
	if os.Getenv("CS_TEST_FS") == "1" { // dieselben Tests gegen den Ordner-Speicher
		f, err := store.NewFS(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		st = f
	}
	a := auth.New(st)
	if err := a.Bootstrap(context.Background(), "root", "root-passwort"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(routes(st, a))
	defer srv.Close()
	if c := reqp(t, srv, "root", "root-passwort", "POST", "/api/users", `{"name":"x1","password":"x1x1x1x1"}`); c != 200 {
		t.Fatalf("bootstrap admin must manage users: %d", c)
	}
}

func mustHash(t *testing.T, pw string) string {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 4)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

func TestFilesShareAndPublic(t *testing.T) {
	t.Setenv("CS_MAX_UPLOAD_MB", "1")
	srv, _ := setup(t)
	defer srv.Close()
	pdf := "%PDF-1.4 testinhalt"

	if c, b := req(t, srv, "anna", "POST", "/api/files?name=bericht.pdf", pdf); c != 200 {
		t.Fatalf("upload: %d %s", c, b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/files?name=a%2F..%2Fb.pdf", pdf); c != 400 {
		t.Fatalf("bad name must be 400, got %d", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/files?name=big.bin", strings.Repeat("x", 2<<20)); c != 413 {
		t.Fatalf("too large must be 413, got %d", c)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/files/anna/bericht.pdf", ""); c != 404 {
		t.Fatalf("unshared must be 404: %d", c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/filesshare/anna/bericht.pdf", `{"read":["bob"]}`); c != 403 {
		t.Fatalf("bob must not share: %d", c)
	}
	req(t, srv, "anna", "POST", "/api/filesshare/anna/bericht.pdf", `{"read":["bob"],"public":true}`)
	if c, b := req(t, srv, "bob", "GET", "/api/files/anna/bericht.pdf", ""); c != 200 || b != pdf {
		t.Fatalf("shared download: %d %q", c, b)
	}
	if c, _ := req(t, srv, "bob", "DELETE", "/api/files/anna/bericht.pdf", ""); c != 403 {
		t.Fatalf("bob must not delete: %d", c)
	}
	// Liste zeigt bob die Freigabe, anna den öffentlichen Link
	_, la := req(t, srv, "anna", "GET", "/api/files", "")
	var l struct {
		Own    []struct{ Name, Pub string }
		Shared []struct{ Name string }
	}
	json.Unmarshal([]byte(la), &l)
	if len(l.Own) != 1 || !strings.HasPrefix(l.Own[0].Pub, "/pub/") {
		t.Fatalf("anna list: %s", la)
	}
	_, lb := req(t, srv, "bob", "GET", "/api/files", "")
	json.Unmarshal([]byte(lb), &l)
	if len(l.Shared) != 1 {
		t.Fatalf("bob list: %s", lb)
	}
	pub := ""
	json.Unmarshal([]byte(la), &l)
	pub = l.Own[0].Pub
	resp, err := http.Get(srv.URL + pub) // ohne Login
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("public: %v %v", err, resp)
	}
	resp.Body.Close()
	req(t, srv, "anna", "POST", "/api/filesshare/anna/bericht.pdf", `{"read":["bob"],"public":false}`)
	if resp, _ = http.Get(srv.URL + pub); resp.StatusCode != 404 {
		t.Fatalf("revoked link must be 404: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// HTML wird nie inline ausgeliefert
	req(t, srv, "anna", "POST", "/api/files?name=x.html", "<script>alert(1)</script>")
	r2, _ := http.NewRequest("GET", srv.URL+"/api/files/anna/x.html", nil)
	r2.SetBasicAuth("anna", "passwort-anna")
	resp, _ = http.DefaultClient.Do(r2)
	if d := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(d, "attachment") {
		t.Fatalf("html must be attachment: %q", d)
	}
	resp.Body.Close()

	if c, _ := req(t, srv, "anna", "DELETE", "/api/files/anna/bericht.pdf", ""); c != 200 {
		t.Fatalf("owner delete: %d", c)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/files/anna/bericht.pdf", ""); c != 404 {
		t.Fatalf("deleted: %d", c)
	}
}

func TestWebDAV(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, b := req(t, srv, "anna", "PUT", "/webdav/note.txt", "hallo webdav"); c != 201 {
		t.Fatalf("put: %d %s", c, b)
	}
	if c, b := req(t, srv, "anna", "PROPFIND", "/webdav/", "", "Depth", "1"); c != 207 || !strings.Contains(b, "note.txt") {
		t.Fatalf("propfind: %d %s", c, b)
	}
	if c, b := req(t, srv, "anna", "GET", "/webdav/note.txt", ""); c != 200 || b != "hallo webdav" {
		t.Fatalf("get: %d %q", c, b)
	}
	if c, _ := req(t, srv, "anna", "MKCOL", "/webdav/ordner", ""); c != 201 {
		t.Fatalf("mkcol: %d", c)
	}
	if c, b := req(t, srv, "anna", "MOVE", "/webdav/note.txt", "", "Destination", srv.URL+"/webdav/neu.txt"); c != 201 && c != 204 {
		t.Fatalf("move: %d %s", c, b)
	}
	if c, _ := req(t, srv, "anna", "GET", "/webdav/note.txt", ""); c != 404 {
		t.Fatalf("old name must be gone: %d", c)
	}
	// Freigabe sichtbar unter /webdav/shared/anna/
	req(t, srv, "anna", "POST", "/api/filesshare/anna/neu.txt", `{"read":["bob"]}`)
	if c, b := req(t, srv, "bob", "PROPFIND", "/webdav/shared/anna/", "", "Depth", "1"); c != 207 || !strings.Contains(b, "neu.txt") {
		t.Fatalf("shared propfind: %d %s", c, b)
	}
	if c, b := req(t, srv, "bob", "GET", "/webdav/shared/anna/neu.txt", ""); c != 200 || b != "hallo webdav" {
		t.Fatalf("shared get: %d %q", c, b)
	}
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/shared/anna/neu.txt", "manipuliert"); c != 403 {
		t.Fatalf("read-only share must reject PUT: %d", c)
	}
	if c, _ := req(t, srv, "bob", "DELETE", "/webdav/shared/anna/neu.txt", ""); c != 403 {
		t.Fatalf("delete on share must be 403: %d", c)
	}
	// Schreibfreigabe erlaubt Ersetzen
	req(t, srv, "anna", "POST", "/api/filesshare/anna/neu.txt", `{"write":["bob"]}`)
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/shared/anna/neu.txt", "von bob"); c != 204 && c != 201 {
		t.Fatalf("write share PUT: %d", c)
	}
	if _, b := req(t, srv, "anna", "GET", "/webdav/neu.txt", ""); b != "von bob" {
		t.Fatalf("content after bob's write: %q", b)
	}
	if c, _ := req(t, srv, "anna", "DELETE", "/webdav/neu.txt", ""); c != 204 {
		t.Fatalf("delete: %d", c)
	}
}

func TestCalendarAPI(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, b := req(t, srv, "anna", "GET", "/api/cal", ""); c != 200 || !strings.Contains(b, `"default"`) {
		t.Fatalf("list: %d %s", c, b)
	}
	c, b := req(t, srv, "anna", "POST", "/api/cal", `{"name":"Team Urlaub"}`)
	if c != 200 || !strings.Contains(b, "team-urlaub") {
		t.Fatalf("create: %d %s", c, b)
	}
	ev := `{"summary":"Sommerfest","location":"Halle 3","start":"2026-10-05T16:00:00Z","end":"2026-10-05T18:30:00Z"}`
	if c, b := req(t, srv, "anna", "POST", "/api/cal/team-urlaub/events", ev); c != 200 {
		t.Fatalf("add event: %d %s", c, b)
	}
	c, b = req(t, srv, "anna", "GET", "/api/cal/team-urlaub/events", "")
	var evs []struct{ File, Summary, Start string }
	json.Unmarshal([]byte(b), &evs)
	if c != 200 || len(evs) != 1 || evs[0].Summary != "Sommerfest" || evs[0].Start != "2026-10-05T16:00:00Z" {
		t.Fatalf("events: %d %s", c, b)
	}
	// derselbe Termin ist über CalDAV sichtbar
	if c, b := req(t, srv, "anna", "GET", "/dav/anna/cal/team-urlaub/"+evs[0].File, ""); c != 200 || !strings.Contains(b, "SUMMARY:Sommerfest") {
		t.Fatalf("caldav get: %d %s", c, b)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/cal/team-urlaub/events", ""); c != 404 {
		t.Fatalf("bob must not find anna's calendar: %d", c)
	}
	if _, b := req(t, srv, "bob", "GET", "/api/cal/team-urlaub/events", ""); strings.Contains(b, "Sommerfest") {
		t.Fatalf("bob must not see anna's events: %s", b)
	}
	if c, _ := req(t, srv, "anna", "DELETE", "/api/cal/default", ""); c != 400 {
		t.Fatalf("default calendar must not be deletable: %d", c)
	}
	if c, _ := req(t, srv, "anna", "DELETE", "/api/cal/team-urlaub/events/"+evs[0].File, ""); c != 200 {
		t.Fatalf("del event: %d", c)
	}
	if c, _ := req(t, srv, "anna", "DELETE", "/api/cal/team-urlaub", ""); c != 200 {
		t.Fatalf("del calendar: %d", c)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/cal", ""); strings.Contains(b, "team-urlaub") {
		t.Fatalf("calendar still listed: %s", b)
	}
}

func TestTeamShareAndUI(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/files?name=team.txt", "fuer alle")
	if c, _ := req(t, srv, "bob", "GET", "/api/files/anna/team.txt", ""); c != 404 {
		t.Fatalf("private file must be 404 for bob: %d", c)
	}
	req(t, srv, "anna", "POST", "/api/filesshare/anna/team.txt", `{"read":["*"]}`)
	if c, b := req(t, srv, "bob", "GET", "/api/files/anna/team.txt", ""); c != 200 || b != "fuer alle" {
		t.Fatalf("team read: %d %q", c, b)
	}
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/shared/anna/team.txt", "x"); c != 403 {
		t.Fatalf("team read-only must reject write: %d", c)
	}
	req(t, srv, "anna", "POST", "/api/filesshare/anna/team.txt", `{"read":["*"],"write":["*"]}`)
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/shared/anna/team.txt", "von team"); c != 204 && c != 201 {
		t.Fatalf("team write: %d", c)
	}
	// Web-UI wird ausgeliefert (nur mit Login)
	if c, b := req(t, srv, "bob", "GET", "/", ""); c != 200 || !strings.Contains(b, `data-s="files"`) {
		t.Fatalf("ui: %d", c)
	}
	r, _ := http.Get(srv.URL + "/")
	if r.StatusCode != 401 {
		t.Fatalf("ui without login must be 401: %d", r.StatusCode)
	}
	r.Body.Close()
}

func newDocFromFile(t *testing.T, srv *httptest.Server, user, owner, file string) (id, typ string) {
	c, b := req(t, srv, user, "POST", "/api/docs/import", `{"owner":"`+owner+`","file":"`+file+`"}`)
	if c != 200 {
		t.Fatalf("import %s: %d %s", file, c, b)
	}
	var r struct{ ID, Type string }
	json.Unmarshal([]byte(b), &r)
	return r.ID, r.Type
}

func TestOfficeImportExport(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()

	// Calc: csv -> Dokument -> xlsx in Files -> Dokument -> csv
	csv := "Name;Wert\nApfel;3\nBirne;=B2*2\n"
	req(t, srv, "anna", "POST", "/api/files?name=obst.csv", csv)
	id, typ := newDocFromFile(t, srv, "anna", "anna", "obst.csv")
	if typ != "sheet" {
		t.Fatalf("type %s", typ)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/docs/"+id+"/tofiles?format=xlsx", ""); c != 200 || !strings.Contains(b, "obst.xlsx") {
		t.Fatalf("tofiles xlsx: %d %s", c, b)
	}
	id2, _ := newDocFromFile(t, srv, "anna", "anna", "obst.xlsx")
	c, b := req(t, srv, "anna", "GET", "/api/docs/"+id2+"/export", "")
	if c != 200 || strings.TrimSpace(b) != "Name,Wert\nApfel,3\nBirne,'=B2*2" {
		t.Fatalf("xlsx roundtrip csv: %d %q", c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/docs/"+id2+"/tofiles?format=cscalc", ""); c != 200 || !strings.Contains(b, "obst.cscalc") {
		t.Fatalf("cscalc: %d %s", c, b)
	}
	newDocFromFile(t, srv, "anna", "anna", "obst.cscalc")

	// Text: txt -> docx -> Text, mit Umlauten
	req(t, srv, "anna", "POST", "/api/files?name=brief.txt", "Hallo Welt\n\nGrüße aus Köln")
	tid, ttyp := newDocFromFile(t, srv, "anna", "anna", "brief.txt")
	if ttyp != "text" {
		t.Fatalf("type %s", ttyp)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/docs/"+tid+"/tofiles?format=docx", ""); c != 200 || !strings.Contains(b, "brief.docx") {
		t.Fatalf("docx: %d %s", c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/docs/"+tid+"/tofiles?format=rtf", ""); c != 200 || !strings.Contains(b, "brief.rtf") {
		t.Fatalf("rtf: %d %s", c, b)
	}
	tid2, _ := newDocFromFile(t, srv, "anna", "anna", "brief.docx")
	if c, b := req(t, srv, "anna", "GET", "/api/docs/"+tid2+"/export", ""); c != 200 || b != "Hallo Welt\n\nGrüße aus Köln" {
		t.Fatalf("docx roundtrip: %d %q", c, b)
	}
	if c, _ := req(t, srv, "anna", "GET", "/api/docs/"+tid2+"/export?format=pdf", ""); c != 400 {
		t.Fatalf("unknown format must be 400: %d", c)
	}

	// Rechte: bob sieht Annas Datei nicht, nach Freigabe darf er importieren (neues Dokument gehört bob)
	if c, _ := req(t, srv, "bob", "POST", "/api/docs/import", `{"owner":"anna","file":"brief.txt"}`); c != 404 {
		t.Fatalf("private import must be 404: %d", c)
	}
	req(t, srv, "anna", "POST", "/api/filesshare/anna/brief.txt", `{"read":["bob"]}`)
	bid, _ := newDocFromFile(t, srv, "bob", "anna", "brief.txt")
	if c, _ := req(t, srv, "anna", "GET", "/api/docs/"+bid+"/export", ""); c != 404 {
		t.Fatalf("anna must not see bob's imported doc: %d", c)
	}
	// Kaputte / falsche Dateien
	req(t, srv, "anna", "POST", "/api/files?name=kaputt.xlsx", "das ist kein zip")
	if c, _ := req(t, srv, "anna", "POST", "/api/docs/import", `{"file":"kaputt.xlsx"}`); c != 400 {
		t.Fatalf("damaged xlsx must be 400: %d", c)
	}
	req(t, srv, "anna", "POST", "/api/files?name=bild.png", "png")
	if c, _ := req(t, srv, "anna", "POST", "/api/docs/import", `{"file":"bild.png"}`); c != 400 {
		t.Fatalf("unsupported type must be 400: %d", c)
	}
}

func TestGroups(t *testing.T) {
	srv, _ := setup(t) // anna = Admin, bob = normal (Gruppe users = alle Bereiche)
	defer srv.Close()
	pa, pb := "passwort-anna", "passwort-bob"

	if c := reqp(t, srv, "bob", pb, "POST", "/api/groups", `{"name":"x","areas":["files"]}`); c != 403 {
		t.Fatalf("bob create group: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/groups", `{"name":"nur-files","areas":["files"]}`); c != 200 {
		t.Fatalf("create group: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/groups", `{"name":"nur-files","areas":["files"]}`); c != 409 {
		t.Fatalf("dup group: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/groups", `{"name":"bad","areas":["mail"]}`); c != 400 {
		t.Fatalf("bad area: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users/bob/groups", `{"groups":["gibts-nicht"]}`); c != 404 {
		t.Fatalf("unknown group: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users/bob/groups", `{"groups":[]}`); c != 400 {
		t.Fatalf("no group: %d", c)
	}
	// bob nur noch in nur-files
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users/bob/groups", `{"groups":["nur-files"]}`); c != 200 {
		t.Fatalf("set groups: %d", c)
	}
	for _, p := range []string{"/api/cal", "/dav/bob/cal/"} {
		if c := reqp(t, srv, "bob", pb, "GET", p, ""); c != 403 {
			t.Fatalf("bob %s: %d", p, c)
		}
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/docs", `{"name":"t","type":"sheet"}`); c != 403 {
		t.Fatalf("bob sheet: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/docs", `{"name":"t","type":"text"}`); c != 403 {
		t.Fatalf("bob text: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "GET", "/api/files", ""); c != 200 {
		t.Fatalf("bob files: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "GET", "/api/me", ""); c != 200 {
		t.Fatalf("bob me: %d", c)
	}
	// Gruppe darf nicht gelöscht werden, wenn sie die einzige eines Benutzers ist
	if c := reqp(t, srv, "anna", pa, "DELETE", "/api/groups/nur-files", ""); c != 400 {
		t.Fatalf("delete sole group: %d", c)
	}
	// Rechte vereinigen: zusätzlich Gruppe users (alle Bereiche)
	if c := reqp(t, srv, "anna", pa, "POST", "/api/users/bob/groups", `{"groups":["nur-files","alluser"]}`); c != 200 {
		t.Fatalf("set groups 2: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "GET", "/api/cal", ""); c != 200 {
		t.Fatalf("bob cal after union: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/docs", `{"name":"t","type":"sheet"}`); c != 200 {
		t.Fatalf("bob sheet after union: %d", c)
	}
	// Bereiche einer Gruppe ändern -> wirkt sofort
	if c := reqp(t, srv, "anna", pa, "POST", "/api/groups/alluser", `{"areas":["cal"]}`); c != 200 {
		t.Fatalf("edit group: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/docs", `{"name":"t2","type":"sheet"}`); c != 403 {
		t.Fatalf("bob sheet after edit: %d", c)
	}
	// Standardgruppe ist nicht löschbar und hat keine eigenen Gruppen-Admins (= globale Admins)
	if c := reqp(t, srv, "anna", pa, "DELETE", "/api/groups/alluser", ""); c != 400 {
		t.Fatalf("delete default group: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/groups/alluser/admins", `{"admins":["bob"]}`); c != 400 {
		t.Fatalf("admins of default group: %d", c)
	}
	_, gl := req(t, srv, "anna", "GET", "/api/groups", "")
	if !strings.Contains(gl, `"name":"alluser","areas":["cal"],"read":null,"admins":["anna"]`) {
		t.Fatalf("default group admins: %s", gl)
	}
	// Gruppe löschen: Benutzer mit nur dieser Gruppe -> 400
	reqp(t, srv, "anna", pa, "POST", "/api/groups", `{"name":"tmp","areas":["cal"]}`)
	reqp(t, srv, "anna", pa, "POST", "/api/users/anna/groups", `{"groups":["tmp"]}`)
	if c := reqp(t, srv, "anna", pa, "DELETE", "/api/groups/tmp", ""); c != 400 {
		t.Fatalf("delete group of sole member: %d", c)
	}
	reqp(t, srv, "anna", pa, "POST", "/api/users/anna/groups", `{"groups":["nur-files","tmp"]}`)
	if c := reqp(t, srv, "anna", pa, "DELETE", "/api/groups/tmp", ""); c != 200 {
		t.Fatalf("delete group: %d", c)
	}
	// anna (Admin) war nur in users -> Admin bleibt trotzdem Superuser
	if c := reqp(t, srv, "anna", pa, "POST", "/api/docs", `{"name":"a","type":"text"}`); c != 200 {
		t.Fatalf("admin superuser: %d", c)
	}
}

func TestGroupAdminShareImport(t *testing.T) {
	srv, _ := setup(t) // anna = Admin, bob = normal
	defer srv.Close()
	pa, pb := "passwort-anna", "passwort-bob"

	// CSV-Import (Admin): Gruppen automatisch anlegen
	csv := "# Klasse 5a\nlisa;lisalisa1;klasse5a\nmax;maxmaxmax1;klasse5a,chor\nkurz;abc;klasse5a\nbad name;abcdefgh1;klasse5a\nlisa;lisalisa1;klasse5a\n"
	r, _ := http.NewRequest("POST", srv.URL+"/api/users/import?create=1", strings.NewReader(csv))
	r.SetBasicAuth("anna", pa)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Created int
		Errors  []string
	}
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()
	if res.Created != 2 || len(res.Errors) != 3 {
		t.Fatalf("import: %+v", res)
	}
	if c := reqp(t, srv, "lisa", "lisalisa1", "GET", "/api/me", ""); c != 200 {
		t.Fatalf("lisa login: %d", c)
	}
	// bob (normal) darf nicht importieren / Gruppen verwalten
	if c := reqp(t, srv, "bob", pb, "POST", "/api/users/import", "x;yyyyyyyy;klasse5a"); c != 403 {
		t.Fatalf("bob import: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/groups/klasse5a/members", `{"add":["bob"]}`); c != 403 {
		t.Fatalf("bob members: %d", c)
	}
	// bob wird Gruppen-Admin von klasse5a
	if c := reqp(t, srv, "anna", pa, "POST", "/api/groups/klasse5a/admins", `{"admins":["bob"]}`); c != 200 {
		t.Fatalf("set admins: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/groups/klasse5a/members", `{"add":["lisa"]}`); c != 200 {
		t.Fatalf("bob members: %d", c)
	}
	// S-01: Gruppen-Admin darf keine Konten hinzufügen, die ausserhalb seiner Gruppen stehen (sonst Passwort-Reset = Übernahme)
	if c := reqp(t, srv, "bob", pb, "POST", "/api/groups/klasse5a/members", `{"add":["anna"]}`); c != 403 {
		t.Fatalf("bob adds admin: %d", c)
	}
	if c := reqp(t, srv, "anna", pa, "POST", "/api/groups/klasse5a/members", `{"add":["bob"]}`); c != 200 {
		t.Fatalf("anna adds bob: %d", c)
	}
	// Gruppen-Admin: Passwort von Mitglied ja, von Fremden/Admin nein
	if c := reqp(t, srv, "bob", pb, "POST", "/api/users/lisa/password", `{"password":"neuneuneu1"}`); c != 200 {
		t.Fatalf("bob reset lisa: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/users/anna/password", `{"password":"neuneuneu1"}`); c != 403 {
		t.Fatalf("bob reset anna: %d", c)
	}
	// Gruppen-Admin legt Benutzer nur in eigener Gruppe an, kein Admin, kein Bereich fremder Gruppen
	if c := reqp(t, srv, "bob", pb, "POST", "/api/users", `{"name":"tom","password":"tomtomtom1","groups":["klasse5a"]}`); c != 200 {
		t.Fatalf("bob create tom: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/users", `{"name":"eve","password":"evevevev1","groups":["chor"]}`); c != 403 {
		t.Fatalf("bob create in foreign group: %d", c)
	}
	if c := reqp(t, srv, "bob", pb, "POST", "/api/users", `{"name":"eve","password":"evevevev1","groups":["klasse5a"],"admin":true}`); c != 403 {
		t.Fatalf("bob create admin: %d", c)
	}
	// Benutzerliste des Gruppen-Admins: nur Mitglieder von klasse5a
	_, body := req(t, srv, "bob", "GET", "/api/users", "")
	if !strings.Contains(body, `"lisa"`) || strings.Contains(body, `"anna"`) || strings.Contains(body, `"max"`) && false {
		t.Fatalf("bob user list: %s", body)
	}
	// Mitglied entfernen: letzte Gruppe schützen
	if c := reqp(t, srv, "bob", pb, "POST", "/api/groups/klasse5a/members", `{"remove":["lisa"]}`); c != 400 {
		t.Fatalf("remove last group: %d", c)
	}

	reqp(t, srv, "anna", pa, "POST", "/api/groups/klasse5a", `{"areas":["files","text"]}`)
	reqp(t, srv, "anna", pa, "POST", "/api/groups/chor", `{"areas":["files","text"]}`)
	// Freigabe über Gruppe: Dokument von anna an g:klasse5a -> lisa lesen, tom (auch klasse5a) ja, carl (nicht) nein
	_, b := req(t, srv, "anna", "POST", "/api/docs", `{"name":"plan","type":"text"}`)
	var d struct{ ID string }
	json.Unmarshal([]byte(b), &d)
	if c, _ := req(t, srv, "anna", "POST", "/api/docs/"+d.ID+"/share", `{"read":["g:klasse5a"]}`); c != 200 {
		t.Fatalf("share: %d", c)
	}
	get := func(user, pw, path string) string {
		r, _ := http.NewRequest("GET", srv.URL+path, nil)
		r.SetBasicAuth(user, pw)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if !strings.Contains(get("lisa", "neuneuneu1", "/api/docs"), d.ID) {
		t.Fatal("lisa should see group-shared doc")
	}
	if !strings.Contains(get("tom", "tomtomtom1", "/api/docs"), d.ID) {
		t.Fatal("tom should see group-shared doc")
	}
	if strings.Contains(get("bob", pb, "/api/docs"), `"plan"`) == false {
		t.Fatal("bob is in klasse5a and should see it")
	}
	// Gruppe entziehen -> Freigabe wirkt sofort nicht mehr
	reqp(t, srv, "anna", pa, "POST", "/api/users/tom/groups", `{"groups":["chor"]}`)
	if strings.Contains(get("tom", "tomtomtom1", "/api/docs"), d.ID) {
		t.Fatal("tom left klasse5a")
	}
	// CSV-Export
	_, ex := req(t, srv, "anna", "GET", "/api/users/export", "")
	if !strings.HasPrefix(ex, "\ufeff# name;password;groups") || !strings.Contains(ex, "lisa;;klasse5a") {
		t.Fatalf("users export: %q", ex)
	}
	_, ex = req(t, srv, "anna", "GET", "/api/groups/export", "")
	if !strings.Contains(ex, "klasse5a;files,text;;bob;") {
		t.Fatalf("groups export: %q", ex)
	}
	if c := reqp(t, srv, "lisa", "neuneuneu1", "GET", "/api/users/export", ""); c != 403 {
		t.Fatalf("lisa export: %d", c)
	}
}

func TestGroupFolders(t *testing.T) {
	srv, _ := setup(t) // anna = Admin
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string) (int, string) {
		r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		r.SetBasicAuth(user, pw)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, _ := do("anna", pa, "POST", "/api/groups", `{"name":"kl","areas":["files"],"folder":"xx"}`); c != 400 {
		t.Fatalf("bad folder mode: %d", c)
	}
	if c, _ := do("anna", pa, "POST", "/api/groups", `{"name":"kl","areas":["files"],"folder":"ro"}`); c != 200 {
		t.Fatalf("create group: %d", c)
	}
	do("anna", pa, "POST", "/api/groups", `{"name":"other","areas":["files"]}`)
	do("anna", pa, "POST", "/api/users/import", "teach;teachteach1;kl\nlisa;lisalisa1;kl\nfred;fredfred1;other\n")
	do("anna", pa, "POST", "/api/groups/kl/admins", `{"admins":["teach"]}`)

	// ro: Mitglied darf nicht schreiben, Gruppen-Admin schon
	if c, _ := do("lisa", "lisalisa1", "POST", "/api/files?name=a.txt&owner=@kl", "x"); c != 403 {
		t.Fatalf("lisa upload ro: %d", c)
	}
	if c, _ := do("teach", "teachteach1", "POST", "/api/files?name=a.txt&owner=@kl", "hallo"); c != 200 {
		t.Fatalf("teach upload: %d", c)
	}
	if c, _ := do("teach", "teachteach1", "POST", "/api/files?name=b.txt&owner=lisa", "x"); c != 400 {
		t.Fatalf("teach upload into foreign home: %d", c)
	}
	_, l := do("lisa", "lisalisa1", "GET", "/api/files", "")
	if !strings.Contains(l, `"owner":"@kl"`) || !strings.Contains(l, `"folders":[{"name":"kl","write":false}]`) {
		t.Fatalf("lisa list: %s", l)
	}
	_, l = do("fred", "fredfred1", "GET", "/api/files", "")
	if strings.Contains(l, "@kl") || !strings.Contains(l, `"folders":[]`) {
		t.Fatalf("fred list: %s", l)
	}
	if c, b := do("lisa", "lisalisa1", "GET", "/api/files/@kl/a.txt", ""); c != 200 || b != "hallo" {
		t.Fatalf("lisa read: %d %q", c, b)
	}
	if c, _ := do("fred", "fredfred1", "GET", "/api/files/@kl/a.txt", ""); c != 404 {
		t.Fatalf("fred read: %d", c)
	}
	if c, _ := do("lisa", "lisalisa1", "DELETE", "/api/files/@kl/a.txt", ""); c != 403 {
		t.Fatalf("lisa delete ro: %d", c)
	}
	// WebDAV
	if c, _ := do("lisa", "lisalisa1", "PROPFIND", "/webdav/groups/kl/", ""); c != 207 {
		t.Fatalf("dav propfind: %d", c)
	}
	if c, _ := do("fred", "fredfred1", "PROPFIND", "/webdav/groups/kl/", ""); c != 404 {
		t.Fatalf("dav fred: %d", c)
	}
	if c, b := do("lisa", "lisalisa1", "GET", "/webdav/groups/kl/a.txt", ""); c != 200 || b != "hallo" {
		t.Fatalf("dav read: %d %q", c, b)
	}
	if c, _ := do("lisa", "lisalisa1", "PUT", "/webdav/groups/kl/w.txt", "w"); c != 403 {
		t.Fatalf("dav put ro: %d", c)
	}
	// rw: Mitglieder schreiben und löschen
	do("anna", pa, "POST", "/api/groups/kl", `{"folder":"rw"}`)
	if c, _ := do("lisa", "lisalisa1", "PUT", "/webdav/groups/kl/w.txt", "w"); c != 201 && c != 204 {
		t.Fatalf("dav put rw: %d", c)
	}
	if c, _ := do("lisa", "lisalisa1", "POST", "/api/files?name=c.txt&owner=@kl", "c"); c != 200 {
		t.Fatalf("lisa upload rw: %d", c)
	}
	if c, _ := do("lisa", "lisalisa1", "DELETE", "/api/files/@kl/a.txt", ""); c != 200 {
		t.Fatalf("lisa delete rw: %d", c)
	}
	// Ordner abschalten
	do("anna", pa, "POST", "/api/groups/kl", `{"folder":""}`)
	if c, _ := do("lisa", "lisalisa1", "GET", "/api/files/@kl/c.txt", ""); c != 404 {
		t.Fatalf("folder off: %d", c)
	}
	// Gruppe ohne Files-Bereich: kein Zugriff
	do("anna", pa, "POST", "/api/groups/kl", `{"folder":"rw","areas":["text"]}`)
	if c, _ := do("lisa", "lisalisa1", "GET", "/api/files/@kl/c.txt", ""); c != 403 {
		t.Fatalf("no files area: %d", c)
	}
}

func TestCalendarScopes(t *testing.T) {
	srv, _ := setup(t) // anna = Admin, bob = normal (Gruppe users)
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string) (int, string) {
		r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		r.SetBasicAuth(user, pw)
		if method == "PUT" {
			r.Header.Set("Content-Type", "text/calendar")
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	do("anna", pa, "POST", "/api/groups", `{"name":"kl","areas":["cal"]}`)
	do("anna", pa, "POST", "/api/groups", `{"name":"other","areas":["cal"]}`)
	do("anna", pa, "POST", "/api/users/import", "teach;teachteach1;kl\nlisa;lisalisa1;kl\nfred;fredfred1;other\n")
	do("anna", pa, "POST", "/api/groups/kl/admins", `{"admins":["teach"]}`)

	// Anlegen: global nur Admin, Gruppenkalender nur Gruppen-Admin
	if c, _ := do("teach", "teachteach1", "POST", "/api/cal", `{"name":"Feiertage","scope":"global"}`); c != 403 {
		t.Fatalf("teach global: %d", c)
	}
	if c, _ := do("anna", pa, "POST", "/api/cal", `{"name":"Feiertage","scope":"global"}`); c != 200 {
		t.Fatalf("admin global: %d", c)
	}
	if c, _ := do("lisa", "lisalisa1", "POST", "/api/cal", `{"name":"Klasse","scope":"group","group":"kl"}`); c != 403 {
		t.Fatalf("lisa group cal: %d", c)
	}
	if c, _ := do("teach", "teachteach1", "POST", "/api/cal", `{"name":"Klasse","scope":"group","group":"kl","mode":"ro"}`); c != 200 {
		t.Fatalf("teach group cal: %d", c)
	}
	if c, _ := do("teach", "teachteach1", "POST", "/api/cal", `{"name":"Fremd","scope":"group","group":"other"}`); c != 403 {
		t.Fatalf("teach foreign group: %d", c)
	}
	do("lisa", "lisalisa1", "POST", "/api/cal", `{"name":"Privat"}`)

	list := func(user, pw string) string { _, b := do(user, pw, "GET", "/api/cal", ""); return b }
	ll := list("lisa", "lisalisa1")
	for _, want := range []string{`"id":"_global~feiertage"`, `"id":"@kl~klasse"`, `"id":"privat"`, `"id":"default"`} {
		if !strings.Contains(ll, want) {
			t.Fatalf("lisa list misses %s: %s", want, ll)
		}
	}
	fl := list("fred", "fredfred1")
	if strings.Contains(fl, "@kl~") || !strings.Contains(fl, "_global~feiertage") {
		t.Fatalf("fred list: %s", fl)
	}

	ev := `{"summary":"Termin","start":"2026-11-01T10:00:00Z"}`
	// global: nur Admin schreibt; alle lesen
	if c, _ := do("lisa", "lisalisa1", "POST", "/api/cal/_global~feiertage/events", ev); c != 403 {
		t.Fatalf("lisa write global: %d", c)
	}
	if c, _ := do("anna", pa, "POST", "/api/cal/_global~feiertage/events", ev); c != 200 {
		t.Fatalf("admin write global: %d", c)
	}
	if c, b := do("fred", "fredfred1", "GET", "/api/cal/_global~feiertage/events", ""); c != 200 || !strings.Contains(b, "Termin") {
		t.Fatalf("fred read global: %d %s", c, b)
	}
	// Gruppe ro: Mitglied liest, schreibt nicht; Gruppen-Admin schreibt; Fremde sehen nichts
	if c, _ := do("lisa", "lisalisa1", "POST", "/api/cal/@kl~klasse/events", ev); c != 403 {
		t.Fatalf("lisa write ro group: %d", c)
	}
	if c, _ := do("teach", "teachteach1", "POST", "/api/cal/@kl~klasse/events", ev); c != 200 {
		t.Fatalf("teach write group: %d", c)
	}
	if c, b := do("lisa", "lisalisa1", "GET", "/api/cal/@kl~klasse/events", ""); c != 200 || !strings.Contains(b, "Termin") {
		t.Fatalf("lisa read group: %d %s", c, b)
	}
	if c, _ := do("fred", "fredfred1", "GET", "/api/cal/@kl~klasse/events", ""); c != 404 {
		t.Fatalf("fred read foreign group: %d", c)
	}
	// persönlicher Kalender: niemand sonst
	if c, _ := do("teach", "teachteach1", "GET", "/api/cal/lisa~privat/events", ""); c != 404 {
		t.Fatalf("foreign user calendar: %d", c)
	}
	// CalDAV: globale und Gruppenkalender erscheinen in der Kalenderliste (Thunderbird); ro wird durchgesetzt
	c, b := do("lisa", "lisalisa1", "PROPFIND", "/dav/lisa/cal/", "")
	if c != 207 || !strings.Contains(b, "@kl~klasse") || !strings.Contains(b, "_global~feiertage") {
		t.Fatalf("caldav list: %d %s", c, b)
	}
	if c, b := do("lisa", "lisalisa1", "PUT", "/dav/lisa/cal/@kl~klasse/x.ics", ics); c != 403 {
		t.Fatalf("caldav put ro group: %d %s", c, b)
	}
	if c, _ := do("teach", "teachteach1", "PUT", "/dav/teach/cal/@kl~klasse/x.ics", ics); c != 201 && c != 204 {
		t.Fatalf("caldav put group admin: %d", c)
	}
	// Löschen: Gruppen-Admin ja, Mitglied nein; globaler Kalender nur Admin
	if c, _ := do("lisa", "lisalisa1", "DELETE", "/api/cal/@kl~klasse", ""); c != 403 {
		t.Fatalf("lisa delete group cal: %d", c)
	}
	if c, _ := do("teach", "teachteach1", "DELETE", "/api/cal/@kl~klasse", ""); c != 200 {
		t.Fatalf("teach delete group cal: %d", c)
	}
	if c, _ := do("lisa", "lisalisa1", "DELETE", "/api/cal/_global~feiertage", ""); c != 403 {
		t.Fatalf("lisa delete global: %d", c)
	}
}

func TestAllDayEvent(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, _ := req(t, srv, "anna", "POST", "/api/cal/default/events", `{"summary":"Feiertag","start":"2026-12-25T00:00:00Z","allDay":true}`); c != 200 {
		t.Fatalf("add: %d", c)
	}
	_, b := req(t, srv, "anna", "GET", "/api/cal/default/events", "")
	if !strings.Contains(b, `"allDay":true`) || !strings.Contains(b, `"start":"2026-12-25T00:00:00Z"`) || !strings.Contains(b, `"end":"2026-12-26T00:00:00Z"`) {
		t.Fatalf("allday: %s", b)
	}
}

func TestMustChangeAndUpdateImport(t *testing.T) {
	srv, _ := setup(t) // anna = Admin
	defer srv.Close()
	auth.ForceChange = true
	defer func() { auth.ForceChange = false }()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string) (int, string) {
		r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		r.SetBasicAuth(user, pw)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	do("anna", pa, "POST", "/api/groups", `{"name":"k5a","areas":["files"]}`)
	do("anna", pa, "POST", "/api/groups", `{"name":"k6a","areas":["files"]}`)
	_, b := do("anna", pa, "POST", "/api/users/import", "lisa;startpw123;k5a\ntom;startpw456;k5a\n")
	if !strings.Contains(b, `"created":2`) {
		t.Fatal(b)
	}
	if c, _ := do("lisa", "startpw123", "GET", "/api/files", ""); c != 403 {
		t.Fatalf("must change: %d", c)
	}
	if c, b := do("lisa", "startpw123", "GET", "/api/me", ""); c != 200 || !strings.Contains(b, `"must":true`) {
		t.Fatalf("me: %d %s", c, b)
	}
	if c, _ := do("lisa", "startpw123", "POST", "/api/me/password", `{"old":"startpw123","new":"startpw123"}`); c != 400 {
		t.Fatalf("same pw: %d", c)
	}
	if c, _ := do("lisa", "startpw123", "POST", "/api/me/password", `{"old":"startpw123","new":"eigenes-pw-1"}`); c != 200 {
		t.Fatalf("change: %d", c)
	}
	if c, _ := do("lisa", "eigenes-pw-1", "GET", "/api/files", ""); c != 200 {
		t.Fatalf("after change: %d", c)
	}
	// Klassenwechsel: Import mit update=1, leeres Passwort = unverändert
	_, b = do("anna", pa, "POST", "/api/users/import?update=1", "lisa;;k6a\ntom;neuespw-789;k6a\nnew1;;k6a\n")
	if !strings.Contains(b, `"updated":2`) || !strings.Contains(b, "password required") {
		t.Fatal(b)
	}
	if c, _ := do("lisa", "eigenes-pw-1", "GET", "/api/files", ""); c != 200 {
		t.Fatalf("lisa pw unchanged: %d", c)
	}
	if c, _ := do("tom", "neuespw-789", "GET", "/api/files", ""); c != 403 {
		t.Fatalf("tom must change again: %d", c)
	}
	// Startpasswort-Dialog ohne "altes Passwort": nur neues Passwort, muss sich vom Startpasswort unterscheiden
	if c, _ := do("tom", "neuespw-789", "POST", "/api/me/password", `{"new":"neuespw-789"}`); c != 400 {
		t.Fatalf("same start pw: %d", c)
	}
	if c, _ := do("tom", "neuespw-789", "POST", "/api/me/password", `{"new":"tom-eigen-42"}`); c != 200 {
		t.Fatalf("new pw without old: %d", c)
	}
	if c, _ := do("tom", "tom-eigen-42", "GET", "/api/files", ""); c != 200 {
		t.Fatalf("tom after change: %d", c)
	}
	if c, _ := do("lisa", "eigenes-pw-1", "POST", "/api/me/password", `{"new":"anderes-pw-77"}`); c == 200 {
		t.Fatal("ohne altes Passwort nur bei Startpasswort")
	}
	_, b = do("anna", pa, "GET", "/api/users", "")
	if !strings.Contains(b, `"name":"lisa"`) || strings.Contains(b, `"k5a"`) {
		t.Fatalf("groups after move: %s", b)
	}
	if c, _ := do("anna", pa, "POST", "/api/users/import", "lisa;;k5a"); c != 200 { // ohne update: exists
		t.Fatal(c)
	}
}

func TestReadOnlyAreas(t *testing.T) {
	srv, _ := setup(t) // anna = Admin
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string) (int, string) {
		r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		r.SetBasicAuth(user, pw)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	// Lehrer: alles ändern; Schüler: Texte/Kalender lesen, Files ändern
	do("anna", pa, "POST", "/api/groups", `{"name":"lehrer","areas":["cal","text","files"]}`)
	do("anna", pa, "POST", "/api/groups", `{"name":"schueler","areas":["files"],"read":["cal","text","calc"]}`)
	do("anna", pa, "POST", "/api/users/import", "t1;lehrerpw1;lehrer\ns1;schuelerpw1;schueler\n")
	if c, b := do("anna", pa, "GET", "/api/groups", ""); c != 200 || !strings.Contains(b, `"read":["cal","calc","text"]`) {
		t.Fatalf("groups: %s", b)
	}
	_, b := do("t1", "lehrerpw1", "POST", "/api/docs", `{"name":"Plan","type":"text"}`)
	i := strings.Index(b, `"id":"`)
	if i < 0 {
		t.Fatal(b)
	}
	id := b[i+6 : i+6+strings.Index(b[i+6:], `"`)]
	do("t1", "lehrerpw1", "POST", "/api/docs/"+id+"/share", `{"read":[],"write":["g:schueler"]}`)
	_, l := do("s1", "schuelerpw1", "GET", "/api/docs", "")
	if !strings.Contains(l, id) || !strings.Contains(l, `"rw":false`) {
		t.Fatalf("s1 sees doc read-only: %s", l)
	}
	if c, _ := do("s1", "schuelerpw1", "POST", "/api/docs", `{"name":"X","type":"text"}`); c != 403 {
		t.Fatalf("s1 create text: %d", c)
	}
	if c, _ := do("s1", "schuelerpw1", "POST", "/api/cal", `{"name":"Mein"}`); c != 403 {
		t.Fatalf("s1 create cal: %d", c)
	}
	if c, _ := do("s1", "schuelerpw1", "POST", "/api/files?name=a.txt", "x"); c != 200 {
		t.Fatalf("s1 upload: %d", c)
	}
	if c, _ := do("t1", "lehrerpw1", "POST", "/api/cal", `{"name":"Mein"}`); c != 200 {
		t.Fatalf("t1 create cal: %d", c)
	}
}

func TestGroupTemplates(t *testing.T) {
	srv, _ := setup(t) // anna = Admin
	defer srv.Close()
	pa := "passwort-anna"
	do := func(user, pw, method, path, body string) (int, string) {
		r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		r.SetBasicAuth(user, pw)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, _ := do("anna", pa, "POST", "/api/groups", `{"name":"x","template":"gibts-nicht"}`); c != 400 {
		t.Fatalf("bad template: %d", c)
	}
	if c, _ := do("anna", pa, "POST", "/api/groups", `{"name":"k7a","template":"klasse"}`); c != 200 {
		t.Fatalf("klasse: %d", c)
	}
	_, g := do("anna", pa, "GET", "/api/groups", "")
	if !strings.Contains(g, `"name":"k7a","areas":["files"],"read":["cal","calc","text"]`) || !strings.Contains(g, `"folder":"ro"`) {
		t.Fatalf("groups: %s", g)
	}
	// Import legt Gruppe 8b mit Vorlage team an, inkl. Gruppenkalender
	do("anna", pa, "POST", "/api/users/import?create=1&template=team", "u1;u1u1u1u1;8b\n")
	_, c := do("u1", "u1u1u1u1", "GET", "/api/cal", "")
	if !strings.Contains(c, `"group":"8b"`) || !strings.Contains(c, `"mode":"rw"`) {
		t.Fatalf("group calendar 8b: %s", c)
	}
	do("anna", pa, "POST", "/api/users/import", "s7;s7s7s7s7;k7a\n")
	_, c = do("s7", "s7s7s7s7", "GET", "/api/cal", "")
	if !strings.Contains(c, `"group":"k7a"`) || !strings.Contains(c, `"write":false`) {
		t.Fatalf("group calendar k7a: %s", c)
	}
}

func TestCalendarSubscription(t *testing.T) {
	feed := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:h1\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;VALUE=DATE:20261224\r\nSUMMARY:Heiligabend\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:h2\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;VALUE=DATE:20261225\r\nSUMMARY:Weihnachten\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cur := &feed
	fs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" || *cur == "DOWN" {
			http.Error(w, "x", 500)
			return
		}
		io.WriteString(w, *cur)
	}))
	defer fs.Close()
	srv, _ := setup(t)
	defer srv.Close()
	// ohne Freigabe für private Adressen: abgelehnt
	if c, _ := req(t, srv, "bob", "POST", "/api/cal", `{"name":"Feiertage","url":"`+fs.URL+`/f.ics"}`); c != 502 {
		t.Fatalf("private blocked: %d", c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/cal", `{"name":"X","url":"ftp://x/y"}`); c != 400 {
		t.Fatalf("bad scheme: %d", c)
	}
	t.Setenv("CS_ICS_PRIVATE", "1")
	if c, _ := req(t, srv, "bob", "POST", "/api/cal", `{"name":"Kaputt","url":"`+fs.URL+`/bad"}`); c != 502 {
		t.Fatalf("bad feed: %d", c)
	}
	c, b := req(t, srv, "bob", "POST", "/api/cal", `{"name":"Feiertage","url":"`+fs.URL+`/f.ics"}`)
	if c != 200 {
		t.Fatalf("subscribe: %d %s", c, b)
	}
	_, ev := req(t, srv, "bob", "GET", "/api/cal/feiertage/events", "")
	if !strings.Contains(ev, "Heiligabend") || !strings.Contains(ev, "Weihnachten") {
		t.Fatalf("events: %s", ev)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/cal/feiertage/events", `{"summary":"x","start":"2026-12-01T10:00:00Z"}`); c != 403 {
		t.Fatalf("subscription is read-only: %d", c)
	}
	_, l := req(t, srv, "bob", "GET", "/api/cal", "")
	if !strings.Contains(l, `"sub":true`) || !strings.Contains(l, `"write":false`) {
		t.Fatalf("list: %s", l)
	}
	// Feed ändert sich: nur noch ein Termin
	f2 := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VEVENT\r\nUID:h2\r\nDTSTAMP:20260101T000000Z\r\nDTSTART;VALUE=DATE:20261225\r\nSUMMARY:Weihnachten\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cur = &f2
	if c, _ := req(t, srv, "bob", "POST", "/api/cal/feiertage/refresh", ""); c != 200 {
		t.Fatalf("refresh: %d", c)
	}
	_, ev = req(t, srv, "bob", "GET", "/api/cal/feiertage/events", "")
	if strings.Contains(ev, "Heiligabend") || !strings.Contains(ev, "Weihnachten") {
		t.Fatalf("after refresh: %s", ev)
	}
	// Status: letzter Abruf sichtbar, ohne Fehler
	_, l = req(t, srv, "bob", "GET", "/api/cal", "")
	if !strings.Contains(l, `"fetched":`) || strings.Contains(l, "suberr") {
		t.Fatalf("status: %s", l)
	}
	// Feed fällt aus: Fehler wird gemerkt, alte Termine bleiben, nächster Erfolg löscht den Fehler
	down := "DOWN"
	cur = &down
	if c, b := req(t, srv, "bob", "POST", "/api/cal/feiertage/refresh", ""); c != 502 || !strings.Contains(b, "HTTP 500") {
		t.Fatalf("refresh error: %d %s", c, b)
	}
	_, l = req(t, srv, "bob", "GET", "/api/cal", "")
	if !strings.Contains(l, `"suberr":"feed: HTTP 500"`) || !strings.Contains(l, `"fetched":`) {
		t.Fatalf("error status: %s", l)
	}
	if _, ev = req(t, srv, "bob", "GET", "/api/cal/feiertage/events", ""); !strings.Contains(ev, "Weihnachten") {
		t.Fatalf("old events must stay: %s", ev)
	}
	cur = &f2
	if c, _ := req(t, srv, "bob", "POST", "/api/cal/feiertage/refresh", ""); c != 200 {
		t.Fatal(c)
	}
	if _, l = req(t, srv, "bob", "GET", "/api/cal", ""); strings.Contains(l, "suberr") {
		t.Fatalf("error not cleared: %s", l)
	}
}

func TestResourceCalendar(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, _ := req(t, srv, "anna", "POST", "/api/cal", `{"name":"Raum 101","resource":true,"scope":"global","mode":"rw"}`); c != 200 {
		t.Fatalf("create: %d", c)
	}
	add := func(user, s, e string) int {
		c, _ := req(t, srv, user, "POST", "/api/cal/_global~raum-101/events", `{"summary":"Unterricht","start":"`+s+`","end":"`+e+`"}`)
		return c
	}
	if c := add("bob", "2026-10-05T08:00:00Z", "2026-10-05T09:00:00Z"); c != 200 {
		t.Fatalf("first: %d", c)
	}
	if c := add("anna", "2026-10-05T08:30:00Z", "2026-10-05T09:30:00Z"); c != 409 {
		t.Fatalf("overlap: %d", c)
	}
	if c := add("anna", "2026-10-05T09:00:00Z", "2026-10-05T10:00:00Z"); c != 200 { // Ende exklusiv
		t.Fatalf("adjacent: %d", c)
	}
	// CalDAV-PUT wird genauso geprüft
	r, _ := http.NewRequest("PUT", srv.URL+"/dav/anna/cal/_global~raum-101/x.ics", strings.NewReader(strings.Replace(strings.Replace(ics, "20260930T120000Z", "20261005T083000Z", 1), "20260930T130000Z", "20261005T093000Z", 1)))
	r.SetBasicAuth("anna", "passwort-anna")
	r.Header.Set("Content-Type", "text/calendar")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 409 {
		t.Fatalf("caldav overlap: %d", resp.StatusCode)
	}
}

func TestSubfolders(t *testing.T) {
	srv, _ := setup(t) // anna = Admin, bob
	defer srv.Close()
	do := func(user, method, path, body string, hdr ...string) (int, string) {
		return req(t, srv, user, method, path, body, hdr...)
	}
	// Upload in Unterordner, Ordner virtuell
	if c, b := do("anna", "POST", "/api/files?name=Mathe%2FAufgaben%2Fa.txt", "eins"); c != 200 {
		t.Fatalf("upload: %d %s", c, b)
	}
	if c, b := do("anna", "GET", "/api/files/anna/Mathe/Aufgaben/a.txt", ""); c != 200 || b != "eins" {
		t.Fatalf("download: %d %q", c, b)
	}
	// Datei und Ordner mit gleichem Namen verboten
	if c, _ := do("anna", "POST", "/api/files?name=Mathe", "x"); c != 400 {
		t.Fatalf("file over folder: %d", c)
	}
	do("anna", "POST", "/api/files?name=Datei", "x")
	if c, _ := do("anna", "POST", "/api/files?name=Datei%2Fb.txt", "x"); c != 400 {
		t.Fatalf("folder under file: %d", c)
	}
	// leerer Ordner + WebDAV sieht die Struktur
	if c, _ := do("anna", "POST", "/api/filesdir?name=Leer", ""); c != 200 {
		t.Fatalf("mkdir: %d", c)
	}
	_, pf := do("anna", "PROPFIND", "/webdav/Mathe/", "", "Depth", "1")
	if !strings.Contains(pf, "/webdav/Mathe/Aufgaben") {
		t.Fatalf("propfind Mathe: %s", pf)
	}
	_, pf = do("anna", "PROPFIND", "/webdav/", "", "Depth", "infinity")
	if !strings.Contains(pf, "/webdav/Leer") || strings.Contains(pf, ".folder") {
		t.Fatalf("propfind root: %s", pf)
	}
	if c, b := do("anna", "GET", "/webdav/Mathe/Aufgaben/a.txt", ""); c != 200 || b != "eins" {
		t.Fatalf("dav get: %d %q", c, b)
	}
	// WebDAV MKCOL + PUT in Ordner
	if c, _ := do("anna", "MKCOL", "/webdav/Neu", ""); c != 201 {
		t.Fatalf("mkcol: %d", c)
	}
	if c, _ := do("anna", "PUT", "/webdav/Neu/x.txt", "dav"); c != 201 {
		t.Fatalf("dav put: %d", c)
	}
	// Ordner verschieben (Web-API) und per WebDAV MOVE
	if c, b := do("anna", "POST", "/api/filesmove?from=Mathe&to=Schule%2FMathe&dir=1", ""); c != 200 {
		t.Fatalf("move dir: %d %s", c, b)
	}
	if c, _ := do("anna", "GET", "/api/files/anna/Schule/Mathe/Aufgaben/a.txt", ""); c != 200 {
		t.Fatalf("moved file: %d", c)
	}
	if c, _ := do("anna", "GET", "/api/files/anna/Mathe/Aufgaben/a.txt", ""); c != 404 {
		t.Fatalf("old location: %d", c)
	}
	if c, _ := do("anna", "MOVE", "/webdav/Neu", "", "Destination", srv.URL+"/webdav/Schule/Neu"); c != 201 && c != 204 {
		t.Fatalf("dav move dir: %d", c)
	}
	if c, b := do("anna", "GET", "/webdav/Schule/Neu/x.txt", ""); c != 200 || b != "dav" {
		t.Fatalf("after dav move: %d %q", c, b)
	}
	// Teilen einer Datei im Unterordner
	do("anna", "POST", "/api/filesshare/anna/Schule/Mathe/Aufgaben/a.txt", `{"read":["bob"]}`)
	if c, b := do("bob", "GET", "/webdav/shared/anna/Schule/Mathe/Aufgaben/a.txt", ""); c != 200 || b != "eins" {
		t.Fatalf("bob shared subfolder: %d %q", c, b)
	}
	_, pf = do("bob", "PROPFIND", "/webdav/shared/anna/", "", "Depth", "1")
	if !strings.Contains(pf, "/webdav/shared/anna/Schule") || strings.Contains(pf, "Neu") {
		t.Fatalf("bob sees only shared part: %s", pf)
	}
	// Ordner löschen
	if c, _ := do("bob", "DELETE", "/api/files/anna/Schule?dir=1", ""); c != 403 {
		t.Fatalf("bob delete dir: %d", c)
	}
	if c, _ := do("anna", "DELETE", "/api/files/anna/Schule?dir=1", ""); c != 200 {
		t.Fatalf("delete dir: %d", c)
	}
	if c, _ := do("anna", "GET", "/api/files/anna/Schule/Mathe/Aufgaben/a.txt", ""); c != 404 {
		t.Fatalf("deleted: %d", c)
	}
	// Gruppenordner mit Unterordner
	do("anna", "POST", "/api/groups", `{"name":"kl","areas":["files"],"folder":"rw"}`)
	do("anna", "POST", "/api/users/import", "lisa;lisalisa1;kl\n")
	r, _ := http.NewRequest("POST", srv.URL+"/api/files?name=Stoff%2Fm.txt&owner=@kl", strings.NewReader("gruppe"))
	r.SetBasicAuth("lisa", "lisalisa1")
	resp, _ := http.DefaultClient.Do(r)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("group upload sub: %d", resp.StatusCode)
	}
	_, pf = do("anna", "PROPFIND", "/webdav/groups/kl/", "", "Depth", "1")
	if !strings.Contains(pf, "/webdav/groups/kl/Stoff") {
		t.Fatalf("group propfind: %s", pf)
	}
}

func TestLang(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("CS_LANGDIR", dir)
	t.Setenv("CS_LANG", "en")
	os.WriteFile(filepath.Join(dir, "xx.json"), []byte(`{"_name":"Testish","Speichern":"SAVE"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "en.json"), []byte(`{"Speichern":"Store"}`), 0o644) // überschreibt eingebaut
	code, b := req(t, srv, "anna", "GET", "/lang/index.json", "")
	if code != 200 || !strings.Contains(b, `"default":"en"`) || !strings.Contains(b, `"code":"xx","name":"Testish"`) || !strings.Contains(b, `"code":"tr"`) || strings.Contains(b, "_template") {
		t.Fatal(code, b)
	}
	code, b = req(t, srv, "anna", "GET", "/lang/en.json", "")
	if code != 200 || !strings.Contains(b, `"Speichern":"Store"`) || !strings.Contains(b, `"Löschen":"Delete"`) {
		t.Fatal(code, b)
	}
	if c, _ := req(t, srv, "anna", "GET", "/lang/..%2f..%2fmain.json", ""); c == 200 {
		t.Fatal("traversal")
	}
	if c, _ := req(t, srv, "anna", "GET", "/lang/nope.json", ""); c != 404 {
		t.Fatal(c)
	}
	// Benutzersprache speichern
	if c, _ := req(t, srv, "anna", "POST", "/api/me/lang", `{"lang":"tr"}`); c != 200 {
		t.Fatal(c)
	}
	_, b = req(t, srv, "anna", "GET", "/api/me", "")
	if !strings.Contains(b, `"lang":"tr"`) {
		t.Fatal(b)
	}
}

func TestGroupCreateWithAdminsAndCal(t *testing.T) {
	srv, _ := setup(t) // anna = Admin, bob = normaler Benutzer
	defer srv.Close()
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k5","areas":["files"],"read":["cal"],"folder":"ro","cal":"ro","admins":["bob"]}`); c != 200 {
		t.Fatal(c, b)
	}
	_, b := req(t, srv, "anna", "GET", "/api/groups", "")
	if !strings.Contains(b, `"admins":["bob"]`) || !strings.Contains(b, `"members":["bob"]`) || !strings.Contains(b, `"folder":"ro"`) {
		t.Fatal(b)
	}
	_, b = req(t, srv, "bob", "GET", "/api/cal", "")
	if !strings.Contains(b, `"group":"k5"`) {
		t.Fatal("Gruppenkalender fehlt", b)
	}
	// unbekannter Gruppen-Admin legt nichts an
	if c, _ := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k6","areas":["files"],"admins":["nobody"]}`); c == 200 {
		t.Fatal("unknown admin accepted")
	}
	if _, b = req(t, srv, "anna", "GET", "/api/groups", ""); strings.Contains(b, `"k6"`) {
		t.Fatal("halb angelegt", b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k7","areas":["files"],"cal":"xx"}`); c != 400 {
		t.Fatal(c)
	}
}

func TestMigrateDefaultGroup(t *testing.T) {
	st := store.NewMem()
	st.Put(context.Background(), "users/groups.json", []byte(`{"users":{"areas":["cal","files"]},"klasse":{"areas":["files"]}}`), "")
	st.Put(context.Background(), "users/users.json", []byte(`{"lisa":{"hash":"x","groups":["users","klasse"]},"max":{"hash":"x"}}`), "")
	a := auth.New(st)
	if err := a.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	g, _, _ := st.Get(context.Background(), "users/groups.json")
	u, _, _ := st.Get(context.Background(), "users/users.json")
	if strings.Contains(string(g), `"users"`) || !strings.Contains(string(g), `"alluser":{"areas":["cal","files"]`) || !strings.Contains(string(u), `"groups":["alluser","klasse"]`) {
		t.Fatalf("migrate: %s | %s", g, u)
	}
	// ohne Standardgruppe wird sie angelegt
	st2 := store.NewMem()
	st2.Put(context.Background(), "users/groups.json", []byte(`{"klasse":{"areas":["files"]}}`), "")
	if err := auth.New(st2).Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g, _, _ := st2.Get(context.Background(), "users/groups.json"); !strings.Contains(string(g), `"alluser"`) {
		t.Fatalf("default group missing: %s", g)
	}
}

func TestLogoutPage(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	r, _ := http.NewRequest("GET", srv.URL+"/logout", nil) // ohne Zugangsdaten erreichbar
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatal(resp.StatusCode, resp.Header)
	}
}

func TestSoftLockAndFormat(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	_, b := req(t, srv, "anna", "POST", "/api/docs", `{"name":"t","type":"text"}`)
	var r struct{ ID string }
	json.Unmarshal([]byte(b), &r)
	req(t, srv, "anna", "POST", "/api/docs/"+r.ID+"/share", `{"read":[],"write":["bob"]}`)
	ca, cb := dial(t, srv, "anna", r.ID), dial(t, srv, "bob", r.ID)
	defer ca.CloseNow()
	defer cb.CloseNow()
	read(t, ca)
	read(t, cb)
	ctx := context.Background()
	w := func(c *websocket.Conn, s string) { c.Write(ctx, websocket.MessageText, []byte(s)) }
	w(ca, `{"t":"set","k":"p1","v":"hallo","pos":1,"f":"b s18 c#ff0000 g#ffff00 n2 l"}`)
	ma := read(t, ca)
	read(t, cb)
	if it := ma["item"].(map[string]any); it["f"] != "b s18 c#ff0000 g#ffff00 n2 l" {
		t.Fatalf("format: %v", ma)
	}
	// ungültige Formatierung (Injektion) wird verworfen
	w(ca, `{"t":"set","k":"p1","v":"x","pos":1,"f":"b;background:url(x)"}`)
	w(ca, `{"t":"lock","k":"p1"}`)
	if m := read(t, ca); m["t"] != "lock" || m["by"] != "anna" {
		t.Fatalf("lock broadcast a: %v", m)
	}
	if m := read(t, cb); m["t"] != "lock" || m["by"] != "anna" {
		t.Fatalf("lock broadcast b: %v", m)
	}
	// bob darf den gesperrten Absatz nicht ändern: bekommt den alten Stand zurück
	w(cb, `{"t":"set","k":"p1","v":"bob schreibt","pos":1}`)
	if m := read(t, cb); m["t"] != "item" || m["item"].(map[string]any)["v"] != "hallo" {
		t.Fatalf("locked write must be reverted: %v", m)
	}
	// anna schreibt selbst
	w(ca, `{"t":"set","k":"p1","v":"hallo welt","pos":1,"f":"i"}`)
	read(t, ca)
	if m := read(t, cb); m["item"].(map[string]any)["v"] != "hallo welt" {
		t.Fatalf("owner write: %v", m)
	}
	// bob kann die Sperre nicht nehmen (nur er erfährt es), nach unlock schon
	w(cb, `{"t":"lock","k":"p1"}`)
	if m := read(t, cb); m["t"] != "lock" || m["by"] != "anna" {
		t.Fatalf("lock denied: %v", m)
	}
	w(ca, `{"t":"unlock","k":"p1"}`)
	if m := read(t, cb); m["t"] != "unlock" {
		t.Fatalf("unlock: %v", m)
	}
	read(t, ca) // unlock-Broadcast
	w(cb, `{"t":"set","k":"p1","v":"bob","pos":1}`)
	read(t, ca)
	if m := read(t, cb); m["item"].(map[string]any)["v"] != "bob" {
		t.Fatalf("after unlock: %v", m)
	}
	// Verbindung weg -> Sperre fällt
	w(cb, `{"t":"lock","k":"p1"}`)
	read(t, ca)
	read(t, cb)
	cb.CloseNow()
	if m := read(t, ca); m["t"] != "unlock" {
		t.Fatalf("disconnect must release: %v", m)
	}
}

func TestUnits(t *testing.T) {
	srv, _ := setup(t) // anna = Admin, bob = normaler Benutzer
	defer srv.Close()
	if c, _ := req(t, srv, "bob", "POST", "/api/units", `{"name":"vertrieb"}`); c != 403 {
		t.Fatal("user darf keine Organisation anlegen", c)
	}
	for _, n := range []string{"vertrieb", "forschung"} {
		if c, b := req(t, srv, "anna", "POST", "/api/units", `{"name":"`+n+`"}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/units", `{"name":"vertrieb"}`); c != 409 {
		t.Fatal("doppelt", c)
	}
	if c, _ := req(t, srv, "anna", "DELETE", "/api/units/all", ""); c != 400 {
		t.Fatal("all loeschbar", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/groups", `{"name":"x1","areas":["files"],"units":["nope"]}`); c == 200 {
		t.Fatal("unbekannte Organisation akzeptiert")
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"x1","areas":["files"],"units":["vertrieb","forschung"]}`); c != 200 {
		t.Fatal(c, b)
	}
	_, b := req(t, srv, "bob", "GET", "/api/groups", "")
	if !strings.Contains(b, `"units":["forschung","vertrieb"]`) || !strings.Contains(b, `"units":["all"]`) {
		t.Fatal(b)
	}
	_, b = req(t, srv, "bob", "GET", "/api/units", "")
	if !strings.Contains(b, `{"name":"forschung","groups":["x1"]}`) || !strings.Contains(b, `{"name":"all","groups":["alluser"]}`) {
		t.Fatal(b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/groups/x1", `{"units":["forschung"]}`); c != 200 {
		t.Fatal(c)
	}
	if c, _ := req(t, srv, "anna", "DELETE", "/api/units/forschung", ""); c != 200 {
		t.Fatal(c)
	}
	_, b = req(t, srv, "anna", "GET", "/api/units", "")
	if strings.Contains(b, "forschung") || !strings.Contains(b, `{"name":"all","groups":["alluser","x1"]}`) {
		t.Fatal("Gruppe ohne Organisation faellt auf all", b)
	}
}

// kleiner SMTP-Testserver: liefert die empfangenen Mails (Rcpt-Liste + Daten)
type fakeMail struct {
	rcpt []string
	data string
}

func smtpServer(t *testing.T) (string, string, chan fakeMail) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan fakeMail, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rd := bufio.NewReader(c)
				fmt.Fprint(c, "220 fake\r\n")
				var m fakeMail
				for {
					l, err := rd.ReadString('\n')
					if err != nil {
						return
					}
					u := strings.ToUpper(strings.TrimSpace(l))
					switch {
					case strings.HasPrefix(u, "EHLO"):
						fmt.Fprint(c, "250 fake\r\n")
					case strings.HasPrefix(u, "RCPT"):
						m.rcpt = append(m.rcpt, strings.Trim(strings.TrimSpace(l)[len("RCPT TO:"):], "<> "))
						fmt.Fprint(c, "250 ok\r\n")
					case strings.HasPrefix(u, "MAIL"):
						fmt.Fprint(c, "250 ok\r\n")
					case u == "DATA":
						fmt.Fprint(c, "354 go\r\n")
						var sb strings.Builder
						for {
							d, _ := rd.ReadString('\n')
							if d == ".\r\n" || d == "" {
								break
							}
							sb.WriteString(d)
						}
						m.data = sb.String()
						fmt.Fprint(c, "250 queued\r\n")
					case u == "QUIT":
						fmt.Fprint(c, "221 bye\r\n")
						ch <- m
						return
					default:
						fmt.Fprint(c, "250 ok\r\n")
					}
				}
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	return h, p, ch
}

func TestChatChannels(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	ctx := context.Background()
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k5","areas":["files"],"chat":"member","chans":"member","msg":"member","admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/groups/k5/members", `{"add":["bob"]}`)
	ca, cb := chatDial(t, srv, "anna"), chatDial(t, srv, "bob")
	defer ca.CloseNow()
	defer cb.CloseNow()
	if m := read(t, cb); m["t"] != "hello" || !strings.Contains(fmt.Sprint(m["groups"]), "k5") {
		t.Fatalf("hello: %v", m)
	}
	read(t, ca)
	w := func(c *websocket.Conn, s string) { c.Write(ctx, websocket.MessageText, []byte(s)) }
	w(cb, `{"t":"send","g":"k5","c":"allgemein","text":"Hallo Klasse"}`)
	for _, c := range []*websocket.Conn{ca, cb} {
		if m := read(t, c); m["t"] != "msg" || m["m"].(map[string]any)["t"] != "Hallo Klasse" {
			t.Fatalf("msg: %v", m)
		}
	}
	// Kanal anlegen (Mitglieder dürfen), Nachricht dort
	w(cb, `{"t":"mkchan","g":"k5","name":"Hausaufgaben"}`)
	if m := read(t, ca); m["t"] != "chans" || !strings.Contains(fmt.Sprint(m["chans"]), "hausaufgaben") {
		t.Fatalf("chans: %v", m)
	}
	read(t, cb)
	w(cb, `{"t":"send","g":"k5","c":"hausaufgaben","text":"Aufgabe 1"}`)
	read(t, ca)
	m := read(t, cb)
	id := int64(m["m"].(map[string]any)["id"].(float64))
	// Reaktion, Bearbeiten nur durch Autor, Löschen durch Gruppen-Admin
	w(ca, fmt.Sprintf(`{"t":"react","g":"k5","c":"hausaufgaben","id":%d,"e":"👍"}`, id))
	if m := read(t, cb); m["t"] != "upd" || !strings.Contains(fmt.Sprint(m["m"]), "anna") {
		t.Fatalf("react: %v", m)
	}
	read(t, ca)
	w(ca, fmt.Sprintf(`{"t":"edit","g":"k5","c":"hausaufgaben","id":%d,"text":"fremd"}`, id))
	if m := read(t, ca); m["t"] != "err" {
		t.Fatalf("fremd bearbeiten: %v", m)
	}
	w(cb, fmt.Sprintf(`{"t":"edit","g":"k5","c":"hausaufgaben","id":%d,"text":"Aufgabe 1+2"}`, id))
	read(t, ca)
	read(t, cb)
	w(ca, fmt.Sprintf(`{"t":"del","g":"k5","c":"hausaufgaben","id":%d}`, id))
	if m := read(t, cb); m["t"] != "upd" || m["m"].(map[string]any)["del"] != true {
		t.Fatalf("admin del: %v", m)
	}
	read(t, ca)
	// Verlauf + Zugriff
	_, b := req(t, srv, "bob", "GET", "/api/chat/k5/allgemein/history", "")
	if !strings.Contains(b, "Hallo Klasse") {
		t.Fatal(b)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/chat/k5/nope/history", ""); c != 404 {
		t.Fatal("unbekannter Kanal", c)
	}
	req(t, srv, "anna", "POST", "/api/users", `{"name":"eve","password":"eve-passwort","groups":["alluser"]}`)
	if c := reqp(t, srv, "eve", "eve-passwort", "GET", "/api/chat/k5/allgemein/history", ""); c != 404 {
		t.Fatal("Nicht-Mitglied darf nicht lesen", c)
	}
	// Anhang: Upload, Abruf nur für Mitglieder
	if c, b := req(t, srv, "bob", "POST", "/api/chat/k5/allgemein/upload?name=bild.png&text=Foto", "\x89PNG\r\n\x1a\nrest"); c != 200 {
		t.Fatal(c, b)
	}
	_, b = req(t, srv, "bob", "GET", "/api/chat/k5/allgemein/history", "")
	var h struct {
		Msgs []struct{ Att *struct{ ID string } }
	}
	json.Unmarshal([]byte(b), &h)
	aid := ""
	for _, m := range h.Msgs {
		if m.Att != nil {
			aid = m.Att.ID
		}
	}
	if aid == "" {
		t.Fatal("kein Anhang", b)
	}
	if c, b := req(t, srv, "anna", "GET", "/api/chat/k5/allgemein/file/"+aid, ""); c != 200 || !strings.HasPrefix(b, "\x89PNG") {
		t.Fatal(c)
	}
	if c := reqp(t, srv, "eve", "eve-passwort", "GET", "/api/chat/k5/allgemein/file/"+aid, ""); c != 404 {
		t.Fatal("Anhang für Fremde", c)
	}
	read(t, ca)
	read(t, cb)
	// Modus admin: bob liest nur; Kanal löschen (Admin)
	req(t, srv, "anna", "POST", "/api/groups/k5", `{"chat":"admin","chans":"admin"}`)
	w(cb, `{"t":"send","g":"k5","c":"allgemein","text":"darf nicht"}`)
	if m := read(t, cb); m["t"] != "err" {
		t.Fatalf("admin-only: %v", m)
	}
	w(cb, `{"t":"mkchan","g":"k5","name":"neu"}`)
	if m := read(t, cb); m["t"] != "err" {
		t.Fatalf("mkchan admin-only: %v", m)
	}
	w(ca, `{"t":"rmchan","g":"k5","name":"hausaufgaben"}`)
	if m := read(t, ca); m["t"] != "chans" || strings.Contains(fmt.Sprint(m["chans"]), "hausaufgaben") {
		t.Fatalf("rmchan: %v", m)
	}
	req(t, srv, "anna", "POST", "/api/groups/k5", `{"chat":"off"}`)
	if c, _ := req(t, srv, "bob", "GET", "/api/chat/k5/allgemein/history", ""); c != 404 {
		t.Fatal("chat off", c)
	}
}

func TestMessageMailAndHook(t *testing.T) {
	host, port, got := smtpServer(t)
	t.Setenv("CS_SMTP_HOST", host)
	t.Setenv("CS_SMTP_PORT", port)
	t.Setenv("CS_SMTP_TLS", "none")
	t.Setenv("CS_SMTP_FROM", "cs-team@example.org")
	var hookBody string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hookBody = string(b)
	}))
	defer hs.Close()
	srv, _ := setup(t)
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"k6","areas":["files"],"msg":"admin","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/k6/members", `{"add":["bob"]}`)
	if c, b := req(t, srv, "anna", "POST", "/api/me/contact", `{"mail":"anna@example.org"}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/me/contact", `{"mail":"kein-mail","chat":""}`); c != 400 {
		t.Fatal("ungueltige Mail", c)
	}
	req(t, srv, "bob", "POST", "/api/me/contact", `{"mail":"bob@example.org","chat":"`+hs.URL+`/topic"}`)
	// bob ist nur Mitglied, Modus admin -> darf nicht senden
	if c, _ := req(t, srv, "bob", "POST", "/api/message", `{"group":"k6","body":"hi","mail":true}`); c != 403 {
		t.Fatal("member darf nicht", c)
	}
	_, b := req(t, srv, "anna", "GET", "/api/message/groups", "")
	if !strings.Contains(b, `"name":"k6","members":2,"mail":2,"hook":1`) {
		t.Fatal(b)
	}
	c, b := req(t, srv, "anna", "POST", "/api/message", `{"group":"k6","subject":"Test Ä","body":"Hallo zusammen","mail":true,"hook":true}`)
	if c != 200 {
		t.Fatal(c, b)
	}
	if !strings.Contains(b, `"MailSent":2`) || !strings.Contains(b, `"HookFailed":["bob`) {
		t.Fatalf("Privatnetz muss blockiert sein: %s", b)
	}
	m := <-got
	if len(m.rcpt) != 2 || !strings.Contains(m.data, "Subject: =?utf-8?q?[k6]") || !strings.Contains(m.data, "Hallo zusammen") {
		t.Fatalf("mail: %+v", m)
	}
	// mit erlaubtem Privatnetz kommt der Webhook an
	t.Setenv("CS_CHAT_ALLOW_PRIVATE", "1")
	srv2, _ := setup(t)
	defer srv2.Close()
	req(t, srv2, "anna", "POST", "/api/groups", `{"name":"k6","areas":["files"],"msg":"admin","admins":["anna"]}`)
	req(t, srv2, "anna", "POST", "/api/groups/k6/members", `{"add":["bob"]}`)
	req(t, srv2, "bob", "POST", "/api/me/contact", `{"chat":"`+hs.URL+`/topic"}`)
	c, b = req(t, srv2, "anna", "POST", "/api/message", `{"group":"k6","subject":"S","body":"Nur Chat","hook":true,"chat":true}`)
	if c != 200 || !strings.Contains(b, `"HookSent":1`) || !strings.Contains(hookBody, "Nur Chat") {
		t.Fatal(c, b, hookBody)
	}
	if _, l := req(t, srv2, "anna", "GET", "/api/message/log", ""); !strings.Contains(l, `"group":"k6"`) {
		t.Fatal(l)
	}
}
func chatDial(t *testing.T, srv *httptest.Server, user string) *websocket.Conn {
	h := http.Header{}
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":passwort-"+user)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/chat/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUserContact(t *testing.T) {
	srv, _ := setup(t) // anna = Admin, bob = normaler Benutzer
	defer srv.Close()
	if c, _ := req(t, srv, "anna", "POST", "/api/users", `{"name":"eva","password":"passwort-eva","groups":["alluser"],"mail":"kein-mail"}`); c != 400 {
		t.Fatal("ungueltige Mail akzeptiert", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"eva","password":"passwort-eva","groups":["alluser"],"mail":"eva@example.com","chat":"https://ntfy.sh/eva"}`); c != 200 {
		t.Fatal(c, b)
	}
	_, b := req(t, srv, "anna", "GET", "/api/users", "")
	if !strings.Contains(b, `"mail":"eva@example.com"`) || !strings.Contains(b, "https://ntfy.sh/eva") {
		t.Fatal(b)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/users/eva/contact", `{"mail":"x@example.com"}`); c != 403 {
		t.Fatal("normaler Benutzer aendert fremde Adresse", c)
	}
	// Gruppen-Admin darf die Adressen seiner Mitglieder pflegen
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"ga1","areas":["files"],"admins":["bob"]}`)
	req(t, srv, "anna", "POST", "/api/groups/ga1/members", `{"add":["eva"]}`)
	if c, b := req(t, srv, "bob", "POST", "/api/users/eva/contact", `{"mail":"eva2@example.com"}`); c != 200 {
		t.Fatal(c, b)
	}
	_, b = req(t, srv, "bob", "GET", "/api/users", "")
	if !strings.Contains(b, "eva2@example.com") {
		t.Fatal(b)
	}
}

func TestSettingsAndImportContacts(t *testing.T) {
	host, port, got := smtpServer(t)
	srv, _ := setup(t) // anna = Admin, bob = normaler Benutzer; keine Startparameter für SMTP
	defer srv.Close()
	if c, _ := req(t, srv, "bob", "GET", "/api/settings", ""); c != 403 {
		t.Fatal("Einstellungen nur fuer Admins", c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/settings", `{}`); c != 403 {
		t.Fatal(c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/settings", `{"host":"smtp.example.org","port":"99999"}`); c != 400 {
		t.Fatal("Port", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/settings", `{"public":"ftp://x"}`); c != 400 {
		t.Fatal("public", c)
	}
	_, b := req(t, srv, "anna", "GET", "/api/message/groups", "")
	if !strings.Contains(b, `"smtp":false`) {
		t.Fatal(b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/settings", `{"host":"`+host+`","port":"`+port+`","tls":"none","from":"cs-team@example.org","user":"u","pass":"geheim","public":"https://team.example.org:9004/","private":true}`); c != 200 {
		t.Fatal(c, b)
	}
	_, b = req(t, srv, "anna", "GET", "/api/settings", "")
	if strings.Contains(b, "geheim") || !strings.Contains(b, `"passSet":true`) || !strings.Contains(b, `"public":"https://team.example.org:9004"`) || !strings.Contains(b, `"enabled":true`) {
		t.Fatal(b)
	}
	// Passwort bleibt, wenn es nicht mitgeschickt wird
	req(t, srv, "anna", "POST", "/api/settings", `{"host":"`+host+`","port":"`+port+`","tls":"none","from":"cs-team@example.org","user":"u","public":"https://team.example.org:9004"}`)
	_, b = req(t, srv, "anna", "GET", "/api/settings", "")
	if !strings.Contains(b, `"passSet":true`) {
		t.Fatal("Passwort verloren", b)
	}
	// C-03: anderer Zielserver ohne neues Passwort = gespeichertes Passwort wird verworfen
	req(t, srv, "anna", "POST", "/api/settings", `{"host":"mail.example.net","port":"`+port+`","tls":"none","from":"cs-team@example.org","user":"u","public":"https://team.example.org:9004"}`)
	_, b = req(t, srv, "anna", "GET", "/api/settings", "")
	if !strings.Contains(b, `"passSet":false`) {
		t.Fatal("Passwort bei neuem Host nicht verworfen", b)
	}
	req(t, srv, "anna", "POST", "/api/settings", `{"host":"`+host+`","port":"`+port+`","tls":"none","from":"cs-team@example.org","user":"","pass":"geheim","public":"https://team.example.org:9004","private":true}`)
	_, b = req(t, srv, "anna", "GET", "/api/message/groups", "")
	if !strings.Contains(b, `"smtp":true`) {
		t.Fatal(b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/settings/testmail", ""); c != 400 {
		t.Fatal("Testmail ohne eigene Adresse", c)
	}
	req(t, srv, "anna", "POST", "/api/me/contact", `{"mail":"anna@example.org"}`)
	if c, b := req(t, srv, "anna", "POST", "/api/settings/testmail", ""); c != 200 {
		t.Fatal(c, b)
	}
	select {
	case m := <-got:
		if !strings.Contains(fmt.Sprint(m), "anna@example.org") {
			t.Fatal(m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("keine Testmail")
	}
	// Import mit Mail und Chat-Adresse; leere Felder lassen vorhandene Werte stehen
	_, b = req(t, srv, "anna", "POST", "/api/users/import", "lea;startpw123;alluser;lea@example.org;https://ntfy.sh/lea\nmax;startpw456;alluser;kaputt\n")
	if !strings.Contains(b, `"created":1`) || !strings.Contains(b, "max") {
		t.Fatal(b)
	}
	_, b = req(t, srv, "anna", "GET", "/api/users", "")
	if !strings.Contains(b, "lea@example.org") || !strings.Contains(b, "https://ntfy.sh/lea") {
		t.Fatal(b)
	}
	req(t, srv, "anna", "POST", "/api/users/import?update=1", "lea;;alluser;;\n")
	_, b = req(t, srv, "anna", "GET", "/api/users/export", "")
	if !strings.Contains(b, "lea;;alluser;lea@example.org;https://ntfy.sh/lea") {
		t.Fatal(b)
	}
}

// Calc: die Markierung sperrt ihre Zellen für andere (lockr), Wechsel/Leeren löst, Fremdsperren werden nur dem Anfragenden gemeldet.
func TestSheetRectLock(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	_, b := req(t, srv, "anna", "POST", "/api/docs", `{"name":"t","type":"sheet"}`)
	var r struct{ ID string }
	json.Unmarshal([]byte(b), &r)
	req(t, srv, "anna", "POST", "/api/docs/"+r.ID+"/share", `{"read":[],"write":["bob"]}`)
	ca, cb := dial(t, srv, "anna", r.ID), dial(t, srv, "bob", r.ID)
	defer ca.CloseNow()
	defer cb.CloseNow()
	read(t, ca)
	read(t, cb)
	ctx := context.Background()
	w := func(c *websocket.Conn, s string) { c.Write(ctx, websocket.MessageText, []byte(s)) }
	keys := func(m map[string]any) map[string]bool {
		out := map[string]bool{}
		for _, k := range m["ks"].([]any) {
			out[k.(string)] = true
		}
		return out
	}
	// anna markiert A1:B2 -> alle sehen vier gesperrte Zellen
	w(ca, `{"t":"lockr","rc":[0,1,1,2]}`)
	for _, c := range []*websocket.Conn{ca, cb} {
		m := read(t, c)
		k := keys(m)
		if m["t"] != "locks" || m["by"] != "anna" || len(k) != 4 || !k["A1"] || !k["B2"] {
			t.Fatalf("locks: %v", m)
		}
	}
	// bob kann in A2 nicht schreiben (alter Stand kommt nicht zurück, weil die Zelle leer ist), außerhalb schon
	w(cb, `{"t":"set","k":"A2","v":"bob"}`)
	w(cb, `{"t":"set","k":"C1","v":"frei"}`)
	if m := read(t, cb); m["t"] != "item" || m["k"] != "C1" {
		t.Fatalf("C1: %v", m)
	}
	read(t, ca)
	// bob markiert B2:C3: B2 gehört anna (nur bob erfährt es), die übrigen drei Zellen sperrt er
	w(cb, `{"t":"lockr","rc":[1,2,2,3]}`)
	got := map[string]map[string]bool{}
	for i := 0; i < 2; i++ {
		m := read(t, cb)
		got[m["by"].(string)] = keys(m)
	}
	if len(got["anna"]) != 1 || !got["anna"]["B2"] || len(got["bob"]) != 3 || got["bob"]["B2"] {
		t.Fatalf("bob: %v", got)
	}
	if m := read(t, ca); m["t"] != "locks" || m["by"] != "bob" || len(keys(m)) != 3 {
		t.Fatalf("anna sieht bob: %v", m)
	}
	// anna wechselt auf A1: A2, B1, B2 werden frei
	w(ca, `{"t":"lockr","rc":[0,1,0,1]}`)
	m := read(t, ca)
	if m["t"] != "unlocks" || len(keys(m)) != 3 || !keys(m)["B2"] {
		t.Fatalf("unlocks: %v", m)
	}
	// leere Markierung löst alles (A1)
	w(ca, `{"t":"lockr","rc":[]}`)
	for {
		m = read(t, ca)
		if m["t"] == "unlock" && m["k"] == "A1" {
			break
		}
		if m["t"] == "unlocks" && keys(m)["A1"] {
			break
		}
	}
	// ungültige / zu große Bereiche sperren nichts
	w(ca, `{"t":"lockr","rc":[0,1,25,99999]}`)
	w(ca, `{"t":"lockr","rc":[5,1,2,1]}`)
	w(ca, `{"t":"set","k":"Z1","v":"ok"}`)
	for {
		m = read(t, ca)
		if m["t"] == "item" && m["k"] == "Z1" {
			break
		}
		if m["t"] == "locks" && m["by"] == "anna" {
			t.Fatalf("ungueltiger Bereich gesperrt: %v", m)
		}
	}
	// Verbindung weg -> bobs Sperren fallen gesammelt
	cb.CloseNow()
	for {
		m = read(t, ca)
		if m["t"] == "unlocks" && len(keys(m)) == 3 {
			break
		}
	}
}

// /api/me nennt die Programmversion (Tooltip am Titel der Oberfläche).
func TestMeVersion(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	c, b := req(t, srv, "anna", "GET", "/api/me", "")
	var m struct{ Version string }
	if c != 200 || json.Unmarshal([]byte(b), &m) != nil || m.Version != version || m.Version == "" {
		t.Fatalf("version: %d %s", c, b)
	}
}
