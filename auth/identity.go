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
)

// RealmLocal: Namensraum der lokalen Konten. "anna" und "anna@local" bezeichnen dasselbe Konto.
const RealmLocal = "local"

var (
	// ErrBadRealm: unbekannter Namensraum - die Anmeldung gehört nicht zu dieser Installation.
	ErrBadRealm = errors.New("unknown realm")
	// ErrNotAdmit: der Verzeichnisbenutzer steht in keiner der Aufnahme-Gruppen.
	ErrNotAdmit = errors.New("not admitted")
	// ErrNoLocal: lokale cs-team-Konten sind nicht erlaubt (Einstellung "nur Verzeichnis").
	ErrNoLocal = errors.New("local login disabled")
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
	Mode         string   // "local" (nur cs-team), "dir" (nur Verzeichnis), "mixed" (beides); leer = "local"
	Realm        string   // eigener Namensraum, z.B. "local.de"
	DefaultRealm string   // Namensraum für Namen ohne @ (leer = Realm); nur Anzeige/Anleitung in der Oberfläche
	AdmitGroups  []string // Verzeichnisgruppen, die zur Anmeldung berechtigen (leer = alle Benutzer)
	LocalGroup   string   // cs-team-Gruppe der angemeldeten Verzeichnisbenutzer (leer = Standardgruppe)
	AllowLocal   bool     // lokale Konten zusätzlich erlaubt (bei Mode "dir")
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

// LocalOK: sind lokale cs-team-Konten erlaubt? Ohne Einrichtung bleibt alles wie bisher.
func (id Identity) LocalOK() bool {
	switch strings.ToLower(strings.TrimSpace(id.Mode)) {
	case "dir":
		return id.AllowLocal
	case "mixed":
		return true
	default: // "" oder "local": lokale Konten sind die Anmeldung
		return true
	}
}

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

// ModeName: Kurzbezeichnung für Protokoll und Oberfläche.
func (id Identity) ModeName() string {
	switch strings.ToLower(strings.TrimSpace(id.Mode)) {
	case "dir":
		return "directory only"
	case "mixed":
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
	if !id.DirOK() {
		return
	}
	log.Printf("identity: realm %s, mode %s, admit %v", id.DirRealm(), id.ModeName(), id.AdmitGroupNames())
	if id.Unencrypted() {
		log.Printf("identity: WARNING - directory login without TLS (%s): use ldaps:// or StartTLS, otherwise the password is protected by the NTLM seal only", id.URL)
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

// verifyDir: Anmeldung eines Verzeichnisbenutzers. Erfolg hält das Spiegelkonto aktuell.
func (a *Auth) verifyDir(ctx context.Context, id loginID, pass string) (Account, error) {
	if pass == "" {
		return Account{}, ErrBadDir
	}
	du, err := a.dirCheck().CheckDir(ctx, id.Name, pass)
	if err != nil {
		return Account{}, err
	}
	if !admitted(a.identity(), du) {
		log.Printf("auth: %s refused: not in an admitted group (%v)", id.Key, du.Groups)
		return Account{}, ErrNotAdmit
	}
	return a.ensureDir(ctx, id, du)
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

// ensureDir: Spiegelkonto des Verzeichnisbenutzers anlegen bzw. aktualisieren. Ohne brauchbaren Passwort-Hash
// ("!"): dieses Konto kann sich nicht mit einem lokalen Passwort anmelden, geprüft wird immer das Verzeichnis.
// Die Rechte in cs-team kommen aus der lokalen Gruppe (Standardgruppe oder Identity.LocalGroup).
func (a *Auth) ensureDir(ctx context.Context, id loginID, du DirUser) (Account, error) {
	grp := a.identity().Group()
	if u, ok := a.get(ctx, id.Key); ok && !dirStale(u, id.Realm, grp, du.Mail) {
		return u, nil // unverändert: den Speicher nicht bei jeder Anmeldung neu schreiben
	}
	err := a.mutate(ctx, func(m map[string]Account) error {
		u := m[id.Key]
		u.Hash, u.Disabled, u.Must = "!", false, false
		u.Realm, u.Source = id.Realm, "dir"
		u.Groups = []string{grp}
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

// dirStale: muss das Spiegelkonto neu geschrieben werden?
func dirStale(u Account, realm, group, mail string) bool {
	return u.Hash != "!" || u.Disabled || u.Must || u.Realm != realm || u.Source != "dir" ||
		len(u.Groups) != 1 || u.Groups[0] != group || (mail != "" && u.Mail != mail)
}
