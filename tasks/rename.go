package tasks

import "context"

// RenameGroup: Aufgaben der Gruppe "alt" gehören danach zur Gruppe "neu".
func (s *Svc) RenameGroup(ctx context.Context, from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.Group == from {
			t.Group = to
			s.save(t)
		}
	}
	return nil
}

// TasksUsed: gibt es Aufgaben der Gruppe?
func (s *Svc) TasksUsed(ctx context.Context, g string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.Group == g {
			return true
		}
	}
	return false
}
