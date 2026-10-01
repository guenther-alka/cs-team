package files

import "context"

// UserCount: Anzahl der Dateien/Ordner im persönlichen Bereich von user.
func (s *Svc) UserCount(ctx context.Context, user string) int {
	s.invalidate()
	all, _ := s.all(ctx)
	n := 0
	for _, m := range all {
		if m.Owner == user {
			n++
		}
	}
	return n
}

func without(l []string, user string) ([]string, bool) {
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

// PurgeUser löscht alle Dateien des Benutzers samt Links und entfernt seinen Namen aus Freigaben fremder Dateien.
func (s *Svc) PurgeUser(ctx context.Context, user string) error {
	s.invalidate()
	all, err := s.all(ctx)
	if err != nil {
		return err
	}
	for _, m := range all {
		if m.Owner == user {
			if m.Token != "" {
				s.St.Delete(ctx, tokKey(m.Token))
			}
			s.St.Delete(ctx, metaKey(m.Owner, m.Name))
			s.St.Delete(ctx, dataKey(m.Owner, m.Name))
			continue
		}
		r, c1 := without(m.Read, user)
		w, c2 := without(m.Write, user)
		if c1 || c2 {
			mm := m
			mm.Read, mm.Write = r, w
			if err := s.saveMeta(ctx, &mm); err != nil {
				return err
			}
		}
	}
	s.invalidate()
	return nil
}
