package auth

// Namensraum-Anmeldung: "name" allein und "name@local" sind lokale cs-team-Konten (users.json, bcrypt) wie bisher.
// "name@<eigener Namensraum>" (z.B. anna@local.de) wird gegen das Verzeichnis geprüft und bekommt hier ein
// Spiegelkonto ohne Passwort, damit Gruppen, Bereiche und Freigaben unverändert funktionieren.

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// RealmLocal: Namensraum der lokalen Konten. "anna" und "anna@local" bezeichnen dasselbe Konto.
const RealmLocal = "local"

var (
	// ErrBadRealm: unbekannter Namensraum - die Anmeldung gehört nicht zu dieser Installation.
	ErrBadRealm = errors.New("unknown realm")
	// ErrNotAdmit: der Verzeichnisbenutzer steht in keiner der Aufnahme-Gruppen.
	ErrNotAdmit = errors.New("not admitted")
)

// validKey: Schlüssel in users.json. Lokale Konten wie bisher, Verzeichnisbenutzer zusätzlich mit Namensraum
// ("anna@local.de") - bewusst eng, damit Schlüssel sicher in Pfaden und Freigabelisten bleiben.
var validKey = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}(@[a-z0-9][a-z0-9.-]{0,63})?$`)

// loginID: zerlegter Anmeldename (Basic Auth).
type loginID struct {
	Key    string // Schlüssel in users.json: "anna" (lokal) oder "anna@local.de" (Verzeichnis)
	Name   string // Anmeldename ohne Namensraum
	Realm  string // Namensraum (klein geschrieben)
	Source string // "local" (users.json) oder "dir" (Verzeichnis)
	Local  bool   // Anmeldung über users.json
}

// Identity: Einstellungen der Anmeldung. Vorgabe aus den Startparametern (CS_IDENTITY_*), änderbar in der Oberfläche.
type Identity struct {
	Mode         string   // "local" (nur cs-team-Konten), "dir"/"mixed" (Verzeichnis und lokale Konten); leer = "local"
	Realm        string   // eigener Namensraum, z.B. "local.de"
	DefaultRealm string   // Namensraum für Namen ohne @ (leer = Realm); nur Anzeige/Anleitung in der Oberfläche
	AdmitGroups  []string // Verzeichnisgruppen, die zur Anmeldung berechtigen (leer = alle Benutzer)
	LocalGroup   string   // cs-team-Gruppe der angemeldeten Verzeichnisbenutzer (leer = Standardgruppe)
	AllowLocal   bool     // ohne Wirkung (0.55): lokale Konten sind immer erlaubt; Feld bleibt für alte Konfigurationen
	CacheDays    int      // Tage, die eine erfolgreiche Verzeichnis-Anmeldung ohne Verzeichnis gilt (Phase 3)
	URL          string   // LDAP-Adresse: ldap://host:389 oder ldaps://host:636
	Base         string   // Suchbasis, z.B. "DC=local,DC=de"
	BindDN       string   // Dienstkonto für die Gruppensuche (leer = Suche mit den Rechten des Benutzers)
	BindPW       string   // Passwort des Dienstkontos
	StartTLS     bool     // ldap:// mit StartTLS (statt einfachem Bind)
}

// DirRealm: Namensraum des Verzeichnisses - aus Realm, sonst aus der Suchbasis abgeleitet (DC=local,DC=de).
func (id Identity) DirRealm() string {
	if r := strings.ToLower(strings.TrimSpace(id.Realm)); r != "" {
		return r
	}
	return realmFromBase(id.Base)
}

// realmFromBase: "DC=local,DC=de" -> "local.de".
func realmFromBase(base string) string {
	var parts []string
	for _, x := range strings.Split(strings.ToLower(base), ",") {
		if x = strings.TrimSpace(x); strings.HasPrefix(x, "dc=") {
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(x, "dc=")))
		}
	}
	return strings.Join(parts, ".")
}

// HomeRealm: Namensraum dieser Installation (zum Anzeigen); ohne Verzeichnis "local".
func (id Identity) HomeRealm() string {
	if r := id.DirRealm(); r != "" {
		return r
	}
	return RealmLocal
}

// DisplayRealm: Namensraum, den die Oberfläche zum Anmelden nennt (DefaultRealm, sonst HomeRealm).
func (id Identity) DisplayRealm() string {
	if r := strings.ToLower(strings.TrimSpace(id.DefaultRealm)); r != "" {
		return r
	}
	return id.HomeRealm()
}

// DirOK: ist eine Verzeichnisanmeldung überhaupt möglich (Namensraum bekannt)?
func (id Identity) DirOK() bool { return id.DirRealm() != "" }

// LocalOK: lokale cs-team-Konten sind in JEDEM Modus anmeldefähig (Notfallzugang; KISS-Regel 0.55: "ohne Verzeichnis
// ist cs-team vollständig, das Verzeichnis ist nur eine Zusatz-Quelle"). Der Modus steuert also nur, ob zusätzlich das
// Verzeichnis befragt wird: "local"/"" nur lokale Konten, "dir"/"mixed" zusätzlich Verzeichnisbenutzer (name@realm).
func (id Identity) LocalOK() bool { return true }

// ValidRealm: gehört der Namensraum zu dieser Installation?
func (id Identity) ValidRealm(realm string) bool {
	realm = strings.ToLower(strings.TrimSpace(realm))
	switch {
	case realm == "":
		return false
	case realm == RealmLocal:
		return true
	default:
		return id.DirOK() && realm == id.DirRealm()
	}
}

// ModeName: Kurzbezeichnung für Protokoll und Oberfläche. "dir" und "mixed" sind dasselbe: lokale Konten bleiben
// immer möglich (0.55), beide gespeicherten Werte bleiben gültig.
func (id Identity) ModeName() string {
	switch strings.ToLower(strings.TrimSpace(id.Mode)) {
	case "dir", "mixed":
		return "directory + local"
	default:
		return "local accounts"
	}
}

// AdmitGroupNames: bereinigte Liste der berechtigenden Verzeichnisgruppen (leer = alle).
func (id Identity) AdmitGroupNames() []string {
	var out []string
	for _, g := range id.AdmitGroups {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// Group: cs-team-Gruppe, die Verzeichnisbenutzer bekommen.
func (id Identity) Group() string {
	if g := strings.TrimSpace(id.LocalGroup); g != "" {
		return g
	}
	return DefaultGroup
}

// Unencrypted: Verzeichnisanmeldung ohne TLS (Passwörter nur durch die NTLM-Versiegelung geschützt).
func (id Identity) Unencrypted() bool {
	if strings.TrimSpace(id.URL) == "" {
		return false
	}
	return !ldapSecure(ldapCheck{url: id.URL, startTLS: id.StartTLS})
}

// IdentitySource: liefert die wirksame Anmelde-Einstellung. In der Anwendung: die Einstellungen der Oberfläche
// (chat.Settings, Startparameter als Vorgabe); in Tests: fixedIdentity.
type IdentitySource interface {
	Identity() Identity
}

// fixedIdentity: feste Einstellung (Tests).
type fixedIdentity Identity

func (f fixedIdentity) Identity() Identity { return Identity(f) }

// SetIdentitySource: Anmeldequelle setzen (beim Start). Ohne Namensraum und Verzeichnis bleibt alles wie bisher.
func (a *Auth) SetIdentitySource(src IdentitySource) {
	a.mu.Lock()
	a.idSrc = src
	a.mu.Unlock()
	id := a.identity()
	go a.dropDirCache(context.Background(), id) // Zwischenspeicher entfernen, der nicht mehr zur Einstellung passt
	if !id.DirOK() {
		return
	}
	log.Printf("identity: realm %s, mode %s, admit %v", id.DirRealm(), id.ModeName(), id.AdmitGroupNames())
	if id.Unencrypted() {
		log.Printf("identity: WARNING - directory login without TLS (%s): use ldaps:// or StartTLS, otherwise the password is protected by the NTLM seal only", id.URL)
	}
	if id.CacheDays > 0 {
		log.Printf("identity: directory logins stay valid for %d days without the directory (bcrypt fingerprint in %s)", id.CacheDays, usersKey)
	}
	if g := id.Group(); g != DefaultGroup {
		if err := a.checkGroups(context.Background(), []string{g}); err != nil {
			log.Printf("identity: group %q does not exist yet - directory users have no rights until it is created", g)
		}
	}
}

// identity: die aktuelle Einstellung; Änderungen in der Oberfläche gelten sofort.
func (a *Auth) identity() Identity {
	a.mu.Lock()
	src, id := a.idSrc, a.id
	a.mu.Unlock()
	if src != nil {
		return src.Identity()
	}
	return id
}

// SetDirChecker: feste Verzeichnisprüfung setzen (Tests). nil = die Einstellung bestimmt sie.
func (a *Auth) SetDirChecker(d DirChecker) {
	a.mu.Lock()
	a.dir = d
	a.mu.Unlock()
}

// parseLogin: zerlegt den Basic-Auth-Namen. Namen ohne @ bleiben lokale Konten (unverändert wie bisher);
// "name@local" ist dasselbe Konto. Andere Namensräume gehen ans Verzeichnis.
func parseLogin(name string, id Identity) (loginID, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	base, realm, has := strings.Cut(n, "@")
	if !has {
		if !validKey.MatchString(n) {
			return loginID{}, ErrBadName
		}
		return loginID{Key: n, Name: n, Realm: RealmLocal, Source: "local", Local: true}, nil
	}
	if !validKey.MatchString(base) {
		return loginID{}, ErrBadName
	}
	switch {
	case realm == RealmLocal:
		return loginID{Key: base, Name: base, Realm: RealmLocal, Source: "local", Local: true}, nil
	case id.ValidRealm(realm) && validKey.MatchString(base+"@"+realm):
		return loginID{Key: base + "@" + realm, Name: base, Realm: realm, Source: "dir"}, nil
	default:
		return loginID{Key: n, Name: base, Realm: realm}, ErrBadRealm
	}
}

// verifyDirCached wie verifyDir, merkt sich aber erfolgreiche Anmeldungen authTTL lang - sonst kostet jede Anfrage
// mit Basic Auth eine Prüfung gegen das Verzeichnis. Ein im Verzeichnis geändertes Passwort wirkt spätestens nach
// authTTL; Phase 3 (Zwischenspeicher über CacheDays) erweitert das auf Zeiten ohne Verzeichnis.
func (a *Auth) verifyDirCached(ctx context.Context, id loginID, pass string) (Account, error) {
	k := sha256.Sum256(append(append(append(a.salt[:], id.Key...), 0), pass...))
	now := time.Now()
	a.mu.Lock()
	e, found := a.cache[k]
	a.mu.Unlock()
	if found && now.Before(e.exp) {
		if u, ok := a.get(ctx, id.Key); ok && !u.Disabled {
			return u, nil
		}
	}
	u, err := a.verifyDir(ctx, id, pass)
	if err != nil {
		return Account{}, err
	}
	h := sha256.Sum256([]byte(u.Hash))
	a.mu.Lock()
	if len(a.cache) >= maxCache {
		a.cache = map[[32]byte]cacheEnt{}
	}
	a.cache[k] = cacheEnt{h: h, exp: now.Add(authTTL)}
	a.mu.Unlock()
	return u, nil
}

// verifyDir: Anmeldung eines Verzeichnisbenutzers. Erfolg hält das Spiegelkonto aktuell. Ist das Verzeichnis nicht
// erreichbar, gilt der Zwischenspeicher (CacheDays), siehe verifyDirOffline.
func (a *Auth) verifyDir(ctx context.Context, id loginID, pass string) (Account, error) {
	if pass == "" {
		return Account{}, ErrBadDir
	}
	du, err := a.dirCheck().CheckDir(ctx, id.Name, pass)
	switch {
	case errors.Is(err, ErrDirDown):
		return a.verifyDirOffline(ctx, id, pass)
	case err != nil:
		return Account{}, err
	}
	if !admitted(a.identity(), du) {
		log.Printf("auth: %s refused: not in an admitted group (%v)", id.Key, du.Groups)
		return Account{}, ErrNotAdmit
	}
	return a.ensureDir(ctx, id, du, pass)
}

// admitted: ist der Verzeichnisbenutzer zur Anmeldung berechtigt? Ohne Aufnahme-Gruppen: jeder gültige Benutzer.
func admitted(id Identity, du DirUser) bool {
	want := id.AdmitGroupNames()
	if len(want) == 0 {
		return true
	}
	for _, have := range du.Groups {
		h := strings.TrimSpace(have)
		for _, w := range want {
			if strings.EqualFold(h, w) || strings.EqualFold(cn(h), w) {
				return true
			}
		}
	}
	return false
}

// cn: "CN=lehrer,OU=Schule" -> "lehrer"; ohne "=" unverändert.
func cn(s string) string {
	if i := strings.Index(s, "="); i >= 0 {
		if c, _, ok := strings.Cut(s, ","); ok {
			s = c
		}
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

// dirCacheWrite: kürzester Abstand, in dem der Zwischenspeicher fortgeschrieben wird. bcrypt ist teuer (rund 60 ms),
// und mehr als ein Schreibvorgang je Benutzer und Tag bringt nichts: die Offline-Frist (CacheDays) verlängert sich
// dadurch um höchstens diesen Zeitraum.
const dirCacheWrite = 24 * time.Hour

// ensureDir: Spiegelkonto des Verzeichnisbenutzers anlegen bzw. aktualisieren. Die Basisgruppe in cs-team kommt aus
// Identity.LocalGroup (Vorgabe: Standardgruppe); die Verzeichnisgruppen des Kontos stehen in Account.DirGroups, daraus
// ergeben sich die weiteren Mitgliedschaften über die Einträge Group.Dir und Group.Sub (KISS-Regel 0.55).
// Ohne Zwischenspeicher (CacheDays = 0) trägt das Konto keinen Passwort-Hash ("!"): geprüft wird immer das Verzeichnis.
// Mit CacheDays > 0 wird zusätzlich ein bcrypt-Abdruck des Verzeichnispassworts und der Zeitpunkt der letzten
// erfolgreichen Prüfung gespeichert - damit bleibt die Anmeldung auch ohne Verzeichnis möglich (verifyDirOffline).
func (a *Auth) ensureDir(ctx context.Context, id loginID, du DirUser, pass string) (Account, error) {
	idc := a.identity()
	grp, now := idc.Group(), time.Now()
	cur, have := a.get(ctx, id.Key)
	hash, seen := "!", ""
	if idc.CacheDays > 0 {
		hash, seen = dirCache(cur, pass, now)
	}
	dirs := dirRefs(du.Groups) // Verzeichnisgruppen des Kontos: Mitgliedschaft in weiteren Gruppen kommt daraus (0.55)
	if have && !dirStale(cur, id.Realm, grp, du.Mail, hash, seen, dirs) {
		return cur, nil // unverändert: den Speicher nicht bei jeder Anmeldung neu schreiben
	}
	err := a.mutate(ctx, func(m map[string]Account) error {
		u := m[id.Key]
		u.Hash, u.Disabled, u.Must = hash, false, false
		u.DirSeen = seen
		u.Realm, u.Source = id.Realm, "dir"
		u.Groups = []string{grp}
		u.DirGroups = dirs
		if du.Mail != "" {
			u.Mail = du.Mail
		}
		if u.Created == "" {
			u.Created = time.Now().UTC().Format(time.RFC3339)
		}
		m[id.Key] = u
		return nil
	})
	if err != nil {
		return Account{}, err
	}
	u, ok := a.get(ctx, id.Key)
	if !ok {
		return Account{}, ErrNoUser
	}
	return u, nil
}

// dirCache: Soll-Zustand des Zwischenspeichers (Abdruck des Passworts und Zeitpunkt der letzten Prüfung). Der Abdruck
// wird nur neu gerechnet, wenn keiner vorliegt oder das Passwort nicht mehr passt (geändertes Verzeichnispasswort);
// der Zeitpunkt wird höchstens alle dirCacheWrite fortgeschrieben.
func dirCache(cur Account, pass string, now time.Time) (hash, seen string) {
	hash, seen = cur.Hash, cur.DirSeen
	if hash != "" && hash != "!" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) == nil {
		if t, err := time.Parse(time.RFC3339, seen); err != nil || now.Sub(t) > dirCacheWrite {
			seen = now.UTC().Format(time.RFC3339)
		}
		return hash, seen
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return "!", cur.DirSeen // kein Abdruck möglich (z.B. Passwort länger als 72 Bytes)
	}
	return string(h), now.UTC().Format(time.RFC3339)
}

// dirStale: muss das Spiegelkonto neu geschrieben werden? hash/seen sind der Soll-Zustand (siehe dirCache);
// dirs sind die Verzeichnisgruppen des Kontos (dirRefs).
func dirStale(u Account, realm, group, mail, hash, seen string, dirs []string) bool {
	return u.Hash != hash || u.DirSeen != seen || u.Disabled || u.Must || u.Realm != realm || u.Source != "dir" ||
		len(u.Groups) != 1 || u.Groups[0] != group || !sameList(u.DirGroups, dirs) || (mail != "" && u.Mail != mail)
}

// verifyDirOffline: Anmeldung aus dem Zwischenspeicher, wenn das Verzeichnis nicht erreichbar ist (Identity.CacheDays).
// Der gespeicherte Abdruck gilt nur, wenn die letzte erfolgreiche Prüfung höchstens CacheDays zurückliegt. Fehlschläge
// bleiben "Verzeichnis nicht erreichbar" (kein falsches Passwort behaupten) und kosten dieselbe Rechenzeit.
func (a *Auth) verifyDirOffline(ctx context.Context, id loginID, pass string) (Account, error) {
	days := a.identity().CacheDays
	u, ok := a.get(ctx, id.Key)
	if !ok || days <= 0 || u.Source != "dir" || u.Realm != id.Realm || u.Disabled || u.Hash == "" || u.Hash == "!" {
		bcrypt.CompareHashAndPassword(a.dummy, []byte(pass))
		return Account{}, ErrDirDown
	}
	seen, err := time.Parse(time.RFC3339, u.DirSeen)
	if err != nil || time.Since(seen) > time.Duration(days)*24*time.Hour {
		bcrypt.CompareHashAndPassword(a.dummy, []byte(pass)) // Zwischenspeicher abgelaufen
		return Account{}, ErrDirDown
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(pass)) != nil {
		return Account{}, ErrDirDown
	}
	log.Printf("auth: %s logged in from the local cache (directory unreachable, last check %s, valid %d days)",
		id.Key, u.DirSeen, days)
	return u, nil
}

// dropDirCache: Zwischenspeicher entfernen, der nicht mehr zur Einstellung passt (Zwischenspeicher abgeschaltet oder
// anderer Namensraum). Läuft im Hintergrund beim Start; ein Fehler ist unkritisch, der Abdruck wird dann nicht benutzt.
func (a *Auth) dropDirCache(ctx context.Context, idc Identity) {
	realm, days := idc.DirRealm(), idc.CacheDays
	a.refresh(ctx)
	a.mu.Lock()
	need := false
	for _, u := range a.users {
		if u.Source == "dir" && (u.Hash != "" && u.Hash != "!" || u.DirSeen != "") && (days <= 0 || u.Realm != realm) {
			need = true
			break
		}
	}
	a.mu.Unlock()
	if !need {
		return
	}
	n := 0
	err := a.mutate(ctx, func(m map[string]Account) error {
		for k, u := range m {
			has := (u.Hash != "" && u.Hash != "!") || u.DirSeen != ""
			if u.Source != "dir" || !has || days > 0 && u.Realm == realm {
				continue
			}
			u.Hash, u.DirSeen = "!", ""
			m[k], n = u, n+1
		}
		return nil
	})
	if err != nil {
		log.Printf("identity: Zwischenspeicher konnte nicht bereinigt werden: %v", err)
		return
	}
	if n > 0 {
		a.invalidate()
		log.Printf("identity: Zwischenspeicher entfernt (betroffen: %d)", n)
	}
}
