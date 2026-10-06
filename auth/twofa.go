package auth

// Zwei-Faktor-Anmeldung (0.60): TOTP nach RFC 6238 (SHA1, 6 Stellen, 30 s, +-1 Schritt) nur mit der Standardbibliothek.
//
// cs-team nutzt auf jeder Anfrage HTTP Basic Auth (Browser, WebDAV, CalDAV, API). Deshalb gilt für Konten mit 2FA:
//   - Anmeldung mit "Passwort" direkt gefolgt vom 6-stelligen Code im Passwortfeld (z.B. geheim123456) oder mit einem
//     Wiederherstellungscode (geheim123ABCD-EFGH-IJKL, jeder Code gilt einmal);
//   - nach der ersten erfolgreichen Prüfung merkt sich der Server (nur im Speicher) den Hash der Zugangsdaten je Benutzer
//     und Adresse sessTTL lang; dieselben Zugangsdaten (derselbe Authorization-Kopf des Browsers) gelten dann ohne neuen
//     Code. Passwortänderung, 2FA ausschalten, Benutzer sperren/löschen und Neustart beenden die Sitzung (der Eintrag
//     trägt einen Hash aus Passwort-Hash und Schlüssel und wird bei jeder Anfrage mit dem Konto verglichen);
//   - für WebDAV/CalDAV und andere Programme ohne Code-Eingabe gibt es App-Passwörter (benannt, widerrufbar, höchstens
//     maxAppPass je Konto), die nur für Dateien und Kalender gelten (appPathOK), nie für Konto- und Verwaltungsfunktionen.
// Ein falscher Code sieht aus wie ein falsches Passwort (gleiche Antwort, gleicher Fehlversuchszähler, gleiche bcrypt-Zeit).
// 2FA gilt nur für lokale Konten; Verzeichniskonten (LDAP/AD) haben ihr eigenes Verfahren.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessTTL    = 12 * time.Hour // so lange gelten die Zugangsdaten (Passwort + Code) eines Browsers ohne neuen Code
	appSessTTL = time.Hour      // App-Passwörter: bcrypt höchstens einmal je Stunde, danach wird auch "zuletzt benutzt" nachgeführt
	totpStep   = 30
	maxAppPass = 10
	nRecovery  = 8
	appLen     = 24
	pendTTL    = 15 * time.Minute
	appAlpha   = "abcdefghijklmnopqrstuvwxyz234567"
)

// Enforce2FA: globale Einstellung "Zwei-Faktor für Admin-Konten verlangen" (von main gesetzt; nil = nein).
var Enforce2FA func() bool

// totpNow: Uhr für die Code-Prüfung (Tests).
var totpNow = time.Now

// AppPass: App-Passwort eines Kontos. ID = die ersten 6 Zeichen (Suchschlüssel, damit höchstens ein bcrypt anfällt).
type AppPass struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Hash    string `json:"hash"` // bcrypt
	Created string `json:"created"`
	Used    string `json:"used,omitempty"`
}

type sessEnt struct {
	h   [32]byte
	exp time.Time
	app string // ID des App-Passworts, leer = Passwort + Code
	user string // Konto (dropSessions verwirft nur die Sitzungen des betroffenen Kontos)
}

type pendEnt struct {
	secret string
	exp    time.Time
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// ---------- TOTP ----------

func hotp(key []byte, ctr uint64, digits int) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], ctr)
	m := hmac.New(sha1.New, key)
	m.Write(b[:])
	s := m.Sum(nil)
	o := s[len(s)-1] & 0x0f
	v := (uint32(s[o])&0x7f)<<24 | uint32(s[o+1])<<16 | uint32(s[o+2])<<8 | uint32(s[o+3])
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, v%mod)
}

// totpCheck: gültiger 6-stelliger Code in den Schritten -1/0/+1? Nur Schritte nach last zählen (Wiederholungsschutz).
// Alle drei Schritte werden immer verglichen (konstante Zeit); geliefert wird der passende Schritt.
func totpCheck(secret, code string, now time.Time, last int64) (int64, bool) {
	key, err := b32.DecodeString(secret)
	if err != nil || len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / totpStep
	hit := int64(-1)
	for d := int64(-1); d <= 1; d++ {
		s := cur + d
		eq := subtle.ConstantTimeCompare([]byte(hotp(key, uint64(s), 6)), []byte(code)) == 1
		if eq && s > last {
			hit = s
		}
	}
	return hit, hit >= 0
}

func newSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return b32.EncodeToString(b)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

var recRe = regexp.MustCompile(`^(.+)([A-Za-z2-7]{4}-[A-Za-z2-7]{4}-[A-Za-z2-7]{4})$`)

// splitCode: Passwortfeld = Passwort + Code. kind 't' = 6 Ziffern, 'r' = Wiederherstellungscode, 0 = kein Code.
func splitCode(pass string) (pw, code string, kind byte) {
	if n := len(pass); n > 6 && isDigits(pass[n-6:]) {
		return pass[:n-6], pass[n-6:], 't'
	}
	if m := recRe.FindStringSubmatch(pass); m != nil {
		return m[1], strings.ToUpper(m[2]), 'r'
	}
	return pass, "", 0
}

func newRecovery() string {
	b := make([]byte, 8)
	rand.Read(b)
	s := b32.EncodeToString(b)[:12]
	return s[:4] + "-" + s[4:8] + "-" + s[8:]
}

func recHash(code string) string {
	h := sha256.Sum256([]byte("cs-team-recovery\x00" + code))
	return hex.EncodeToString(h[:])
}

// normRecovery: Eingabe -> "ABCD-EFGH-IJKL" oder "" (ungültig).
func normRecovery(s string) string {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
	if len(s) != 12 || strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") != "" {
		return ""
	}
	return s[:4] + "-" + s[4:8] + "-" + s[8:]
}

// appPathOK: wofür ein App-Passwort gilt: WebDAV, CalDAV und die Datei-Schnittstelle, sonst nichts.
func appPathOK(p string) bool {
	return p == "/webdav" || strings.HasPrefix(p, "/webdav/") || strings.HasPrefix(p, "/dav/") || p == "/.well-known/caldav" ||
		strings.HasPrefix(p, "/api/files")
}

// need2FA: muss dieses Konto die Einrichtung nachholen (Einstellung "für Admin-Konten verlangen")?
func need2FA(u Account, local bool) bool {
	return local && Enforce2FA != nil && Enforce2FA() && u.Admin && u.TOTP == "" && u.Source != "dir"
}

// ---------- Sitzungen im Speicher ----------

func (a *Auth) sessKey(name, ip, pass string) [32]byte {
	return sha256.Sum256([]byte(string(a.salt[:]) + name + "\x00" + ip + "\x00" + pass))
}

func sessHash(u Account) [32]byte { return sha256.Sum256([]byte(u.Hash + "\x00" + u.TOTP)) }

func appHash(u Account, p AppPass) [32]byte { return sha256.Sum256([]byte("app\x00" + p.Hash + "\x00" + u.TOTP)) }

// sessGood: gültige Sitzung (Passwort + Code bzw. App-Passwort) aus dem Speicher; app = der Eintrag gehört einem App-Passwort.
func (a *Auth) sessGood(u Account, key [32]byte) (ok, app bool) {
	a.mu.Lock()
	e, found := a.sess[key]
	a.mu.Unlock()
	if !found || !time.Now().Before(e.exp) || u.Disabled || u.TOTP == "" {
		return false, false
	}
	if e.app == "" {
		return e.h == sessHash(u), false
	}
	for _, p := range u.AppPw {
		if p.ID == e.app {
			return e.h == appHash(u, p), true
		}
	}
	return false, false
}

func (a *Auth) sessPut(key [32]byte, e sessEnt) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sess) >= maxCache {
		now := time.Now()
		for k, v := range a.sess {
			if now.After(v.exp) {
				delete(a.sess, k)
			}
		}
		if len(a.sess) >= maxCache {
			a.sess = map[[32]byte]sessEnt{}
		}
	}
	a.sess[key] = e
}

// dropSessions: die Sitzungen eines Kontos verwerfen (2FA ein-/ausgeschaltet oder zurückgesetzt). Andere Konten bleiben
// angemeldet. Passwortänderung, Sperren und Löschen brauchen es nicht: sessGood vergleicht bei jeder Anfrage mit dem Konto.
// Das Widerrufen eines App-Passworts lässt die Sitzungen ebenfalls unberührt (sessGood prüft, ob es das Passwort noch gibt).
func (a *Auth) dropSessions(user string) {
	a.mu.Lock()
	for k, e := range a.sess {
		if e.user == user {
			delete(a.sess, k)
		}
	}
	a.mu.Unlock()
}

// ---------- Anmeldung ----------

// loginLocal: Anmeldung eines lokalen Kontos in Wrap. Ohne 2FA wie bisher (verifyCached). Mit 2FA: Sitzung aus dem Speicher,
// App-Passwort (nur für appPathOK) oder Passwort + Code (+ Wiederholungsschutz). Alles unter tmu, damit die parallelen
// Anfragen eines Browsers nach der ersten Prüfung die Sitzung finden statt den Code als "schon benutzt" abzulehnen.
// Dritter Rückgabewert scope: gültiges App-Passwort, aber für diesen Pfad nicht erlaubt - Wrap antwortet 403 und zählt keinen
// Fehlversuch (es war kein falsches Passwort); die Rechenzeit ist dieselbe wie bei einer normalen Prüfung.
func (a *Auth) loginLocal(ctx context.Context, name, pass, ip, path string) (Account, bool, bool) {
	u, ok := a.get(ctx, name)
	if !ok || u.TOTP == "" || u.Disabled || !validName.MatchString(name) {
		v, good := a.verifyCached(ctx, name, pass)
		return v, good, false
	}
	key := a.sessKey(name, ip, pass)
	if ok, app := a.sessGood(u, key); ok {
		g := !app || appPathOK(path)
		return u, g, !g
	}
	a.tmu.Lock()
	defer a.tmu.Unlock()
	if ok, app := a.sessGood(u, key); ok {
		g := !app || appPathOK(path)
		return u, g, !g
	}
	if p := appFind(u, pass); p != nil && bcrypt.CompareHashAndPassword([]byte(p.Hash), []byte(pass)) == nil {
		a.sessPut(key, sessEnt{h: appHash(u, *p), exp: time.Now().Add(appSessTTL), app: p.ID, user: name})
		a.touchApp(ctx, name, p.ID)
		g := appPathOK(path)
		return u, g, !g
	}
	pw, code, kind := splitCode(pass)
	if kind == 0 { // Passwort ohne Code genügt nie; gleiche Rechenzeit wie ein falsches Passwort
		bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(pass))
		return Account{}, false, false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(pw)) != nil {
		return Account{}, false, false
	}
	if kind == 't' {
		step, ok := totpCheck(u.TOTP, code, totpNow(), a.step[name])
		if !ok {
			return Account{}, false, false
		}
		a.step[name] = step
	} else if !a.useRecovery(ctx, name, code) {
		return Account{}, false, false
	}
	a.sessPut(key, sessEnt{h: sessHash(u), exp: time.Now().Add(sessTTL), user: name})
	return u, true, false
}

// cachedGood2FA: Sitzung eines 2FA-Kontos nur aus dem Speicher (für die Adress-Sperre, siehe cachedGood).
func (a *Auth) cachedGood2FA(u Account, name, ip, pass, path string) bool {
	ok, app := a.sessGood(u, a.sessKey(name, ip, pass))
	return ok && (!app || appPathOK(path))
}

func appFind(u Account, pass string) *AppPass {
	if len(pass) != appLen {
		return nil
	}
	for i := range u.AppPw {
		if u.AppPw[i].ID == pass[:6] {
			return &u.AppPw[i]
		}
	}
	return nil
}

// touchApp: "zuletzt benutzt" höchstens stündlich schreiben.
func (a *Auth) touchApp(ctx context.Context, name, id string) {
	now := time.Now().UTC()
	a.mutate(ctx, func(m map[string]Account) error {
		u := m[name]
		for i, p := range u.AppPw {
			if p.ID != id {
				continue
			}
			if t, err := time.Parse(time.RFC3339, p.Used); err == nil && now.Sub(t) < time.Hour {
				return errors.New("fresh")
			}
			ap := append([]AppPass(nil), u.AppPw...)
			ap[i].Used = now.Format(time.RFC3339)
			u.AppPw = ap
			m[name] = u
		}
		return nil
	})
}

// useRecovery: Wiederherstellungscode einlösen (gilt einmal). Aufrufer hält tmu.
func (a *Auth) useRecovery(ctx context.Context, name, code string) bool {
	code = normRecovery(code)
	if code == "" {
		return false
	}
	h := recHash(code)
	u, ok := a.get(ctx, name)
	if !ok {
		return false
	}
	found := false
	for _, x := range u.Recovery {
		if subtle.ConstantTimeCompare([]byte(x), []byte(h)) == 1 {
			found = true
		}
	}
	if !found {
		return false
	}
	used, left := false, 0
	err := a.mutate(ctx, func(m map[string]Account) error {
		v := m[name]
		var keep []string
		for _, x := range v.Recovery {
			if !used && x == h {
				used = true
				continue
			}
			keep = append(keep, x)
		}
		v.Recovery, left = keep, len(keep)
		m[name] = v
		return nil
	})
	if err == nil && used {
		log.Printf("auth: recovery code used user=%q (%d left)", safeName(name), left)
	}
	return err == nil && used
}

// codeOK: TOTP-Code oder Wiederherstellungscode für Konto-Änderungen prüfen. Aufrufer hält tmu.
func (a *Auth) codeOK(ctx context.Context, u Account, name, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) == 6 && isDigits(code) {
		step, ok := totpCheck(u.TOTP, code, totpNow(), a.step[name])
		if ok {
			a.step[name] = step
		}
		return ok
	}
	return a.useRecovery(ctx, name, code)
}

// clear2FA: Schlüssel, Wiederherstellungscodes und App-Passwörter entfernen.
func clear2FA(u Account) Account {
	u.TOTP, u.Recovery, u.AppPw = "", nil, nil
	return u
}

// ---------- Schnittstelle ----------

func (a *Auth) gate(w http.ResponseWriter, r *http.Request, me string) bool {
	if a.locked(me + "|" + a.ip(r)) {
		w.Header().Set("Retry-After", "300")
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return false
	}
	return true
}

func (a *Auth) deny(w http.ResponseWriter, r *http.Request, me string) {
	a.failed(me + "|" + a.ip(r))
	http.Error(w, "password or code wrong", http.StatusForbidden)
}

func (a *Auth) twofaRoutes(mux *http.ServeMux, adm func(http.HandlerFunc) http.Handler) {
	me := func(r *http.Request) (string, Account, bool) {
		n := User(r.Context())
		u, ok := a.get(r.Context(), n)
		return n, u, ok && isLocalKey(n, u)
	}
	noStore := func(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }
	mux.Handle("GET /api/me/2fa", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, u, local := me(r)
		apps := []map[string]string{}
		for _, p := range u.AppPw {
			apps = append(apps, map[string]string{"id": p.ID, "name": p.Name, "created": p.Created, "used": p.Used})
		}
		noStore(w)
		json.NewEncoder(w).Encode(map[string]any{"local": local, "on": u.TOTP != "", "recovery": len(u.Recovery), "apps": apps,
			"max": maxAppPass, "enforce": Enforce2FA != nil && Enforce2FA() && u.Admin})
	})))
	// Einrichtung beginnen: neuer geheimer Schlüssel (nur im Speicher, bis ein gültiger Code ihn bestätigt)
	mux.Handle("POST /api/me/2fa/setup", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, u, local := me(r)
		if !local || u.TOTP != "" {
			http.Error(w, "not available", http.StatusBadRequest)
			return
		}
		s := newSecret()
		a.tmu.Lock()
		a.pend[n] = pendEnt{s, time.Now().Add(pendTTL)}
		a.tmu.Unlock()
		noStore(w)
		json.NewEncoder(w).Encode(map[string]string{"secret": s,
			"uri": "otpauth://totp/" + url.PathEscape("cs-team:"+n) + "?secret=" + s + "&issuer=cs-team&algorithm=SHA1&digits=6&period=30"})
	})))
	// Einschalten: Passwort + erster gültiger Code; liefert die Wiederherstellungscodes (einmalig)
	mux.Handle("POST /api/me/2fa/activate", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Password, Code string }
		if !body(w, r, &in) {
			return
		}
		n, u, local := me(r)
		if !local || u.TOTP != "" {
			http.Error(w, "not available", http.StatusBadRequest)
			return
		}
		if !a.gate(w, r, n) {
			return
		}
		a.tmu.Lock()
		defer a.tmu.Unlock()
		p, have := a.pend[n]
		if !have || time.Now().After(p.exp) {
			http.Error(w, "setup expired, start again", http.StatusBadRequest)
			return
		}
		if _, ok := a.verify(r.Context(), n, in.Password); !ok {
			a.deny(w, r, n)
			return
		}
		step, ok := totpCheck(p.secret, strings.TrimSpace(in.Code), totpNow(), 0)
		if !ok {
			a.deny(w, r, n)
			return
		}
		codes, hashes := make([]string, nRecovery), make([]string, nRecovery)
		for i := range codes {
			codes[i] = newRecovery()
			hashes[i] = recHash(codes[i])
		}
		if err := a.mutate(r.Context(), func(m map[string]Account) error {
			v, ok := m[n]
			if !ok {
				return ErrNoUser
			}
			v.TOTP, v.Recovery, v.AppPw = p.secret, hashes, nil
			m[n] = v
			return nil
		}); err != nil {
			fail_(w, err)
			return
		}
		delete(a.pend, n)
		a.step[n] = step
		a.dropSessions(n)
		// Die laufende Sitzung (nur Passwort) wird bewusst NICHT als Sitzung vermerkt: sonst bliebe Passwort ohne Code für diese
		// Adresse 12 Stunden gültig. Die Oberfläche meldet nach dem Einschalten ab; der Benutzer meldet sich mit Passwort + Code an
		// (der eben benutzte Code ist verbraucht: auf den nächsten warten).
		noStore(w)
		json.NewEncoder(w).Encode(map[string]any{"codes": codes})
	})))
	// Ausschalten: Passwort + Code (oder Wiederherstellungscode)
	mux.Handle("POST /api/me/2fa/disable", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Password, Code string }
		if !body(w, r, &in) {
			return
		}
		n, u, local := me(r)
		if !local || u.TOTP == "" {
			http.Error(w, "not available", http.StatusBadRequest)
			return
		}
		if Enforce2FA != nil && Enforce2FA() && u.Admin {
			http.Error(w, "two-factor login is required for admin accounts", http.StatusBadRequest)
			return
		}
		if !a.gate(w, r, n) {
			return
		}
		a.tmu.Lock()
		defer a.tmu.Unlock()
		if _, ok := a.verify(r.Context(), n, in.Password); !ok || !a.codeOK(r.Context(), u, n, in.Code) {
			a.deny(w, r, n)
			return
		}
		if err := a.mutate(r.Context(), func(m map[string]Account) error {
			m[n] = clear2FA(m[n])
			return nil
		}); err != nil {
			fail_(w, err)
			return
		}
		delete(a.step, n)
		a.dropSessions(n)
	})))
	// App-Passwort anlegen (Passwort nötig); wird nur hier angezeigt
	mux.Handle("POST /api/me/apppass", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Password, Name string }
		if !body(w, r, &in) {
			return
		}
		n, u, local := me(r)
		name := strings.TrimSpace(strings.Map(func(c rune) rune {
			if c < 32 || c == 127 {
				return -1
			}
			return c
		}, in.Name))
		if !local || u.TOTP == "" || name == "" || len([]rune(name)) > 40 {
			http.Error(w, "need 2FA and a name (max 40)", http.StatusBadRequest)
			return
		}
		if len(u.AppPw) >= maxAppPass {
			http.Error(w, "too many app passwords", http.StatusBadRequest)
			return
		}
		if !a.gate(w, r, n) {
			return
		}
		if _, ok := a.verify(r.Context(), n, in.Password); !ok {
			a.deny(w, r, n)
			return
		}
		b := make([]byte, appLen)
		rand.Read(b)
		for i := range b {
			b[i] = appAlpha[int(b[i])%len(appAlpha)]
		}
		pw := string(b)
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			fail_(w, err)
			return
		}
		ap := AppPass{ID: pw[:6], Name: name, Hash: string(h), Created: time.Now().UTC().Format(time.RFC3339)}
		if err := a.mutate(r.Context(), func(m map[string]Account) error {
			v, ok := m[n]
			if !ok {
				return ErrNoUser
			}
			if len(v.AppPw) >= maxAppPass || v.TOTP == "" {
				return errors.New("not available")
			}
			v.AppPw = append(append([]AppPass(nil), v.AppPw...), ap)
			m[n] = v
			return nil
		}); err != nil {
			fail_(w, err)
			return
		}
		noStore(w)
		json.NewEncoder(w).Encode(map[string]string{"id": ap.ID, "name": ap.Name, "password": pw})
	})))
	mux.Handle("DELETE /api/me/apppass/{id}", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _, local := me(r)
		if !local {
			http.Error(w, "not available", http.StatusBadRequest)
			return
		}
		found := false
		if err := a.mutate(r.Context(), func(m map[string]Account) error {
			v := m[n]
			var keep []AppPass
			for _, p := range v.AppPw {
				if p.ID == r.PathValue("id") {
					found = true
					continue
				}
				keep = append(keep, p)
			}
			v.AppPw = keep
			m[n] = v
			return nil
		}); err != nil {
			fail_(w, err)
			return
		}
		if !found {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		// keine Sitzung verwerfen: App-Passwörter haben nur ihren eigenen Eintrag, und sessGood lehnt ein widerrufenes ab
	})))
	// Admin: 2FA eines anderen Kontos zurücksetzen (verlorenes Gerät); das eigene Konto nur mit Passwort + Code (disable)
	mux.Handle("POST /api/users/{name}/2fa/reset", adm(func(w http.ResponseWriter, r *http.Request) {
		t := r.PathValue("name")
		if t == User(r.Context()) {
			http.Error(w, "use the account page to switch off your own 2FA", http.StatusBadRequest)
			return
		}
		if tu, ok := a.get(r.Context(), t); ok && tu.Sys {
			fail_(w, ErrSysAdmin)
			return
		}
		if err := a.mutate(r.Context(), func(m map[string]Account) error {
			v, ok := m[t]
			if !ok {
				return ErrNoUser
			}
			m[t] = clear2FA(v)
			return nil
		}); err != nil {
			fail_(w, err)
			return
		}
		a.tmu.Lock()
		delete(a.step, t)
		a.tmu.Unlock()
		a.dropSessions(t)
	}))
}
