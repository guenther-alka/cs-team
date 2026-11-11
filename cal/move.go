package cal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"cs-team/store"
)

// apiMoveEvent: POST /api/cal/{kal}/events/{file}/move {"to": "<cid>"} - Termin (ganzes Objekt, bei Serien die ganze
// Serie samt Einzeländerungen) in einen anderen Kalender verschieben. Web-UI: Termin greifen und in der linken
// Kalenderliste auf einen anderen Kalender fallen lassen. Schreibrecht auf Quelle und Ziel nötig, Abos sind kein Ziel;
// ist das Ziel eine Ressource, gilt deren Doppelbuchungs-Prüfung. Teilnehmer bekommen keine Mail (Termin bleibt gleich).
func (b *Backend) apiMoveEvent(w http.ResponseWriter, r *http.Request) {
	file, ok := fileArg(w, r)
	if !ok {
		return
	}
	var in struct{ To string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || in.To == "" {
		http.Error(w, "to required", http.StatusBadRequest)
		return
	}
	src, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	dst, err := b.open(r.Context(), in.To)
	if err != nil {
		http.Error(w, "target calendar not found", http.StatusNotFound)
		return
	}
	if !src.write || !dst.write || dst.m.URL != "" || src.m.URL != "" {
		http.Error(w, "read-only calendar", http.StatusForbidden)
		return
	}
	if src.owner == dst.owner && src.kal == dst.kal {
		http.Error(w, "same calendar", http.StatusBadRequest)
		return
	}
	// beide Kalender sperren, immer in derselben Reihenfolge (keine Verklemmung bei gegenläufigem Verschieben)
	a, z := src, dst
	if a.owner+"/"+a.kal > z.owner+"/"+z.kal {
		a, z = z, a
	}
	defer lockCal(a.owner, a.kal)()
	defer lockCal(z.owner, z.kal)()

	data, _, err := b.St.Get(r.Context(), key(src.owner, src.kal, file))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "no such event", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cal, err := decode(data)
	if err != nil || masterOf(cal) == nil {
		http.Error(w, "event cannot be moved here", http.StatusBadRequest)
		return
	}
	if _, _, err := b.St.Get(r.Context(), key(dst.owner, dst.kal, file)); err == nil {
		http.Error(w, "event exists in target calendar", http.StatusConflict)
		return
	}
	if msg := b.conflict(r.Context(), dst, cal, ""); msg != "" {
		http.Error(w, msg, http.StatusConflict)
		return
	}
	if _, err := b.St.Put(r.Context(), key(dst.owner, dst.kal, file), data, "*"); errors.Is(err, store.ErrConflict) {
		http.Error(w, "event exists in target calendar", http.StatusConflict)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := b.St.Delete(r.Context(), key(src.owner, src.kal, file)); err != nil {
		// Ziel ist geschrieben; ohne Löschen stünde der Termin doppelt - Ziel wieder entfernen
		b.St.Delete(r.Context(), key(dst.owner, dst.kal, file))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"to": dst.cid, "file": file})
}

// moveAll: ganzen Kalender (fo/fk) mit allen Objekten nach (to/tk) umziehen, _meta.json wird durch metaRaw ersetzt.
// Ziel wird nur angelegt (nie überschrieben); bei einem Fehler wird das schon Kopierte wieder entfernt, die Quelle bleibt.
func (b *Backend) moveAll(ctx context.Context, fo, fk, to, tk string, metaRaw []byte) error {
	a, z := fo+"/"+fk, to+"/"+tk // feste Sperr-Reihenfolge
	if a > z {
		defer lockCal(fo, fk)()
		defer lockCal(to, tk)()
	} else {
		defer lockCal(to, tk)()
		defer lockCal(fo, fk)()
	}
	src, dst := key(fo, fk, ""), key(to, tk, "")
	infos, err := b.St.List(ctx, src)
	if err != nil {
		return err
	}
	var done []string
	undo := func() {
		for _, k := range done {
			b.St.Delete(ctx, k)
		}
	}
	for _, i := range infos {
		rest := strings.TrimPrefix(i.Key, src)
		data := metaRaw
		if rest != "_meta.json" {
			if data, _, err = b.St.Get(ctx, i.Key); err != nil {
				undo()
				return err
			}
		}
		if _, err := b.St.Put(ctx, dst+rest, data, "*"); err != nil {
			undo()
			return err
		}
		done = append(done, dst+rest)
	}
	for _, i := range infos {
		b.St.Delete(ctx, i.Key)
	}
	return nil
}
