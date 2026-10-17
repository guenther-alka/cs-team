package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Systemgruppen und Rollen (Namen und Verhalten einheitlich):
//
//	Sysadmin       Startup-Admin (CS_ADMIN_USER), lokal, immer globaler Admin, nicht loeschbar.
//	Globaler Admin Konto mit Admin-Recht. Nur der Sysadmin legt globale Admins an oder aendert sie.
//	Gruppen-Admin  verwaltet einzelne Gruppen. Sysadmin und globale Admins legen sie an (Feld "Gruppen-Admins" der Gruppe).
//
// Zwei feste Gruppen bilden das ab: "globaladmins" (Mitglieder = alle Konten mit Admin-Recht, der Sysadmin ist immer dabei;
// Mitglieder aufnehmen/entfernen = Admin-Recht vergeben/entziehen, nur durch den Sysadmin) und "groupadmins" (Mitglieder = alle
// Gruppen-Admins, aus den Admin-Listen der Gruppen berechnet; globale Admins duerfen Mitglieder entfernen). Beide sind nicht
// loeschbar und nicht umbenennbar; sie stehen nie in der Handliste eines Kontos.
const (
	AdminGroup  = "globaladmins"
	GAdminGroup = "groupadmins"
)

var (
	ErrSysOnly  = errors.New("only the sysadmin can create or change global admins")
	ErrBadComment = errors.New("comment: at most 200 characters, one line")
	ErrSysGroup = errors.New("system groups (" + AdminGroup + ", " + GAdminGroup + ") cannot be deleted, renamed or given group admins")
)

func sysGroup(n string) bool { return n == AdminGroup || n == GAdminGroup }

// noSysGroups: die Systemgruppen sind berechnet und gehoeren nicht in die Handliste eines Kontos.
func noSysGroups(l []string) []string {
	out := make([]string, 0, len(l))
	for _, x := range l {
		if !sysGroup(x) {
			out = append(out, x)
		}
	}
	return out
}

// isSys: ist der Aufrufer der Sysadmin?
func (a *Auth) isSys(ctx context.Context) bool {
	u, ok := a.get(ctx, User(ctx))
	return ok && u.Sys
}

// adminGuard: ein globaler Admin (target) darf nur vom Sysadmin geaendert werden (selfOK: auch von ihm selbst).
func (a *Auth) adminGuard(ctx context.Context, target string, selfOK bool) error {
	u, ok := a.get(ctx, target)
	if !ok || !u.Admin {
		return nil
	}
	if a.isSys(ctx) || (selfOK && User(ctx) == target) {
		return nil
	}
	return ErrSysOnly
}

// setSysMembers: Mitglieder einer Systemgruppe aendern.
func (a *Auth) setSysMembers(ctx context.Context, group string, add, remove []string) error {
	names := func(l []string) ([]string, error) {
		var out []string
		for _, e := range l {
			k, n := a.entryKind(e)
			if k != entAccount {
				return nil, fmt.Errorf("%w: %q", ErrBadMember, e)
			}
			out = append(out, n)
		}
		return out, nil
	}
	ad, err := names(add)
	if err != nil {
		return err
	}
	rm, err := names(remove)
	if err != nil {
		return err
	}
	if group == AdminGroup { // Admin-Recht vergeben/entziehen: nur der Sysadmin
		if !a.isSys(ctx) {
			return ErrSysOnly
		}
		return a.mutate(ctx, func(m map[string]Account) error {
			for _, n := range ad {
				u, ok := m[n]
				if !ok {
					return fmt.Errorf("%w: %s", ErrNoUser, n)
				}
				if u.Guest || u.Source == "dir" {
					return fmt.Errorf("%w: %s", ErrNotEligible, n)
				}
				u.Admin = true
				m[n] = u
			}
			for _, n := range rm {
				u, ok := m[n]
				if !ok {
					continue
				}
				if u.Sys {
					return ErrSysAdmin
				}
				u.Admin = false
				m[n] = u
			}
			if !hasLocalAdmin(m) {
				return ErrLastAdm
			}
			return nil
		})
	}
	// groupadmins: Aufnahme geschieht in der jeweiligen Gruppe (Feld Gruppen-Admins); hier kann man Gruppen-Admins entfernen
	if !IsAdmin(ctx) {
		return ErrSysOnly
	}
	if len(ad) > 0 {
		return fmt.Errorf("%w: group admins are set in the group itself (field Gruppen-Admins)", ErrBadMember)
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		for gn, g := range m {
			var keep []string
			for _, x := range g.Admins {
				if !contains(rm, x) {
					keep = append(keep, x)
				}
			}
			if len(keep) != len(g.Admins) {
				g.Admins = keep
				m[gn] = g
			}
		}
		return nil
	})
}

// SetGroupComment: Beschreibung der Gruppe (eine Zeile, hoechstens 200 Zeichen).
func (a *Auth) SetGroupComment(ctx context.Context, group, c string) error {
	c = strings.TrimSpace(c)
	if utf8.RuneCountInString(c) > 200 || strings.ContainsAny(c, "\r\n\t\x00") {
		return ErrBadComment
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		g, ok := m[group]
		if !ok {
			return ErrNoGroup
		}
		g.Comment = c
		m[group] = g
		return nil
	})
}
