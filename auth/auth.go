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

// Version: Programmversion (von main gesetzt), erscheint in /api/me für die Oberfläche.
var Version string

// MaxFailsIP: Fehlversuche je Adresse (alle Namen), dann ist die Adresse lockFor gesperrt (Passwort-Spraying). Hinter einem
// Proxy ohne CS_TRUST_PROXY=1 oder in einem Schul-NAT teilen sich viele Nutzer eine Adresse - daher großzügig und per
// CS_MAX_FAILS_IP einstellbar. Bereits angemeldete Nutzer (gültiger Kurzzeit-Cache) bleiben trotz Adress-Sperre zugelassen.
var MaxFailsIP = 60

// ForceChange: Startpasswörter (Admin/Import) müssen beim ersten Login geändert werden. Nur Tests schalten ab.
var ForceChange = true

const (
	usersKey     = "users/users.json"
	cacheTTL     = 30 * time.Second
	maxFailsUser = 100 // Fehlversuche je Benutzername (alle Adressen), dann lockFor gesperrt (Adresse: MaxFailsIP)
	failWindow   = 15 * time.Minute // ohne neuen Fehlversuch so lange, dann beginnt der Zähler wieder bei 0
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
	ErrSysAdmin = errors.New("the sysadmin account is protected: only the sysadmin itself or the command line can change it")
	ErrSysLocal = errors.New("the sysadmin must be a local cs-team account")
	ErrBadName  = errors.New("name: a-z 0-9 . _ - (max 32)")
	ErrBadPass  = errors.New("password: 8..72 bytes")
	ErrWeakPass = errors.New("password too common (e.g. 12345678, password, one repeated character)")
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
	Sys      bool     `json:"sys,omitempty"` // Sysadmin-Konto (0.57.1): lokal, immer Admin, nicht löschbar
	Disabled bool     `json:"disabled,omitempty"`
	Created  string   `json:"created,omitempty"`
	Groups   []string `json:"groups,omitempty"` // Handliste: Gruppen, die diesem Konto ausdrücklich gegeben wurden
	Must     bool     `json:"must,omitempty"`   // Passwort muss beim nächsten Login geändert werden
	Lang     string   `json:"lang,omitempty"`   // Oberflächensprache (leer = Browser/Serverstandard)
	Mail     string   `json:"mail,omitempty"`   // externe E-Mail-Adresse (für Nachrichten)
	Chat     string   `json:"chat,omitempty"`   // externe Chat-Adresse (URL, z.B. Webhook/ntfy)
	Realm    string   `json:"realm,omitempty"`
	Source   string   `json:"source,omitempty"`    // "dir" = Spiegelkonto eines Verzeichnisbenutzers
	DirSeen  string   `json:"dirSeen,omitempty"`   // letzte erfolgreiche Verzeichnisprüfung (Zwischenspeicher, Phase 3)
	DirGroups []string `json:"dirGroups,omitempty"` // Verzeichnisgruppen des Kontos (beim Login gelesen; 0.55)
	Ack       map[string]string `json:"ack,omitempty"` // bestätigte Datenschutzhinweise: "ai" | "video" -> Hash der Adresse zum Zeitpunkt der Bestätigung (ack.go)
	TOTP      string    `json:"totp,omitempty"`     // Zwei-Faktor-Anmeldung (0.60): geheimer Schlüssel (Base32), leer = aus; twofa.go
	Recovery  []string  `json:"recovery,omitempty"`  // Hashes der noch gültigen Wiederherstellungscodes
	AppPw     []AppPass `json:"appPw,omitempty"`     // App-Passwörter (nur Dateien und Kalender, ohne Code)
	Member    []string `json:"-"`                   // berechnete Mitgliedschaften aus Group.Dir/Group.Sub (nur im Speicher)
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
	lmu    sync.Mutex // ein Lader gleichzeitig (refresh)
	gen    int        // zählt invalidate(); verwirft veraltete Ladeergebnisse
	retry  time.Time  // nach einem Ladefehler: bis dahin den alten Stand benutzen
	users  map[string]Account
	groups map[string]Group
	load   time.Time
	fails  map[string]*fail
	dummy  []byte
	cache  map[[32]byte]cacheEnt // erfolgreiche Anmeldungen (kurz), spart bcrypt je Anfrage
	salt   [16]byte
	id     Identity       // Anmelde-Einstellung, wenn keine Quelle (Einstellungen) gesetzt ist
	idSrc  IdentitySource // Quelle der Anmelde-Einstellung (chat.Settings, Tests)
	dir    DirChecker     // feste Verzeichnisprüfung (Tests); sonst aus der Einstellung
	sess   map[[32]byte]sessEnt // 2FA-Sitzungen und App-Passwörter im Speicher (twofa.go)
	tmu    sync.Mutex           // serialisiert die Prüfung des zweiten Faktors (Wiederholungsschutz)
	step   map[string]int64     // zuletzt benutzter TOTP-Schritt je Benutzer, unter tmu
	pend   map[string]pendEnt   // laufende 2FA-Einrichtung je Benutzer, unter tmu
	warm   chan struct{}  // beendet das Warmhalten der Verzeichnisverbindung (WarmDir), nil = läuft nicht
}

var std *Auth // zuletzt erzeugte Instanz: Freigabe-Prüfung (Allowed) braucht die Gruppen eines Benutzers

func New(st store.Store) *Auth {
	d, _ := bcrypt.GenerateFromPassword([]byte("dummy"), bcrypt.DefaultCost)
	a := &Auth{st: st, fails: map[string]*fail{}, dummy: d, cache: map[[32]byte]cacheEnt{},
		sess: map[[32]byte]sessEnt{}, step: map[string]int64{}, pend: map[string]pendEnt{}}
	rand.Read(a.salt[:])
	std = a
	return a
}

// ---------- Laden / Ändern ----------

func (a *Auth) invalidate() { a.mu.Lock(); a.load = time.Time{}; a.gen++; a.mu.Unlock() }

// loadTimeout: so lange darf das Laden der Benutzer/Gruppen aus dem Speicher höchstens dauern (S-11).
var loadTimeout = 5 * time.Second

// refresh lädt Benutzer und Gruppen neu, wenn der Zwischenspeicher älter als cacheTTL ist. Der Speicherzugriff läuft ohne a.mu
// (nur ein Lader gleichzeitig, mit Zeitlimit); ein hängender Speicher blockiert so keine Anfragen mit gültigem Zwischenspeicher.
// Ist der Zwischenspeicher nur abgelaufen (nicht ungültig gemacht) und lädt gerade jemand, wird der alte Stand benutzt.
// Nach einer Änderung (invalidate) wartet der Aufrufer auf frische Daten.
func (a *Auth) refresh(ctx context.Context) {
	a.mu.Lock()
	fresh, have := time.Since(a.load) < cacheTTL, !a.load.IsZero()
	a.mu.Unlock()
	if fresh {
		return
	}
	if have && time.Now().Before(a.retryAt()) {
		return
	}
	if !a.lmu.TryLock() {
		if have {
			return
		}
		a.lmu.Lock()
	}
	defer a.lmu.Unlock()
	a.mu.Lock()
	fresh, gen := time.Since(a.load) < cacheTTL, a.gen
	a.mu.Unlock()
	if fresh {
		return // ein anderer Lader war schneller
	}
	ctx, cancel := context.WithTimeout(ctx, loadTimeout)
	defer cancel()
	fail := func() { a.mu.Lock(); a.retry = time.Now().Add(2 * time.Second); a.mu.Unlock() }
	m := map[string]Account{}
	b, _, err := a.st.Get(ctx, usersKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		fail()
		return
	case json.Unmarshal(b, &m) != nil:
		fail()
		return
	}
	g, ok := a.loadGroups(ctx)
	if !ok {
		fail()
		return
	}
	m = resolveMembers(m, g) // Mitgliedschaften aus Verzeichnis-/Untergruppen-Einträgen berechnen (0.55)
	a.mu.Lock()
	if a.gen == gen { // währenddessen geändert: Ergebnis verwerfen, der nächste Aufruf lädt neu
		a.users, a.groups, a.load = m, g, time.Now()
	}
	a.mu.Unlock()
}

func (a *Auth) retryAt() time.Time { a.mu.Lock(); defer a.mu.Unlock(); return a.retry }

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

// hasLocalAdmin: gibt es noch einen aktiven Admin unter den lokalen cs-team-Konten (Schlüssel ohne Namensraum)?
// Nur die lokalen Konten sind ohne Verzeichnis anmeldefähig - deshalb muss immer eines davon Admin bleiben
// (KISS-Regel 0.55: Notfallzugang, "Zugehoerigkeit darf aus dem Verzeichnis kommen, Verantwortung nie").
func hasLocalAdmin(m map[string]Account) bool {
	for n, u := range m {
		if u.Admin && !u.Disabled && !strings.Contains(n, "@") {
			return true
		}
	}
	return false
}

// LocalAdmins: Anzahl der aktiven Admins unter den lokalen cs-team-Konten. Vorbedingungen, die den Notfallzugang
// schützen (0.55), prüfen damit, ob nach einer Änderung noch ein lokaler Admin übrig bleibt.
func (a *Auth) LocalAdmins(ctx context.Context) int {
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for name, u := range a.users {
		if u.Admin && !u.Disabled && !strings.Contains(name, "@") {
			n++
		}
	}
	return n
}

// weakPasswords: die häufigsten Trivialpasswörter (klein geschrieben). Bewusst kurz (KISS): kein Wörterbuch, nur die
// Fälle, die jeder Angreifer zuerst probiert. Dazu: ein einziges Zeichen wiederholt (aaaaaaaa).
var weakPasswords = map[string]bool{
	"password": true, "password1": true, "passwort": true, "passwort1": true, "12345678": true, "123456789": true,
	"1234567890": true, "11111111": true, "00000000": true, "qwertzui": true, "qwertyui": true, "qwerty123": true,
	"abcd1234": true, "abcdefgh": true, "admin123": true, "administrator": true, "willkommen": true, "welcome1": true,
	"changeme": true, "letmein1": true, "csteam123": true, "cs-team123": true, "schule123": true, "iloveyou": true,
}

func weakPass(p string) bool {
	l := []rune(strings.ToLower(p))
	if weakPasswords[string(l)] {
		return true
	}
	for _, r := range l {
		if r != l[0] {
			return false
		}
	}
	return len(l) > 0
}

func hash(pass string) (string, error) {
	if len(pass) < minPass || len(pass) > maxPass {
		return "", ErrBadPass
	}
	if weakPass(pass) {
		return "", ErrWeakPass
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
		if err := addTo(m, user, pass, true, nil); err != nil {
			return err
		}
		u := m[user]
		u.Must, u.Sys = true, true // Sysadmin; Startpasswort steht in Konfiguration/Umgebung: beim ersten Login ändern (ForceChange)
		m[user] = u
		return nil
	})
}

// okName: gültiger Name für neue Konten, Gruppen und Organisationen; auf Windows-Servern zusätzlich ohne
// reservierte Gerätenamen (con, nul, com1 ...), weil daraus Verzeichnisnamen werden.
func okName(n string) bool { return validName.MatchString(n) && !store.WinReserved(n) }

func addTo(m map[string]Account, name, pass string, admin bool, groups []string) error {
	if !okName(name) {
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
			if err := addTo(m, name, pass, admin, nil); err != nil {
				return err
			}
			u = m[name]
			u.Must = true // das Passwort stand auf der Kommandozeile (Shell-Verlauf, ps): beim ersten Login ändern
			m[name] = u
			return nil
		}
		h, err := hash(pass)
		if err != nil {
			return err
		}
		u.Hash, u.Disabled, u.Must = h, false, true
		u = clear2FA(u) // Kommandozeile = Notfallzugang (auch für das Sysadmin-Konto): ein verlorener zweiter Faktor wird mit entfernt
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

// SetFlags: nil = unverändert. Schützt Admin-Rechte: mindestens ein aktiver LOKALER Admin muss bleiben.
func (a *Auth) SetFlags(ctx context.Context, name string, admin, disabled *bool) error {
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		if u.Sys && ((admin != nil && !*admin) || (disabled != nil && *disabled)) {
			return ErrSysAdmin
		}
		if admin != nil {
			u.Admin = *admin
		}
		if disabled != nil {
			u.Disabled = *disabled
		}
		m[name] = u
		if !hasLocalAdmin(m) {
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
		if m[name].Sys {
			return ErrSysAdmin
		}
		delete(m, name)
		if !hasLocalAdmin(m) {
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
	if f == nil || (f.n >= max && now.After(f.until)) || (!now.Before(f.until) && now.Sub(f.last) > failWindow) {
		f = &fail{}
		a.fails[key] = f
	}
	f.n++
	f.last = now
	if f.n >= max {
		f.until = now.Add(lockFor)
		if f.n == max {
			logLocked("auth: locked %q for %v after %d failed attempts", safeName(key), lockFor, max)
		}
	}
}

// ok löscht den Fehlzähler von key (erfolgreiche Anmeldung) und liefert die Zahl der Fehlversuche, die
// innerhalb von failWindow vorausgingen (0 = keine).
func (a *Auth) ok(key string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := a.recentFails(key)
	delete(a.fails, key)
	return n
}

// recentFails: Fehlversuche von key im Zeitfenster (Aufrufer hält a.mu).
func (a *Auth) recentFails(key string) int {
	if f := a.fails[key]; f != nil && time.Since(f.last) <= failWindow {
		return f.n
	}
	return 0
}

// failsBefore: größte Zahl jüngster Fehlversuche über mehrere Schlüssel (Benutzer+Adresse, Adresse, Benutzer).
func (a *Auth) failsBefore(keys ...string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := 0
	for _, k := range keys {
		if n := a.recentFails(k); n > m {
			m = n
		}
	}
	return m
}

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
		idc := a.identity()
		id, err := parseLogin(name, idc)
		key, ipKey, userKey := id.Key+"|"+cip, "ip|"+cip, "user|"+id.Key
		var u Account
		var good, scope bool
		lockKey, lockUser, lockIP := a.locked(key), a.locked(userKey), a.locked(ipKey)
		if lockKey || lockUser || lockIP {
			// Nur die Adress-Sperre lässt Nutzer durch, die von dieser Adresse gerade gültig angemeldet sind (Kurzzeit-Cache,
			// kein bcrypt, keine Verzeichnisabfrage): so sperrt ein Angriff aus dem Schul-NAT/Proxy nicht alle bestehenden Sitzungen.
			if err == nil && lockIP && !lockKey && !lockUser {
				u, good = a.cachedGood(r.Context(), id.Key, pass, cip, r.URL.Path)
			}
			if !good {
				w.Header().Set("Retry-After", "300")
				http.Error(w, "too many attempts", http.StatusTooManyRequests)
				return
			}
		}
		if !good {
			switch {
			case err != nil: // unbekannter Namensraum oder ungültiger Name: wie falsche Zugangsdaten behandeln
			case id.Local: // lokale cs-team-Konten: in jedem Modus möglich (Notfallzugang, 0.55)
				u, good, scope = a.loginLocal(r.Context(), id.Key, pass, cip, r.URL.Path) // mit 2FA: Passwort + Code oder App-Passwort (twofa.go)
			default: // Verzeichnisbenutzer: name@realm
				u, err = a.verifyDirCached(r.Context(), id, pass)
				good = err == nil
			}
		}
		if scope { // gültiges App-Passwort an einer Stelle, für die es nicht gilt: kein Fehlversuch
			http.Error(w, "app password not allowed here", http.StatusForbidden)
			return
		}
		if !good {
			logLimited("auth: login failed user=%q ip=%s (%s %s)", safeName(name), cip, r.Method, safeName(r.URL.Path))
			a.failed(key)
			a.failedMax(ipKey, MaxFailsIP)     // Passwort-Spraying über viele Namen von einer Adresse
			a.failedMax(userKey, maxFailsUser) // verteilter Angriff auf einen Namen (hohe Schwelle)
			switch {
			case errors.Is(err, ErrDirDown):
				w.Header().Set("Retry-After", "30")
				http.Error(w, "directory unavailable", http.StatusServiceUnavailable)
			case errors.Is(err, ErrNotAdmit):
				http.Error(w, "not admitted for this service", http.StatusForbidden)
			case errors.Is(err, ErrBadRealm):
				http.Error(w, "unknown login realm", http.StatusUnauthorized)
			default: // falsches Passwort, unbekanntes Konto
				w.Header().Set("WWW-Authenticate", `Basic realm="cs-team"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
			}
			return
		}
		// Nur wenn für genau dieses Konto von dieser Adresse Fehlversuche vorausgingen, wird die Anmeldung einmal protokolliert
		// (der Zähler wird dabei gelöscht; Fehlversuche anderer Konten derselben Adresse erzeugen keine Zeilen).
		if n := a.ok(key); n > 0 {
			logLimited("auth: login ok user=%q ip=%s after %d failed attempts", safeName(name), cip, n)
		}
		if u.Must && ForceChange && r.URL.Path != "/api/me" && r.URL.Path != "/api/me/password" && !strings.HasPrefix(r.URL.Path, "/lang/") && !(r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/index.html")) {
			w.Header().Set("X-Must-Change", "1")
			http.Error(w, "password change required", http.StatusForbidden)
			return
		}
		if need2FA(u, id.Local) && r.URL.Path != "/api/me" && !strings.HasPrefix(r.URL.Path, "/api/me/") && !strings.HasPrefix(r.URL.Path, "/lang/") && !(r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/index.html")) {
			w.Header().Set("X-2FA-Required", "1") // Admin ohne 2FA bei gesetzter Pflicht: nur Konto-Seite, bis sie eingerichtet ist
			http.Error(w, "two-factor setup required", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, id.Key)
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
