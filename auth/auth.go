// Package auth: Benutzerverwaltung + HTTP Basic Auth gegen users/users.json (bcrypt) im Bucket.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"cs-team/store"
)

// ForceChange: Startpasswörter (Admin/Import) müssen beim ersten Login geändert werden. Nur Tests schalten ab.
var ForceChange = true

const (
	usersKey     = "users/users.json"
	cacheTTL     = 30 * time.Second
	maxFailsIP   = 20  // Fehlversuche je Adresse (alle Namen) ...
	maxFailsUser = 100 // ... und je Benutzername (alle Adressen), dann jeweils lockFor gesperrt
	authTTL      = 45 * time.Second
	maxCache     = 4096
	maxFails     = 5               // Fehlversuche je Benutzer+IP ...
	lockFor      = 5 * time.Minute // ... dann gesperrt
	minPass      = 8
	maxPass      = 72 // bcrypt-Limit
)

var (
	ErrExists   = errors.New("user exists")
	ErrNoUser   = errors.New("no such user")
	ErrLastAdm  = errors.New("at least one enabled admin must remain")
	ErrBadName  = errors.New("user: a-z 0-9 . _ - (max 32)")
	ErrBadPass  = errors.New("password: 8..72 bytes")
	validName   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	errLockedIP = errors.New("locked")
)

type ctxKey struct{}
type ctxAdmin struct{}
type ctxAreas struct{}
type ctxWAreas struct{}
type ctxAdminOf struct{}

type Account struct {
	Hash     string   `json:"hash"`
	Admin    bool     `json:"admin,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
	Created  string   `json:"created,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	Must     bool     `json:"must,omitempty"` // Passwort muss beim nächsten Login geändert werden
	Lang     string   `json:"lang,omitempty"` // Oberflächensprache (leer = Browser/Serverstandard)
	Mail     string   `json:"mail,omitempty"` // externe E-Mail-Adresse (für Nachrichten)
	Chat     string   `json:"chat,omitempty"` // externe Chat-Adresse (URL, z.B. Webhook/ntfy)
}

// Altformat der Version 0.1: "name": "<hash>"
func (u *Account) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &u.Hash)
	}
	type plain Account
	return json.Unmarshal(b, (*plain)(u))
}

type fail struct {
	n     int
	until time.Time
	last  time.Time
}

type Auth struct {
	st         store.Store
	TrustProxy bool // X-Forwarded-For für die Client-IP verwenden (nur hinter eigenem Proxy!)

	mu     sync.Mutex
	users  map[string]Account
	groups map[string]Group
	load   time.Time
	fails  map[string]*fail
	dummy  []byte
	cache  map[[32]byte]cacheEnt // erfolgreiche Anmeldungen (kurz), spart bcrypt je Anfrage
	salt   [16]byte
}

var std *Auth // zuletzt erzeugte Instanz: Freigabe-Prüfung (Allowed) braucht die Gruppen eines Benutzers

func New(st store.Store) *Auth {
	d, _ := bcrypt.GenerateFromPassword([]byte("dummy"), bcrypt.DefaultCost)
	a := &Auth{st: st, fails: map[string]*fail{}, dummy: d, cache: map[[32]byte]cacheEnt{}}
	rand.Read(a.salt[:])
	std = a
	return a
}

// ---------- Laden / Ändern ----------

func (a *Auth) invalidate() { a.mu.Lock(); a.load = time.Time{}; a.mu.Unlock() }

func (a *Auth) refresh(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.load) < cacheTTL {
		return
	}
	m := map[string]Account{}
	b, _, err := a.st.Get(ctx, usersKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return
	case json.Unmarshal(b, &m) != nil:
		return
	}
	g, ok := a.loadGroups(ctx)
	if !ok {
		return
	}
	a.users, a.groups, a.load = m, g, time.Now()
}

func (a *Auth) get(ctx context.Context, name string) (Account, bool) {
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	u, ok := a.users[name]
	return u, ok
}

// mutate: ETag-gesichertes Read-Modify-Write von users.json.
func (a *Auth) mutate(ctx context.Context, fn func(m map[string]Account) error) error {
	err := store.Update(ctx, a.st, usersKey, func(cur []byte) ([]byte, error) {
		m := map[string]Account{}
		if cur != nil {
			if err := json.Unmarshal(cur, &m); err != nil {
				return nil, err
			}
		}
		if err := fn(m); err != nil {
			return nil, err
		}
		return json.Marshal(m)
	})
	a.invalidate()
	return err
}

func hasAdmin(m map[string]Account) bool {
	for _, u := range m {
		if u.Admin && !u.Disabled {
			return true
		}
	}
	return false
}

func hash(pass string) (string, error) {
	if len(pass) < minPass || len(pass) > maxPass {
		return "", ErrBadPass
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	return string(h), err
}

// Bootstrap legt den ersten Admin an, falls es noch keinen Benutzer gibt.
func (a *Auth) Bootstrap(ctx context.Context, user, pass string) error {
	return a.mutate(ctx, func(m map[string]Account) error {
		if len(m) > 0 {
			return nil
		}
		return addTo(m, user, pass, true, nil)
	})
}

func addTo(m map[string]Account, name, pass string, admin bool, groups []string) error {
	if !validName.MatchString(name) {
		return ErrBadName
	}
	if _, ok := m[name]; ok {
		return ErrExists
	}
	h, err := hash(pass)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		groups = []string{DefaultGroup}
	}
	m[name] = Account{Hash: h, Admin: admin, Created: time.Now().UTC().Format(time.RFC3339), Groups: groups}
	return nil
}

func (a *Auth) AddUser(ctx context.Context, name, pass string, admin bool, groups []string) error {
	if err := a.checkGroups(ctx, groups); err != nil {
		return err
	}
	return a.mutate(ctx, func(m map[string]Account) error {
		if err := addTo(m, name, pass, admin, groups); err != nil {
			return err
		}
		u := m[name]
		u.Must = true // vom Admin vergebenes Startpasswort: Benutzer muss es ändern
		m[name] = u
		return nil
	})
}

// SetUser (CLI): anlegen oder Passwort setzen; admin=true macht zusätzlich zum Admin.
func (a *Auth) SetUser(ctx context.Context, name, pass string, admin bool) error {
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return addTo(m, name, pass, admin, nil)
		}
		h, err := hash(pass)
		if err != nil {
			return err
		}
		u.Hash, u.Disabled = h, false
		u.Admin = u.Admin || admin
		m[name] = u
		return nil
	})
}

// SetPassword: Benutzer ändert sein eigenes Passwort (hebt die Änderungspflicht auf).
func (a *Auth) SetPassword(ctx context.Context, name, pass string) error {
	return a.setPass(ctx, name, pass, false)
}

// ResetPassword: Admin/Gruppen-Admin setzt ein Startpasswort, der Benutzer muss es beim Login ändern.
func (a *Auth) ResetPassword(ctx context.Context, name, pass string) error {
	return a.setPass(ctx, name, pass, true)
}

func (a *Auth) setPass(ctx context.Context, name, pass string, must bool) error {
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		h, err := hash(pass)
		if err != nil {
			return err
		}
		u.Hash, u.Must = h, must
		m[name] = u
		return nil
	})
}

// SetFlags: nil = unverändert. Schützt den letzten aktiven Admin.
func (a *Auth) SetFlags(ctx context.Context, name string, admin, disabled *bool) error {
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		if admin != nil {
			u.Admin = *admin
		}
		if disabled != nil {
			u.Disabled = *disabled
		}
		m[name] = u
		if !hasAdmin(m) {
			return ErrLastAdm
		}
		return nil
	})
}

func (a *Auth) DeleteUser(ctx context.Context, name string) error {
	err := a.mutate(ctx, func(m map[string]Account) error {
		if _, ok := m[name]; !ok {
			return ErrNoUser
		}
		delete(m, name)
		if !hasAdmin(m) {
			return ErrLastAdm
		}
		return nil
	})
	if err != nil {
		return err
	}
	a.dropAdminRefs(ctx, name)
	return nil
}

// dropAdminRefs: der Name verschwindet aus allen Gruppen-Admin-Listen. Sonst erbt, wer den Namen später neu anlegt, die
// Gruppen-Admin-Rechte des gelöschten Kontos (Audit S-07).
func (a *Auth) dropAdminRefs(ctx context.Context, name string) {
	a.mutateGroups(ctx, func(m map[string]Group) error {
		for n, g := range m {
			var keep []string
			for _, x := range g.Admins {
				if x != name {
					keep = append(keep, x)
				}
			}
			if len(keep) != len(g.Admins) {
				g.Admins = keep
				m[n] = g
			}
		}
		return nil
	})
}

// ---------- Prüfen / Fehlversuch-Sperre ----------

// ip: Client-Adresse. Hinter dem eigenen Proxy (CS_TRUST_PROXY=1) zählt das LETZTE Element von X-Forwarded-For
// (vom Proxy angehängt); die davor stehenden Elemente setzt der Client frei und werden nie genutzt.
func (a *Auth) ip(r *http.Request) string {
	if a.TrustProxy {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			parts := strings.Split(f, ",")
			if v := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(v) != nil {
				return v
			}
		}
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (a *Auth) locked(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.fails[key]
	return f != nil && time.Now().Before(f.until)
}

func (a *Auth) failed(key string) { a.failedMax(key, maxFails) }

// failedMax zählt einen Fehlversuch für key; ab max Versuchen ist key lockFor lang gesperrt.
func (a *Auth) failedMax(key string, max int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if len(a.fails) > 10000 { // Speicher begrenzen: nur abgelaufene Sperren und alte Zähler entfernen, aktive Sperren bleiben
		for k, f := range a.fails {
			if (!f.until.IsZero() && now.After(f.until)) || (f.until.IsZero() && now.Sub(f.last) > lockFor) {
				delete(a.fails, k)
			}
		}
		if len(a.fails) > 50000 { // Angriff mit sehr vielen Namen: ungesperrte Zähler verwerfen
			for k, f := range a.fails {
				if f.until.IsZero() || now.After(f.until) {
					delete(a.fails, k)
				}
			}
		}
	}
	f := a.fails[key]
	if f == nil || (f.n >= max && now.After(f.until)) {
		f = &fail{}
		a.fails[key] = f
	}
	f.n++
	f.last = now
	if f.n >= max {
		f.until = now.Add(lockFor)
	}
}

func (a *Auth) ok(key string) { a.mu.Lock(); delete(a.fails, key); a.mu.Unlock() }

// verify prüft Passwort; deaktivierte/unbekannte Benutzer kosten dieselbe Rechenzeit.
func (a *Auth) verify(ctx context.Context, name, pass string) (Account, bool) {
	u, ok := a.get(ctx, name)
	if !ok || !validName.MatchString(name) || u.Disabled {
		bcrypt.CompareHashAndPassword(a.dummy, []byte(pass))
		return Account{}, false
	}
	return u, bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(pass)) == nil
}

// sameOrigin: CSRF-Schutz für Basic Auth. Browser senden die Zugangsdaten auch von fremden Seiten mit.
// Schreibende Methoden sind nur erlaubt, wenn der Browser "gleicher Ursprung" meldet (Sec-Fetch-Site) bzw. der
// Origin-Header zum Host passt. Clients ohne diese Header (WebDAV/CalDAV-Apps, curl) sind keine Browser und bleiben erlaubt.
func sameOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" {
		return s == "same-origin" || s == "none"
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	} else if o == "null" {
		return false
	}
	return true
}

func (a *Auth) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		name, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="cs-team"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		cip := a.ip(r)
		key, ipKey, userKey := name+"|"+cip, "ip|"+cip, "user|"+name
		if a.locked(key) || a.locked(ipKey) || a.locked(userKey) {
			w.Header().Set("Retry-After", "300")
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		u, good := a.verifyCached(r.Context(), name, pass)
		if !good {
			a.failed(key)
			a.failedMax(ipKey, maxFailsIP)     // Passwort-Spraying über viele Namen von einer Adresse
			a.failedMax(userKey, maxFailsUser) // verteilter Angriff auf einen Namen (hohe Schwelle)
			w.Header().Set("WWW-Authenticate", `Basic realm="cs-team"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		a.ok(key)
		if u.Must && ForceChange && r.URL.Path != "/api/me" && r.URL.Path != "/api/me/password" && !strings.HasPrefix(r.URL.Path, "/lang/") && !(r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/index.html")) {
			w.Header().Set("X-Must-Change", "1")
			http.Error(w, "password change required", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, name)
		ctx = context.WithValue(ctx, ctxAdmin{}, u.Admin)
		all, wr := a.areas(u)
		ctx = context.WithValue(ctx, ctxAreas{}, all)
		ctx = context.WithValue(ctx, ctxWAreas{}, wr)
		ctx = context.WithValue(ctx, ctxAdminOf{}, a.adminOf(name))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func User(ctx context.Context) string { s, _ := ctx.Value(ctxKey{}).(string); return s }

func IsAdmin(ctx context.Context) bool { b, _ := ctx.Value(ctxAdmin{}).(bool); return b }
