package auth

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cs-team/store"
)

// fakeIdP: ein Mini-Anbieter (Discovery, JWKS, Token-Endpunkt) fuer die Tests.
type fakeIdP struct {
	srv       *httptest.Server
	rsaKey    *rsa.PrivateKey
	ecKey     *ecdsa.PrivateKey
	challenge string // erwartete PKCE-Pruefsumme (aus dem Start-Redirect)
	build     func() string
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func newIdP(t *testing.T) *fakeIdP {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f := &fakeIdP{rsaKey: rk, ecKey: ek}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{
			{"kty": "RSA", "kid": "r1", "n": b64(rk.N.Bytes()), "e": b64(big.NewInt(int64(rk.E)).Bytes())},
			{"kty": "EC", "kid": "e1", "crv": "P-256", "x": b64(ek.X.Bytes()), "y": b64(ek.Y.Bytes())},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good-code" || r.Form.Get("client_secret") != "geheim" || r.Form.Get("grant_type") != "authorization_code" ||
			b64(sum[:]) != f.challenge || r.Form.Get("redirect_uri") != "http://localhost:9999/oidc/callback" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": f.build()})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) sign(alg, kid string, claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	p, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(in))
	switch alg {
	case "RS256":
		s, _ := rsa.SignPKCS1v15(rand.Reader, f.rsaKey, crypto.SHA256, sum[:])
		return in + "." + b64(s)
	case "ES256":
		r, s, _ := ecdsa.Sign(rand.Reader, f.ecKey, sum[:])
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		s.FillBytes(sig[32:])
		return in + "." + b64(sig)
	}
	return in + "." // alg none
}

type oidcEnv struct {
	a   *Auth
	mux *http.ServeMux
	idp *fakeIdP
}

func newOIDCEnv(t *testing.T) *oidcEnv {
	ctx := context.Background()
	oldF, oldC, oldH, oldB, oldP := ForceChange, LoginForm, oidcAllowHTTP, oidcCfg.base, oidcCfg.provs
	ForceChange, LoginForm, oidcAllowHTTP = false, true, true
	t.Cleanup(func() {
		ForceChange, LoginForm, oidcAllowHTTP, oidcCfg.base, oidcCfg.provs = oldF, oldC, oldH, oldB, oldP
	})
	e := &oidcEnv{a: New(store.NewMem()), idp: newIdP(t)}
	ConfigureOIDC("http://localhost:9999", []*OIDCProvider{
		{ID: "fake", Name: "Fake", Issuer: e.idp.srv.URL, ClientID: "cid", Secret: "geheim", Scope: "openid"},
		{ID: "other", Name: "Other", Issuer: e.idp.srv.URL, ClientID: "cid", Secret: "geheim", Scope: "openid"},
	})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.a.Bootstrap(ctx, "root", "Root-Passwort-1x"))
	for _, n := range []string{"anna", "carl", "erik"} {
		must(e.a.AddUser(ctx, n, "Passwort-"+n+"-77x", false, nil))
	}
	e.mux = http.NewServeMux()
	e.a.Routes(e.mux)
	return e
}

// login: Anmeldung mit Passwort, liefert das Sitzungs-Cookie.
func (e *oidcEnv) login(t *testing.T, name string) string {
	t.Helper()
	pw := "Passwort-" + name + "-77x"
	if name == "root" {
		pw = "Root-Passwort-1x"
	}
	tok := cookieOf(webReq(e.mux, "POST", "/api/login", `{"name":"`+name+`","password":"`+pw+`"}`, "", nil))
	if tok == "" {
		t.Fatalf("Anmeldung von %s fehlgeschlagen", name)
	}
	return tok
}

func (e *oidcEnv) claims(sub, nonce string) map[string]any {
	now := time.Now().Unix()
	return map[string]any{"iss": e.idp.srv.URL, "aud": "cid", "sub": sub, "exp": now + 300, "iat": now, "nonce": nonce}
}

// runOpt: mod aendert die Anspruechen des id_token vor dem Signieren; signer erzeugt das Token (Standard: RS256);
// link: Sitzungs-Cookie, mit dem das Verknuepfen gestartet wird (leer = Anmeldung).
type runOpt struct {
	prov    string
	link    string
	mod     func(c map[string]any)
	signer  func(e *oidcEnv, c map[string]any) string
	cookie  string // "" = Cookie des Starts, "-" = keins, sonst dieser Wert
	state   string // "" = state des Starts
	badChal bool
}

// run: kompletter Ablauf (Start -> Anbieter -> Rueckkehr als fremde Navigation, also ohne Sitzungs-Cookie).
func (e *oidcEnv) run(t *testing.T, sub string, o runOpt) (*httptest.ResponseRecorder, string) {
	t.Helper()
	if o.prov == "" {
		o.prov = "fake"
	}
	path := "/oidc/start/" + o.prov
	if o.link != "" {
		path = "/oidc/link/" + o.prov
	}
	w := webReq(e.mux, "GET", path, "", o.link, nil)
	if w.Code != 302 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	q := loc.Query()
	if !strings.HasPrefix(w.Header().Get("Location"), e.idp.srv.URL+"/authorize?") || q.Get("response_type") != "code" || q.Get("client_id") != "cid" ||
		q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "http://localhost:9999/oidc/callback" || q.Get("scope") != "openid" ||
		q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge") == "" {
		t.Fatalf("Start-Redirect: %s", w.Header().Get("Location"))
	}
	var stc string
	for _, c := range w.Result().Cookies() {
		if c.Name == oidcCookie {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/oidc" {
				t.Fatalf("Cookie des Starts: %+v", c)
			}
			stc = c.Value
		}
	}
	e.idp.challenge = q.Get("code_challenge")
	if o.badChal {
		e.idp.challenge = "falsch"
	}
	e.idp.build = func() string {
		c := e.claims(sub, q.Get("nonce"))
		if o.mod != nil {
			o.mod(c)
		}
		if o.signer != nil {
			return o.signer(e, c)
		}
		return e.idp.sign("RS256", "r1", c)
	}
	state := q.Get("state")
	if o.state != "" {
		state = o.state
	}
	r := httptest.NewRequest("GET", "/oidc/callback?code=good-code&state="+url.QueryEscape(state), nil)
	switch o.cookie {
	case "":
		r.AddCookie(&http.Cookie{Name: oidcCookie, Value: stc})
	case "-":
	default:
		r.AddCookie(&http.Cookie{Name: oidcCookie, Value: o.cookie})
	}
	w2 := httptest.NewRecorder()
	e.mux.ServeHTTP(w2, r)
	return w2, state
}

func (e *oidcEnv) okLogin(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != 302 || w.Header().Get("Location") != "/" {
		t.Fatalf("Anmeldung erwartet, bekam %d %s", w.Code, w.Header().Get("Location"))
	}
	tok := cookieOf(w)
	if tok == "" {
		t.Fatal("kein Sitzungs-Cookie")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName && (!c.HttpOnly || c.SameSite != http.SameSiteStrictMode) {
			t.Fatalf("Sitzungs-Cookie: %+v", c)
		}
	}
	return tok
}

func (e *oidcEnv) refused(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	if w.Code != 302 || w.Header().Get("Location") != "/?login=failed" || cookieOf(w) != "" {
		t.Fatalf("%s: erwartet abgelehnt, bekam %d %s cookie=%q", what, w.Code, w.Header().Get("Location"), cookieOf(w))
	}
}

func TestOIDCLinkAndLogin(t *testing.T) {
	e := newOIDCEnv(t)
	ctx := context.Background()

	// Anbieterliste fuer das Formular
	w := webReq(e.mux, "GET", "/api/login/info", "", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"fake"`) || !strings.Contains(w.Body.String(), `"form":true`) {
		t.Fatalf("login/info: %d %s", w.Code, w.Body.String())
	}
	if w = webReq(e.mux, "GET", "/oidc/start/gibtesnicht", "", "", nil); w.Code != 404 {
		t.Fatalf("unbekannter Anbieter: %d", w.Code)
	}

	// ohne Verknuepfung gibt es keine Anmeldung ueber den Anbieter - auch nicht fuer eine "passende" Person
	w, _ = e.run(t, "sub-anna", runOpt{})
	e.refused(t, w, "nicht verknuepft")

	// Verknuepfen geht nur angemeldet
	if w = webReq(e.mux, "GET", "/oidc/link/fake", "", "", nil); w.Code != 403 {
		t.Fatalf("Verknuepfen ohne Sitzung: %d", w.Code)
	}
	// Sitzungs-Cookie aus fremdem Zusammenhang (SameSite=Strict heisst: gar nicht gesendet; hier: Sec-Fetch-Site cross-site)
	atok := e.login(t, "anna")
	if w = webReq(e.mux, "GET", "/oidc/link/fake", "", atok, map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != 403 {
		t.Fatalf("Verknuepfen von fremder Seite: %d", w.Code)
	}

	// anna verknuepft: Passwort entfaellt, die alte Sitzung endet, eine neue OIDC-Sitzung beginnt
	w, _ = e.run(t, "sub-anna", runOpt{link: atok})
	tok := e.okLogin(t, w)
	u, _ := e.a.get(ctx, "anna")
	if u.Link == nil || u.Link.Sub != "sub-anna" || u.Link.Iss != e.idp.srv.URL || u.Link.Prov != "fake" || u.Hash != "" || u.Must || u.InitExp != "" {
		t.Fatalf("Konto nach dem Verknuepfen: %+v", u)
	}
	if w = webReq(e.mux, "GET", "/api/me", "", atok, nil); w.Code != 401 {
		t.Fatalf("alte Passwort-Sitzung nach dem Verknuepfen: %d", w.Code)
	}
	if w = webReq(e.mux, "GET", "/api/me", "", tok, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"oidc":"fake"`) {
		t.Fatalf("/api/me: %d %s", w.Code, w.Body.String())
	}
	// Passwort XOR Anbieter: Passwort-Anmeldung geht nicht mehr (Formular und Basic)
	if w = webReq(e.mux, "POST", "/api/login", `{"name":"anna","password":"Passwort-anna-77x"}`, "", nil); w.Code != 401 {
		t.Fatalf("Passwort nach dem Verknuepfen: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.SetBasicAuth("anna", "Passwort-anna-77x")
	ww := httptest.NewRecorder()
	e.mux.ServeHTTP(ww, r)
	if ww.Code != 401 {
		t.Fatalf("Basic nach dem Verknuepfen: %d", ww.Code)
	}
	r = httptest.NewRequest("GET", "/api/me", nil)
	r.SetBasicAuth("anna", "")
	ww = httptest.NewRecorder()
	e.mux.ServeHTTP(ww, r)
	if ww.Code != 401 {
		t.Fatalf("Basic mit leerem Passwort: %d", ww.Code)
	}

	// ab jetzt Anmeldung ueber die Kennung (iss+sub); eine andere Kennung kommt nicht hinein
	w, _ = e.run(t, "sub-anna", runOpt{})
	e.okLogin(t, w)
	w, _ = e.run(t, "sub-fremd", runOpt{})
	e.refused(t, w, "andere Kennung")

	// die Kennung gehoert hoechstens einem Konto
	etok := e.login(t, "erik")
	w, _ = e.run(t, "sub-anna", runOpt{link: etok})
	e.refused(t, w, "Kennung schon vergeben")
	if u, _ := e.a.get(ctx, "erik"); u.Link != nil || u.Hash == "" {
		t.Fatalf("erik darf unveraendert bleiben: %+v", u)
	}
	// ein bereits verknuepftes Konto kann nicht noch einmal verknuepfen
	if w = webReq(e.mux, "GET", "/oidc/link/other", "", tok, nil); w.Code != 403 {
		t.Fatalf("zweites Verknuepfen: %d", w.Code)
	}
	// Admin nie per OIDC
	rtok := e.login(t, "root")
	if w = webReq(e.mux, "GET", "/oidc/link/fake", "", rtok, nil); w.Code != 403 {
		t.Fatalf("Admin verknuepft: %d", w.Code)
	}
	// Verknuepfung mit einem anderen Anbieter (gleicher Aussteller im Test, andere sub) fuer carl
	ctok := e.login(t, "carl")
	w, _ = e.run(t, "sub-carl", runOpt{link: ctok, prov: "other"})
	e.okLogin(t, w)
	w, _ = e.run(t, "sub-carl", runOpt{prov: "other"})
	e.okLogin(t, w)
}

func TestOIDCRefusals(t *testing.T) {
	e := newOIDCEnv(t)
	ctx := context.Background()
	atok := e.login(t, "anna")
	bad := map[string]runOpt{
		"falsche Zielgruppe":   {mod: func(c map[string]any) { c["aud"] = "anderer" }},
		"abgelaufen":           {mod: func(c map[string]any) { c["exp"] = time.Now().Unix() - 3600 }},
		"falscher Aussteller":  {mod: func(c map[string]any) { c["iss"] = "https://evil.example" }},
		"falscher nonce":       {mod: func(c map[string]any) { c["nonce"] = "anderer" }},
		"kein nonce":           {mod: func(c map[string]any) { delete(c, "nonce") }},
		"kein sub":             {mod: func(c map[string]any) { c["sub"] = "" }},
		"aus der Zukunft":      {mod: func(c map[string]any) { c["iat"] = time.Now().Unix() + 3600 }},
		"mehrere aud ohne azp": {mod: func(c map[string]any) { c["aud"] = []string{"cid", "x"} }},
		"alg none":             {signer: func(e *oidcEnv, c map[string]any) string { return e.idp.sign("none", "r1", c) }},
		"HS256": {signer: func(e *oidcEnv, c map[string]any) string {
			return strings.Replace(e.idp.sign("RS256", "r1", c), b64([]byte(`{"alg":"RS256","kid":"r1","typ":"JWT"}`)), b64([]byte(`{"alg":"HS256","kid":"r1","typ":"JWT"}`)), 1)
		}},
		"fremder Schluessel": {signer: func(e *oidcEnv, c map[string]any) string {
			k, _ := rsa.GenerateKey(rand.Reader, 2048)
			old := e.idp.rsaKey
			e.idp.rsaKey = k
			defer func() { e.idp.rsaKey = old }()
			return e.idp.sign("RS256", "r1", c)
		}},
		"ES256 mit RSA-Kennung": {signer: func(e *oidcEnv, c map[string]any) string { return e.idp.sign("ES256", "r1", c) }},
		"unbekannte kid":        {signer: func(e *oidcEnv, c map[string]any) string { return e.idp.sign("RS256", "zzz", c) }},
		"verfaelschte Nutzlast": {signer: func(e *oidcEnv, c map[string]any) string {
			p := strings.Split(e.idp.sign("RS256", "r1", c), ".")
			c["sub"] = "sub-anderer"
			q := strings.Split(e.idp.sign("RS256", "r1", c), ".")
			return p[0] + "." + q[1] + "." + p[2]
		}},
		"PKCE falsch":     {badChal: true},
		"state unbekannt": {state: "gibtesnicht"},
		"Cookie fehlt":    {cookie: "-"},
		"fremdes Cookie":  {cookie: "anderer-browser"},
	}
	for name, o := range bad {
		o.link = atok
		w, _ := e.run(t, "sub-anna", o)
		e.refused(t, w, name)
	}
	// auch die Anmeldung (ohne Verknuepfen) lehnt alles ab
	for name, o := range bad {
		w, _ := e.run(t, "sub-anna", o)
		e.refused(t, w, "Anmeldung: "+name)
	}
	if u, _ := e.a.get(ctx, "anna"); u.Link != nil || u.Hash == "" {
		t.Fatalf("nach abgelehnten Versuchen bleibt das Konto unveraendert: %+v", u)
	}
	if w := webReq(e.mux, "GET", "/api/me", "", atok, nil); w.Code != 200 {
		t.Fatalf("Sitzung nach abgelehnten Versuchen: %d", w.Code)
	}

	// state ist einmalig
	w, st := e.run(t, "sub-anna", runOpt{link: atok})
	e.okLogin(t, w)
	r := httptest.NewRequest("GET", "/oidc/callback?code=good-code&state="+url.QueryEscape(st), nil)
	r.AddCookie(&http.Cookie{Name: oidcCookie, Value: st})
	w2 := httptest.NewRecorder()
	e.mux.ServeHTTP(w2, r)
	e.refused(t, w2, "Wiederholung")

	// Fehler vom Anbieter (Nutzer bricht ab)
	w = httptest.NewRecorder()
	e.mux.ServeHTTP(w, httptest.NewRequest("GET", "/oidc/start/fake", nil))
	loc, _ := url.Parse(w.Header().Get("Location"))
	r = httptest.NewRequest("GET", "/oidc/callback?error=access_denied&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	r.AddCookie(&http.Cookie{Name: oidcCookie, Value: loc.Query().Get("state")})
	w2 = httptest.NewRecorder()
	e.mux.ServeHTTP(w2, r)
	e.refused(t, w2, "Abbruch")
}

func TestOIDCReconfigureAndSessions(t *testing.T) {
	e := newOIDCEnv(t)
	ctx := context.Background()
	atok := e.login(t, "anna")
	w, _ := e.run(t, "sub-anna", runOpt{link: atok})
	tok := e.okLogin(t, w)
	etok := e.login(t, "erik")
	rtok := e.login(t, "root")

	// ein verknuepftes Konto setzt sein Passwort nicht selbst (Verknuepfung loest nur der Admin)
	if w := webReq(e.mux, "POST", "/api/me/password", `{"old":"","new":"Neues-Passwort-4711x"}`, tok, nil); w.Code != 403 {
		t.Fatalf("verknuepftes Konto setzt Passwort: %d", w.Code)
	}
	if u, _ := e.a.get(ctx, "anna"); u.Link == nil || u.Hash != "" {
		t.Fatalf("Konto muss verknuepft bleiben: %+v", u)
	}

	// ein normaler Benutzer darf nicht zuruecksetzen
	if w := webReq(e.mux, "POST", "/api/users/anna/reconfigure", `{}`, etok, nil); w.Code == 200 {
		t.Fatalf("Benutzer setzt zurueck: %d", w.Code)
	}
	// Admins nicht zuruecksetzbar (kein Initialpasswort fuer Admins)
	if w := webReq(e.mux, "POST", "/api/users/root/reconfigure", `{}`, rtok, nil); w.Code != 400 {
		t.Fatalf("Admin zuruecksetzen: %d", w.Code)
	}
	// Admin setzt zurueck: Verknuepfung weg, neues Initialpasswort, laufende OIDC-Sitzung endet sofort
	wr := webReq(e.mux, "POST", "/api/users/anna/reconfigure", `{}`, rtok, nil)
	if wr.Code != 200 {
		t.Fatalf("zuruecksetzen: %d %s", wr.Code, wr.Body.String())
	}
	var out struct{ Name, PW string }
	json.Unmarshal(wr.Body.Bytes(), &out)
	if out.Name != "anna" || len(out.PW) < 10 {
		t.Fatalf("Antwort: %s", wr.Body.String())
	}
	u, _ := e.a.get(ctx, "anna")
	if u.Link != nil || !u.Must || u.InitExp == "" || u.TOTP != "" || pwState(u) != "init" {
		t.Fatalf("Konto nach dem Zuruecksetzen: %+v", u)
	}
	if strings.Contains(u.Hash, out.PW) || u.Hash == "" {
		t.Fatal("nur der Abdruck darf gespeichert sein")
	}
	if w := webReq(e.mux, "GET", "/api/me", "", tok, nil); w.Code != 401 {
		t.Fatalf("OIDC-Sitzung nach dem Zuruecksetzen: %d", w.Code)
	}
	// die alte Kennung kommt nicht mehr hinein, das Initialpasswort schon
	w, _ = e.run(t, "sub-anna", runOpt{})
	e.refused(t, w, "alte Kennung")
	if w := webReq(e.mux, "POST", "/api/login", `{"name":"anna","password":"Passwort-anna-77x"}`, "", nil); w.Code != 401 {
		t.Fatalf("altes Passwort: %d", w.Code)
	}
	nt := cookieOf(webReq(e.mux, "POST", "/api/login", `{"name":"anna","password":"`+out.PW+`"}`, "", nil))
	if nt == "" {
		t.Fatal("Initialpasswort funktioniert nicht")
	}
	// abgelaufenes Initialpasswort sperrt das Konto bis zum naechsten
	e.a.mutate(ctx, func(m map[string]Account) error {
		x := m["anna"]
		x.InitExp = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
		m["anna"] = x
		return nil
	})
	if w := webReq(e.mux, "GET", "/api/me", "", nt, nil); w.Code != 401 {
		t.Fatalf("abgelaufenes Initialpasswort: %d", w.Code)
	}
	if u, _ := e.a.get(ctx, "anna"); pwState(u) != "expired" {
		t.Fatalf("Zustand: %s", pwState(u))
	}

	// gesperrtes Konto: laufende OIDC-Sitzung endet, Anmeldung abgelehnt
	ctok := e.login(t, "carl")
	w, _ = e.run(t, "sub-carl", runOpt{link: ctok})
	otok := e.okLogin(t, w)
	dis := true
	if err := e.a.SetFlags(ctx, "carl", nil, &dis); err != nil {
		t.Fatal(err)
	}
	if w := webReq(e.mux, "GET", "/api/me", "", otok, nil); w.Code != 401 {
		t.Fatalf("gesperrt: %d", w.Code)
	}
	w, _ = e.run(t, "sub-carl", runOpt{})
	e.refused(t, w, "gesperrt")
	// wird ein OIDC-Konto spaeter zum Admin, enden seine OIDC-Sitzungen und die Anmeldung ist verwehrt
	dis = false
	e.a.SetFlags(ctx, "carl", nil, &dis)
	w, _ = e.run(t, "sub-carl", runOpt{})
	otok = e.okLogin(t, w)
	adm := true
	e.a.SetFlags(ctx, "carl", &adm, nil)
	if w := webReq(e.mux, "GET", "/api/me", "", otok, nil); w.Code != 401 {
		t.Fatalf("OIDC-Sitzung eines neuen Admins: %d", w.Code)
	}
	w, _ = e.run(t, "sub-carl", runOpt{})
	e.refused(t, w, "neuer Admin")
}
