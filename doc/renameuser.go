package doc

import (
	"context"
	"encoding/json"
)

// RenameUser: Dokumente von from gehoeren danach to, Freigaben an from lauten auf to (Konto umbenennen, 0.72).
// Wiederholbar: ein zweiter Aufruf findet den alten Namen nicht mehr.
func (h *Hub) RenameUser(ctx context.Context, from, to string) error {
	ids, ms := h.metas(ctx)
	for n, m := range ms {
		id := ids[n]
		ch := false
		if m.Owner == from {
			m.Owner, ch = to, true
		}
		r, c1 := swapName(m.Read, from, to)
		w, c2 := swapName(m.Write, from, to)
		if !ch && !c1 && !c2 {
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
			l.meta.Owner, l.meta.Read, l.meta.Write = m.Owner, r, w
			l.mu.Unlock()
		}
	}
	return nil
}

func swapName(l []string, from, to string) ([]string, bool) {
	var out []string
	ch := false
	seen := map[string]bool{}
	for _, x := range l {
		if x == from {
			x, ch = to, true
		}
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out, ch
}
