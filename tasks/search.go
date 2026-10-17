package tasks

import (
	"sort"
	"strings"

	"cs-team/hit"
)

// Search: Aufgaben, die der Benutzer sehen darf (Titel, Beschreibung, Label, Kommentare im Verlauf).
func (s *Svc) Search(user, q string, limit int) []hit.Hit {
	s.mu.Lock()
	defer s.mu.Unlock()
	type cand struct {
		h hit.Hit
		u int64
	}
	var l []cand
	for _, t := range s.tasks {
		if !s.canSee(user, t) {
			continue
		}
		sn, ok := "", false
		if _, ok = hit.Snip(t.Title, q); !ok {
			if sn, ok = hit.Snip(t.Desc, q); !ok {
				if sn, ok = hit.Snip(strings.Join(t.Labels, " "), q); !ok {
					for _, e := range t.Log {
						if !e.Sys && !e.Del {
							if sn, ok = hit.Snip(e.T, q); ok {
								break
							}
						}
					}
				}
			}
		}
		if ok {
			l = append(l, cand{hit.Hit{Kind: "task", ID: t.ID, Title: t.Title, Snippet: sn, Where: t.Group}, t.Updated})
		}
	}
	sort.Slice(l, func(i, j int) bool { return l[i].u > l[j].u })
	var out []hit.Hit
	for i := 0; i < len(l) && i < limit; i++ {
		out = append(out, l[i].h)
	}
	return out
}
