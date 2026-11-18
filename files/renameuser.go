package files

import (
	"context"
	"encoding/json"
	"strings"
)

// RenameUser: der persoenliche Bereich von from gehoert danach to (Daten, Versionen, Papierkorb, Metadaten, oeffentliche
// Links), und Freigaben an from in fremden Dateien und Gruppenordnern lauten auf to (Konto umbenennen, 0.72).
// Erst kopieren, dann loeschen; ein zweiter Aufruf findet nichts mehr und aendert nichts.
func (s *Svc) RenameUser(ctx context.Context, from, to string) error {
	defer s.invalidate()
	if from == "" || to == "" || strings.HasPrefix(from, "@") || strings.HasPrefix(to, "@") || strings.Contains(from+to, "/") {
		return ErrExists
	}
	dataOld, dataNew := "files/"+from+"/", "files/"+to+"/"
	metaOld, metaNew := "filesmeta/"+from+"/", "filesmeta/"+to+"/"
	data, err := s.St.List(ctx, dataOld)
	if err != nil {
		return err
	}
	metas, err := s.St.List(ctx, metaOld)
	if err != nil {
		return err
	}
	for _, i := range data {
		rc, err := s.St.GetStream(ctx, i.Key)
		if err != nil {
			return err
		}
		err = s.St.PutStream(ctx, dataNew+strings.TrimPrefix(i.Key, dataOld), rc, i.Size, "application/octet-stream")
		rc.Close()
		if err != nil {
			return err
		}
	}
	var toks []Meta
	for _, i := range metas {
		b, _, err := s.St.Get(ctx, i.Key)
		if err != nil {
			return err
		}
		var m Meta
		if json.Unmarshal(b, &m) == nil {
			if m.Owner == from {
				m.Owner = to
			}
			m.Read, _ = swapUser(m.Read, from, to)
			m.Write, _ = swapUser(m.Write, from, to)
			if m.Token != "" {
				toks = append(toks, m)
			}
			b, _ = json.Marshal(m)
		}
		if _, err := s.St.Put(ctx, metaNew+strings.TrimPrefix(i.Key, metaOld), b, ""); err != nil {
			return err
		}
	}
	for _, m := range toks { // oeffentliche Links zeigen auf den neuen Besitzer
		s.St.Put(ctx, tokKey(m.Token), []byte(to+"/"+m.Name), "")
	}
	for _, i := range metas {
		s.St.Delete(ctx, i.Key)
	}
	for _, i := range data {
		s.St.Delete(ctx, i.Key)
	}
	// Freigaben an from in allen anderen Dateien (persoenliche Bereiche anderer und Gruppenordner)
	all, err := s.St.List(ctx, "filesmeta/")
	if err != nil {
		return err
	}
	for _, i := range all {
		if !strings.HasSuffix(i.Key, ".json") || strings.HasPrefix(i.Key, metaNew) {
			continue
		}
		b, _, err := s.St.Get(ctx, i.Key)
		if err != nil || !strings.Contains(string(b), `"`+from+`"`) {
			continue
		}
		var m Meta
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		r, c1 := swapUser(m.Read, from, to)
		w, c2 := swapUser(m.Write, from, to)
		if !c1 && !c2 {
			continue
		}
		m.Read, m.Write = r, w
		nb, _ := json.Marshal(m)
		if _, err := s.St.Put(ctx, i.Key, nb, ""); err != nil {
			return err
		}
	}
	return nil
}

// swapUser: Freigabe an den Benutzer from (Eintrag ohne "g:") lautet auf to; doppelte Eintraege entfallen.
func swapUser(l []string, from, to string) ([]string, bool) {
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
