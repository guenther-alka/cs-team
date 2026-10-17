package chat

import (
	"context"

	"cs-team/auth"
	"cs-team/hit"
)

// Search: Chat-Nachrichten in den Kanaelen der Gruppen, in denen der Benutzer lesen darf (neueste zuerst).
func (s *Svc) Search(ctx context.Context, user, q string, limit int) []hit.Hit {
	var out []hit.Hit
	for _, g := range auth.GroupsOf(user) {
		if r, _ := access(user, g); !r {
			continue
		}
		for _, row := range s.chanRows(ctx, g, user) {
			c := s.ch(ctx, g, row.Name)
			c.mu.Lock()
			for i := len(c.msgs) - 1; i >= 0 && len(out) < limit; i-- {
				m := c.msgs[i]
				if m.Del {
					continue
				}
				if sn, ok := hit.Snip(m.T, q); ok {
					out = append(out, hit.Hit{Kind: "chat", ID: g + "/" + row.Name, Title: g + " #" + row.Name, Snippet: m.By + ": " + sn})
				}
			}
			c.mu.Unlock()
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}
