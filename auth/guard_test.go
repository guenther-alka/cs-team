package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"cs-team/store"
)

func TestGuard(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "root", "rootrootroot"); err != nil {
		t.Fatal(err)
	}
	h := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	call := func(user, pass, ip string) int {
		r := httptest.NewRequest("GET", "/x", nil)
		r.SetBasicAuth(user, pass)
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// X-Forwarded-For: nur das letzte Element zählt
	a.TrustProxy = true
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 7.7.7.7, 192.0.2.9")
	if got := a.ip(r); got != "192.0.2.9" {
		t.Fatalf("XFF: %s", got)
	}
	r.Header.Set("X-Forwarded-For", "nonsense")
	if got := a.ip(r); got != "10.0.0.1" {
		t.Fatalf("XFF ungültig: %s", got)
	}
	a.TrustProxy = false
	// Auth-Cache: zweiter Aufruf bedient aus dem Cache, falsches Passwort nie
	if call("root", "rootrootroot", "192.0.2.1") != 200 || call("root", "rootrootroot", "192.0.2.1") != 200 {
		t.Fatal("login")
	}
	if len(a.cache) != 1 {
		t.Fatalf("cache %d", len(a.cache))
	}
	if call("root", "wrongwrong", "192.0.2.1") != 401 {
		t.Fatal("falsches Passwort")
	}
	// Passwortänderung macht den Cache ungültig
	if err := a.SetPassword(ctx, "root", "anotheranother"); err != nil {
		t.Fatal(err)
	}
	a.invalidate()
	if call("root", "rootrootroot", "192.0.2.2") != 401 {
		t.Fatal("altes Passwort nach Änderung noch gültig (Cache)")
	}
	if call("root", "anotheranother", "192.0.2.2") != 200 {
		t.Fatal("neues Passwort")
	}
	// Spraying: viele Namen von einer Adresse -> Adresse gesperrt. Wer von dort gerade gültig angemeldet ist (Cache), bleibt
	// zugelassen (Schul-NAT/Proxy); eine neue Anmeldung - auch mit richtigem Passwort - wird blockiert.
	if err := a.AddUser(ctx, "anna", "annaannaanna", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := call("root", "anotheranother", "198.51.100.7"); got != 200 {
		t.Fatalf("Anmeldung vor der Sperre: %d", got)
	}
	for i := 0; i < MaxFailsIP; i++ {
		call("nobody"+strconv.Itoa(i), "x", "198.51.100.7")
	}
	if !a.locked("ip|198.51.100.7") {
		t.Fatal("Adresse nicht gesperrt")
	}
	if got := call("root", "anotheranother", "198.51.100.7"); got != 200 {
		t.Fatalf("angemeldeter Nutzer durch Adress-Sperre blockiert: %d", got)
	}
	if got := call("anna", "annaannaanna", "198.51.100.7"); got != 429 {
		t.Fatalf("neue Anmeldung von gesperrter Adresse nicht blockiert: %d", got)
	}
	if got := call("root", "falschfalsch", "198.51.100.7"); got != 429 {
		t.Fatalf("falsches Passwort von gesperrter Adresse nicht blockiert: %d", got)
	}
	// Zähler verfällt: ohne neuen Fehlversuch innerhalb von failWindow beginnt er wieder bei 0
	a.failedMax("win|x", 3)
	a.failedMax("win|x", 3)
	a.mu.Lock()
	a.fails["win|x"].last = time.Now().Add(-2 * failWindow)
	a.mu.Unlock()
	a.failedMax("win|x", 3)
	if a.locked("win|x") {
		t.Fatal("veralteter Fehlversuchs-Zähler nicht zurückgesetzt")
	}
	if got := call("root", "anotheranother", "198.51.100.8"); got != 200 {
		t.Fatalf("andere Adresse blockiert: %d", got)
	}
	// Bereinigung entfernt keine aktiven Sperren
	for i := 0; i < 10100; i++ {
		a.failedMax("junk|"+string(rune(i+1000))+"x"+string(rune(i/7+500)), 5)
	}
	a.failedMax("ip|198.51.100.99", 1)
	for i := 0; i < 20; i++ {
		a.failedMax("more|"+string(rune(70000+i)), 5)
	}
	if !a.locked("ip|198.51.100.7") {
		t.Fatal("aktive Sperre durch Bereinigung verloren")
	}
}
