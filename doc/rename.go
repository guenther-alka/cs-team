package doc

import (
	"context"
	"encoding/json"
	"strings"
)

// RenameShares: Freigaben "g:alt" in allen Dokumenten werden zu "g:neu" (auch in geöffneten).
func (h *Hub) RenameShares(ctx context.Context, from, to string) error {
	infos, err := h.st.List(ctx, "doc/")
	if err != nil {
		return err
	}
	sw := func(l []string) ([]string, bool) {
		var out []string
		ch := false
		for _, x := range l {
			if x == "g:"+from {
				x, ch = "g:"+to, true
			}
			out = append(out, x)
		}
		return out, ch
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
		r, c1 := sw(m.Read)
		w, c2 := sw(m.Write)
		if !c1 && !c2 {
			continue
		}
		m.Read, m.Write = r, w
		nb, _ := json.Marshal(m)
		if _, err := h.st.Put(ctx, i.Key, nb, ""); err != nil {
			return err
		}
		id := strings.TrimSuffix(strings.TrimPrefix(i.Key, "doc/"), "/meta.json")
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
