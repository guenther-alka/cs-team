package cal

import (
	"context"
	"encoding/json"
	"strings"
)

// RenameUser: die persoenlichen Kalender von from gehoeren danach to; Ersteller-Eintraege (meta.By) der Gruppenkalender
// folgen dem Namen (Konto umbenennen, 0.72). Teilnehmer und Organisator in Terminen ersetzt danach Anon (AnonHooks).
// Erst kopieren, dann loeschen; wiederholbar.
func (b *Backend) RenameUser(ctx context.Context, from, to string) error {
	if from == "" || to == "" || strings.ContainsAny(from+to, "/~") || strings.HasPrefix(from, "@") || strings.HasPrefix(from, "_") {
		return nil
	}
	op, np := "cal/"+from+"/", "cal/"+to+"/"
	infos, err := b.St.List(ctx, op)
	if err != nil {
		return err
	}
	for _, i := range infos {
		data, _, err := b.St.Get(ctx, i.Key)
		if err != nil {
			return err
		}
		if _, err := b.St.Put(ctx, np+strings.TrimPrefix(i.Key, op), data, ""); err != nil {
			return err
		}
	}
	for _, i := range infos {
		b.St.Delete(ctx, i.Key)
	}
	all, err := b.St.List(ctx, "cal/")
	if err != nil {
		return err
	}
	for _, i := range all {
		if !strings.HasSuffix(i.Key, "/_meta.json") {
			continue
		}
		data, etag, err := b.St.Get(ctx, i.Key)
		if err != nil {
			continue
		}
		var m meta
		if json.Unmarshal(data, &m) != nil || m.By != from {
			continue
		}
		m.By = to
		nb, _ := json.Marshal(m)
		if _, err := b.St.Put(ctx, i.Key, nb, etag); err != nil {
			return err
		}
	}
	return nil
}
