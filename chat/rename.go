package chat

import (
	"context"
	"errors"
	"strings"
)

// RenameGroup: Chat-Verlauf, Kanalliste und Anhänge der Gruppe wandern unter den neuen Namen.
func (s *Svc) RenameGroup(ctx context.Context, from, to string) error {
	if from == "f" || to == "f" { // "chat/f/" ist der Anhangsbereich
		return errors.New("group name f is reserved")
	}
	defer s.dropGroup(from, to)
	for _, pre := range []string{"chat/", "chat/f/"} {
		op, np := pre+from+"/", pre+to+"/"
		infos, err := s.St.List(ctx, op)
		if err != nil {
			return err
		}
		if ex, _ := s.St.List(ctx, np); len(ex) > 0 {
			return errors.New("target chat exists")
		}
		var done []string
		for _, i := range infos {
			if pre == "chat/" && strings.HasPrefix(i.Key, "chat/f/") { // Anhänge werden im zweiten Durchgang behandelt
				continue
			}
			rc, err := s.St.GetStream(ctx, i.Key)
			if err != nil {
				for _, k := range done {
					s.St.Delete(ctx, k)
				}
				return err
			}
			nk := np + strings.TrimPrefix(i.Key, op)
			err = s.St.PutStream(ctx, nk, rc, i.Size, "application/octet-stream")
			rc.Close()
			if err != nil {
				for _, k := range done {
					s.St.Delete(ctx, k)
				}
				return err
			}
			done = append(done, nk)
		}
		for _, i := range infos {
			if pre == "chat/" && strings.HasPrefix(i.Key, "chat/f/") {
				continue
			}
			s.St.Delete(ctx, i.Key)
		}
	}
	return nil
}

// ChatUsed: gibt es Chat-Daten der Gruppe?
func (s *Svc) ChatUsed(ctx context.Context, g string) bool {
	a, _ := s.St.List(ctx, "chat/"+g+"/")
	n := 0
	for _, i := range a {
		if !strings.HasPrefix(i.Key, "chat/f/") {
			n++
		}
	}
	return n > 0
}

// dropGroup: Zwischenspeicher der beiden Gruppen verwerfen (Verlauf wird aus dem Speicher neu geladen).
func (s *Svc) dropGroup(gs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range gs {
		for k := range s.chans {
			if strings.HasPrefix(k, g+"/") {
				delete(s.chans, k)
			}
		}
		delete(s.lists, g)
	}
}
