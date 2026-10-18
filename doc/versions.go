package doc

// Versionen aus ZFS-Snapshots (0.63.0): liegt der Speicher auf einem ZFS-Dataset, enthalten dessen Snapshots fruehere Staende
// von doc/<id>/snapshot.json (siehe store.SnapVersions). Ersteller und Admins sehen die Liste und koennen einen Stand als NEUES,
// schreibgeschuetztes Dokument "<Name>_<Datum>" importieren. Das Original bleibt unveraendert; ob der Import wieder von mehreren
// bearbeitet werden darf, entscheidet der Ersteller (oder ein Admin) mit dem Schreibschutz (setReadonly). Wie viele Staende es
// gibt, bestimmt der Speicher-Admin mit seinen Autosnap-Jobs (keep/hold, delzero).

import (
	"encoding/json"
	"errors"
	"net/http"

	"cs-team/auth"
	"cs-team/store"
)

// versions: GET /api/docs/{id}/versions (Ersteller oder Admin).
func (h *Hub) versions(w http.ResponseWriter, r *http.Request) {
	l := h.owned(w, r)
	if l == nil {
		return
	}
	ok, vs := store.SnapVersions(h.st, snapKey(l.id))
	json.NewEncoder(w).Encode(map[string]any{"supported": ok, "versions": vs})
}

// importVersion: POST /api/docs/{id}/import {snap}: Stand als neues schreibgeschuetztes Dokument des Aufrufers anlegen.
func (h *Hub) importVersion(w http.ResponseWriter, r *http.Request) {
	l := h.owned(w, r)
	if l == nil {
		return
	}
	ctx, me := r.Context(), auth.User(r.Context())
	var in struct{ Snap string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || in.Snap == "" {
		http.Error(w, "snap required", http.StatusBadRequest)
		return
	}
	l.mu.Lock()
	meta := l.meta
	l.mu.Unlock()
	if !auth.CanWrite(ctx, Area(meta.Type)) {
		http.Error(w, "no write permission for "+Area(meta.Type), http.StatusForbidden)
		return
	}
	b, mod, err := store.ReadSnap(h.st, snapKey(l.id), in.Snap)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "version not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var d Doc
	if json.Unmarshal(b, &d) != nil {
		http.Error(w, "version unreadable", http.StatusUnprocessableEntity)
		return
	}
	items := make(map[string]Item, len(d.Items))
	for k, it := range d.Items {
		if len(items) >= maxItems || !ValidKey(meta.Type, k) || len(it.V) > maxValue || !ValidFmt(it.F) || !ValidRuns(it.R) {
			continue
		}
		items[k] = it
	}
	name := importName(meta.Name, mod)
	id, err := h.CreateMeta(ctx, Meta{Name: name, Type: meta.Type, Owner: me, RO: true}, items)
	if err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	h.Log(ctx, id, Change{By: me, Op: "create", Src: "snapshot", N: len(items), Note: in.Snap + " <- " + meta.Name})
	json.NewEncoder(w).Encode(map[string]any{"id": id, "type": meta.Type, "name": name, "ro": true})
}
