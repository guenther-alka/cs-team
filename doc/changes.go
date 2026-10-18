package doc

// Aenderungsliste (0.63.0): je Dokument eine kleine Liste "wer hat wann was geaendert", fuer Admins. Gespeichert wird nie der
// Inhalt, nur Zeit, Benutzer, Art, Quelle (KI oder Mensch) und die Zahl/Namen der geaenderten Zellen bzw. Absaetze.
// Aufeinanderfolgende Bearbeitungen desselben Benutzers (gleiche Quelle) innerhalb von 10 Minuten bilden einen Eintrag.
// Die Liste liegt als doc/<id>/changes.json im Speicher, hoechstens 500 Eintraege (die aeltesten fallen weg).
// Als KI-Herkunft gilt nur, was cs-team selbst aus einer KI-Antwort anlegt (KI-Vorschlag, Recherche/Zusammenfassung speichern).
// Text, den jemand aus einer KI-Antwort von Hand in ein Dokument kopiert, ist nicht erkennbar und zaehlt als Bearbeitung.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"cs-team/auth"
	"cs-team/store"
)

const (
	maxChanges  = 500
	maxKeys     = 12
	mergeWindow = 600 // Sekunden
)

// Change: ein Eintrag der Aenderungsliste.
type Change struct {
	T0   int64    `json:"t0"`            // erste Aenderung (unix s)
	T    int64    `json:"t"`             // letzte Aenderung (unix s)
	By   string   `json:"by"`            // Benutzer
	Op   string   `json:"op"`            // create | edit | readonly | editable
	Src  string   `json:"src,omitempty"` // create: user | ai | import | merge | snapshot; edit: user
	Set  int      `json:"set,omitempty"` // edit: geaenderte Zellen/Absaetze
	Del  int      `json:"del,omitempty"` // edit: geloeschte
	N    int      `json:"n,omitempty"`   // create: Zahl der Zellen/Absaetze
	Keys []string `json:"keys,omitempty"`
	Note string   `json:"note,omitempty"` // snapshot: Name des Snapshots (und des Quelldokuments)
}

var reID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func changesKey(id string) string { return "doc/" + id + "/changes.json" }

func hasKey(l []string, k string) bool {
	for _, x := range l {
		if x == k {
			return true
		}
	}
	return false
}

// mergeChange haengt c an die Liste (Bearbeitungen desselben Benutzers dicht hintereinander werden zusammengefasst).
func mergeChange(list []Change, c Change) []Change {
	if c.Op == "edit" && len(list) > 0 {
		l := &list[len(list)-1]
		if l.Op == "edit" && l.By == c.By && l.Src == c.Src && c.T0-l.T <= mergeWindow {
			if c.T > l.T {
				l.T = c.T
			}
			l.Set += c.Set
			l.Del += c.Del
			for _, k := range c.Keys {
				if len(l.Keys) >= maxKeys {
					break
				}
				if !hasKey(l.Keys, k) {
					l.Keys = append(l.Keys, k)
				}
			}
			return list
		}
	}
	list = append(list, c)
	if len(list) > maxChanges {
		list = list[len(list)-maxChanges:]
	}
	return list
}

func appendChanges(ctx context.Context, st store.Store, id string, cs []Change) error {
	if len(cs) == 0 {
		return nil
	}
	return store.Update(ctx, st, changesKey(id), func(cur []byte) ([]byte, error) {
		var list []Change
		if cur != nil {
			json.Unmarshal(cur, &list)
		}
		for _, c := range cs {
			list = mergeChange(list, c)
		}
		return json.Marshal(list)
	})
}

func readChanges(ctx context.Context, st store.Store, id string) []Change {
	b, _, err := st.Get(ctx, changesKey(id))
	if err != nil {
		return []Change{}
	}
	var list []Change
	if json.Unmarshal(b, &list) != nil || list == nil {
		return []Change{}
	}
	return list
}

// Log schreibt einen Eintrag (Fehler werden nur protokolliert: die Liste darf nie das Arbeiten verhindern).
func (h *Hub) Log(ctx context.Context, id string, c Change) {
	if c.T == 0 {
		c.T = time.Now().Unix()
	}
	if c.T0 == 0 {
		c.T0 = c.T
	}
	if err := appendChanges(ctx, h.st, id, []Change{c}); err != nil {
		log.Printf("doc changes: %s: %v", id, err)
	}
}

// Created haelt das Anlegen fest. src: user | ai | import | merge | snapshot; n = Zahl der Zellen/Absaetze.
func (h *Hub) Created(ctx context.Context, id, by, src string, n int) {
	h.Log(ctx, id, Change{By: by, Op: "create", Src: src, N: n})
}

// note merkt eine Bearbeitung vor (mu gehalten); persist() schreibt sie in die Liste.
func (l *live) note(user, key string, del bool) {
	const src = "user"
	pk := user + "|" + src
	if l.pend == nil {
		l.pend = map[string]*Change{}
	}
	now := time.Now().Unix()
	c := l.pend[pk]
	if c == nil {
		c = &Change{T0: now, By: user, Op: "edit", Src: src}
		l.pend[pk] = c
	}
	c.T = now
	if del {
		c.Del++
	} else {
		c.Set++
	}
	if len(c.Keys) < maxKeys && !hasKey(c.Keys, key) {
		c.Keys = append(c.Keys, key)
	}
}

// takePend entnimmt die vorgemerkten Bearbeitungen, aelteste zuerst (mu gehalten).
func (l *live) takePend() []Change {
	if len(l.pend) == 0 {
		return nil
	}
	out := make([]Change, 0, len(l.pend))
	for _, c := range l.pend {
		out = append(out, *c)
	}
	l.pend = nil
	sort.Slice(out, func(i, j int) bool { return out[i].T0 < out[j].T0 })
	return out
}

// flushChanges schreibt die vorgemerkten Bearbeitungen sofort in die Liste.
func (l *live) flushChanges(ctx context.Context) {
	l.mu.Lock()
	p := l.takePend()
	l.mu.Unlock()
	if err := appendChanges(ctx, l.st, l.id, p); err != nil {
		log.Printf("doc changes: %s: %v", l.id, err)
	}
}

// Summary: Antwort auf "ist das reine KI-Recherche oder hat jemand etwas geaendert?".
type Summary struct {
	Origin    string         `json:"origin"`            // ai | ai-edited | human | unknown (aelter als die Liste)
	Created   *Change        `json:"created,omitempty"` // Eintrag "create"
	UserEdits int            `json:"userEdits"`         // Bearbeitungs-Eintraege von Hand
	AIEdits   int            `json:"aiEdits"`           // Bearbeitungs-Eintraege aus KI-Funktionen
	Editors   map[string]int `json:"editors"`           // Benutzer -> Zahl der Bearbeitungs-Eintraege (von Hand)
}

func summarize(list []Change) Summary {
	s := Summary{Origin: "unknown", Editors: map[string]int{}}
	for i := range list {
		c := list[i]
		switch c.Op {
		case "create":
			if s.Created == nil {
				cc := c
				s.Created = &cc
			}
		case "edit":
			if c.Src == "ai" {
				s.AIEdits++
			} else {
				s.UserEdits++
				s.Editors[c.By]++
			}
		}
	}
	switch {
	case s.Created == nil:
	case s.Created.Src == "ai" && s.UserEdits == 0:
		s.Origin = "ai"
	case s.Created.Src == "ai":
		s.Origin = "ai-edited"
	default:
		s.Origin = "human"
	}
	return s
}

// changes: GET /api/docs/{id}/changes (nur globale Admins): Zusammenfassung und Liste, neueste zuerst.
func (h *Hub) changes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if !auth.IsAdmin(ctx) || !reID.MatchString(id) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	l, err := h.open(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	l.flushChanges(ctx)
	list := readChanges(ctx, h.st, id)
	sum := summarize(list)
	rev := make([]Change, len(list))
	for i, c := range list {
		rev[len(list)-1-i] = c
	}
	l.mu.Lock()
	meta := l.meta
	l.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]any{
		"id": id, "name": meta.Name, "type": meta.Type, "owner": meta.Owner, "ro": meta.RO,
		"summary": sum, "changes": rev,
	})
}

// setReadonly: POST /api/docs/{id}/readonly {ro}: Schreibschutz setzen oder aufheben (Ersteller oder Admin).
func (h *Hub) setReadonly(w http.ResponseWriter, r *http.Request) {
	l := h.owned(w, r)
	if l == nil {
		return
	}
	var in struct{ RO bool }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	l.mu.Lock()
	changed := l.meta.RO != in.RO
	l.meta.RO = in.RO
	b, _ := json.Marshal(l.meta)
	l.mu.Unlock()
	if !changed {
		json.NewEncoder(w).Encode(map[string]bool{"ro": in.RO})
		return
	}
	if _, err := h.st.Put(r.Context(), metaKey(l.id), b, ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	op := "editable"
	if in.RO {
		op = "readonly"
	}
	h.Log(r.Context(), l.id, Change{By: auth.User(r.Context()), Op: op})
	l.recheck() // gilt sofort auch fuer offene Verbindungen
	json.NewEncoder(w).Encode(map[string]bool{"ro": in.RO})
}

// cleanName: Dokumentname fuer den Import eines Standes: "<Name>_<Datum>" (hoechstens 100 Bytes).
func importName(name string, mod int64) string {
	suf := "_" + time.Unix(mod, 0).Format("2006-01-02_15-04")
	r := []rune(strings.TrimSpace(name))
	for len(string(r))+len(suf) > 100 && len(r) > 0 {
		r = r[:len(r)-1]
	}
	return string(r) + suf
}
