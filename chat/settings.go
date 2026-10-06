package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"cs-team/auth"
	"cs-team/store"
)

// Settings: Einstellungen von cs-team, gepflegt in der Oberfläche (Titel "cs-team", nur globale Admins) und im Speicher
// abgelegt (settings.json). Die Startparameter (Umgebung: CS_SMTP_*, CS_CHAT_ALLOW_PRIVATE) gelten als Vorgabe, solange
// hier nichts gesetzt ist.
type Settings struct {
	St  store.Store
	Env SMTP // Vorgabe aus den Startparametern
	// EnvPrivate: Vorgabe für "Webhooks in private Netze"
	EnvPrivate bool
	// EnvQuotaMB: Vorgabe für das Dateikontingent (Startparameter CS_QUOTA_MB)
	EnvQuotaMB int64
	// EnvTrashDays: Vorgabe für die Aufbewahrung im Papierkorb (CS_TRASH_DAYS, Standard 30; 0 = aus)
	EnvTrashDays int
	// EnvIdentity: Vorgabe der Anmeldung (Namensraum + Verzeichnis, CS_IDENTITY_*)
	EnvIdentity auth.Identity

	mu  sync.RWMutex
	cur stored
}

type stored struct {
	Host      string     `json:"host,omitempty"`
	Port      string     `json:"port,omitempty"`
	User      string     `json:"user,omitempty"`
	Pass      string     `json:"pass,omitempty"`
	From      string     `json:"from,omitempty"`
	TLS       string     `json:"tls,omitempty"`
	Public    string     `json:"public,omitempty"`
	Private   *bool      `json:"private,omitempty"`
	Video     []VideoOpt `json:"video,omitempty"`     // Videochat-Server (bis zu 3)
	VSecret   string     `json:"vsecret,omitempty"`   // Geheimnis für Raumnamen
	RTC       RTCCfg     `json:"rtc"`                 // eingebauter Videochat (WebRTC)
	QuotaMB   *int64     `json:"quotaMB,omitempty"`   // Kontingent je Benutzer / Gruppenordner (MB, 0 = unbegrenzt)
	TrashDays *int       `json:"trashDays,omitempty"` // Papierkorb: Tage (0 = aus)
	PubDays   *int       `json:"pubDays,omitempty"`   // längste Gültigkeit öffentlicher Datei-Links: Tage (0 = unbegrenzt erlaubt; 0.60)
	Enf2FA    *bool      `json:"enf2fa,omitempty"`    // Zwei-Faktor-Anmeldung für Admin-Konten verlangen (0.60)
	ClosedDays *int      `json:"closedDays,omitempty"` // Aufbewahrung abgeschlossener Aufgaben: Tage (0 = unbegrenzt)
	Privacy   string     `json:"privacy,omitempty"`   // zusätzlicher Datenschutzhinweis (an die Hinweise zu KI und Videochat angehängt)

	// Anmeldung (Namensraum + Verzeichnis): leer = Vorgabe aus den Startparametern
	IdMode     string    `json:"idMode,omitempty"`     // "local", "dir" oder "mixed" ("dir" = wie "mixed", siehe 0.55)
	IdRealm    string    `json:"idRealm,omitempty"`    // eigener Namensraum, z.B. "local.de"
	IdDefRealm string    `json:"idDefRealm,omitempty"` // Namensraum für Namen ohne @ (Anzeige in der Oberfläche)
	IdAdmit    *[]string `json:"idAdmit,omitempty"`    // Aufnahme-Gruppen im Verzeichnis (leer = alle)
	IdGroup    string    `json:"idGroup,omitempty"`    // cs-team-Gruppe der Verzeichnisbenutzer
	IdLocal    *bool     `json:"idLocal,omitempty"`    // ohne Wirkung (0.55): lokale Konten sind immer erlaubt
	IdCache    *int      `json:"idCache,omitempty"`    // Tage ohne Verzeichnis (Phase 3)
	IdURL      string    `json:"idURL,omitempty"`      // ldap://host:389 oder ldaps://host:636
	IdBase     string    `json:"idBase,omitempty"`     // Suchbasis, z.B. DC=local,DC=de
	IdBindDN   string    `json:"idBindDn,omitempty"`   // Dienstkonto für die Gruppensuche
	IdBindPW   string    `json:"idBindPw,omitempty"`   // Passwort des Dienstkontos
	IdStartTLS *bool     `json:"idStartTls,omitempty"` // ldap:// mit StartTLS
}

const settingsKey = "settings.json"

func NewSettings(st store.Store, env SMTP, envPrivate bool) *Settings {
	s := &Settings{St: st, Env: env, EnvPrivate: envPrivate}
	if b, _, err := st.Get(context.Background(), settingsKey); err == nil {
		json.Unmarshal(b, &s.cur)
	}
	return s
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// SMTP: wirksame Mail-Einstellungen (gespeichert vor Startparameter).
func (s *Settings) SMTP() SMTP {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SMTP{Host: or(s.cur.Host, s.Env.Host), Port: or(s.cur.Port, s.Env.Port), User: or(s.cur.User, s.Env.User),
		Pass: or(s.cur.Pass, s.Env.Pass), From: or(s.cur.From, s.Env.From), TLS: or(s.cur.TLS, s.Env.TLS)}
}

func (s *Settings) AllowPrivate() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur.Private != nil {
		return *s.cur.Private
	}
	return s.EnvPrivate
}

// QuotaMB: wirksames Dateikontingent je Benutzer und Gruppenordner in MB (0 = unbegrenzt).
func (s *Settings) QuotaMB() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur.QuotaMB != nil {
		return *s.cur.QuotaMB
	}
	return s.EnvQuotaMB
}

// TrashDays: wirksame Aufbewahrung gelöschter Dateien in Tagen (0 = Papierkorb aus).
func (s *Settings) TrashDays() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur.TrashDays != nil {
		return *s.cur.TrashDays
	}
	return s.EnvTrashDays
}

func (s *Settings) setTrashDays(d int) error {
	if d < 0 || d > 3650 {
		return errors.New("trash: 0..3650 days")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	n.TrashDays = &d
	b, _ := json.Marshal(n)
	if _, err := s.St.Put(context.Background(), settingsKey, b, ""); err != nil {
		return err
	}
	s.cur = n
	return nil
}

// PubMaxDays (0.60): längste Gültigkeit öffentlicher Datei-Links in Tagen (0 = unbegrenzt erlaubt, Vorgabe).
func (s *Settings) PubMaxDays() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur.PubDays != nil {
		return *s.cur.PubDays
	}
	return 0
}

// Enforce2FA (0.60): müssen globale Admins mit lokalem Konto die Zwei-Faktor-Anmeldung einrichten (Vorgabe: nein)?
func (s *Settings) Enforce2FA() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur.Enf2FA != nil && *s.cur.Enf2FA
}

func (s *Settings) setPub2FA(days *int, enforce *bool) error {
	if days != nil && (*days < 0 || *days > 3650) {
		return errors.New("public links: 0..3650 days")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	if days != nil {
		n.PubDays = days
	}
	if enforce != nil {
		n.Enf2FA = enforce
	}
	b, _ := json.Marshal(n)
	if _, err := s.St.Put(context.Background(), settingsKey, b, ""); err != nil {
		return err
	}
	s.cur = n
	return nil
}

// ClosedDays: Aufbewahrung abgeschlossener Aufgaben in Tagen (0 = unbegrenzt, Vorgabe).
func (s *Settings) ClosedDays() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cur.ClosedDays != nil {
		return *s.cur.ClosedDays
	}
	return 0
}

func (s *Settings) setClosedDays(d int) error {
	if d < 0 || (d > 0 && d < 7) || d > 36500 { // 7 = auth.MinRetentionDays
		return errors.New("closed tasks: 0 (off) or 7..36500 days")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	n.ClosedDays = &d
	return s.persist(n)
}

// Privacy: zusätzlicher Datenschutzhinweis des Admins (leer = keiner).
func (s *Settings) Privacy() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur.Privacy
}

func (s *Settings) setPrivacy(text string) error {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if len([]rune(text)) > 2000 || strings.ContainsRune(text, 0) {
		return errors.New("privacy note: at most 2000 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	n.Privacy = text
	return s.persist(n)
}

// VideoAddr: Adressen der eingerichteten externen Videoserver (nur Rechnernamen, sortiert, durch Komma getrennt; leer = keine).
// Ändert sich die Angabe, muss der Hinweis zum Videochat erneut bestätigt werden (auth.AckAddr).
func (s *Settings) VideoAddr() string {
	seen := map[string]bool{}
	var hosts []string
	for _, o := range s.Video() {
		if o.URL == "" {
			continue
		}
		if u, err := url.Parse(strings.Replace(o.URL, "{room}", "room", 1)); err == nil && u.Host != "" && !seen[u.Host] {
			seen[u.Host] = true
			hosts = append(hosts, u.Host)
		}
	}
	sort.Strings(hosts)
	return strings.Join(hosts, ", ")
}

// Quota: Kontingent in Byte (für files.Svc.Quota).
func (s *Settings) Quota() int64 { return s.QuotaMB() << 20 }

func (s *Settings) setQuota(mb int64) error {
	if mb < 0 || mb > 1<<30 {
		return errors.New("quota: 0..1073741824 MB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	n.QuotaMB = &mb
	b, _ := json.Marshal(n)
	if _, err := s.St.Put(context.Background(), settingsKey, b, ""); err != nil {
		return err
	}
	s.cur = n
	return nil
}

// authIn: Anmeldung aus der Oberfläche. BindPW: nil = unverändert, "" = löschen.
// AllowLocal wird weiter angenommen (alte Oberflächen), hat aber keine Wirkung (0.55).
type authIn struct {
	Mode, Realm, DefaultRealm, LocalGroup, URL, Base, BindDN string
	AdmitGroups                                              []string
	BindPW                                                   *string
	AllowLocal                                               *bool
	CacheDays                                                *int
	StartTLS                                                 *bool
}

// setAuth: Anmeldung (Namensraum + Verzeichnis) speichern. Prüft die Werte, damit ein Tippfehler nicht alle
// Anmeldungen lahmlegt.
func (s *Settings) setAuth(in authIn) error {
	eff := s.Identity() // wirksame Werte: nicht gesetzte Felder behalten die Vorgabe
	in.Mode = strings.ToLower(strings.TrimSpace(in.Mode))
	in.Realm = strings.ToLower(strings.TrimSpace(in.Realm))
	in.DefaultRealm = strings.ToLower(strings.TrimSpace(in.DefaultRealm))
	in.LocalGroup, in.URL = strings.TrimSpace(in.LocalGroup), strings.TrimSpace(in.URL)
	in.Base, in.BindDN = strings.TrimSpace(in.Base), strings.TrimSpace(in.BindDN)
	switch in.Mode {
	case "", "local", "dir", "mixed":
	default:
		return errors.New("login mode: local, dir or mixed")
	}
	for _, x := range []string{in.Realm, in.DefaultRealm, in.LocalGroup, in.URL, in.Base, in.BindDN} {
		if !noCtl(x) || len(x) > 300 {
			return errors.New("invalid characters")
		}
	}
	if in.Realm != "" && !realmRe.MatchString(in.Realm) {
		return errors.New("realm: a-z 0-9 . - (like local.de)")
	}
	if in.DefaultRealm != "" && !realmRe.MatchString(in.DefaultRealm) {
		return errors.New("default realm: a-z 0-9 . - (like local.de)")
	}
	if in.URL != "" && !ldapRe.MatchString(in.URL) {
		return errors.New("directory address: ldap://host[:port] or ldaps://host[:port]")
	}
	if in.LocalGroup != "" && !groupRe.MatchString(in.LocalGroup) {
		return errors.New("group: a-z A-Z 0-9 . _ - (max 64)")
	}
	if len(in.AdmitGroups) > 32 {
		return errors.New("admit groups: max 32")
	}
	admit := []string{}
	for _, g := range in.AdmitGroups {
		if g = strings.TrimSpace(g); g == "" {
			continue
		}
		if !groupRe.MatchString(g) {
			return errors.New("admit group " + g + ": a-z A-Z 0-9 . _ - (max 64)")
		}
		admit = append(admit, g)
	}
	if in.CacheDays != nil && (*in.CacheDays < 0 || *in.CacheDays > 365) {
		return errors.New("offline cache: 0..365 days")
	}
	if in.StartTLS != nil && *in.StartTLS && strings.HasPrefix(in.URL, "ldaps://") {
		return errors.New("starttls: not needed with ldaps://")
	}
	if in.Mode == "dir" || in.Mode == "mixed" { // Verzeichnisanmeldung braucht Adresse und Namensraum
		url, realm, base := or(in.URL, eff.URL), or(in.Realm, eff.Realm), or(in.Base, eff.Base)
		if url == "" || (realm == "" && base == "") {
			return errors.New("directory login: address (ldap://...) and realm (or base) are needed")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	n.IdMode, n.IdRealm, n.IdDefRealm, n.IdGroup = in.Mode, in.Realm, in.DefaultRealm, in.LocalGroup
	if len(admit) > 0 { // leer = Vorgabe aus den Startparametern (bzw. alle Benutzer)
		n.IdAdmit = &admit
	} else {
		n.IdAdmit = nil
	}
	n.IdURL, n.IdBase, n.IdBindDN = in.URL, in.Base, in.BindDN
	switch {
	case in.BindPW != nil:
		n.IdBindPW = *in.BindPW
	case in.BindDN != or(s.cur.IdBindDN, s.EnvIdentity.BindDN) || in.URL != or(s.cur.IdURL, s.EnvIdentity.URL):
		n.IdBindPW = "" // Ziel geändert: das Passwort des Dienstkontos nie an einen anderen Server weitergeben
	}
	n.IdLocal, n.IdCache, n.IdStartTLS = in.AllowLocal, in.CacheDays, in.StartTLS
	b, _ := json.Marshal(n)
	if _, err := s.St.Put(context.Background(), settingsKey, b, ""); err != nil {
		return err
	}
	s.cur = n
	return nil
}

// PublicURL: öffentliche Adresse ohne Schrägstrich am Ende (leer = unbekannt).
func (s *Settings) PublicURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur.Public
}

func noCtl(x string) bool { return !strings.ContainsAny(x, "\r\n\x00") }

var (
	realmRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`)                 // Namensraum: local.de
	ldapRe  = regexp.MustCompile(`^(ldap|ldaps)://[a-z0-9.-]+(:[0-9]{1,5})?$`) // Verzeichnisadresse
	groupRe = regexp.MustCompile(`^[a-zA-Z0-9 _.-]{1,64}$`)                    // Gruppenname
)

// Identity: wirksame Anmelde-Einstellung (gespeicherte Werte vor den Startparametern). auth.Auth liest sie bei jeder
// Anmeldung, deshalb gelten Änderungen in der Oberfläche sofort.
func (s *Settings) Identity() auth.Identity {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := s.EnvIdentity
	c := s.cur
	if c.IdMode != "" {
		id.Mode = c.IdMode
	}
	if c.IdRealm != "" {
		id.Realm = c.IdRealm
	}
	if c.IdDefRealm != "" {
		id.DefaultRealm = c.IdDefRealm
	}
	if c.IdAdmit != nil {
		id.AdmitGroups = *c.IdAdmit
	}
	if c.IdGroup != "" {
		id.LocalGroup = c.IdGroup
	}
	if c.IdLocal != nil {
		id.AllowLocal = *c.IdLocal
	}
	if c.IdCache != nil {
		id.CacheDays = *c.IdCache
	}
	if c.IdURL != "" {
		id.URL = c.IdURL
	}
	if c.IdBase != "" {
		id.Base = c.IdBase
	}
	if c.IdBindDN != "" {
		id.BindDN = c.IdBindDN
	}
	if c.IdBindPW != "" {
		id.BindPW = c.IdBindPW
	}
	if c.IdStartTLS != nil {
		id.StartTLS = *c.IdStartTLS
	}
	return id
}

type settingsIn struct {
	Host, Port, User, From, TLS, Public string
	Pass                                *string // nil = unverändert, "" = löschen
	Private                             bool
}

func (s *Settings) set(in settingsIn) error {
	in.Host, in.Port, in.User, in.From, in.TLS = strings.TrimSpace(in.Host), strings.TrimSpace(in.Port), strings.TrimSpace(in.User), strings.TrimSpace(in.From), strings.TrimSpace(in.TLS)
	in.Public = strings.TrimRight(strings.TrimSpace(in.Public), "/")
	for _, x := range []string{in.Host, in.Port, in.User, in.From, in.TLS, in.Public} {
		if !noCtl(x) || len(x) > 300 {
			return errors.New("invalid characters")
		}
	}
	if in.Pass != nil && (!noCtl(*in.Pass) || len(*in.Pass) > 300) {
		return errors.New("invalid characters")
	}
	if strings.ContainsAny(in.Host, " /\\") {
		return errors.New("smtp host: name only, without port")
	}
	if in.Port != "" {
		if n, err := strconv.Atoi(in.Port); err != nil || n < 1 || n > 65535 {
			return errors.New("smtp port: 1..65535")
		}
	}
	switch in.TLS {
	case "", "starttls", "ssl", "none":
	default:
		return errors.New("smtp encryption: starttls, ssl or none")
	}
	if in.From != "" {
		if _, err := mail.ParseAddress(in.From); err != nil {
			return errors.New("sender address invalid")
		}
	}
	if in.Public != "" {
		u, err := url.Parse(in.Public)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("public address: http(s)://host[:port]")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	n.Host, n.Port, n.User, n.From, n.TLS, n.Public = in.Host, in.Port, in.User, in.From, in.TLS, in.Public
	if in.Pass != nil {
		n.Pass = *in.Pass
	} else if in.Host != or(s.cur.Host, s.Env.Host) || in.User != or(s.cur.User, s.Env.User) {
		n.Pass = "" // Ziel geändert: gespeichertes Passwort nie an einen neuen Server/Benutzer weitergeben
	}
	p := in.Private
	n.Private = &p
	b, _ := json.Marshal(n)
	if _, err := s.St.Put(context.Background(), settingsKey, b, ""); err != nil {
		return err
	}
	s.cur = n
	return nil
}

func (s *Settings) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler, m *Mailer) {
	admin := func(fn http.HandlerFunc) http.Handler {
		return wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !auth.IsAdmin(r.Context()) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			fn(w, r)
		}))
	}
	mux.Handle("GET /api/settings", admin(func(w http.ResponseWriter, r *http.Request) {
		c, rtc := s.SMTP(), s.RTC()
		idc := s.Identity()
		s.mu.RLock()
		own := s.cur
		s.mu.RUnlock()
		json.NewEncoder(w).Encode(map[string]any{
			"host": c.Host, "port": c.Port, "user": c.User, "from": c.From, "tls": c.TLS, "passSet": c.Pass != "",
			"public": own.Public, "private": s.AllowPrivate(), "enabled": c.Enabled(),
			"envHost":   s.Env.Host != "", // Vorgabe aus den Startparametern vorhanden
			"video":     s.Video(),
			"quotaMB":   s.QuotaMB(),
			"trashDays": s.TrashDays(),
			"closedDays": s.ClosedDays(),
			"pubMaxDays": s.PubMaxDays(),
			"enforce2fa": s.Enforce2FA(),
			"privacy":   own.Privacy,
			"rtc":       map[string]any{"on": rtc.On, "stun": rtc.Stun, "turn": rtc.Turn, "secretSet": rtc.Secret != "", "defStun": DefaultSTUN},
			"identity": map[string]any{
				"mode": own.IdMode, "realm": own.IdRealm, "defaultRealm": own.IdDefRealm, "admit": own.IdAdmit,
				"group": own.IdGroup, "allowLocal": own.IdLocal, "cacheDays": own.IdCache, "url": own.IdURL,
				"base": own.IdBase, "bindDn": own.IdBindDN, "bindPwSet": own.IdBindPW != "", "startTls": own.IdStartTLS,
				"effective": map[string]any{"mode": idc.ModeName(), "realm": idc.DirRealm(), "displayRealm": idc.DisplayRealm(),
					"admit": idc.AdmitGroupNames(), "group": idc.Group(), "allowLocal": idc.LocalOK(), "url": idc.URL,
					"unencrypted": idc.Unencrypted()},
			},
		})
	}))
	mux.Handle("POST /api/settings/quota", admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ MB int64 }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.setQuota(in.MB); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("POST /api/settings/trash", admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Days int }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.setTrashDays(in.Days); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("POST /api/settings/pub2fa", admin(func(w http.ResponseWriter, r *http.Request) { // 0.60: Obergrenze öffentlicher Links, 2FA-Pflicht für Admins
		var in struct {
			PubDays *int
			Enforce *bool
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.setPub2FA(in.PubDays, in.Enforce); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("POST /api/settings/closedtasks", admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Days *int }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) != nil || in.Days == nil { // "null"/fehlend: nicht stillschweigend 0
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.setClosedDays(*in.Days); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("POST /api/settings/privacy", admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Text string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.setPrivacy(in.Text); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("POST /api/settings/auth", admin(func(w http.ResponseWriter, r *http.Request) {
		var in authIn
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.setAuth(in); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("POST /api/settings/rtc", admin(func(w http.ResponseWriter, r *http.Request) {
		var in rtcIn
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.setRTC(in); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("POST /api/settings/video", admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Options []VideoOpt }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.setVideo(in.Options); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("POST /api/settings", admin(func(w http.ResponseWriter, r *http.Request) {
		var in settingsIn
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.set(in); err != nil {
			http.Error(w, err.Error(), 400)
		}
	}))
	// Testmail an die eigene Adresse des Admins
	mux.Handle("POST /api/settings/testmail", admin(func(w http.ResponseWriter, r *http.Request) {
		me := auth.User(r.Context())
		to, _, _ := auth.ContactOf(me)
		if to == "" {
			http.Error(w, "set your own e-mail address first (Konto)", 400)
			return
		}
		if err := s.SMTP().Send([]string{to}, "cs-team: Testmail", "Die Mail-Einstellungen von cs-team funktionieren.", ""); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"to": to})
	}))
}
