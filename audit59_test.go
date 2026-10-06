package main

import (
	"bytes"
	"cs-team/auth"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
)

type logBuf struct {
	sync.Mutex
	b bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.Lock(); defer l.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.Lock(); defer l.Unlock(); return l.b.String() }

// captureLog leitet das Log in einen Puffer um (bis zum Ende des Tests).
func captureLog(t *testing.T) *logBuf {
	lb := &logBuf{}
	log.SetOutput(lb)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return lb
}

// 5xx: Antworttext (bereinigt, gekürzt) im Log; Audit-Präfixe audit-failed (Validierung) und audit-denied (Rechte).
func TestLogMW(t *testing.T) {
	lb := captureLog(t)
	h := logMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/boom":
			http.Error(w, "kaputt\nzeile2 "+strings.Repeat("a", 2000), 500)
		case "/api/groups/x":
			http.Error(w, "bad", 409)
		case "/api/users/x":
			http.Error(w, "no", 403)
		case "/api/settings/y":
			http.Error(w, "bad", 400)
		}
	}))
	do := func(method, path string) {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
	}
	do("GET", "/boom")
	do("POST", "/api/groups/x")
	do("POST", "/api/users/x")
	do("POST", "/api/settings/y")
	s := lb.String()
	i := strings.Index(s, "http: 500 GET /boom")
	if i < 0 || !strings.Contains(s, `body="kaputt?zeile2 aaaa`) {
		t.Fatal("5xx ohne Antworttext:", s)
	}
	if line := s[i : i+strings.Index(s[i:], "\n")]; len(line) > 300 {
		t.Fatal("Antworttext nicht gekürzt:", len(line))
	}
	for _, w := range []string{"audit-failed: POST /api/groups/x -> 409", "audit-denied: POST /api/users/x -> 403", "audit-failed: POST /api/settings/y -> 400"} {
		if !strings.Contains(s, w) {
			t.Fatal("fehlt:", w, "\n", s)
		}
	}
}

// Export wird protokolliert (auch der eigene); erfolgreiche Anmeldung nur nach Fehlversuchen.
func TestAuditExportAndLoginOK(t *testing.T) {
	_, st := setup(t)
	a := auth.New(st)
	srv := httptest.NewServer(logMW(routes(st, a)))
	defer srv.Close()
	lb := captureLog(t)
	if c, b := req(t, srv, "anna", "GET", "/api/me/export", ""); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, "anna", "GET", "/api/users/bob/export", ""); c != 200 {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/users/anna/export", ""); c != 403 {
		t.Fatal("Export fremder Daten nur für Admin:", c)
	}
	s := lb.String()
	if !strings.Contains(s, `audit: export user="anna" by="anna" ip=`) || !strings.Contains(s, `audit: export user="bob" by="anna" ip=`) {
		t.Fatal("Export-Audit fehlt:", s)
	}
	if strings.Contains(s, `export user="anna" by="bob"`) {
		t.Fatal("abgelehnter Export darf nicht als Export protokolliert werden:", s)
	}
	if strings.Contains(s, "login ok") { // keine Fehlversuche vorher
		t.Fatal("login ok ohne Fehlversuch:", s)
	}
	r, _ := http.NewRequest("GET", srv.URL+"/api/me", nil)
	r.SetBasicAuth("bob", "falsch")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if c, _ := req(t, srv, "bob", "GET", "/api/me", ""); c != 200 {
		t.Fatal("Anmeldung:", c)
	}
	if s := lb.String(); !strings.Contains(s, `auth: login ok user="bob" ip=`) || !strings.Contains(s, "after 1 failed attempts") {
		t.Fatal("login ok fehlt:", s)
	}
}

// Windows-Servern: reservierte Gerätenamen für Konten und Gruppen abgelehnt (auf anderen Systemen erlaubt).
func TestWinReservedNames(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	want := 200
	if runtime.GOOS == "windows" {
		want = 400
	}
	if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"con","password":"passwort-con","groups":["alluser"]}`); c != want {
		t.Fatal("Konto con:", c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"nul","areas":["files"],"admins":["anna"]}`); c != want {
		t.Fatal("Gruppe nul:", c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"console","areas":["files"],"admins":["anna"]}`); c != 200 {
		t.Fatal("Gruppe console ist erlaubt:", c, b)
	}
}
