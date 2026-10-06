package auth

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"cs-team/store"
)

// UserHook: ein Modul räumt die Daten eines gelöschten Benutzers auf (Dateien, Dokumente, Kalender).
// Purge löscht auch Freigaben des Namens in fremden Objekten. Count wird für die Vorschau gebraucht.
type UserHook struct {
	Name  string
	Count func(ctx context.Context, user string) int
	Purge func(ctx context.Context, user string) error
}

var UserHooks []UserHook

// DeletedUser: Ersatztext für den Namen eines gelöschten Kontos (Option "anonymize", Standard an).
const DeletedUser = "gelöschter Benutzer"

// AnonHook: ein Modul ersetzt den Namen des gelöschten Kontos in Inhalten, die bleiben (Chat, Aufgaben, Termin-Teilnehmer).
// Count = Zahl der betroffenen Einträge (Vorschau), Anon ersetzt den Namen durch repl und liefert die Zahl der geänderten Einträge.
type AnonHook struct {
	Name  string
	Count func(ctx context.Context, user string) int
	Anon  func(ctx context.Context, user, repl string) (int, error)
}

var AnonHooks []AnonHook

const delLogKey = "users/_delete-log.json"

type delEntry struct {
	T        int64          `json:"t"`
	Admin    string         `json:"admin"`
	User     string         `json:"user"`
	Counts   map[string]int `json:"counts"`
	Anon     map[string]int `json:"anon,omitempty"` // Zahl der anonymisierten Einträge (nur Zahlen, kein Inhalt)
	Snapshot string         `json:"snapshot"`
}

// purgeRoutes: Benutzer löschen mit Vorschau, Passwort, Snapshot und Entfernen aller Daten (Audit S-07).
// Gruppen- und Chatinhalte (Nachrichten, Aufgaben in Gruppen, Gruppenordner) bleiben; sie gehören der Gruppe.
func (a *Auth) purgeRoutes(mux *http.ServeMux, adm func(http.HandlerFunc) http.Handler) {
	counts := func(ctx context.Context, name string) map[string]int {
		out := map[string]int{}
		for _, h := range UserHooks {
			if h.Count != nil {
				out[h.Name] = h.Count(ctx, name)
			}
		}
		return out
	}
	anonCounts := func(ctx context.Context, name string) map[string]int {
		out := map[string]int{}
		for _, h := range AnonHooks {
			if h.Count != nil {
				out[h.Name] = h.Count(ctx, name)
			}
		}
		return out
	}
	check := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		name := r.PathValue("name")
		if !validName.MatchString(name) {
			http.Error(w, ErrBadName.Error(), http.StatusBadRequest)
			return "", false
		}
		if _, ok := a.get(r.Context(), name); !ok {
			http.Error(w, ErrNoUser.Error(), http.StatusNotFound)
			return "", false
		}
		if name == User(r.Context()) {
			http.Error(w, "you cannot delete your own account", http.StatusBadRequest)
			return "", false
		}
		return name, true
	}
	mux.Handle("GET /api/users/{name}/delete/preview", adm(func(w http.ResponseWriter, r *http.Request) {
		name, ok := check(w, r)
		if !ok {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"counts": counts(r.Context(), name), "anon": anonCounts(r.Context(), name), "replacement": DeletedUser, "snapshot": SnapshotMode(), "dataset": SnapshotInfo})
	}))
	mux.Handle("POST /api/users/{name}/delete", adm(func(w http.ResponseWriter, r *http.Request) {
		name, ok := check(w, r)
		if !ok {
			return
		}
		var in struct {
			Password  string
			NoSnap    bool  `json:"noSnapshot"`
			Anonymize *bool `json:"anonymize"` // Standard: an
		}
		if !body(w, r, &in) {
			return
		}
		if SnapshotMode() == "none" && !in.NoSnap {
			http.Error(w, "no snapshot possible - confirm to run without snapshot (noSnapshot) or set CS_SNAPSHOT_CMD", http.StatusBadRequest)
			return
		}
		if code, msg := a.confirm(r, in.Password); code != 0 {
			http.Error(w, msg, code)
			return
		}
		ctx := r.Context()
		if t, _ := a.get(ctx, name); t.Sys { // das Sysadmin-Konto bleibt (Notfallzugang)
			fail_(w, ErrSysAdmin)
			return
		}
		// Vorbedingung vor allem Löschen: nicht der letzte aktive lokale Admin (Notfallzugang, 0.55)
		if u, _ := a.get(ctx, name); u.Admin && !strings.Contains(name, "@") {
			if a.LocalAdmins(ctx) <= 1 {
				fail_(w, ErrLastAdm)
				return
			}
		}
		snap := "none"
		id := newRunID()
		if !in.NoSnap && SnapshotMode() != "none" {
			snap = "cs-team-deluser-" + id
			if err := runSnapshot("deluser-" + id); err != nil {
				http.Error(w, "snapshot failed, nothing deleted: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		cnt := counts(ctx, name)
		var anon map[string]int
		if in.Anonymize == nil || *in.Anonymize { // Name in Nachrichten, Aufgaben und Terminen ersetzen (vor dem Löschen: bei Fehler bleibt das Konto)
			anon = map[string]int{}
			for _, h := range AnonHooks {
				if h.Anon == nil {
					continue
				}
				n, err := h.Anon(ctx, name, DeletedUser)
				anon[h.Name] = n
				if err != nil {
					http.Error(w, "anonymizing "+h.Name+" failed: "+err.Error()+" (account not deleted; snapshot "+snap+")", http.StatusInternalServerError)
					return
				}
			}
		}
		for _, h := range UserHooks {
			if h.Purge != nil {
				if err := h.Purge(ctx, name); err != nil {
					http.Error(w, "deleting "+h.Name+" failed: "+err.Error()+" (account not deleted; snapshot "+snap+")", http.StatusInternalServerError)
					return
				}
			}
		}
		if err := a.DeleteUser(ctx, name); err != nil {
			fail_(w, err)
			return
		}
		admin := User(ctx)
		store.Update(ctx, a.st, delLogKey, func(cur []byte) ([]byte, error) {
			var l []delEntry
			if cur != nil {
				json.Unmarshal(cur, &l)
			}
			l = append(l, delEntry{T: time.Now().Unix(), Admin: admin, User: name, Counts: cnt, Anon: anon, Snapshot: snap})
			if len(l) > 200 {
				l = l[len(l)-200:]
			}
			return json.Marshal(l)
		})
		log.Printf("user %s deleted by %s (%v), anonymized %v, snapshot %s", name, admin, cnt, anon, snap)
		json.NewEncoder(w).Encode(map[string]any{"deleted": name, "counts": cnt, "anon": anon, "snapshot": snap})
	}))
}
