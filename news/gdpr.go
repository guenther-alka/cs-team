package news

import (
	"archive/zip"
	"context"
	"encoding/json"
	"sort"
	"time"
)

type exportItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Group   string `json:"group,omitempty"` // leer = an alle
	Created string `json:"created"`
	Until   string `json:"until,omitempty"`
}

// ExportUser schreibt notices.json: die Aushaenge, die der Benutzer verfasst hat (Datenauskunft). Ohne Aushaenge keine Datei.
func (s *Svc) ExportUser(ctx context.Context, user string, zw *zip.Writer) error {
	s.mu.Lock()
	var its []Item
	for _, it := range s.items {
		if it.By == user {
			its = append(its, *it)
		}
	}
	s.mu.Unlock()
	if len(its) == 0 {
		return nil
	}
	sort.Slice(its, func(i, j int) bool { return its[i].Created < its[j].Created })
	out := make([]exportItem, len(its))
	for i, it := range its {
		out[i] = exportItem{ID: it.ID, Title: it.Title, Text: it.Text, Group: it.Group, Created: time.Unix(it.Created, 0).UTC().Format(time.RFC3339), Until: it.Until}
	}
	w, err := zw.Create("notices.json")
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(map[string]any{"count": len(out), "notices": out}, "", " ")
	_, err = w.Write(b)
	return err
}
