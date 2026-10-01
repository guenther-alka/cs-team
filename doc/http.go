package doc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/coder/websocket"

	"cs-team/auth"
	"cs-team/store"
)

// Routes hängt die Dokument-API an den Mux (alles hinter Basic Auth).
func (h *Hub) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	f := func(fn http.HandlerFunc) http.Handler { return wrap(fn) }
	mux.Handle("GET /api/docs", f(h.list))
	mux.Handle("POST /api/docs", f(h.create))
	mux.Handle("POST /api/docs/{id}/share", f(h.share))
	mux.Handle("DELETE /api/docs/{id}", f(h.remove))
	mux.Handle("GET /ws/{id}", f(h.ws))
}

// Area: Berechtigungsbereich eines Dokumenttyps.
func Area(typ string) string {
	if typ == "sheet" {
		return "calc"
	}
	return "text"
}

func newID() string { b := make([]byte, 8); rand.Read(b); return hex.EncodeToString(b) }

func (h *Hub) list(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	infos, err := h.st.List(r.Context(), "doc/")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type row struct {
		ID string `json:"id"`
		Meta
		RW bool `json:"rw"`
	}
	out := []row{}
	for _, i := range infos {
		if !strings.HasSuffix(i.Key, "/meta.json") {
			continue
		}
		b, _, err := h.st.Get(r.Context(), i.Key)
		var m Meta
		if err != nil || json.Unmarshal(b, &m) != nil {
			continue
		}
		if rd, wr := m.Level(me); rd && auth.Can(r.Context(), Area(m.Type)) {
			out = append(out, row{strings.Split(i.Key, "/")[1], m, wr})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	json.NewEncoder(w).Encode(out)
}

func (h *Hub) create(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Type string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil ||
		(in.Type != "sheet" && in.Type != "text") || in.Name == "" || len(in.Name) > 100 {
		http.Error(w, "name/type (sheet|text) required", 400)
		return
	}
	if !auth.CanWrite(r.Context(), Area(in.Type)) {
		http.Error(w, "no write permission for "+Area(in.Type), http.StatusForbidden)
		return
	}
	id := newID()
	m := Meta{Name: in.Name, Type: in.Type, Owner: auth.User(r.Context())}
	b, _ := json.Marshal(m)
	if _, err := h.st.Put(r.Context(), metaKey(id), b, "*"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func (h *Hub) owned(w http.ResponseWriter, r *http.Request) *live {
	l, err := h.open(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && ((l.meta.Owner != auth.User(r.Context()) && !auth.IsAdmin(r.Context())) || !auth.Can(r.Context(), Area(l.meta.Type)))) {
		http.Error(w, "not found", 404)
		return nil
	} else if err != nil {
		http.Error(w, err.Error(), 500)
		return nil
	}
	return l
}

func (h *Hub) share(w http.ResponseWriter, r *http.Request) {
	l := h.owned(w, r)
	if l == nil {
		return
	}
	var in struct{ Read, Write []string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	l.mu.Lock()
	l.meta.Read, l.meta.Write = in.Read, in.Write
	b, _ := json.Marshal(l.meta)
	l.mu.Unlock()
	if _, err := h.st.Put(r.Context(), metaKey(l.id), b, ""); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	l.recheck() // geänderte Freigabe gilt sofort auch für offene Verbindungen (F4)
}

func (h *Hub) remove(w http.ResponseWriter, r *http.Request) {
	l := h.owned(w, r)
	if l == nil {
		return
	}
	h.mu.Lock()
	delete(h.docs, l.id)
	h.mu.Unlock()
	l.kill()
	infos, _ := h.st.List(r.Context(), "doc/"+l.id+"/")
	for _, i := range infos {
		h.st.Delete(r.Context(), i.Key)
	}
}

func (h *Hub) ws(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	l, err := h.open(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	rd, wr := l.meta.Level(me)
	if !rd || !auth.Can(r.Context(), Area(l.meta.Type)) {
		http.Error(w, "not found", 404)
		return
	}
	conn, err := websocket.Accept(w, r, nil) // Origin-Check: nur gleicher Host
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxValue + 1024)
	c := &client{user: me, write: wr, out: make(chan []byte, 256)}
	l.join(c)
	defer l.leave(c)
	ctx := r.Context()
	go func() { // Writer
		for b := range c.out {
			if conn.Write(ctx, websocket.MessageText, b) != nil {
				return
			}
		}
		conn.Close(websocket.StatusPolicyViolation, "resync")
	}()
	for { // Reader
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var m msgIn
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.T {
		case "set", "del":
			l.apply(h, c, m)
		case "lock":
			l.lockKey(c, m.K)
		case "unlock":
			l.unlockKey(c, m.K)
		}
	}
}
