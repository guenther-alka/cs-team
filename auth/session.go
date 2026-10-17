package auth

// Anmeldeformular mit Sitzungs-Cookie (0.63). Aus (Vorgabe): nur HTTP Basic Auth wie bisher. Mit CS_LOGIN_FORM=1 zeigt die
// Oberflaeche ein Formular (POST /api/login), der Server vergibt ein Cookie (HttpOnly, SameSite=Strict, bei HTTPS Secure).
// WebDAV, CalDAV und Apps bleiben bei Basic Auth bzw. App-Passwoertern: sie senden keine Browser-Header und bekommen weiter
// die Basic-Abfrage. Die Pruefung der Zugangsdaten selbst (Sperren, 2FA, Verzeichnis) macht unveraendert Wrap.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LoginForm: Anmeldung ueber das Formular einschalten (von main aus CS_LOGIN_FORM gesetzt).
var LoginForm bool

const (
	cookieName = "cs_session"
	webIdle    = 12 * time.Hour     // ohne Anfrage so lange, dann ist die Sitzung weg
	webAbs     = 7 * 24 * time.Hour // laengste Lebensdauer, danach neue Anmeldung
	maxWeb     = 4096               // Sitzungen im Speicher
)

// webSess: eine Browser-Sitzung. h bindet sie an Passwort und zweiten Faktor des Kontos: wer eines davon aendert,
// meldet damit alle seine Sitzungen ab.
type webSess struct {
	user string
	h    [32]byte
	idle time.Time
	abs  time.Time
	oidc bool // über OIDC angemeldet: kürzere Laufzeit, endet mit der Verknüpfung
}

func webHash(u Account) [32]byte { return sha256.Sum256([]byte("web\x00" + u.Hash + "\x00" + u.TOTP)) }

func tokKey(tok string) [32]byte { return sha256.Sum256([]byte("cs-web\x00" + tok)) }

// sameOriginStrict: wie sameOrigin, aber fuer Cookie-Anmeldung muss der Browser "gleicher Ursprung" belegen
// (Sec-Fetch-Site oder passender Origin); fehlen beide Header, wird abgelehnt.
func sameOriginStrict(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" {
		return s == "same-origin"
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	return false
}

// browserReq: Anfrage eines Browsers (sendet Sec-Fetch-Mode). WebDAV-/CalDAV-Clients und curl tun das nicht.
func browserReq(r *http.Request) bool { return r.Header.Get("Sec-Fetch-Mode") != "" }

// challenge: Basic-Abfrage bei 401. Mit Formular-Anmeldung bekommen Browser keine (sonst oeffnet er seinen Dialog) und
// stattdessen X-Login-Form; die Oberflaeche zeigt dann das Formular.
func (a *Auth) challenge(w http.ResponseWriter, r *http.Request) {
	if LoginForm && browserReq(r) {
		w.Header().Set("X-Login-Form", "1")
		return
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="cs-team"`)
}

func (a *Auth) secureCookie(r *http.Request) bool {
	return r.TLS != nil || (a.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

// webStart legt eine Sitzung fuer user an und setzt das Cookie.
func (a *Auth) webStart(w http.ResponseWriter, r *http.Request, user string, u Account, oidc bool) {
	b := make([]byte, 32)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	a.mu.Lock()
	if len(a.web) >= maxWeb {
		for k, e := range a.web {
			if now.After(e.idle) || now.After(e.abs) {
				delete(a.web, k)
			}
		}
		if len(a.web) >= maxWeb { // alles voll mit gueltigen: die aeltesten fliegen raus (hier einfach alle)
			a.web = map[[32]byte]webSess{}
		}
	}
	idle, abs := webIdle, webAbs
	if oidc { // Entzug beim Anbieter wirkt so spätestens nach einem Tag
		idle, abs = 8*time.Hour, 24*time.Hour
	}
	a.web[tokKey(tok)] = webSess{user: user, h: webHash(u), idle: now.Add(idle), abs: now.Add(abs), oidc: oidc}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: a.secureCookie(r),
		SameSite: http.SameSiteStrictMode, MaxAge: int(abs / time.Second)})
}

// webUser: Konto zur Sitzung im Cookie. Gesperrte, geloeschte oder (Passwort/2FA) geaenderte Konten gelten nicht mehr.
func (a *Auth) webUser(r *http.Request) (string, Account, bool) {
	if !LoginForm {
		return "", Account{}, false
	}
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" || len(c.Value) > 64 {
		return "", Account{}, false
	}
	k := tokKey(c.Value)
	now := time.Now()
	a.mu.Lock()
	e, ok := a.web[k]
	if ok && (now.After(e.idle) || now.After(e.abs)) {
		delete(a.web, k)
		ok = false
	}
	a.mu.Unlock()
	if !ok {
		return "", Account{}, false
	}
	u, have := a.get(r.Context(), e.user)
	if !have || u.Disabled || e.h != webHash(u) || (e.oidc && (u.Admin || u.Link == nil)) {
		a.mu.Lock()
		delete(a.web, k)
		a.mu.Unlock()
		return "", Account{}, false
	}
	a.mu.Lock()
	if cur, still := a.web[k]; still && now.Add(webIdle).Sub(cur.idle) > time.Minute { // Leerlaufzeit verlaengern, nicht bei jeder Anfrage schreiben
		cur.idle = now.Add(webIdle)
		if cur.oidc {
			cur.idle = now.Add(8 * time.Hour)
		}
		a.web[k] = cur
	}
	a.mu.Unlock()
	return e.user, u, true
}

func (a *Auth) webDrop(r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		a.mu.Lock()
		delete(a.web, tokKey(c.Value))
		a.mu.Unlock()
	}
}

// renewWeb: nach einer Passwortaenderung ist die Sitzung ungueltig (webHash). Wer mit Cookie angemeldet war, bekommt eine neue,
// damit er nicht sofort wieder das Formular sieht.
func (a *Auth) renewWeb(w http.ResponseWriter, r *http.Request, user string) {
	if !LoginForm {
		return
	}
	if _, err := r.Cookie(cookieName); err != nil {
		return
	}
	a.webDrop(r)
	if u, ok := a.get(r.Context(), user); ok {
		a.webStart(w, r, user, u, false)
	}
}

// dropWeb: alle Browser-Sitzungen eines Kontos verwerfen.
func (a *Auth) dropWeb(user string) {
	a.mu.Lock()
	for k, e := range a.web {
		if e.user == user {
			delete(a.web, k)
		}
	}
	a.mu.Unlock()
}

// Public: statische Dateien der Oberflaeche (HTML/JS/CSS) brauchen bei Formular-Anmeldung keine Anmeldung - sonst
// koennte das Formular selbst nicht geladen werden. Sie enthalten keine Daten. Alles andere geht durch Wrap.
func (a *Auth) Public(static http.Handler) http.Handler {
	prot := a.Wrap(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if LoginForm && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			static.ServeHTTP(w, r)
			return
		}
		prot.ServeHTTP(w, r)
	})
}

// loginRoutes: POST /api/login (Name, Passwort, optional Code) und POST /api/logout.
func (a *Auth) loginRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		if !LoginForm {
			http.NotFound(w, r)
			return
		}
		if !sameOriginStrict(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		var in struct{ Name, Password, Code string }
		if !body(w, r, &in) {
			return
		}
		pw := in.Password
		if c := strings.TrimSpace(in.Code); c != "" {
			pw += c // Passwortfeld = Passwort + Code (splitCode in twofa.go)
		}
		// Die Pruefung ist die von Basic Auth (Sperren, 2FA, Verzeichnis). Als Pfad dient /api/me: dort greifen weder die
		// Passwortpflicht noch die 2FA-Einrichtung, und App-Passwoerter sind dort nicht erlaubt (sie eroeffnen keine Sitzung).
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/api/me"
		r2.Header.Del("Authorization")
		r2.SetBasicAuth(in.Name, pw)
		a.Wrap(http.HandlerFunc(func(w2 http.ResponseWriter, r2 *http.Request) {
			me := User(r2.Context())
			u, _ := a.get(r2.Context(), me)
			a.webStart(w2, r2, me, u, false)
			w2.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w2).Encode(map[string]any{"ok": true, "must": u.Must})
		})).ServeHTTP(w, r2)
	})
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginStrict(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		a.webDrop(r)
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.secureCookie(r),
			SameSite: http.SameSiteStrictMode, MaxAge: -1})
	})
}
