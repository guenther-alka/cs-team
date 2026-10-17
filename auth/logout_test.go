package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"cs-team/store"
)

// Abmelden muss auch wirken, wenn der Browser vom frueheren Basic-Dialog noch gueltige Zugangsdaten gemerkt hat.
func TestLogoutIgnoresStaleBasic(t *testing.T) {
	ctx := context.Background()
	oldF, oldC := ForceChange, LoginForm
	ForceChange, LoginForm = false, true
	defer func() { ForceChange, LoginForm = oldF, oldC }()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "root", "Root-Passwort-1x"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "anna", "Anna-Passwort-77x", false, nil); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	basic := map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("anna:Anna-Passwort-77x"))}

	w := webReq(mux, "POST", "/api/login", `{"name":"anna","password":"Anna-Passwort-77x"}`, "", basic) // Browser sendet gemerkte Basic-Daten mit
	tok := cookieOf(w)
	if w.Code != 200 || tok == "" {
		t.Fatalf("Anmeldung: %d", w.Code)
	}
	if w = webReq(mux, "GET", "/api/me", "", tok, basic); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"anna"`) {
		t.Fatalf("mit Cookie: %d", w.Code)
	}
	if w = webReq(mux, "POST", "/api/logout", "", tok, basic); w.Code != 200 {
		t.Fatalf("Abmelden: %d", w.Code)
	}
	// danach: Browser mit den gemerkten (gueltigen) Basic-Daten ist NICHT angemeldet
	if w = webReq(mux, "GET", "/api/me", "", "", basic); w.Code != 401 || w.Header().Get("X-Login-Form") != "1" {
		t.Fatalf("nach dem Abmelden: %d %v", w.Code, w.Header())
	}
	if w = webReq(mux, "GET", "/api/me", "", tok, nil); w.Code != 401 {
		t.Fatalf("altes Cookie: %d", w.Code)
	}
	// Nicht-Browser (WebDAV, curl): Basic gilt weiter
	nb := map[string]string{"Authorization": basic["Authorization"], "Sec-Fetch-Mode": "", "Sec-Fetch-Site": ""}
	if w = webReq(mux, "GET", "/api/me", "", "", nb); w.Code != 200 {
		t.Fatalf("Basic ohne Browser: %d", w.Code)
	}
}
