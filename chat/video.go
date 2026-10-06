package chat

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cs-team/auth"
)

// Videochat über externe Server (Jitsi, MiroTalk, ...): cs-team legt nur den Raum fest und lädt ein.
// Der Server ruft die Videoserver nie auf. Der Raumname wird aus einem Server-Geheimnis abgeleitet (HMAC), ist also nicht
// erratbar; er steht nie in der Nachricht, sondern wird beim Beitritt neu berechnet (mit Leserecht und Ablaufprüfung).
//
// Platz 1 ("group"): fester Raum je Gruppe (z.B. Klassenraum), bleibt bestehen.
// Platz 2 und 3 ("adhoc"): jeder Start legt einen neuen Raum an, die Einladung (Chat-Nachricht) gilt 24 Stunden;
// Platz 2 nur für Gruppen-Admins, Platz 3 für alle mit Schreibrecht im Kanal.

// AdhocTTL: Gültigkeit einer Ad-hoc-Einladung (in Tests kürzer).
var AdhocTTL = 24 * time.Hour

const (
	VideoSlots = 3

	dedupeAdhoc = time.Minute      // zweiter Klick kurz nach dem ersten: gleicher Raum
	dedupeGroup = 30 * time.Minute // Einladung in den Kanal höchstens alle 30 Minuten
)

type VideoOpt struct {
	Name string `json:"name"`
	URL  string `json:"url"`  // Vorlage mit {room}; leer = Platz unbenutzt
	Mode string `json:"mode"` // adhoc | group
	Who  string `json:"who"`  // member (alle mit Schreibrecht im Kanal) | admin (nur Gruppen-Admins)
}

// Vid hängt an der Einladungsnachricht (kein Raumname, keine Adresse).
type Vid struct {
	Slot int    `json:"slot"`
	Name string `json:"name"`
	Mode string `json:"mode"`
	Exp  int64  `json:"exp,omitempty"` // Unix-Millisekunden, 0 = ohne Ablauf
}

type videoRow struct {
	Slot int    `json:"slot"`
	Name string `json:"name"`
	Mode string `json:"mode"`
	Can  bool   `json:"can"` // darf in dieser Gruppe starten
}

// Die drei Plätze: 1 fester Raum je Gruppe (alle Mitglieder), 2 Ad-hoc-Raum nur für Gruppen-Admins, 3 Ad-hoc-Raum für alle Schreiber.
// Je Platz wählt der Admin den Anbieter (Name + Adresse mit {room}) oder lässt ihn leer (= aus).
var videoKinds = [VideoSlots]struct{ mode, who string }{{"group", "member"}, {"adhoc", "admin"}, {"adhoc", "member"}}

func cleanVideo(in []VideoOpt) ([]VideoOpt, error) {
	if len(in) > VideoSlots {
		return nil, errors.New("at most 3 video servers")
	}
	out := make([]VideoOpt, VideoSlots)
	for i, o := range in {
		o.Name, o.URL, o.Mode, o.Who = strings.TrimSpace(o.Name), strings.TrimSpace(o.URL), strings.TrimSpace(o.Mode), strings.TrimSpace(o.Who)
		if o.URL == "" {
			continue
		}
		if !noCtl(o.Name+o.URL) || len(o.URL) > 300 || len([]rune(o.Name)) > 40 {
			return nil, errors.New("invalid characters or too long")
		}
		if strings.Count(o.URL, "{room}") != 1 {
			return nil, errors.New("address must contain {room} exactly once")
		}
		u, err := url.Parse(strings.Replace(o.URL, "{room}", "room", 1))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return nil, errors.New("address: http(s)://host/…{room}")
		}
		if o.Name == "" {
			o.Name = u.Hostname()
		}
		o.Mode, o.Who = videoKinds[i].mode, videoKinds[i].who // Art und Berechtigung ergeben sich aus dem Platz
		out[i] = o
	}
	return out, nil
}

func (s *Settings) Video() []VideoOpt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]VideoOpt, VideoSlots)
	copy(out, s.cur.Video)
	return out
}

func (s *Settings) setVideo(v []VideoOpt) error {
	v, err := cleanVideo(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	n.Video = v
	if err := s.persist(n); err != nil {
		return err
	}
	return nil
}

// persist speichert (s.mu gehalten) und übernimmt den neuen Stand.
func (s *Settings) persist(n stored) error {
	b, _ := json.Marshal(n)
	if _, err := s.St.Put(context.Background(), settingsKey, b, ""); err != nil {
		return err
	}
	s.cur = n
	return nil
}

// secret: Server-Geheimnis für Raumnamen (wird beim ersten Gebrauch erzeugt, nie ausgegeben).
func (s *Settings) secret() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cur.VSecret) != 64 {
		b := make([]byte, 32)
		rand.Read(b)
		n := s.cur
		n.VSecret = hex.EncodeToString(b)
		if s.persist(n) != nil {
			return b // nicht gespeichert: gilt nur bis zum Neustart
		}
	}
	b, _ := hex.DecodeString(s.cur.VSecret)
	return b
}

// slug: Gruppenname als Teil des Raumnamens (nur a-z 0-9 -).
func slug(g string) string {
	var sb strings.Builder
	dash := true
	for _, r := range strings.ToLower(g) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			dash = false
		} else if !dash {
			sb.WriteByte('-')
			dash = true
		}
	}
	x := strings.Trim(sb.String(), "-")
	if len(x) > 24 {
		x = strings.Trim(x[:24], "-")
	}
	if x == "" {
		x = "room"
	}
	return x
}

func (s *Settings) room(mode, g, c string, id int64) string {
	m := hmac.New(sha256.New, s.secret())
	n := 6
	if mode == "group" {
		m.Write([]byte("g|" + g))
		n = 5
	} else {
		m.Write([]byte("a|" + g + "|" + c + "|" + strconv.FormatInt(id, 10)))
	}
	return slug(g) + "-" + hex.EncodeToString(m.Sum(nil))[:n*2]
}

func (o VideoOpt) link(room string) string { return strings.Replace(o.URL, "{room}", room, 1) }

// videoFor: Videochat-Einträge, die der Benutzer in der Gruppe sieht.
func (s *Svc) videoFor(user, g string, write bool) []videoRow {
	if s.Cfg == nil {
		return nil
	}
	var out []videoRow
	for i, o := range s.Cfg.Video() {
		if o.URL == "" {
			continue
		}
		out = append(out, videoRow{i, o.Name, o.Mode, write && (o.Who != "admin" || auth.IsGroupAdmin(user, g))})
	}
	if s.Cfg.RTC().On { // eingebauter Videochat: alle mit Schreibrecht
		out = append(out, videoRow{RTCSlot, "WebRTC", "rtc", write})
	}
	return out
}

func (s *Svc) videoRoutes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	h := func(fn http.HandlerFunc) http.Handler { return wrap(http.HandlerFunc(fn)) }
	// Raum starten bzw. betreten; adhoc: neue Einladung im Kanal, group: fester Raum (Einladung höchstens alle 30 Minuten)
	mux.Handle("POST /api/chat/{g}/{c}/video", h(func(w http.ResponseWriter, r *http.Request) {
		me, g, cn := auth.User(r.Context()), r.PathValue("g"), r.PathValue("c")
		var in struct{ Slot int }
		if s.Cfg == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&in) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if in.Slot == RTCSlot { // eingebauter Videochat: Einladung anlegen, beigetreten wird über die Chat-Verbindung
			id, err := s.rtcStart(r.Context(), me, g, cn)
			switch {
			case err == errNA:
				http.Error(w, "not available", 404)
			case err == errForbidden:
				http.Error(w, "forbidden", 403)
			case err != nil:
				http.Error(w, err.Error(), 400)
			default:
				json.NewEncoder(w).Encode(map[string]any{"rtc": true, "id": id})
			}
			return
		}
		opts := s.Cfg.Video()
		if in.Slot < 0 || in.Slot >= len(opts) || opts[in.Slot].URL == "" {
			http.Error(w, "not available", 404)
			return
		}
		o := opts[in.Slot]
		if _, wr := access(me, g); !wr || !s.exists(r.Context(), g, cn) || (o.Who == "admin" && !auth.IsGroupAdmin(me, g)) {
			http.Error(w, "forbidden", 403)
			return
		}
		now := time.Now()
		var last *Msg // letzte Einladung dieses Platzes im Kanal
		c := s.ch(r.Context(), g, cn)
		c.mu.Lock()
		for i := len(c.msgs) - 1; i >= 0; i-- {
			if v := c.msgs[i].Vid; v != nil && !c.msgs[i].Del && v.Slot == in.Slot && v.Mode == o.Mode {
				m := c.msgs[i]
				last = &m
				break
			}
		}
		c.mu.Unlock()
		age := time.Duration(0)
		if last != nil {
			age = now.Sub(time.UnixMicro(last.ID))
		}
		var id int64
		switch {
		case last != nil && o.Mode == "group" && age < dedupeGroup, last != nil && o.Mode == "adhoc" && age < dedupeAdhoc:
			id = last.ID // zweiter Klick: gleicher Raum, keine zweite Einladung
		default:
			v := &Vid{Slot: in.Slot, Name: o.Name, Mode: o.Mode}
			if o.Mode == "adhoc" {
				v.Exp = now.Add(AdhocTTL).UnixMilli()
			}
			m, err := s.post(r.Context(), me, g, cn, "📹 Videochat: "+o.Name, nil, v, nil)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			id = m.ID
		}
		json.NewEncoder(w).Encode(map[string]string{"url": o.link(s.Cfg.room(o.Mode, g, cn, id))})
	}))
	// Einladung annehmen: Leserecht und Ablauf werden hier geprüft, der Raumname nicht gespeichert
	mux.Handle("GET /api/chat/{g}/{c}/video/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		me, g, cn := auth.User(r.Context()), r.PathValue("g"), r.PathValue("c")
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if rd, _ := access(me, g); !rd || s.Cfg == nil || !s.exists(r.Context(), g, cn) {
			http.Error(w, "not found", 404)
			return
		}
		c := s.ch(r.Context(), g, cn)
		c.mu.Lock()
		var v *Vid
		if i := find(c, id); i >= 0 && !c.msgs[i].Del && c.msgs[i].Vid != nil {
			x := *c.msgs[i].Vid
			v = &x
		}
		c.mu.Unlock()
		if v != nil && v.Mode == "rtc" { // eingebauter Videochat
			if !s.Cfg.RTC().On {
				http.Error(w, "not found", 404)
			} else if v.Exp > 0 && time.Now().UnixMilli() > v.Exp {
				http.Error(w, "expired", http.StatusGone)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"rtc": true, "id": id})
			}
			return
		}
		opts := s.Cfg.Video()
		if v == nil || v.Slot < 0 || v.Slot >= len(opts) || opts[v.Slot].URL == "" {
			http.Error(w, "not found", 404)
			return
		}
		if v.Exp > 0 && time.Now().UnixMilli() > v.Exp {
			http.Error(w, "expired", http.StatusGone)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"url": opts[v.Slot].link(s.Cfg.room(v.Mode, g, cn, id))})
	}))
}
