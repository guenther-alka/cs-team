package files

import (
	"context"
	"encoding/json"
	"strings"
)

// RenameGroup: der Gruppenordner "@alt" wird zu "@neu" (Daten, Metadaten, öffentliche Links). Das Ziel muss leer sein.
// Erst kopieren, dann die alten Schlüssel löschen: bei einem Fehler bleibt der alte Stand vollständig erhalten.
func (s *Svc) RenameGroup(ctx context.Context, from, to string) error {
	defer s.invalidate()
	oo, no := "@"+from, "@"+to
	infos, err := s.St.List(ctx, "filesmeta/"+oo+"/")
	if err != nil {
		return err
	}
	if ex, _ := s.St.List(ctx, "filesmeta/"+no+"/"); len(ex) > 0 {
		return ErrExists
	}
	var done []Meta
	undo := func() {
		for _, m := range done {
			s.St.Delete(ctx, dataKey(no, m.Name))
			s.St.Delete(ctx, metaKey(no, m.Name))
		}
	}
	var old []Meta
	for _, i := range infos {
		b, _, err := s.St.Get(ctx, i.Key)
		if err != nil {
			undo()
			return err
		}
		var m Meta
		if json.Unmarshal(b, &m) != nil || m.Name == "" {
			continue
		}
		old = append(old, m)
		n := m
		n.Owner = no
		if rc, err := s.St.GetStream(ctx, dataKey(oo, m.Name)); err == nil {
			err = s.St.PutStream(ctx, dataKey(no, m.Name), rc, m.Size, m.Type)
			rc.Close()
			if err != nil {
				undo()
				return err
			}
		} else if m.Size > 0 { // Metadaten ohne Daten, aber mit Größe: nicht stillschweigend übergehen
			undo()
			return err
		}
		nb, _ := json.Marshal(n)
		if _, err := s.St.Put(ctx, metaKey(no, m.Name), nb, ""); err != nil {
			undo()
			return err
		}
		done = append(done, n)
	}
	for _, m := range done { // öffentliche Links zeigen auf den neuen Besitzer
		if m.Token != "" {
			s.St.Put(ctx, tokKey(m.Token), []byte(no+"/"+m.Name), "")
		}
	}
	for _, m := range old {
		s.St.Delete(ctx, dataKey(oo, m.Name))
		s.St.Delete(ctx, metaKey(oo, m.Name))
	}
	return nil
}

// FolderUsed: enthält der Gruppenordner Dateien?
func (s *Svc) FolderUsed(ctx context.Context, g string) bool {
	l, _ := s.St.List(ctx, "filesmeta/@"+g+"/")
	return len(l) > 0
}

// RenameShares: Freigaben "g:alt" in allen Dateien (Gruppenordner und persönliche) werden zu "g:neu".
func (s *Svc) RenameShares(ctx context.Context, from, to string) error {
	defer s.invalidate()
	infos, err := s.St.List(ctx, "filesmeta/")
	if err != nil {
		return err
	}
	for _, i := range infos {
		if !strings.HasSuffix(i.Key, ".json") {
			continue
		}
		b, _, err := s.St.Get(ctx, i.Key)
		if err != nil {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		r, ch1 := swapShare(m.Read, from, to)
		w, ch2 := swapShare(m.Write, from, to)
		if ch1 || ch2 {
			m.Read, m.Write = r, w
			nb, _ := json.Marshal(m)
			if _, err := s.St.Put(ctx, i.Key, nb, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func swapShare(l []string, from, to string) ([]string, bool) {
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
