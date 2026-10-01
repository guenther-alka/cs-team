package doc

import (
	"context"
	"encoding/json"
	"strings"
)

func (h *Hub) metas(ctx context.Context) (ids []string, ms []Meta) {
	infos, err := h.st.List(ctx, "doc/")
	if err != nil {
		return nil, nil
	}
	for _, i := range infos {
		if !strings.HasSuffix(i.Key, "/meta.json") {
			continue
		}
		b, _, err := h.st.Get(ctx, i.Key)
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(i.Key, "doc/"), "/meta.json"))
		ms = append(ms, m)
	}
	return
}

// UserCount: Anzahl der Dokumente, die user gehören.
func (h *Hub) UserCount(ctx context.Context, user string) int {
	_, ms := h.metas(ctx)
	n := 0
	for _, m := range ms {
		if m.Owner == user {
			n++
		}
	}
	return n
}

func dropName(l []string, user string) ([]string, bool) {
	var out []string
	ch := false
	for _, x := range l {
		if x == user {
			ch = true
			continue
		}
		out = append(out, x)
	}
	return out, ch
}

// PurgeUser löscht die Dokumente des Benutzers und entfernt seinen Namen aus den Freigaben fremder Dokumente.
func (h *Hub) PurgeUser(ctx context.Context, user string) error {
	ids, ms := h.metas(ctx)
	for n, m := range ms {
		id := ids[n]
		if m.Owner == user {
			h.mu.Lock()
			delete(h.docs, id)
			h.mu.Unlock()
			infos, _ := h.st.List(ctx, "doc/"+id+"/")
			for _, i := range infos {
				h.st.Delete(ctx, i.Key)
			}
			continue
		}
		r, c1 := dropName(m.Read, user)
		w, c2 := dropName(m.Write, user)
		if !c1 && !c2 {
			continue
		}
		m.Read, m.Write = r, w
		nb, _ := json.Marshal(m)
		if _, err := h.st.Put(ctx, metaKey(id), nb, ""); err != nil {
			return err
		}
		h.mu.Lock()
		l := h.docs[id]
		h.mu.Unlock()
		if l != nil {
			l.mu.Lock()
			l.meta.Read, l.meta.Write = r, w
			l.mu.Unlock()
		}
	}
	return nil
}
