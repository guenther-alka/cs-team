package auth

// Gast-Konten mit Ablauf (Eltern, externe Referenten, Pruefer ...). Ein Gast ist ein lokales Konto mit
//   - einem Ablaufdatum (Pflicht, letzter gueltiger Tag, hoechstens 400 Tage voraus; spaeter verlaengerbar),
//   - der Gruppe "guests" (GuestGroup): jedes Gast-Konto ist immer dort Mitglied; die Gruppe wird beim ersten Gast automatisch und restriktiv
//     angelegt (keine Bereiche, Chat aus) - was Gaeste duerfen, stellt der Admin an dieser Gruppe ein (Bereiche, Chat, Ordner ...);
//     weitere Gruppen sind optional (ein Gruppen-Admin gibt seine Gruppen an, damit er den Gast verwalten kann); nie die Standardgruppe,
//   - keinen abgeleiteten Mitgliedschaften (Unter-/Organisations-/Verzeichnisgruppen),
//   - ohne Verwaltungsrechte (kein Admin, nie Gruppen-Admin), ohne KI-Assistent, ohne Aushaenge "an alle".
// Nach dem Ablauf gilt das Konto wie gesperrt (nur im Speicher berechnet, siehe expireGuests; gespeichert wird nichts):
// keine Anmeldung, keine Mitgliedschaft, keine Meldungen. Admins koennen es verlaengern oder ueber den Loeschassistenten entfernen.

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const (
	GuestGroup   = "guests"
	guestMaxDays = 400
)

var ErrGuest = errors.New("guest account: give a date YYYY-MM-DD (today up to 400 days ahead), optional groups (not the default group), no admin flag")

func today() string { return time.Now().Format("2006-01-02") }

func validExpires(s string) bool {
	d, err := time.Parse("2006-01-02", s)
	if err != nil || s < today() {
		return false
	}
	return !d.After(time.Now().AddDate(0, 0, guestMaxDays))
}

// expireGuests (nach resolveMembers): abgelaufene Gaeste gelten als gesperrt; Gaeste haben nie abgeleitete Mitgliedschaften.
func expireGuests(m map[string]Account) {
	now := today()
	for n, u := range m {
		if !u.Guest {
			continue
		}
		u.Member = nil
		if u.Expires == "" || u.Expires < now {
			u.Disabled = true
		}
		m[n] = u
	}
}

// IsGuest: ist der Benutzer ein (aktives oder abgelaufenes) Gast-Konto?
func IsGuest(user string) bool {
	if std == nil {
		return false
	}
	u, ok := std.get(context.Background(), user)
	return ok && u.Guest
}

// GuestOf: Ablaufdatum des Gast-Kontos ("" = kein Gast).
func GuestOf(user string) string {
	if std == nil {
		return ""
	}
	if u, ok := std.get(context.Background(), user); ok && u.Guest {
		return u.Expires
	}
	return ""
}

// ensureGuestGroup legt die Gruppe "guests" an, falls es sie nicht gibt: ohne Bereiche, Chat aus (der Admin schaltet frei, was Gaeste duerfen).
func (a *Auth) ensureGuestGroup(ctx context.Context) error {
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		if _, ok := m[GuestGroup]; !ok {
			m[GuestGroup] = Group{Areas: []string{}, Chat: ModeOff}
		}
		return nil
	})
}

// AddGuest legt ein Gast-Konto an: Mitglied der Gruppe "guests" und der angegebenen weiteren Gruppen (nicht der Standardgruppe).
func (a *Auth) AddGuest(ctx context.Context, name, pass string, groups []string, expires string) error {
	if !validExpires(expires) {
		return ErrGuest
	}
	gs := []string{GuestGroup}
	for _, g := range groups {
		if g == DefaultGroup {
			return ErrGuest
		}
		if g != GuestGroup {
			gs = append(gs, g)
		}
	}
	if err := a.ensureGuestGroup(ctx); err != nil {
		return err
	}
	if err := a.AddUser(ctx, name, pass, false, gs); err != nil {
		return err
	}
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		u.Guest, u.Expires = true, expires
		m[name] = u
		return nil
	})
}

// SetGuestExpiry verlaengert (oder verkuerzt) ein Gast-Konto.
func (a *Auth) SetGuestExpiry(ctx context.Context, name, expires string) error {
	if !validExpires(expires) {
		return ErrGuest
	}
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok || !u.Guest {
			return ErrNoUser
		}
		u.Expires = expires
		m[name] = u
		return nil
	})
}

func (a *Auth) guestRoutes(mux *http.ServeMux) {
	// Ablauf eines Gast-Kontos aendern: globaler Admin oder Gruppen-Admin einer Gruppe des Gastes
	mux.Handle("POST /api/users/{name}/expires", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Expires string }
		if !body(w, r, &in) {
			return
		}
		if !a.manages(r.Context(), r.PathValue("name")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := a.SetGuestExpiry(r.Context(), r.PathValue("name"), in.Expires); err != nil {
			fail_(w, err)
		}
	})))
}
