package files

import (
	"context"

	"cs-team/hit"
)

// Search: Dateien (Name und Pfad) der eigenen Ablage und der mit dem Benutzer geteilten; Papierkorb-Eintraege zaehlen nicht.
func (s *Svc) Search(ctx context.Context, user, q string, limit int) []hit.Hit {
	own, shared, err := s.List(ctx, user)
	if err != nil {
		return nil
	}
	var out []hit.Hit
	for _, m := range append(append([]Meta{}, own...), shared...) {
		if m.Trash != nil {
			continue
		}
		if _, ok := hit.Snip(m.Name, q); ok {
			where := Dir(m.Name)
			if m.Owner != user {
				where = m.Owner + " " + where
			}
			out = append(out, hit.Hit{Kind: "file", ID: m.Owner + "/" + m.Name, Title: Base(m.Name), Where: where})
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}
