package doc

import (
	"context"
	"encoding/json"

	"cs-team/auth"
	"cs-team/hit"
)

const (
	searchDocs  = 300     // so viele lesbare Dokumente werden hoechstens im Inhalt durchsucht (Namen immer alle)
	searchBytes = 2 << 20 // groessere Dokumente werden nur nach dem Namen durchsucht
)

// Search: Calc- und Text-Dokumente, die der Benutzer lesen darf (Name, sonst Inhalt des gespeicherten Standes).
func (h *Hub) Search(ctx context.Context, user, q string, limit int) []hit.Hit {
	ids, ms := h.metas(ctx)
	var out []hit.Hit
	scanned := 0
	for i, m := range ms {
		if len(out) >= limit {
			break
		}
		if rd, _ := m.Level(user); !rd || !auth.Can(ctx, Area(m.Type)) {
			continue
		}
		if _, ok := hit.Snip(m.Name, q); ok {
			out = append(out, hit.Hit{Kind: m.Type, ID: ids[i], Title: m.Name, Where: m.Owner})
			continue
		}
		if scanned++; scanned > searchDocs {
			continue
		}
		b, _, err := h.st.Get(ctx, snapKey(ids[i]))
		if err != nil || len(b) > searchBytes {
			continue
		}
		var d Doc
		if json.Unmarshal(b, &d) != nil {
			continue
		}
		for _, it := range d.Items {
			if it.Del {
				continue
			}
			if sn, ok := hit.Snip(it.V, q); ok {
				out = append(out, hit.Hit{Kind: m.Type, ID: ids[i], Title: m.Name, Snippet: sn, Where: m.Owner})
				break
			}
		}
	}
	return out
}
