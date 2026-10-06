package auth

import (
	"context"
	"sort"
	"strings"
)

// Sysadmin (0.57.1): genau ein lokales cs-team-Konto ist das Sysadmin-Konto. Es ist immer Admin und aktiv, kann nicht
// gelöscht, gesperrt oder herabgestuft werden und sein Passwort ändert nur es selbst oder die Kommandozeile
// ("cs-team sysadmin NAME", "cs-team adduser NAME PW"). Es ist der Notfallzugang, der auch ohne Verzeichnis geht.
// Alle weiteren globalen Admins und Gruppen-Admins sind beliebig (lokale cs-team-Konten oder Verzeichniskonten);
// vergeben wird die Rolle immer lokal, das Verzeichnis liefert sie nie.

// isLocalKey: lokale cs-team-Konten haben keinen Namensraum (kein "@") und sind keine Spiegelkonten.
func isLocalKey(name string, u Account) bool { return !strings.Contains(name, "@") && u.Source != "dir" }

// EnsureSys legt beim Start fest, welches Konto das Sysadmin-Konto ist, und stellt Admin-Recht und Aktivierung sicher.
// Ist schon eines markiert, bleibt es (ein Wechsel nur per SetSys/Kommandozeile). Sonst gilt das Konto aus
// CS_ADMIN_USER (prefer), sonst bei Altbeständen das älteste aktive lokale Admin-Konto.
func (a *Auth) EnsureSys(ctx context.Context, prefer string) error {
	return a.mutate(ctx, func(m map[string]Account) error {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		pick := ""
		for _, n := range names {
			if u := m[n]; u.Sys && isLocalKey(n, u) {
				pick = n
				break
			}
		}
		if pick == "" && prefer != "" {
			if u, ok := m[prefer]; ok && isLocalKey(prefer, u) {
				pick = prefer
			}
		}
		if pick == "" {
			for _, n := range names {
				u := m[n]
				if !u.Admin || u.Disabled || !isLocalKey(n, u) {
					continue
				}
				if pick == "" || u.Created < m[pick].Created {
					pick = n
				}
			}
		}
		if pick == "" {
			return nil // noch kein lokales Konto (z.B. erster Start ohne CS_ADMIN_USER)
		}
		markSys(m, pick)
		return nil
	})
}

// SetSys (CLI): macht ein vorhandenes lokales Konto zum Sysadmin-Konto (Admin, aktiv); das bisherige verliert die Markierung.
func (a *Auth) SetSys(ctx context.Context, name string) error {
	return a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		if !isLocalKey(name, u) {
			return ErrSysLocal
		}
		markSys(m, name)
		return nil
	})
}

func markSys(m map[string]Account, pick string) {
	for n, u := range m {
		if want := n == pick; u.Sys != want {
			u.Sys = want
			m[n] = u
		}
	}
	u := m[pick]
	u.Admin, u.Disabled = true, false
	m[pick] = u
}

// SysAdmin: Name des Sysadmin-Kontos ("" = noch keines markiert).
func (a *Auth) SysAdmin(ctx context.Context) string {
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	for n, u := range a.users {
		if u.Sys {
			return n
		}
	}
	return ""
}
