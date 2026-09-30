package doc

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"cs-team/auth"
	"cs-team/store"
)

const (
	maxValue = 64 << 10
	maxItems = 200000
)

type Meta struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"` // "sheet" | "text"
	Owner string   `json:"owner"`
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
}

func (m *Meta) Level(user string) (read, write bool) {
	read, write = m.level(user)
	return read, write && auth.WriteArea(user, Area(m.Type)) // Bereich nur "lesen": nie schreiben
}

func (m *Meta) level(user string) (read, write bool) {
	if user == m.Owner {
		return true, true
	}
	if auth.Allowed(m.Write, user) { // "*" = alle, "g:<gruppe>" = Gruppe, sonst Benutzername
		return true, true
	}
	return auth.Allowed(m.Read, user), false
}

type client struct {
	user  string
	write bool
	out   chan []byte
}

// lock: weiche Sperre auf Zelle/Absatz, solange dort jemand schreibt (Heartbeat vom Client, verfällt nach lockTTL).
type lock struct {
	user string
	exp  time.Time
	c    *client
}

const lockTTL = 30 * time.Second

type live struct {
	st      store.Store
	mu      sync.Mutex
	id      string
	meta    Meta
	doc     *Doc
	last    int64
	clients map[*client]struct{}
	timer   *time.Timer
	locks   map[string]lock
}

type Hub struct {
	st   store.Store
	mu   sync.Mutex
	docs map[string]*live
}

func NewHub(st store.Store) *Hub { return &Hub{st: st, docs: map[string]*live{}} }

func metaKey(id string) string { return "doc/" + id + "/meta.json" }
func snapKey(id string) string { return "doc/" + id + "/snapshot.json" }

func (h *Hub) open(ctx context.Context, id string) (*live, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l, ok := h.docs[id]; ok {
		return l, nil
	}
	mb, _, err := h.st.Get(ctx, metaKey(id))
	if err != nil {
		return nil, err
	}
	l := &live{st: h.st, id: id, doc: NewDoc(), clients: map[*client]struct{}{}, locks: map[string]lock{}}
	if err := json.Unmarshal(mb, &l.meta); err != nil {
		return nil, err
	}
	if sb, _, err := h.st.Get(ctx, snapKey(id)); err == nil {
		json.Unmarshal(sb, l.doc)
	}
	if l.doc.Items == nil {
		l.doc.Items = map[string]Item{}
	}
	h.docs[id] = l
	return l, nil
}

type msgIn struct {
	T   string  `json:"t"`
	K   string  `json:"k"`
	V   string  `json:"v"`
	Pos float64 `json:"pos"`
	F   string  `json:"f"`
	R   string  `json:"r"`
}

type msgOut struct {
	T     string            `json:"t"`
	K     string            `json:"k,omitempty"`
	Item  *Item             `json:"item,omitempty"`
	Items map[string]Item   `json:"items,omitempty"`
	Type  string            `json:"type,omitempty"`
	Me    string            `json:"me,omitempty"`
	RW    bool              `json:"rw,omitempty"`
	By    string            `json:"by,omitempty"`
	Locks map[string]string `json:"locks,omitempty"`
}

// apply: vom Server vergebener Zeitstempel, LWW, Broadcast, Persistenz per Debounce.
func (l *live) apply(h *Hub, c *client, m msgIn) {
	if !c.write || !ValidKey(l.meta.Type, m.K) || len(m.V) > maxValue || !ValidFmt(m.F) || !ValidRuns(m.R) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep()
	if lk, ok := l.locks[m.K]; ok && lk.user != c.user { // dort schreibt gerade ein anderer: Änderung verwerfen, Stand zurücksenden
		if cur, ok := l.doc.Items[m.K]; ok {
			b, _ := json.Marshal(msgOut{T: "item", K: m.K, Item: &cur})
			select {
			case c.out <- b:
			default:
			}
		}
		return
	}
	if _, ok := l.doc.Items[m.K]; !ok && len(l.doc.Items) >= maxItems {
		return
	}
	ts := time.Now().UnixNano()
	if ts <= l.last {
		ts = l.last + 1
	}
	l.last = ts
	it := Item{V: m.V, Pos: m.Pos, F: m.F, R: m.R, TS: ts, By: c.user, Del: m.T == "del"}
	if it.Del {
		it.V, it.F, it.R = "", "", ""
	}
	if !l.doc.Apply(m.K, it) {
		return
	}
	b, _ := json.Marshal(msgOut{T: "item", K: m.K, Item: &it})
	for cl := range l.clients {
		select {
		case cl.out <- b:
		default: // zu langsam: wird beim Reconnect per init neu synchronisiert
			close(cl.out)
			delete(l.clients, cl)
		}
	}
	if l.timer == nil {
		l.timer = time.AfterFunc(2*time.Second, func() { l.persist() })
	}
}

// broadcast: an alle Clients (mu gehalten).
func (l *live) broadcast(m msgOut) {
	b, _ := json.Marshal(m)
	for cl := range l.clients {
		select {
		case cl.out <- b:
		default:
			close(cl.out)
			delete(l.clients, cl)
		}
	}
}

// sweep: abgelaufene Sperren lösen (mu gehalten).
func (l *live) sweep() {
	now := time.Now()
	for k, lk := range l.locks {
		if now.After(lk.exp) {
			delete(l.locks, k)
			l.broadcast(msgOut{T: "unlock", K: k})
		}
	}
}

// lockKey: Sperre setzen bzw. verlängern; ist sie von einem anderen belegt, erfährt es nur der Anfragende.
func (l *live) lockKey(c *client, k string) {
	if !c.write || !ValidKey(l.meta.Type, k) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep()
	if lk, ok := l.locks[k]; ok && lk.user != c.user {
		b, _ := json.Marshal(msgOut{T: "lock", K: k, By: lk.user})
		select {
		case c.out <- b:
		default:
		}
		return
	}
	l.locks[k] = lock{user: c.user, exp: time.Now().Add(lockTTL), c: c}
	l.broadcast(msgOut{T: "lock", K: k, By: c.user})
}

func (l *live) unlockKey(c *client, k string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lk, ok := l.locks[k]; ok && lk.c == c {
		delete(l.locks, k)
		l.broadcast(msgOut{T: "unlock", K: k})
	}
}

// persist: Snapshot mit ETag schreiben; bei Konflikt (anderer Schreiber) LWW-mergen und wiederholen.
func (l *live) persist() {
	l.mu.Lock()
	l.timer = nil
	local := &Doc{Items: make(map[string]Item, len(l.doc.Items))}
	for k, v := range l.doc.Items {
		local.Items[k] = v
	}
	l.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var merged *Doc
	err := store.Update(ctx, l.st, snapKey(l.id), func(cur []byte) ([]byte, error) {
		merged = NewDoc()
		if cur != nil {
			json.Unmarshal(cur, merged)
			if merged.Items == nil {
				merged.Items = map[string]Item{}
			}
		}
		merged.Merge(local)
		return json.Marshal(merged)
	})
	if err == nil && merged != nil {
		l.mu.Lock()
		l.doc.Merge(merged) // Änderungen anderer Instanzen übernehmen (Broadcast: TODO)
		l.mu.Unlock()
	}
}

func (l *live) join(c *client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clients[c] = struct{}{}
	l.sweep()
	lks := map[string]string{}
	for k, lk := range l.locks {
		lks[k] = lk.user
	}
	b, _ := json.Marshal(msgOut{T: "init", Type: l.meta.Type, Items: l.doc.Items, Me: c.user, RW: c.write, Locks: lks})
	c.out <- b
}

func (l *live) leave(c *client) {
	l.mu.Lock()
	if _, ok := l.clients[c]; ok {
		delete(l.clients, c)
		close(c.out)
	}
	for k, lk := range l.locks { // Sperren dieses Clients lösen
		if lk.c == c {
			delete(l.locks, k)
			l.broadcast(msgOut{T: "unlock", K: k})
		}
	}
	empty := len(l.clients) == 0
	l.mu.Unlock()
	if empty {
		l.persist()
	}
}

// Snapshot: Kopie des aktuellen Standes (für Export). user braucht Leserecht.
func (h *Hub) Snapshot(ctx context.Context, user, id string) (Meta, *Doc, error) {
	l, err := h.open(ctx, id)
	if err != nil {
		return Meta{}, nil, err
	}
	if rd, _ := l.meta.Level(user); !rd {
		return Meta{}, nil, store.ErrNotFound
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cp := &Doc{Items: make(map[string]Item, len(l.doc.Items))}
	for k, v := range l.doc.Items {
		cp.Items[k] = v
	}
	return l.meta, cp, nil
}

// Create legt ein Dokument an (optional mit Startinhalt, z. B. beim Import).
func (h *Hub) Create(ctx context.Context, owner, name, typ string, items map[string]Item) (string, error) {
	id := newID()
	b, _ := json.Marshal(Meta{Name: name, Type: typ, Owner: owner})
	if _, err := h.st.Put(ctx, metaKey(id), b, "*"); err != nil {
		return "", err
	}
	if len(items) > 0 {
		sb, _ := json.Marshal(&Doc{Items: items})
		if _, err := h.st.Put(ctx, snapKey(id), sb, ""); err != nil {
			return "", err
		}
	}
	return id, nil
}

// Cells liefert die Zellwerte (Calc), Paragraphs (Text) steht in lww.go.
func (d *Doc) Cells() map[string]string {
	out := map[string]string{}
	for k, it := range d.Items {
		if !it.Del && it.V != "" {
			out[k] = it.V
		}
	}
	return out
}
