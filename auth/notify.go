package auth

// Benachrichtigungen: jeder Benutzer waehlt, welche Meldungsarten er ueber seine externen Adressen (E-Mail, Chat-Adresse/ntfy/Webhook) bekommt.
// Arten: task (Aufgaben), mention (Erwaehnung/Antwort im Chat), news (Aushang), dm (1:1-Nachricht). Standard: alle an; wer keine Adresse hinterlegt hat, bekommt nichts.

import (
	"context"
	"net/http"
	"sort"
)

var notifyKinds = map[string]bool{"task": true, "mention": true, "news": true, "dm": true}

// NotifyWanted: will der aktive Benutzer Meldungen dieser Art? (false bei unbekanntem oder gesperrtem Konto)
func NotifyWanted(user, kind string) bool {
	if std == nil {
		return false
	}
	u, ok := std.get(context.Background(), user)
	if !ok || u.Disabled {
		return false
	}
	for _, k := range u.NotifyOff {
		if k == kind {
			return false
		}
	}
	return true
}

// SetNotifyOff speichert die abgewaehlten Arten (unbekannte Namen werden verworfen, doppelte entfernt).
func (a *Auth) SetNotifyOff(ctx_ context.Context, name string, off []string) error {
	seen := map[string]bool{}
	var l []string
	for _, k := range off {
		if notifyKinds[k] && !seen[k] {
			seen[k] = true
			l = append(l, k)
		}
	}
	sort.Strings(l)
	return a.mutate(ctx_, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		u.NotifyOff = l
		m[name] = u
		return nil
	})
}

// ActiveUserNames: alle aktiven Konten (fuer Aushaenge an alle).
func ActiveUserNames() []string {
	if std == nil {
		return nil
	}
	std.refresh(context.Background())
	std.mu.Lock()
	defer std.mu.Unlock()
	var out []string
	for n, u := range std.users {
		if !u.Disabled {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (a *Auth) notifyRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/me/notify", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Off []string }
		if !body(w, r, &in) {
			return
		}
		if err := a.SetNotifyOff(r.Context(), User(r.Context()), in.Off); err != nil {
			fail_(w, err)
		}
	})))
}
