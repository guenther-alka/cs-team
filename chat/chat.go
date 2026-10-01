// Package chat: Gruppen-Chat mit Kanälen (pro Gruppe immer "allgemein", weitere je nach Gruppeneinstellung) per WebSocket
// mit Verlauf, Anhängen, Bearbeiten, Löschen und Emoji-Reaktionen.
// Zugriff: Mitglieder der Gruppe; Modus der Gruppe: member (alle schreiben), admin (nur Gruppen-Admins schreiben), off.
package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"cs-team/auth"
	"cs-team/store"
)

const (
	maxText   = 4000
	maxKeep   = 1000 // Nachrichten je Kanal (ältere fallen weg, samt Anhängen)
	histPage  = 100
	maxChans  = 20 // Kanäle je Gruppe
	DefaultCh = "allgemein"
)

var (
	Reactions = []string{"👍", "❤️", "😂", "🎉", "😮", "😢"}
	reChan    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,29}$`)
)

type Att struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type"`
}

type Msg struct {
	ID  int64               `json:"id"`
	By  string              `json:"by"`
	T   string              `json:"t,omitempty"`
	Ed  bool                `json:"ed,omitempty"`
	Att *Att                `json:"att,omitempty"`
	Re  map[string][]string `json:"re,omitempty"`
	Del bool                `json:"del,omitempty"`
	Vid *Vid                `json:"vid,omitempty"` // Einladung zu einem Videochat (siehe video.go)
}

type channel struct {
	mu   sync.Mutex
	g, c string
	msgs []Msg
	last int64
}

type chanInfo struct {
	By      string `json:"by"`
	Created int64  `json:"created"`
}

type client struct {
	user string
	out  chan []byte
	done chan struct{} // wird (unter s.mu, genau einmal) geschlossen; out selbst bleibt offen -> kein Senden auf geschlossenen Kanal
}

type Svc struct {
	St    store.Store
	MaxMB int64 // Anhänge
	mu    sync.Mutex
	chans map[string]*channel
	lists map[string]map[string]chanInfo // Gruppe -> zusätzliche Kanäle
	conns map[*client]bool
	rate  map[string][]time.Time
	// AIReview: ist die KI-Auswertung für Chat-Vorfälle freigegeben? (wird von main gesetzt; nil = nein)
	AIReview func() bool
	// Cfg: Einstellungen (Videochat-Server); nil = kein Videochat
	Cfg *Settings
}

func New(st store.Store) *Svc {
	return &Svc{St: st, MaxMB: 10, chans: map[string]*channel{}, lists: map[string]map[string]chanInfo{}, conns: map[*client]bool{}, rate: map[string][]time.Time{}}
}

var (
	ErrNoChat   = errors.New("chat not available")
	ErrReadonly = errors.New("only group admins may write")
	ErrRate     = errors.New("too many messages")
	ErrNoChan   = errors.New("no such channel")
)

func chatKey(g, c string) string     { return "chat/" + g + "/" + c + ".json" }
func listKey(g string) string        { return "chat/" + g + "/_channels.json" }
func fileKey(g, c, id string) string { return "chat/f/" + g + "/" + c + "/" + id }

// access: darf der Benutzer den Chat der Gruppe lesen / schreiben?
func access(user, g string) (read, write bool) {
	info, ok := auth.GroupInfoOf(g)
	if !ok || info.Chat == auth.ModeOff || !auth.IsMember(user, g) {
		return
	}
	return true, info.Chat == auth.ModeMember || auth.IsGroupAdmin(user, g)
}

// canMake: darf der Benutzer in der Gruppe Kanäle anlegen?
func canMake(user, g string) bool {
	info, ok := auth.GroupInfoOf(g)
	if r, _ := access(user, g); !ok || !r {
		return false
	}
	return info.Chans == auth.ModeMember || (info.Chans == auth.ModeAdmin && auth.IsGroupAdmin(user, g))
}

func (s *Svc) list(ctx context.Context, g string) map[string]chanInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.lists[g]
	if !ok {
		l = map[string]chanInfo{}
		if b, _, err := s.St.Get(ctx, listKey(g)); err == nil {
			json.Unmarshal(b, &l)
		}
		s.lists[g] = l
	}
	return l
}

func (s *Svc) saveList(ctx context.Context, g string) {
	s.mu.Lock()
	b, _ := json.Marshal(s.lists[g])
	s.mu.Unlock()
	s.St.Put(ctx, listKey(g), b, "")
}

func (s *Svc) exists(ctx context.Context, g, c string) bool {
	if c == DefaultCh {
		return true
	}
	l := s.list(ctx, g)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := l[c]
	return ok
}

func (s *Svc) ch(ctx context.Context, g, cn string) *channel {
	k := g + "/" + cn
	s.mu.Lock()
	c, ok := s.chans[k]
	if !ok {
		c = &channel{g: g, c: cn}
		if b, _, err := s.St.Get(ctx, chatKey(g, cn)); err == nil {
			json.Unmarshal(b, &c.msgs)
		}
		if n := len(c.msgs); n > 0 {
			c.last = c.msgs[n-1].ID
		}
		s.chans[k] = c
	}
	s.mu.Unlock()
	return c
}

// save: Kanal sichern (c.mu gehalten), ältere Nachrichten samt Anhängen entfernen.
func (s *Svc) save(ctx context.Context, c *channel) {
	for len(c.msgs) > maxKeep {
		if a := c.msgs[0].Att; a != nil {
			s.St.Delete(ctx, fileKey(c.g, c.c, a.ID))
		}
		c.msgs = c.msgs[1:]
	}
	b, _ := json.Marshal(c.msgs)
	s.St.Put(ctx, chatKey(c.g, c.c), b, "")
}

func (s *Svc) limited(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	l := s.rate[user][:0]
	for _, t := range s.rate[user] {
		if now.Sub(t) < 10*time.Second {
			l = append(l, t)
		}
	}
	if len(l) >= 20 {
		s.rate[user] = l
		return true
	}
	s.rate[user] = append(l, now)
	return false
}

// broadcast an alle verbundenen Mitglieder der Gruppe.
func (s *Svc) broadcast(g string, v any) {
	b, _ := json.Marshal(v)
	s.mu.Lock()
	var cl []*client
	for c := range s.conns {
		cl = append(cl, c)
	}
	s.mu.Unlock()
	for _, c := range cl {
		if r, _ := access(c.user, g); !r {
			continue
		}
		select {
		case c.out <- b:
		case <-c.done:
		default: // zu langsam: Verbindung wird beendet, Client lädt neu
			s.mu.Lock()
			if s.conns[c] {
				delete(s.conns, c)
				close(c.done)
			}
			s.mu.Unlock()
		}
	}
}

func clean(t string) string { return strings.TrimSpace(strings.ReplaceAll(t, "\r\n", "\n")) }

// Post legt eine Nachricht an (auch für "Nachricht senden -> Gruppen-Chat": Kanal allgemein).
func (s *Svc) Post(ctx context.Context, user, g, cn, text string, att *Att) (*Msg, error) {
	return s.post(ctx, user, g, cn, text, att, nil)
}

func (s *Svc) post(ctx context.Context, user, g, cn, text string, att *Att, vid *Vid) (*Msg, error) {
	text = clean(text)
	if _, w := access(user, g); !w {
		if r, _ := access(user, g); r {
			return nil, ErrReadonly
		}
		return nil, ErrNoChat
	}
	if !s.exists(ctx, g, cn) {
		return nil, ErrNoChan
	}
	if (text == "" && att == nil) || utf8.RuneCountInString(text) > maxText {
		return nil, errors.New("bad text")
	}
	if s.limited(user) {
		return nil, ErrRate
	}
	c := s.ch(ctx, g, cn)
	c.mu.Lock()
	id := time.Now().UnixMicro() // passt verlustfrei in eine JavaScript-Zahl
	if id <= c.last {
		id = c.last + 1
	}
	c.last = id
	m := Msg{ID: id, By: user, T: text, Att: att, Vid: vid}
	c.msgs = append(c.msgs, m)
	s.save(ctx, c)
	c.mu.Unlock()
	s.broadcast(g, map[string]any{"t": "msg", "g": g, "c": cn, "m": m})
	return &m, nil
}

func find(c *channel, id int64) int {
	for i := len(c.msgs) - 1; i >= 0; i-- {
		if c.msgs[i].ID == id {
			return i
		}
	}
	return -1
}

// change: Nachricht bearbeiten/löschen/reagieren. fn läuft unter c.mu und liefert false bei Ablehnung.
func (s *Svc) change(ctx context.Context, user, g, cn string, id int64, fn func(m *Msg) bool) error {
	if r, _ := access(user, g); !r || !s.exists(ctx, g, cn) {
		return ErrNoChat
	}
	c := s.ch(ctx, g, cn)
	c.mu.Lock()
	i := find(c, id)
	if i < 0 || c.msgs[i].Del || !fn(&c.msgs[i]) {
		c.mu.Unlock()
		return errors.New("not allowed")
	}
	m := c.msgs[i]
	s.save(ctx, c)
	c.mu.Unlock()
	s.broadcast(g, map[string]any{"t": "upd", "g": g, "c": cn, "m": m})
	return nil
}

type inMsg struct {
	T    string `json:"t"`
	G    string `json:"g"`
	C    string `json:"c"`
	ID   int64  `json:"id"`
	Text string `json:"text"`
	E    string `json:"e"`
	Name string `json:"name"`
}

func (s *Svc) handle(ctx context.Context, user string, m inMsg) error {
	switch m.T {
	case "send":
		_, err := s.Post(ctx, user, m.G, m.C, m.Text, nil)
		return err
	case "edit":
		text := clean(m.Text)
		if text == "" || utf8.RuneCountInString(text) > maxText {
			return errors.New("bad text")
		}
		return s.change(ctx, user, m.G, m.C, m.ID, func(x *Msg) bool {
			if x.By != user || x.Vid != nil {
				return false
			}
			x.T, x.Ed = text, true
			return true
		})
	case "del":
		var att *Att
		err := s.change(ctx, user, m.G, m.C, m.ID, func(x *Msg) bool {
			if x.By != user && !auth.IsGroupAdmin(user, m.G) { // Autor oder Gruppen-Admin
				return false
			}
			att = x.Att
			x.T, x.Att, x.Re, x.Vid, x.Del = "", nil, nil, nil, true
			return true
		})
		if err == nil && att != nil {
			s.St.Delete(ctx, fileKey(m.G, m.C, att.ID))
		}
		return err
	case "react":
		ok := false
		for _, e := range Reactions {
			ok = ok || e == m.E
		}
		if !ok {
			return errors.New("bad emoji")
		}
		return s.change(ctx, user, m.G, m.C, m.ID, func(x *Msg) bool {
			if x.Re == nil {
				x.Re = map[string][]string{}
			}
			l := x.Re[m.E]
			for i, u := range l {
				if u == user {
					x.Re[m.E] = append(l[:i:i], l[i+1:]...)
					if len(x.Re[m.E]) == 0 {
						delete(x.Re, m.E)
					}
					return true
				}
			}
			x.Re[m.E] = append(l, user)
			return true
		})
	case "mkchan":
		return s.MakeChannel(ctx, user, m.G, m.Name)
	case "rmchan":
		return s.RemoveChannel(ctx, user, m.G, m.Name)
	}
	return errors.New("unknown")
}

// MakeChannel legt einen Kanal an (Recht laut Gruppeneinstellung).
func (s *Svc) MakeChannel(ctx context.Context, user, g, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !canMake(user, g) {
		return errors.New("not allowed")
	}
	if !reChan.MatchString(name) || name == DefaultCh {
		return errors.New("channel name: a-z 0-9 _ - (max 30)")
	}
	l := s.list(ctx, g)
	s.mu.Lock()
	if _, ok := l[name]; ok || len(l) >= maxChans {
		s.mu.Unlock()
		return errors.New("exists or too many channels")
	}
	l[name] = chanInfo{By: user, Created: time.Now().UnixNano()}
	s.mu.Unlock()
	s.saveList(ctx, g)
	s.broadcast(g, map[string]any{"t": "chans", "g": g, "chans": s.chanRows(ctx, g)})
	return nil
}

// RemoveChannel löscht einen Kanal samt Nachrichten und Anhängen (Gruppen-Admin oder Ersteller).
func (s *Svc) RemoveChannel(ctx context.Context, user, g, name string) error {
	l := s.list(ctx, g)
	s.mu.Lock()
	info, ok := l[name]
	s.mu.Unlock()
	if !ok || name == DefaultCh {
		return ErrNoChan
	}
	if r, _ := access(user, g); !r || (!auth.IsGroupAdmin(user, g) && info.By != user) {
		return errors.New("not allowed")
	}
	s.mu.Lock()
	delete(l, name)
	delete(s.chans, g+"/"+name)
	s.mu.Unlock()
	s.saveList(ctx, g)
	s.St.Delete(ctx, chatKey(g, name))
	if infos, err := s.St.List(ctx, "chat/f/"+g+"/"+name+"/"); err == nil {
		for _, i := range infos {
			s.St.Delete(ctx, i.Key)
		}
	}
	s.broadcast(g, map[string]any{"t": "chans", "g": g, "chans": s.chanRows(ctx, g)})
	return nil
}

type chanRow struct {
	Name string `json:"name"`
	By   string `json:"by,omitempty"`
	Last int64  `json:"last"`
}

func (s *Svc) chanRows(ctx context.Context, g string) []chanRow {
	l := s.list(ctx, g)
	s.mu.Lock()
	names := []string{}
	by := map[string]string{}
	for n, i := range l {
		names = append(names, n)
		by[n] = i.By
	}
	s.mu.Unlock()
	sort.Strings(names)
	out := []chanRow{}
	for _, n := range append([]string{DefaultCh}, names...) {
		c := s.ch(ctx, g, n)
		c.mu.Lock()
		out = append(out, chanRow{n, by[n], c.last})
		c.mu.Unlock()
	}
	return out
}

type groupRow struct {
	Name  string     `json:"name"`
	W     bool       `json:"w"`    // darf schreiben
	Adm   bool       `json:"adm"`  // Gruppen-Admin (darf alles löschen)
	Make  bool       `json:"make"` // darf Kanäle anlegen
	Chans []chanRow  `json:"chans"`
	Video []videoRow `json:"video,omitempty"` // Videochat-Einträge dieser Gruppe
}

func (s *Svc) groupsOf(ctx context.Context, user string) []groupRow {
	out := []groupRow{}
	for _, g := range auth.GroupsOf(user) {
		if r, w := access(user, g); r {
			out = append(out, groupRow{g, w, auth.IsGroupAdmin(user, g), canMake(user, g), s.chanRows(ctx, g), s.videoFor(user, g, w)})
		}
	}
	return out
}

func (s *Svc) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	h := func(fn http.HandlerFunc) http.Handler { return wrap(http.HandlerFunc(fn)) }
	mux.Handle("GET /api/chat/groups", h(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"groups": s.groupsOf(r.Context(), auth.User(r.Context())), "reactions": Reactions})
	}))
	mux.Handle("GET /api/chat/{g}/{c}/history", h(func(w http.ResponseWriter, r *http.Request) {
		g, cn := r.PathValue("g"), r.PathValue("c")
		if rd, _ := access(auth.User(r.Context()), g); !rd || !s.exists(r.Context(), g, cn) {
			http.Error(w, "not found", 404)
			return
		}
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		c := s.ch(r.Context(), g, cn)
		c.mu.Lock()
		end := len(c.msgs)
		if before > 0 {
			end = 0
			for end < len(c.msgs) && c.msgs[end].ID < before {
				end++
			}
		}
		start := end - histPage
		if start < 0 {
			start = 0
		}
		page := append([]Msg{}, c.msgs[start:end]...)
		c.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"msgs": page, "more": start > 0})
	}))
	mux.Handle("POST /api/chat/{g}/{c}/upload", h(func(w http.ResponseWriter, r *http.Request) {
		me, g, cn := auth.User(r.Context()), r.PathValue("g"), r.PathValue("c")
		if _, wr := access(me, g); !wr || !s.exists(r.Context(), g, cn) {
			http.Error(w, "forbidden", 403)
			return
		}
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" || len(name) > 200 || strings.ContainsAny(name, "/\\\r\n") {
			http.Error(w, "bad name", 400)
			return
		}
		max := s.MaxMB << 20
		id := strconv.FormatInt(time.Now().UnixNano(), 36)
		ctype := "application/octet-stream"
		if i := strings.LastIndex(name, "."); i >= 0 {
			if t := mime.TypeByExtension(strings.ToLower(name[i:])); t != "" {
				ctype = t
			}
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max+1))
		if err != nil || int64(len(data)) > max || len(data) == 0 {
			http.Error(w, "too large or empty", http.StatusRequestEntityTooLarge)
			return
		}
		if err := s.St.PutStream(r.Context(), fileKey(g, cn, id), bytes.NewReader(data), int64(len(data)), ctype); err != nil {
			http.Error(w, "store", 500)
			return
		}
		att := &Att{ID: id, Name: name, Size: int64(len(data)), Type: ctype}
		if _, err := s.Post(r.Context(), me, g, cn, r.URL.Query().Get("text"), att); err != nil {
			s.St.Delete(r.Context(), fileKey(g, cn, id))
			http.Error(w, err.Error(), 400)
		}
	}))
	mux.Handle("GET /api/chat/{g}/{c}/file/{id}", h(func(w http.ResponseWriter, r *http.Request) {
		g, cn, id := r.PathValue("g"), r.PathValue("c"), r.PathValue("id")
		if rd, _ := access(auth.User(r.Context()), g); !rd || !s.exists(r.Context(), g, cn) || strings.ContainsAny(id, "/.\\") {
			http.Error(w, "not found", 404)
			return
		}
		var att *Att // nur Anhänge ausliefern, die in einer vorhandenen Nachricht hängen
		c := s.ch(r.Context(), g, cn)
		c.mu.Lock()
		for i := len(c.msgs) - 1; i >= 0; i-- {
			if a := c.msgs[i].Att; a != nil && a.ID == id && !c.msgs[i].Del {
				att = a
				break
			}
		}
		c.mu.Unlock()
		if att == nil {
			http.Error(w, "not found", 404)
			return
		}
		rc, err := s.St.GetStream(r.Context(), fileKey(g, cn, id))
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		defer rc.Close()
		disp, ct := "attachment", "application/octet-stream"
		if att.Type == "image/png" || att.Type == "image/jpeg" || att.Type == "image/gif" || att.Type == "image/webp" {
			disp, ct = "inline", att.Type
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Content-Disposition", disp+"; filename*=UTF-8''"+urlEnc(att.Name))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		io.Copy(w, rc)
	}))
	mux.Handle("GET /api/chat/ws", h(s.ws))
	s.auditRoutes(mux, wrap)
	s.videoRoutes(mux, wrap)
}

func urlEnc(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			b.WriteByte(c)
		} else {
			b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)+256, 16)[1:]))
		}
	}
	return b.String()
}

func (s *Svc) ws(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 10)
	c := &client{user: me, out: make(chan []byte, 256), done: make(chan struct{})}
	s.mu.Lock()
	s.conns[c] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.conns[c] {
			delete(s.conns, c)
			close(c.done)
		}
		s.mu.Unlock()
	}()
	ctx := r.Context()
	hello, _ := json.Marshal(map[string]any{"t": "hello", "groups": s.groupsOf(ctx, me)})
	c.out <- hello
	go func() {
		for {
			select {
			case b := <-c.out:
				if conn.Write(ctx, websocket.MessageText, b) != nil {
					return
				}
			case <-c.done:
				conn.Close(websocket.StatusPolicyViolation, "resync")
				return
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var m inMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if err := s.handle(ctx, me, m); err != nil {
			b, _ := json.Marshal(map[string]any{"t": "err", "g": m.G, "m": err.Error()})
			select {
			case c.out <- b:
			case <-c.done:
			default:
			}
		}
	}
}
