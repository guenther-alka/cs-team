package files

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"sort"

	"cs-team/auth"
)

// UsageRow: Belegung eines Besitzers (Benutzer oder Gruppenordner "@gruppe").
type UsageRow struct {
	Owner string `json:"owner"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"` // belegt, einschließlich Papierkorb
	Trash int64  `json:"trash"` // davon im Papierkorb
	Items int    `json:"trashItems"`
}

// UsageAll: Belegung aller Besitzer, größte zuerst.
func (s *Svc) UsageAll() ([]UsageRow, error) {
	all, err := s.all(context.Background())
	if err != nil {
		return nil, err
	}
	m := map[string]*UsageRow{}
	for _, f := range all {
		r := m[f.Owner]
		if r == nil {
			r = &UsageRow{Owner: f.Owner}
			m[f.Owner] = r
		}
		r.Bytes += f.Size
		if isTrash(&f) {
			r.Trash += f.Size
			r.Items++
		} else if path.Base(f.Name) != ".folder" {
			r.Files++
		}
	}
	out := make([]UsageRow, 0, len(m))
	for _, r := range m {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Owner < out[j].Owner
	})
	return out, nil
}

// usageList: GET /api/filesusage (nur globale Admins): Belegung je Benutzer und Gruppenordner.
func (s *Svc) usageList(w http.ResponseWriter, r *http.Request) {
	if !auth.IsAdmin(r.Context()) {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	rows, err := s.UsageAll()
	if err != nil {
		fail(w, err)
		return
	}
	var q int64
	if s.Quota != nil {
		q = s.Quota()
	}
	json.NewEncoder(w).Encode(map[string]any{"quota": q, "rows": rows})
}
