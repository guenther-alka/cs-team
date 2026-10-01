package cal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"cs-team/store"
)

// RenameGroup: Gruppenkalender "@alt" -> "@neu" (alle Objekte); der Anzeigename des Kalenders folgt der Gruppe.
func (b *Backend) RenameGroup(ctx context.Context, from, to string) error {
	op, np := "cal/@"+from+"/", "cal/@"+to+"/"
	infos, err := b.St.List(ctx, op)
	if err != nil {
		return err
	}
	if ex, _ := b.St.List(ctx, np); len(ex) > 0 {
		return errors.New("target calendar exists")
	}
	var done []string
	undo := func() {
		for _, k := range done {
			b.St.Delete(ctx, k)
		}
	}
	for _, i := range infos {
		data, _, err := b.St.Get(ctx, i.Key)
		if err != nil {
			undo()
			return err
		}
		rest := strings.TrimPrefix(i.Key, op)
		if strings.HasSuffix(rest, "/_meta.json") {
			var m meta
			if json.Unmarshal(data, &m) == nil && m.Name == from {
				m.Name = to
				data, _ = json.Marshal(m)
			}
		}
		if _, err := b.St.Put(ctx, np+rest, data, ""); err != nil {
			undo()
			return err
		}
		done = append(done, np+rest)
	}
	for _, i := range infos {
		b.St.Delete(ctx, i.Key)
	}
	return nil
}

// GroupCalMode: Modus des Gruppenkalenders ("ro"/"rw") oder "" wenn es keinen gibt.
func (b *Backend) GroupCalMode(ctx context.Context, g string) string {
	raw, _, err := b.St.Get(ctx, key("@"+g, "gruppe", "_meta.json"))
	if err != nil {
		return ""
	}
	var m meta
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	if m.Mode != "ro" {
		return "rw"
	}
	return m.Mode
}

// CalUsed: enthält der Gruppenkalender Termine (mehr als die Metadaten)?
func (b *Backend) CalUsed(ctx context.Context, g string) bool {
	infos, _ := b.St.List(ctx, "cal/@"+g+"/")
	for _, i := range infos {
		if !strings.HasSuffix(i.Key, "/_meta.json") {
			return true
		}
	}
	return false
}

// DropGroup: löscht den (leeren) Gruppenkalender.
func (b *Backend) DropGroup(ctx context.Context, g string) {
	infos, _ := b.St.List(ctx, "cal/@"+g+"/")
	for _, i := range infos {
		b.St.Delete(ctx, i.Key)
	}
}

var _ = store.ErrNotFound
