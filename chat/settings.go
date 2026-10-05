package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
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

	// Anmeldung (Namensraum + Verzeichnis): leer = Vorgabe aus den Startparametern
	IdMode     string    `json:"idMode,omitempty"`     // "local", "dir" oder "mixed"
	IdRealm    string    `json:"idRealm,omitempty"`    // eigener Namensraum, z.B. "local.de"
	IdDefRealm string    `json:"idDefRealm,omitempty"` // Namensraum für Namen ohne @ (Anzeige in der Oberfläche)
	IdAdmit    *[]string `json:"idAdmit,omitempty"`    // Aufnahme-Gruppen im Verzeichnis (leer = alle)
	IdGroup    string    `json:"idGroup,omitempty"`    // cs-team-Gruppe der Verzeichnisbenutzer
	IdLocal    *bool     `json:"idLocal,omitempty"`    // lokale cs-team-Konten zusätzlich erlaubt
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
