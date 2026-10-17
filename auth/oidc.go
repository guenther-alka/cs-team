package auth

// OIDC (0.63): Anmeldung ueber Google, Microsoft oder einen eigenen OpenID-Connect-Anbieter als ALTERNATIVE zum Passwort.
// Es gibt keinen Abgleich ueber E-Mail-Adressen und der Anbieter legt nie ein Konto an. Der Benutzer meldet sich einmal mit dem
// Initialpasswort an und verknuepft sein Konto dann ANGEMELDET (/oidc/link/{id}) mit Aussteller + sub; danach hat das Konto
// kein Passwort mehr (Passwort XOR Anbieter) und nur noch diese Kennung zaehlt. Nur der Admin loest die Verknuepfung
// (Zuruecksetzen). Admins melden sich nie so an. Ablauf: Authorization Code mit PKCE (S256), state (an den Browser gebunden),
// nonce; das id_token wird selbst geprueft (RS256/ES256, iss, aud, exp, nonce). Nur Standardbibliothek.

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoOIDCProv = errors.New("unknown OIDC provider")
	ErrOIDCAdmin  = errors.New("the sysadmin and directory accounts sign in locally, not with OIDC")
)

// OIDCLink: feste Verknuepfung eines Kontos mit der Kennung beim Anbieter (Aussteller + sub).
type OIDCLink struct {
	Prov string `json:"prov"`
	Iss  string `json:"iss"`
	Sub  string `json:"sub"`
	At   string `json:"at,omitempty"`
}

// OIDCProvider: ein eingerichteter Anbieter (Startparameter CS_OIDC_*, siehe OIDCFromEnv).
type OIDCProvider struct {
	ID, Name, Issuer, ClientID, Secret, Scope string

	mu      sync.Mutex
	disc    *oidcDisc
	discAt  time.Time
	keys    map[string]crypto.PublicKey
	keysAt  time.Time
	keysTry time.Time
}

type oidcDisc struct{ Issuer, AuthURL, TokenURL, JWKSURL string }

var oidcCfg struct {
	base  string
	provs []*OIDCProvider // aus den Startparametern
	gui   []*OIDCProvider // aus den Einstellungen (oidcgui.go), hinter oidcGuiMu
}

var (
	oidcAllowHTTP bool // nur Tests: http-Adressen des Anbieters zulassen
	oidcClient    = &http.Client{Timeout: 10 * time.Second}
	oidcMu        sync.Mutex
	oidcStates    = map[string]oidcState{}
)

const oidcCookie = "cs_oidc"

type oidcState struct {
	prov, nonce, verifier string
	link                  string // Konto, das verknuepft werden soll (leer: Anmeldung)
	exp                   time.Time
}

// ConfigureOIDC setzt Basisadresse (oeffentliche Adresse dieses Servers) und Anbieter.
func ConfigureOIDC(base string, provs []*OIDCProvider) {
	oidcCfg.base, oidcCfg.provs = strings.TrimRight(base, "/"), provs
}

func oidcProv(id string) *OIDCProvider {
	for _, p := range oidcProvList() {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// OIDCFromEnv liest die Anbieter aus den Startparametern:
//
//	CS_OIDC_BASE=https://team.example.org       oeffentliche Adresse (Rueckkehradresse = BASE/oidc/callback)
//	CS_OIDC=google,ms,keycloak                  Kennungen der Anbieter
//	CS_OIDC_<ID>_CLIENT_ID / _CLIENT_SECRET     beim Anbieter registrierte Anwendung
//	CS_OIDC_<ID>_ISSUER                         Aussteller (google: voreingestellt; ms: aus _TENANT; sonst Pflicht)
//	CS_OIDC_MS_TENANT=<Mandanten-ID>            Microsoft nur mit fester Mandanten-ID (nicht common/organizations/consumers)
//	CS_OIDC_<ID>_NAME, _SCOPE                    Anzeigename, Umfang (Vorgabe "openid": es wird keine Adresse gebraucht)
func OIDCFromEnv(env func(string) string, logf func(string, ...any)) {
	ids := strings.Split(env("CS_OIDC"), ",")
	var provs []*OIDCProvider
	base := strings.TrimRight(strings.TrimSpace(env("CS_OIDC_BASE")), "/")
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if !LoginForm {
			logf("oidc: CS_OIDC needs CS_LOGIN_FORM=1 - OIDC is off")
			return
		}
		if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://localhost") && !strings.HasPrefix(base, "http://127.0.0.1") {
			logf("oidc: CS_OIDC_BASE must be the public https address of this server - OIDC is off")
			return
		}
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,15}$`).MatchString(id) {
			logf("oidc: bad provider id %q", id)
			continue
		}
		key := "CS_OIDC_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_"
		p := &OIDCProvider{ID: id, Name: env(key + "NAME"), Issuer: env(key + "ISSUER"), ClientID: env(key + "CLIENT_ID"),
			Secret: env(key + "CLIENT_SECRET"), Scope: env(key + "SCOPE")}
		switch id {
		case "google":
			if p.Issuer == "" {
				p.Issuer = "https://accounts.google.com"
			}
			if p.Name == "" {
				p.Name = "Google"
			}
		case "ms":
			t := strings.TrimSpace(env(key + "TENANT"))
			if p.Issuer == "" {
				if t == "" || t == "common" || t == "organizations" || t == "consumers" {
					logf("oidc: ms needs CS_OIDC_MS_TENANT=<fixed tenant id> (not common/organizations/consumers) - provider off")
					continue
				}
				p.Issuer = "https://login.microsoftonline.com/" + t + "/v2.0"
			}
			if p.Name == "" {
				p.Name = "Microsoft"
			}
		}
		if p.Name == "" {
			p.Name = id
		}
		if p.Scope == "" {
			p.Scope = "openid"
		}
		if p.Issuer == "" || p.ClientID == "" || p.Secret == "" {
			logf("oidc: provider %s needs ISSUER, CLIENT_ID and CLIENT_SECRET - provider off", id)
			continue
		}
		provs = append(provs, p)
		logf("oidc: provider %s (%s)", id, p.Issuer)
	}
	if len(provs) > 0 {
		ConfigureOIDC(base, provs)
	}
}

// ---------- Anbieter abfragen ----------

func oidcCheckURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && oidcAllowHTTP)) {
		return errors.New("bad provider address")
	}
	return nil
}

func oidcGetJSON(ctx context.Context, s string, v any) error {
	if err := oidcCheckURL(s); err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", s, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := oidcClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

func sameIss(a, b string) bool {
	n := func(s string) string { return strings.TrimPrefix(strings.TrimRight(s, "/"), "https://") }
	return n(a) == n(b)
}

func (p *OIDCProvider) discover(ctx context.Context) (*oidcDisc, error) {
	p.mu.Lock()
	if p.disc != nil && time.Since(p.discAt) < time.Hour {
		d := p.disc
		p.mu.Unlock()
		return d, nil
	}
	p.mu.Unlock()
	var raw struct {
		Issuer string `json:"issuer"`
		Auth   string `json:"authorization_endpoint"`
		Token  string `json:"token_endpoint"`
		JWKS   string `json:"jwks_uri"`
	}
	if err := oidcGetJSON(ctx, strings.TrimRight(p.Issuer, "/")+"/.well-known/openid-configuration", &raw); err != nil {
		return nil, err
	}
	if !sameIss(raw.Issuer, p.Issuer) {
		return nil, errors.New("issuer mismatch in discovery")
	}
	for _, s := range []string{raw.Auth, raw.Token, raw.JWKS} {
		if err := oidcCheckURL(s); err != nil {
			return nil, err
		}
	}
	d := &oidcDisc{Issuer: p.Issuer, AuthURL: raw.Auth, TokenURL: raw.Token, JWKSURL: raw.JWKS}
	p.mu.Lock()
	p.disc, p.discAt = d, time.Now()
	p.mu.Unlock()
	return d, nil
}

func b64u(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

func parseJWKS(raw []byte) map[string]crypto.PublicKey {
	var set struct {
		Keys []struct {
			Kty, Kid, Crv, N, E, X, Y string
		} `json:"keys"`
	}
	out := map[string]crypto.PublicKey{}
	if json.Unmarshal(raw, &set) != nil {
		return out
	}
	for i, k := range set.Keys {
		id := k.Kid
		if id == "" {
			id = fmt.Sprint("#", i)
		}
		switch k.Kty {
		case "RSA":
			n, e := new(big.Int), new(big.Int)
			nb, err1 := b64u(k.N)
			eb, err2 := b64u(k.E)
			if err1 != nil || err2 != nil {
				continue
			}
			n.SetBytes(nb)
			e.SetBytes(eb)
			if n.BitLen() < 2048 || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 {
				continue
			}
			out[id] = &rsa.PublicKey{N: n, E: int(e.Int64())}
		case "EC":
			if k.Crv != "P-256" {
				continue
			}
			xb, err1 := b64u(k.X)
			yb, err2 := b64u(k.Y)
			if err1 != nil || err2 != nil {
				continue
			}
			pk := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
			if !pk.Curve.IsOnCurve(pk.X, pk.Y) {
				continue
			}
			out[id] = pk
		}
	}
	return out
}

// key: Schluessel zur kid; unbekannte kid loest hoechstens einmal je Minute einen neuen Abruf aus.
func (p *OIDCProvider) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	find := func() (crypto.PublicKey, bool) {
		if k, ok := p.keys[kid]; ok && kid != "" {
			return k, true
		}
		if kid == "" && len(p.keys) == 1 {
			for _, k := range p.keys {
				return k, true
			}
		}
		return nil, false
	}
	p.mu.Lock()
	k, ok := find()
	stale := time.Since(p.keysAt) > 24*time.Hour
	retry := time.Since(p.keysTry) > time.Minute
	p.mu.Unlock()
	if ok && !stale {
		return k, nil
	}
	if !retry && ok {
		return k, nil
	}
	if !retry {
		return nil, errors.New("unknown signing key")
	}
	d, err := p.discover(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.keysTry = time.Now()
	p.mu.Unlock()
	if err := oidcCheckURL(d.JWKSURL); err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", d.JWKSURL, nil)
	resp, err := oidcClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("jwks HTTP %d", resp.StatusCode)
	}
	keys := parseJWKS(raw)
	p.mu.Lock()
	p.keys, p.keysAt = keys, time.Now()
	k, ok = find()
	p.mu.Unlock()
	if !ok {
		return nil, errors.New("unknown signing key")
	}
	return k, nil
}

// ---------- id_token pruefen ----------

type audList []string

func (a *audList) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*a = audList{s}
		return nil
	}
	var l []string
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	*a = l
	return nil
}

type oidcClaims struct {
	Iss   string  `json:"iss"`
	Sub   string  `json:"sub"`
	Aud   audList `json:"aud"`
	Azp   string  `json:"azp"`
	Exp   float64 `json:"exp"`
	Iat   float64 `json:"iat"`
	Nonce string  `json:"nonce"`
}

func (p *OIDCProvider) verifyIDToken(ctx context.Context, tok, nonce string) (oidcClaims, error) {
	var c oidcClaims
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || len(tok) > 16384 {
		return c, errors.New("malformed id_token")
	}
	hb, err := b64u(parts[0])
	if err != nil {
		return c, errors.New("bad header")
	}
	var h struct{ Alg, Kid string }
	if json.Unmarshal(hb, &h) != nil || (h.Alg != "RS256" && h.Alg != "ES256") { // kein "none", kein HS256
		return c, errors.New("unsupported algorithm")
	}
	pub, err := p.key(ctx, h.Kid)
	if err != nil {
		return c, err
	}
	sig, err := b64u(parts[2])
	if err != nil {
		return c, errors.New("bad signature")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch k := pub.(type) {
	case *rsa.PublicKey:
		if h.Alg != "RS256" || rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], sig) != nil {
			return c, errors.New("signature invalid")
		}
	case *ecdsa.PublicKey:
		if h.Alg != "ES256" || len(sig) != 64 || !ecdsa.Verify(k, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return c, errors.New("signature invalid")
		}
	default:
		return c, errors.New("key type")
	}
	pb, err := b64u(parts[1])
	if err != nil || json.Unmarshal(pb, &c) != nil {
		return c, errors.New("bad payload")
	}
	now := float64(time.Now().Unix())
	switch {
	case !sameIss(c.Iss, p.Issuer):
		return c, errors.New("issuer mismatch")
	case !containsStr(c.Aud, p.ClientID) || (len(c.Aud) > 1 && c.Azp != p.ClientID):
		return c, errors.New("audience mismatch")
	case c.Exp == 0 || now > c.Exp+60:
		return c, errors.New("id_token expired")
	case c.Iat > now+300:
		return c, errors.New("id_token from the future")
	case c.Sub == "" || len(c.Sub) > 255:
		return c, errors.New("no subject")
	case nonce == "" || subtle.ConstantTimeCompare([]byte(c.Nonce), []byte(nonce)) != 1:
		return c, errors.New("nonce mismatch")
	}
	return c, nil
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func (p *OIDCProvider) exchange(ctx context.Context, code, verifier string) (string, error) {
	d, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oidcBase() + "/oidc/callback"},
		"client_id": {p.ClientID}, "client_secret": {p.Secret}, "code_verifier": {verifier}}
	req, _ := http.NewRequestWithContext(ctx, "POST", d.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := oidcClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tr struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr)
	if resp.StatusCode != 200 || tr.IDToken == "" {
		return "", fmt.Errorf("token endpoint: HTTP %d %s", resp.StatusCode, safeName(tr.Error))
	}
	return tr.IDToken, nil
}

// ---------- Konto zuordnen ----------

func linkProv(u Account) string {
	if u.Link != nil {
		return u.Link.Prov
	}
	return ""
}

// oidcAccount: Konto zur bestaetigten Kennung (iss+sub). Gefunden wird nur ueber die feste Verknuepfung, nie ueber eine
// Adresse: wer sich nie angemeldet und verknuepft hat, hat hier kein Konto.
func (a *Auth) oidcAccount(ctx context.Context, p *OIDCProvider, c oidcClaims) (string, Account, error) {
	a.refresh(ctx)
	a.mu.Lock()
	key := ""
	for k, u := range a.users {
		if u.Link != nil && u.Link.Iss == p.Issuer && u.Link.Sub == c.Sub {
			key = k
			break
		}
	}
	u := a.users[key]
	a.mu.Unlock()
	switch {
	case key == "":
		return "", Account{}, errNotLinked
	case u.Disabled:
		return "", Account{}, errors.New("account disabled")
	case u.Sys || u.Source == "dir":
		return "", Account{}, ErrOIDCAdmin
	}
	return key, u, nil
}

var errNotLinked = errors.New("identity not linked to an account")

// bindOIDC: das angemeldete Konto key mit der Kennung verknuepfen (Passwort XOR Anbieter): das Passwort entfaellt,
// ebenso Initialpasswort, Zwei-Faktor und App-Passwoerter. Eine Kennung gehoert hoechstens einem Konto.
func (a *Auth) bindOIDC(ctx context.Context, key string, p *OIDCProvider, c oidcClaims) error {
	err := a.mutate(ctx, func(m map[string]Account) error {
		x, ok := m[key]
		switch {
		case !ok:
			return ErrNoUser
		case x.Disabled:
			return errors.New("account disabled")
		case x.Sys || x.Source == "dir":
			return ErrOIDCAdmin
		case x.Link != nil:
			return errors.New("account already linked")
		}
		for k, o := range m {
			if k != key && o.Link != nil && o.Link.Iss == p.Issuer && o.Link.Sub == c.Sub {
				return errors.New("identity already linked to another account")
			}
		}
		x = clear2FA(x)
		x.Hash, x.Must, x.InitExp = "", false, ""
		x.Link = &OIDCLink{Prov: p.ID, Iss: p.Issuer, Sub: c.Sub, At: time.Now().UTC().Format(time.RFC3339)}
		x.Reset, x.ResetExp = "", ""
		m[key] = x
		return nil
	})
	if err == nil {
		log.Printf("auth: account %q linked to provider %s", safeName(key), p.ID)
	}
	return err
}

// UnlinkOIDC: Verknuepfung loesen (z.B. beim Zuruecksetzen). Das Konto hat danach kein Passwort mehr, bis ein
// Initialpasswort gesetzt wird (initpw.go).
func (a *Auth) UnlinkOIDC(ctx context.Context, name string) error {
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		u.Link = nil
		m[name] = u
		return nil
	})
}

// ---------- Strecken ----------

func oidcFail(w http.ResponseWriter, r *http.Request, why string) {
	logLimited("auth: oidc login refused: %s ip=%s", safeName(why), safeName(r.RemoteAddr))
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: "", Path: "/oidc", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/?login=failed", http.StatusFound)
}

func randTok(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// oidcBegin: Zustand anlegen, an den Browser binden (Cookie) und zum Anbieter weiterleiten. link: Konto, das verknuepft werden soll.
func oidcBegin(w http.ResponseWriter, r *http.Request, p *OIDCProvider, link string) {
	d, err := p.discover(r.Context())
	if err != nil {
		logLimited("auth: oidc provider %s unavailable: %s", p.ID, safeName(err.Error()))
		http.Error(w, "identity provider not reachable", http.StatusBadGateway)
		return
	}
	st, nonce, ver := randTok(24), randTok(24), randTok(32)
	sum := sha256.Sum256([]byte(ver))
	now := time.Now()
	oidcMu.Lock()
	if len(oidcStates) > 2000 {
		for k, v := range oidcStates {
			if now.After(v.exp) {
				delete(oidcStates, k)
			}
		}
		if len(oidcStates) > 2000 {
			oidcStates = map[string]oidcState{}
		}
	}
	oidcStates[st] = oidcState{prov: p.ID, nonce: nonce, verifier: ver, link: link, exp: now.Add(10 * time.Minute)}
	oidcMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: st, Path: "/oidc", MaxAge: 600, HttpOnly: true,
		Secure: strings.HasPrefix(oidcBase(), "https://"), SameSite: http.SameSiteLaxMode})
	u, _ := url.Parse(d.AuthURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", oidcBase()+"/oidc/callback")
	q.Set("scope", p.Scope)
	q.Set("state", st)
	q.Set("nonce", nonce)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	q.Set("code_challenge_method", "S256")
	q.Set("prompt", "select_account")
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (a *Auth) oidcRoutes(mux *http.ServeMux) {
	a.oidcGuiRoutes(mux) // Einstellungen: Google und Microsoft (oidcgui.go)
	mux.HandleFunc("GET /api/login/info", func(w http.ResponseWriter, r *http.Request) {
		type pr struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		out := struct {
			Form   bool `json:"form"`
			Forgot bool `json:"forgot"` // "Passwort vergessen" ist moeglich (Mailversand und Adresse eingerichtet)
			OIDC   []pr `json:"oidc"`
		}{Form: LoginForm, Forgot: resetEnabled(), OIDC: []pr{}}
		if LoginForm {
			for _, p := range oidcProvList() {
				out.OIDC = append(out.OIDC, pr{p.ID, p.Name})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})

	// Anmelden mit einem bereits verknuepften Konto
	mux.HandleFunc("GET /oidc/start/{id}", func(w http.ResponseWriter, r *http.Request) {
		p := oidcProv(r.PathValue("id"))
		if !LoginForm || p == nil {
			http.NotFound(w, r)
			return
		}
		oidcBegin(w, r, p, "")
	})

	// Verknuepfen: nur angemeldet (Sitzungs-Cookie, SameSite=Strict: ein Aufruf von fremder Seite traegt es nicht).
	// Das Konto wird hier in den Zustand geschrieben, weil der Rueckweg (Callback) eine fremde Navigation ist.
	mux.HandleFunc("GET /oidc/link/{id}", func(w http.ResponseWriter, r *http.Request) {
		p := oidcProv(r.PathValue("id"))
		if !LoginForm || p == nil {
			http.NotFound(w, r)
			return
		}
		key, u, ok := a.webUser(r)
		if !ok || u.Disabled || u.Sys || u.Source == "dir" || u.Link != nil || !sameOriginStrict(r) || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		oidcBegin(w, r, p, key)
	})

	mux.HandleFunc("GET /oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		if !LoginForm {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		state := q.Get("state")
		oidcMu.Lock()
		st, found := oidcStates[state]
		delete(oidcStates, state) // einmalig
		oidcMu.Unlock()
		c, cerr := r.Cookie(oidcCookie)
		switch {
		case !found || time.Now().After(st.exp):
			oidcFail(w, r, "unknown or expired state")
			return
		case cerr != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1:
			oidcFail(w, r, "state not bound to this browser")
			return
		case q.Get("error") != "" || q.Get("code") == "" || len(q.Get("code")) > 2048:
			oidcFail(w, r, "provider error "+q.Get("error"))
			return
		}
		p := oidcProv(st.prov)
		if p == nil {
			oidcFail(w, r, "unknown provider")
			return
		}
		ctx := r.Context()
		tok, err := p.exchange(ctx, q.Get("code"), st.verifier)
		if err != nil {
			oidcFail(w, r, err.Error())
			return
		}
		claims, err := p.verifyIDToken(ctx, tok, st.nonce)
		if err != nil {
			oidcFail(w, r, err.Error())
			return
		}
		key, linking := st.link, st.link != ""
		if linking {
			if err := a.bindOIDC(ctx, key, p, claims); err != nil {
				oidcFail(w, r, "link: "+err.Error())
				return
			}
		}
		var u Account
		if linking {
			var have bool
			if u, have = a.get(ctx, key); !have {
				oidcFail(w, r, "link check failed")
				return
			}
		} else if key, u, err = a.oidcAccount(ctx, p, claims); err != nil {
			oidcFail(w, r, "account: "+err.Error())
			return
		}
		http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: "", Path: "/oidc", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		a.webStart(w, r, key, u, true) // neue Sitzung (beendet die alte, Passwort-Sitzungen ungueltig durch geaenderten Hash)
		logLimited("auth: oidc login ok user=%q provider=%s linked=%v", safeName(key), p.ID, linking)
		http.Redirect(w, r, "/", http.StatusFound)
	})
}
