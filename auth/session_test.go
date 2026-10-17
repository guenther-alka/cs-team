package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cs-team/store"
)

// post/get: Anfrage eines Browsers (Sec-Fetch-Mode/-Site) ans Testprogramm, optional mit Cookie.
func webReq(h http.Handler, method, path, body, cookie string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func cookieOf(w *httptest.ResponseRecorder) string {
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName && c.MaxAge >= 0 {
			return c.Value
		}
	}
	return ""
}

func TestLoginForm(t *testing.T) {
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
	login := func(name, pw string) *httptest.ResponseRecorder {
		return webReq(mux, "POST", "/api/login", `{"name":"`+name+`","password":"`+pw+`"}`, "", nil)
	}

	// ohne Anmeldung: Browser bekommen X-Login-Form statt Basic-Abfrage, andere Clients die Basic-Abfrage
	w := webReq(mux, "GET", "/api/me", "", "", nil)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != "" || w.Header().Get("X-Login-Form") != "1" {
		t.Fatalf("Browser ohne Anmeldung: %d %v", w.Code, w.Header())
	}
	w = webReq(mux, "GET", "/api/me", "", "", map[string]string{"Sec-Fetch-Mode": "", "Sec-Fetch-Site": ""})
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("Nicht-Browser: %d %v", w.Code, w.Header())
	}

	// falsches Passwort / unbekannter Name: 401 ohne Cookie und ohne Basic-Dialog
	for _, c := range [][2]string{{"anna", "falsch-falsch"}, {"gibtesnicht", "irgendwas1234"}} {
		if w = login(c[0], c[1]); w.Code != 401 || cookieOf(w) != "" || w.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("%v: %d %v", c, w.Code, w.Header())
		}
	}

	// richtig: Cookie mit HttpOnly + SameSite=Strict, danach geht /api/me ohne Basic Auth
	w = login("anna", "Anna-Passwort-77x")
	tok := cookieOf(w)
	if w.Code != 200 || tok == "" {
		t.Fatalf("Anmeldung: %d %s", w.Code, w.Body.String())
	}
	ck := w.Result().Cookies()[0]
	if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" {
		t.Fatalf("Cookie-Flags: %+v", ck)
	}
	if w = webReq(mux, "GET", "/api/me", "", tok, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"anna"`) || !strings.Contains(w.Body.String(), `"loginForm":true`) {
		t.Fatalf("/api/me mit Cookie: %d %s", w.Code, w.Body.String())
	}

	// Gross-/Kleinschreibung des Namens wie bei Basic Auth; anna@local ist dasselbe Konto
	if w = login("Anna@local", "Anna-Passwort-77x"); w.Code != 200 {
		t.Fatalf("anna@local: %d", w.Code)
	}

	// schreibende Anfrage mit Cookie: nur bei belegtem gleichen Ursprung (fremde Seite / fehlende Header werden abgelehnt)
	body := `{"lang":"en"}`
	if w = webReq(mux, "POST", "/api/me/lang", body, tok, nil); w.Code != 200 {
		t.Fatalf("same-origin POST: %d %s", w.Code, w.Body.String())
	}
	if w = webReq(mux, "POST", "/api/me/lang", body, tok, map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != 403 {
		t.Fatalf("cross-site POST: %d", w.Code)
	}
	if w = webReq(mux, "POST", "/api/me/lang", body, tok, map[string]string{"Sec-Fetch-Site": "", "Sec-Fetch-Mode": ""}); w.Code != 403 {
		t.Fatalf("POST ohne Herkunftsheader mit Cookie: %d", w.Code)
	}
	if w = webReq(mux, "POST", "/api/me/lang", body, tok, map[string]string{"Sec-Fetch-Site": "", "Origin": "https://evil.example"}); w.Code != 403 {
		t.Fatalf("fremder Origin mit Cookie: %d", w.Code)
	}

	// frei erfundener oder zu langer Cookie
	for _, c := range []string{"abc", strings.Repeat("x", 100)} {
		if w = webReq(mux, "GET", "/api/me", "", c, nil); w.Code != 401 {
			t.Fatalf("falsches Cookie %q: %d", c, w.Code)
		}
	}

	// Ein Cookie kein Zugang zu Adminfunktionen (Benutzer ist kein Admin)
	if w = webReq(mux, "GET", "/api/groups", "", tok, nil); w.Code == 200 && strings.Contains(w.Body.String(), "root") {
		t.Log("Gruppenliste fuer normalen Benutzer:", w.Code)
	}

	// Passwortaenderung beendet alle Sitzungen des Kontos
	if err := a.SetPassword(ctx, "anna", "Anna-Neues-Passwort-9y"); err != nil {
		t.Fatal(err)
	}
	if w = webReq(mux, "GET", "/api/me", "", tok, nil); w.Code != 401 {
		t.Fatalf("nach Passwortaenderung: %d", w.Code)
	}
	if login("anna", "Anna-Passwort-77x").Code != 401 {
		t.Fatal("altes Passwort darf nicht mehr gehen")
	}
	tok = cookieOf(login("anna", "Anna-Neues-Passwort-9y"))
	if tok == "" {
		t.Fatal("neue Anmeldung")
	}

	// Abmelden: POST /api/logout beendet die Sitzung (cross-site nicht)
	if w = webReq(mux, "POST", "/api/logout", "", tok, map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != 403 {
		t.Fatalf("logout cross-site: %d", w.Code)
	}
	if w = webReq(mux, "POST", "/api/logout", "", tok, nil); w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w = webReq(mux, "GET", "/api/me", "", tok, nil); w.Code != 401 {
		t.Fatalf("nach Abmelden: %d", w.Code)
	}

	// gesperrtes Konto: bestehende Sitzung endet sofort
	tok = cookieOf(login("anna", "Anna-Neues-Passwort-9y"))
	dis := true
	if err := a.SetFlags(ctx, "anna", nil, &dis); err != nil {
		t.Fatal(err)
	}
	if w = webReq(mux, "GET", "/api/me", "", tok, nil); w.Code != 401 {
		t.Fatalf("gesperrt: %d", w.Code)
	}

	// Formular aus: /api/login gibt es nicht, Cookies zaehlen nicht
	LoginForm = false
	if w = login("root", "Root-Passwort-1x"); w.Code != 404 {
		t.Fatalf("ohne CS_LOGIN_FORM: %d", w.Code)
	}
}

// Pflicht zur Passwortaenderung gilt auch fuer Sitzungen: /api/me und /api/me/password gehen, der Rest nicht.
func TestLoginFormMustChange(t *testing.T) {
	ctx := context.Background()
	oldF, oldC := ForceChange, LoginForm
	ForceChange, LoginForm = true, true
	defer func() { ForceChange, LoginForm = oldF, oldC }()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "root", "Root-Passwort-1x"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "neu", "Start-Passwort-55x", false, nil); err != nil { // Must = true
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	w := webReq(mux, "POST", "/api/login", `{"name":"neu","password":"Start-Passwort-55x"}`, "", nil)
	tok := cookieOf(w)
	if w.Code != 200 || tok == "" || !strings.Contains(w.Body.String(), `"must":true`) {
		t.Fatalf("Anmeldung mit Startpasswort: %d %s", w.Code, w.Body.String())
	}
	if w = webReq(mux, "GET", "/api/users", "", tok, nil); w.Code != 403 || w.Header().Get("X-Must-Change") != "1" {
		t.Fatalf("Startpasswort: andere Seiten muessen gesperrt sein: %d", w.Code)
	}
	if w = webReq(mux, "POST", "/api/me/password", `{"New":"Eigenes-Passwort-3z"}`, tok, nil); w.Code != 200 {
		t.Fatalf("Passwort aendern: %d %s", w.Code, w.Body.String())
	}
	// die alte Sitzung ist beendet; die Antwort bringt eine neue Sitzung mit, die gilt
	if w2 := webReq(mux, "GET", "/api/me", "", tok, nil); w2.Code != 401 {
		t.Fatalf("alte Sitzung: %d", w2.Code)
	}
	if nt := cookieOf(w); nt == "" || nt == tok {
		t.Fatalf("neue Sitzung nach Passwortaenderung fehlt: %q", nt)
	} else if w2 := webReq(mux, "GET", "/api/users", "", nt, nil); w2.Code != 200 && w2.Code != 403 || w2.Header().Get("X-Must-Change") != "" {
		t.Fatalf("neue Sitzung: %d must=%q", w2.Code, w2.Header().Get("X-Must-Change"))
	}
	if w = webReq(mux, "POST", "/api/login", `{"name":"neu","password":"Eigenes-Passwort-3z"}`, "", nil); w.Code != 200 {
		t.Fatalf("neue Anmeldung: %d", w.Code)
	}
}
