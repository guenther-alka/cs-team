package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
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

	mu  sync.RWMutex
	cur stored
}

type stored struct {
	Host    string     `json:"host,omitempty"`
	Port    string     `json:"port,omitempty"`
	User    string     `json:"user,omitempty"`
	Pass    string     `json:"pass,omitempty"`
	From    string     `json:"from,omitempty"`
	TLS     string     `json:"tls,omitempty"`
	Public  string     `json:"public,omitempty"`
	Private *bool      `json:"private,omitempty"`
	Video   []VideoOpt `json:"video,omitempty"`   // Videochat-Server (bis zu 3)
	VSecret string     `json:"vsecret,omitempty"` // Geheimnis für Raumnamen
	RTC     RTCCfg     `json:"rtc"`               // eingebauter Videochat (WebRTC)
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

// PublicURL: öffentliche Adresse ohne Schrägstrich am Ende (leer = unbekannt).
func (s *Settings) PublicURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur.Public
}

func noCtl(x string) bool { return !strings.ContainsAny(x, "\r\n\x00") }

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
		s.mu.RLock()
		own := s.cur
		s.mu.RUnlock()
		json.NewEncoder(w).Encode(map[string]any{
			"host": c.Host, "port": c.Port, "user": c.User, "from": c.From, "tls": c.TLS, "passSet": c.Pass != "",
			"public": own.Public, "private": s.AllowPrivate(), "enabled": c.Enabled(),
			"envHost": s.Env.Host != "", // Vorgabe aus den Startparametern vorhanden
			"video":   s.Video(),
			"rtc":     map[string]any{"on": rtc.On, "stun": rtc.Stun, "turn": rtc.Turn, "secretSet": rtc.Secret != "", "defStun": DefaultSTUN},
		})
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
