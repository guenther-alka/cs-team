package main

import (
	"net/http"
	"strings"
	"testing"
)

// 0.10.4: Sicherheitsfixes aus dem Audit (AUDIT.md)

func TestCSRFRefused(t *testing.T) { // S-02
	srv, _ := setup(t)
	defer srv.Close()
	body := `{"name":"mallory","password":"passwort-mallory","groups":["alluser"]}`
	// fremder Ursprung (Browser sendet Origin / Sec-Fetch-Site) -> abgelehnt
	if c, _ := req(t, srv, "anna", "POST", "/api/users", body, "Origin", "https://evil.example"); c != 403 {
		t.Fatal("Origin fremd:", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/users", body, "Sec-Fetch-Site", "cross-site"); c != 403 {
		t.Fatal("cross-site:", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/users", body, "Origin", "null"); c != 403 {
		t.Fatal("Origin null:", c)
	}
	// gleicher Ursprung, same-origin und Nicht-Browser (ohne Header) -> erlaubt
	host := strings.TrimPrefix(srv.URL, "http://")
	if c, b := req(t, srv, "anna", "POST", "/api/users", body, "Origin", "http://"+host, "Sec-Fetch-Site", "same-origin"); c != 200 {
		t.Fatal("same-origin:", c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"trent","password":"passwort-trent","groups":["alluser"]}`); c != 200 {
		t.Fatal("ohne Header (DAV/curl):", c, b)
	}
	// Lesen bleibt von fremden Seiten aus technisch möglich, aber nicht schreibend
	if c, _ := req(t, srv, "anna", "GET", "/api/users", "", "Sec-Fetch-Site", "cross-site"); c != 200 {
		t.Fatal("GET:", c)
	}
	// WebDAV-Schreiben von fremder Seite
	if c, _ := req(t, srv, "anna", "PUT", "/webdav/x.txt", "x", "Origin", "https://evil.example"); c != 403 {
		t.Fatal("DAV PUT fremd:", c)
	}
}

func TestWebDAVNoInlineHTML(t *testing.T) { // F1
	srv, _ := setup(t)
	defer srv.Close()
	if c, _ := req(t, srv, "anna", "PUT", "/webdav/x.html", "<script>alert(1)</script>"); c != 201 {
		t.Fatal("put", c)
	}
	r, _ := http.NewRequest("GET", srv.URL+"/webdav/x.html", nil)
	r.SetBasicAuth("anna", "passwort-anna")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	h := resp.Header
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("Header fehlen: %v", h)
	}
}

func TestGroupAdminCannotTakeOver(t *testing.T) { // S-01
	srv, _ := setup(t) // anna = Admin, bob = normal
	defer srv.Close()
	for _, n := range []string{"eve", "vic"} {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"geheim","areas":["files"],"admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"gruppeg","areas":["files"],"admins":["eve"]}`)
	req(t, srv, "anna", "POST", "/api/groups/geheim/members", `{"add":["vic"]}`)
	// eve ist Admin von gruppeg, vic sitzt in der vertraulichen Gruppe "geheim"
	if c, _ := req(t, srv, "eve", "POST", "/api/groups/gruppeg/members", `{"add":["vic"]}`); c != 403 {
		t.Fatal("Gruppen-Admin holt fremdes Mitglied:", c)
	}
	if c, _ := req(t, srv, "eve", "POST", "/api/users/vic/password", `{"password":"uebernommen1"}`); c != 403 {
		t.Fatal("Passwort-Reset eines fremden Kontos:", c)
	}
	// Konto nur in der eigenen Gruppe: erlaubt
	req(t, srv, "anna", "POST", "/api/groups/gruppeg/members", `{"add":["bob"]}`)
	if c, b := req(t, srv, "eve", "POST", "/api/users/bob/password", `{"password":"neues-passwort1"}`); c != 200 {
		t.Fatal("eigenes Mitglied:", c, b)
	}
	// bob ist jetzt auch in einer zweiten, nicht von eve verwalteten Gruppe -> kein Reset mehr
	req(t, srv, "anna", "POST", "/api/groups/geheim/members", `{"add":["bob"]}`)
	if c, _ := req(t, srv, "eve", "POST", "/api/users/bob/password", `{"password":"nochmal-neu1"}`); c != 403 {
		t.Fatal("Mitglied mit fremder Zweitgruppe:", c)
	}
}

func TestCalDAVBodyLimit(t *testing.T) { // K-04
	srv, _ := setup(t)
	defer srv.Close()
	big := "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:x\r\nSUMMARY:" + strings.Repeat("a", 2<<20) + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	c, _ := req(t, srv, "anna", "PUT", "/dav/anna/default/x.ics", big, "Content-Type", "text/calendar")
	if c == 201 || c == 204 {
		t.Fatal("2 MB Termin angenommen:", c)
	}
}
