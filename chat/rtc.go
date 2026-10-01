package chat

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Eingebauter Videochat (Platz 4, Ad-hoc, nur Browser): WebRTC im Mesh, höchstens RTCMax Teilnehmer.
// Der Server überträgt weder Bild noch Ton, sondern vermittelt nur die Verbindungsdaten (Signalisierung) über die
// vorhandene Chat-Verbindung (WebSocket). Der Raum ist die Einladungsnachricht (gültig AdhocTTL); beigetreten wird nur mit Leserecht
// im Kanal. STUN/TURN werden in den Einstellungen festgelegt; TURN mit zeitlich begrenzten Zugangsdaten (coturn "use-auth-secret").

const (
	RTCSlot     = VideoSlots // Platznummer in videoRow / Vid.Slot
	RTCMax      = 6          // Teilnehmer je Raum
	DefaultSTUN = "stun:stun.l.google.com:19302"

	rtcSigMax   = 14 << 10 // größte Signalnachricht (SDP / ICE), Byte
	rtcSigBurst = 300      // Signalnachrichten je Verbindung und Zeitfenster
	rtcSigWin   = 10 * time.Second
	turnTTL     = 12 * time.Hour
)

// RTCCfg: Einstellungen des eingebauten Videochats.
type RTCCfg struct {
	On     bool     `json:"on,omitempty"`
	Stun   []string `json:"stun,omitempty"`   // stun:host:port
	Turn   []string `json:"turn,omitempty"`   // turn:host:port[?transport=udp|tcp]
	Secret string   `json:"secret,omitempty"` // coturn static-auth-secret (nie ausgegeben)
}

type rtcIn struct {
	On     bool
	Stun   []string
	Turn   []string
	Secret *string // nil = unverändert, "" = löschen
}

var (
	reStun = regexp.MustCompile(`^stuns?:[A-Za-z0-9.\-_\[\]:]+$`)
	reTurn = regexp.MustCompile(`^turns?:[A-Za-z0-9.\-_\[\]:]+(\?transport=(udp|tcp))?$`)
)

func cleanICE(in []string, max int, re *regexp.Regexp) ([]string, error) {
	var out []string
	for _, x := range in {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		if len(x) > 200 || !re.MatchString(x) {
			return nil, errors.New("invalid server address: " + x)
		}
		out = append(out, x)
	}
	if len(out) > max {
		return nil, errors.New("too many servers")
	}
	return out, nil
}

// RTC: aktuelle Einstellung (Kopie).
func (s *Settings) RTC() RTCCfg {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.cur.RTC
	r.Stun = append([]string(nil), r.Stun...)
	r.Turn = append([]string(nil), r.Turn...)
	return r
}

func (s *Settings) setRTC(in rtcIn) error {
	st, err := cleanICE(in.Stun, 4, reStun)
	if err != nil {
		return err
	}
	tu, err := cleanICE(in.Turn, 3, reTurn)
	if err != nil {
		return err
	}
	if in.Secret != nil && (!noCtl(*in.Secret) || len(*in.Secret) > 200) {
		return errors.New("invalid characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.cur
	n.RTC = RTCCfg{On: in.On, Stun: st, Turn: tu, Secret: s.cur.RTC.Secret}
	if in.Secret != nil {
		n.RTC.Secret = *in.Secret
	}
	if len(tu) == 0 {
		n.RTC.Secret = ""
	} else if n.RTC.Secret == "" {
		return errors.New("turn: shared secret required")
	}
	return s.persist(n)
}

// ICE: Server-Liste für RTCPeerConnection (TURN-Zugang gilt turnTTL und ist an den Benutzer gebunden).
func (s *Settings) ICE(user string) (bool, []map[string]any) {
	r := s.RTC()
	if !r.On {
		return false, nil
	}
	var out []map[string]any
	if len(r.Stun) > 0 {
		out = append(out, map[string]any{"urls": r.Stun})
	}
	if len(r.Turn) > 0 && r.Secret != "" {
		name := strconv.FormatInt(time.Now().Add(turnTTL).Unix(), 10) + ":" + user
		m := hmac.New(sha1.New, []byte(r.Secret))
		m.Write([]byte(name))
		out = append(out, map[string]any{"urls": r.Turn, "username": name, "credential": base64.StdEncoding.EncodeToString(m.Sum(nil))})
	}
	return true, out
}

type rtcRoom struct{ peers map[string]*client } // Teilnehmerkennung -> Verbindung

func (s *Svc) rtcKey(g, c string, id int64) string {
	return g + "\x00" + c + "\x00" + strconv.FormatInt(id, 10)
}

// rtcActive: sind im Raum gerade Teilnehmer?
func (s *Svc) rtcActive(g, c string, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rtc[s.rtcKey(g, c, id)]
	return r != nil && len(r.peers) > 0
}

func (s *Svc) send(c *client, v any) {
	b, _ := json.Marshal(v)
	select {
	case c.out <- b:
	case <-c.done:
	default: // Warteschlange voll: Signal verwerfen (der Aufbau scheitert dann, Teilnehmer tritt neu bei)
	}
}

// rtcHandle: Nachrichten rtcjoin / rtcsig / rtcleave einer Chat-Verbindung.
func (s *Svc) rtcHandle(ctx context.Context, c *client, m inMsg) error {
	switch m.T {
	case "rtcjoin":
		return s.rtcJoin(ctx, c, m)
	case "rtcsig":
		return s.rtcSig(c, m)
	case "rtcleave":
		s.rtcLeave(c)
	}
	return nil
}

func (s *Svc) rtcJoin(ctx context.Context, c *client, m inMsg) error {
	if s.Cfg == nil {
		return errors.New("not available")
	}
	on, ice := s.Cfg.ICE(c.user)
	if rd, _ := access(c.user, m.G); !on || !rd || !s.exists(ctx, m.G, m.C) {
		return errors.New("not available")
	}
	ch := s.ch(ctx, m.G, m.C)
	ch.mu.Lock()
	var v *Vid
	if i := find(ch, m.ID); i >= 0 && !ch.msgs[i].Del && ch.msgs[i].Vid != nil {
		x := *ch.msgs[i].Vid
		v = &x
	}
	ch.mu.Unlock()
	if v == nil || v.Mode != "rtc" {
		return errors.New("not available")
	}
	if v.Exp > 0 && time.Now().UnixMilli() > v.Exp {
		return errors.New("expired")
	}
	s.rtcLeave(c) // eine Verbindung = höchstens ein Raum
	b := make([]byte, 4)
	rand.Read(b)
	pid := hex.EncodeToString(b)
	key := s.rtcKey(m.G, m.C, m.ID)
	type peer struct {
		Pid  string `json:"pid"`
		User string `json:"user"`
	}
	s.mu.Lock()
	if s.rtc == nil {
		s.rtc = map[string]*rtcRoom{}
	}
	r := s.rtc[key]
	if r == nil {
		r = &rtcRoom{peers: map[string]*client{}}
		s.rtc[key] = r
	}
	if len(r.peers) >= RTCMax {
		s.mu.Unlock()
		return errors.New("room full")
	}
	var others []*client
	peers := []peer{}
	for id, o := range r.peers {
		peers = append(peers, peer{id, o.user})
		others = append(others, o)
	}
	r.peers[pid] = c
	c.pid, c.room = pid, key
	s.mu.Unlock()
	s.send(c, map[string]any{"t": "rtcjoined", "g": m.G, "c": m.C, "id": m.ID, "pid": pid, "ice": ice, "peers": peers, "max": RTCMax})
	for _, o := range others {
		s.send(o, map[string]any{"t": "rtcpeer", "pid": pid, "user": c.user})
	}
	return nil
}

func (s *Svc) rtcSig(c *client, m inMsg) error {
	if len(m.Data) == 0 || len(m.Data) > rtcSigMax {
		return errors.New("bad signal")
	}
	now := time.Now()
	if now.Sub(c.sigT) > rtcSigWin {
		c.sigT, c.sigN = now, 0
	}
	if c.sigN++; c.sigN > rtcSigBurst {
		return ErrRate
	}
	s.mu.Lock()
	var to *client
	pid := c.pid
	if r := s.rtc[c.room]; r != nil && pid != "" {
		to = r.peers[m.To]
	}
	s.mu.Unlock()
	if to == nil || to == c {
		return nil
	}
	s.send(to, map[string]any{"t": "rtcsig", "from": pid, "user": c.user, "data": m.Data})
	return nil
}

// rtcLeave: Raum verlassen (auch beim Schließen der Verbindung); der Raum wird frei, wenn der Letzte geht.
func (s *Svc) rtcLeave(c *client) {
	s.mu.Lock()
	key, pid := c.room, c.pid
	c.room, c.pid = "", ""
	var others []*client
	if r := s.rtc[key]; r != nil && pid != "" {
		delete(r.peers, pid)
		for _, o := range r.peers {
			others = append(others, o)
		}
		if len(r.peers) == 0 {
			delete(s.rtc, key)
		}
	}
	s.mu.Unlock()
	for _, o := range others {
		s.send(o, map[string]any{"t": "rtcleft", "pid": pid})
	}
}

// rtcStart: Einladung (Platz RTCSlot) im Kanal anlegen bzw. die laufende wiederverwenden.
func (s *Svc) rtcStart(ctx context.Context, me, g, cn string) (int64, error) {
	if on, _ := s.Cfg.ICE(me); !on {
		return 0, errNA
	}
	if _, wr := access(me, g); !wr || !s.exists(ctx, g, cn) {
		return 0, errForbidden
	}
	now := time.Now()
	var last *Msg
	c := s.ch(ctx, g, cn)
	c.mu.Lock()
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if v := c.msgs[i].Vid; v != nil && !c.msgs[i].Del && v.Mode == "rtc" {
			m := c.msgs[i]
			last = &m
			break
		}
	}
	c.mu.Unlock()
	if last != nil && last.Vid.Exp > now.UnixMilli() && (now.Sub(time.UnixMicro(last.ID)) < dedupeAdhoc || s.rtcActive(g, cn, last.ID)) {
		return last.ID, nil // zweiter Klick bzw. Anruf läuft schon: dort beitreten
	}
	m, err := s.post(ctx, me, g, cn, "📹 Videochat (WebRTC)", nil, &Vid{Slot: RTCSlot, Name: "WebRTC", Mode: "rtc", Exp: now.Add(AdhocTTL).UnixMilli()})
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}

var (
	errNA        = errors.New("not available")
	errForbidden = errors.New("forbidden")
)
