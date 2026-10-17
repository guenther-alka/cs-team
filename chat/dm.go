package chat

// 1:1-Nachrichten (Direktnachrichten in einer Gruppe) und Antworten (Threads).
//
// 1:1: ein Kanal "dm-<12 Hex>" je Paar und Gruppe (Name aus beiden Benutzernamen, stabil), eingetragen in der Kanalliste der Gruppe mit den beiden
// Teilnehmern (chanInfo.Dm). Lesen und Schreiben nur fuer die beiden (chanOK); Gruppen-Admins und globale Admins sehen den Kanal NICHT, er kommt nicht
// in die Auswertung (audit.go), nicht in die Suche anderer und nicht in die Meldungen an andere Mitglieder. Wer mit wem schreiben darf, schaltet je
// Gruppe der Gruppen-Admin (Gruppe > "1:1-Nachrichten"): aus (Vorgabe) | admin (nur Gruppen-Admin und Mitglied) | member (Mitglieder untereinander).
// Wird der Modus spaeter abgeschaltet, bleibt der Verlauf lesbar, geschrieben werden kann nicht mehr.
//
// Threads: eine Antwort traegt Msg.Rp = Nummer der ersten Nachricht des Threads (eine Ebene; die Antwort auf eine Antwort zeigt auf die Wurzel).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"cs-team/auth"
)

var ErrDM = errors.New("direct messages not allowed")

// dmName: stabiler Kanalname des 1:1-Chats zweier Benutzer ("dm-" + 12 Hex-Zeichen; passt zu reChan).
func dmName(a, b string) string {
	if a > b {
		a, b = b, a
	}
	h := sha256.Sum256([]byte(a + "\x00" + b))
	return "dm-" + hex.EncodeToString(h[:6])
}

// dmOf: die beiden Teilnehmer, wenn der Kanal ein 1:1-Kanal ist, sonst nil.
func (s *Svc) dmOf(ctx context.Context, g, cn string) []string {
	if !strings.HasPrefix(cn, "dm-") {
		return nil
	}
	l := s.list(ctx, g)
	s.mu.Lock()
	defer s.mu.Unlock()
	if i, ok := l[cn]; ok && len(i.Dm) == 2 {
		return append([]string(nil), i.Dm...)
	}
	return nil
}

func inDM(p []string, user string) bool { return len(p) == 2 && (p[0] == user || p[1] == user) }

func otherOf(p []string, user string) string {
	if len(p) == 2 && p[0] == user {
		return p[1]
	}
	if len(p) == 2 {
		return p[0]
	}
	return ""
}

// dmAllowed: darf das Paar in dieser Gruppe 1:1 schreiben? (beide Mitglieder, Gruppen-Chat nicht aus, Modus der Gruppe)
func dmAllowed(g, a, b string) bool {
	info, ok := auth.GroupInfoOf(g)
	if !ok || info.Chat == auth.ModeOff || a == b || !auth.IsMember(a, g) || !auth.IsMember(b, g) {
		return false
	}
	switch info.DM {
	case auth.ModeMember:
		return true
	case auth.ModeAdmin:
		return auth.IsGroupAdmin(a, g) || auth.IsGroupAdmin(b, g)
	}
	return false
}

func dmModeOf(g string) string {
	info, _ := auth.GroupInfoOf(g)
	return info.DM
}

// chanOK: Rechte des Benutzers im Kanal (lesen, schreiben); ein 1:1-Kanal gehoert nur seinen beiden Teilnehmern.
func (s *Svc) chanOK(ctx context.Context, user, g, cn string) (read, write bool) {
	r, w := access(user, g)
	if !r || !s.exists(ctx, g, cn) {
		return false, false
	}
	if p := s.dmOf(ctx, g, cn); p != nil {
		if !inDM(p, user) {
			return false, false
		}
		return true, dmAllowed(g, p[0], p[1])
	}
	return r, w
}

// countChans: Kanaele ohne die 1:1-Kanaele (die zaehlen nicht gegen maxChans). s.mu muss gehalten sein (Aufruf aus MakeChannel).
func (s *Svc) countChans(l map[string]chanInfo) int {
	n := 0
	for _, i := range l {
		if len(i.Dm) == 0 {
			n++
		}
	}
	return n
}

// broadcastChans schickt jedem verbundenen Mitglied seine Kanalliste (only: nur an diese, z. B. die beiden eines neuen 1:1-Kanals).
func (s *Svc) broadcastChans(ctx context.Context, g string, only ...string) {
	s.broadcastF(g, func(user string) []byte {
		if len(only) > 0 {
			ok := false
			for _, o := range only {
				ok = ok || o == user
			}
			if !ok {
				return nil
			}
		}
		b, _ := json.Marshal(map[string]any{"t": "chans", "g": g, "chans": s.chanRows(ctx, g, user)})
		return b
	})
}

// OpenDM legt den 1:1-Kanal an (falls neu) und liefert seinen Namen.
func (s *Svc) OpenDM(ctx context.Context, user, g, with string) (string, error) {
	if r, _ := access(user, g); !r {
		return "", ErrNoChat
	}
	if !dmAllowed(g, user, with) {
		return "", ErrDM
	}
	cn := dmName(user, with)
	l := s.list(ctx, g)
	created := false
	s.mu.Lock()
	if _, ok := l[cn]; !ok {
		p := []string{user, with}
		sort.Strings(p)
		l[cn] = chanInfo{By: user, Created: time.Now().UnixNano(), Dm: p}
		created = true
	}
	s.mu.Unlock()
	if created {
		s.saveList(ctx, g)
		s.broadcastChans(ctx, g, user, with)
	}
	return cn, nil
}

func (s *Svc) dmRoutes(mux *http.ServeMux, h func(http.HandlerFunc) http.Handler) {
	// wen darf ich in dieser Gruppe 1:1 anschreiben? (nur Namen)
	mux.Handle("GET /api/chat/{g}/dm", h(func(w http.ResponseWriter, r *http.Request) {
		me, g := auth.User(r.Context()), r.PathValue("g")
		if rd, _ := access(me, g); !rd {
			http.Error(w, "not found", 404)
			return
		}
		people := []string{}
		for _, m := range auth.MembersOf(g) {
			if m.Name != me && dmAllowed(g, me, m.Name) {
				people = append(people, m.Name)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"people": people, "mode": dmModeOf(g)})
	}))
	mux.Handle("POST /api/chat/{g}/dm", h(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ With string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		cn, err := s.OpenDM(r.Context(), auth.User(r.Context()), r.PathValue("g"), strings.TrimSpace(in.With))
		switch {
		case errors.Is(err, ErrDM):
			http.Error(w, err.Error(), http.StatusForbidden)
		case err != nil:
			http.Error(w, "not found", 404)
		default:
			json.NewEncoder(w).Encode(map[string]string{"name": cn})
		}
	}))
}
