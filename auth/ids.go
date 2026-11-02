package auth

// Feste numerische Kennungen (HA_SPEC.md, Abschnitt 2): Account.Uid und Group.Gid.
// Sie sind die Grundlage fuer LDAP (posixAccount/posixGroup), Betriebssystemkonten und gleiche SIDs nach einem Failover.
// Regeln: ab 10000, fortlaufend, nie wiederverwendet (auch nicht nach dem Loeschen), bleiben bei Umbenennung bestehen.
// Der Zaehler steht in users/ids.json. Reihenfolge: erst reservieren, dann users.json/groups.json schreiben - bricht ein
// Schreibvorgang ab, entsteht hoechstens eine Luecke, nie eine doppelte Nummer.

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"

	"cs-team/store"
)

const (
	idsKey  = "users/ids.json"
	firstID = 10000
)

var (
	reShort       = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
	ErrBadShort   = errors.New("short name: up to 8 lowercase letters or digits")
	ErrShortTaken = errors.New("short name already used by another group")
	ErrNoExport   = errors.New("system groups are not exported")
)

// idCounter: die jeweils naechste freie Nummer.
type idCounter struct {
	Uid int `json:"uid"`
	Gid int `json:"gid"`
}

func max3(a, b, c int) int {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

// reserveIDs: reserviert nu Uids und ng Gids (ETag-gesichert) und gibt die jeweils erste Nummer zurueck.
// floorU/floorG = hoechste vorhandene Nummer + 1: heilt einen verlorenen Zaehler, ohne eine vergebene Nummer zu erreichen.
func (a *Auth) reserveIDs(ctx context.Context, nu, ng, floorU, floorG int) (u0, g0 int, err error) {
	err = store.Update(ctx, a.st, idsKey, func(cur []byte) ([]byte, error) {
		var c idCounter
		if cur != nil {
			if err := json.Unmarshal(cur, &c); err != nil {
				return nil, err
			}
		}
		u0, g0 = max3(c.Uid, floorU, firstID), max3(c.Gid, floorG, firstID)
		c.Uid, c.Gid = u0+nu, g0+ng
		return json.Marshal(c)
	})
	return
}

// assignUids: Konten ohne Uid bekommen die naechste freie Nummer (nach Kontoname sortiert, damit ein Import stabil ist).
func (a *Auth) assignUids(ctx context.Context, m map[string]Account) error {
	var need []string
	top := 0
	for n, u := range m {
		if u.Uid == 0 {
			need = append(need, n)
		} else if u.Uid > top {
			top = u.Uid
		}
	}
	if len(need) == 0 {
		return nil
	}
	sort.Strings(need)
	u0, _, err := a.reserveIDs(ctx, len(need), 0, top+1, 0)
	if err != nil {
		return err
	}
	for i, n := range need {
		u := m[n]
		u.Uid = u0 + i
		m[n] = u
	}
	return nil
}

// assignGids: wie assignUids fuer Gruppen; ausserdem bleibt jeder Kurzname hoechstens einmal vergeben.
func (a *Auth) assignGids(ctx context.Context, m map[string]Group) error {
	var need, names []string
	top := 0
	for n, g := range m {
		names = append(names, n)
		if g.Gid == 0 {
			need = append(need, n)
		} else if g.Gid > top {
			top = g.Gid
		}
	}
	sort.Strings(names)
	used := map[string]bool{}
	for _, n := range names { // doppelte oder ungueltige Kurznamen (z.B. aus einem Import) entfernen
		g := m[n]
		if g.Short == "" {
			continue
		}
		if !reShort.MatchString(g.Short) || used[g.Short] {
			g.Short = ""
			m[n] = g
			continue
		}
		used[g.Short] = true
	}
	if len(need) == 0 {
		return nil
	}
	sort.Strings(need)
	_, g0, err := a.reserveIDs(ctx, 0, len(need), 0, top+1)
	if err != nil {
		return err
	}
	for i, n := range need {
		g := m[n]
		g.Gid = g0 + i
		m[n] = g
	}
	return nil
}

// EnsureIDs: vergibt Uid/Gid an vorhandene Konten und Gruppen (einmalig beim ersten Start nach dem Update).
func (a *Auth) EnsureIDs(ctx context.Context) error {
	a.refresh(ctx)
	a.mu.Lock()
	needU, needG := false, false
	for _, u := range a.users {
		if u.Uid == 0 {
			needU = true
			break
		}
	}
	for _, g := range a.groups {
		if g.Gid == 0 {
			needG = true
			break
		}
	}
	a.mu.Unlock()
	if needU {
		if err := a.mutate(ctx, func(map[string]Account) error { return nil }); err != nil {
			return err
		}
	}
	if needG {
		if err := a.mutateGroups(ctx, func(map[string]Group) error { return nil }); err != nil {
			return err
		}
	}
	return nil
}

// SetGroupShort: Kurzname fuer SMB-Gruppen (leer = keiner). Eindeutig, bis 8 Kleinbuchstaben/Ziffern.
func (a *Auth) SetGroupShort(ctx context.Context, name, short string) error {
	if short != "" && !reShort.MatchString(short) {
		return ErrBadShort
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		g, ok := m[name]
		if !ok {
			return ErrNoGroup
		}
		if short != "" {
			for n, o := range m {
				if n != name && o.Short == short {
					return ErrShortTaken
				}
			}
		}
		g.Short = short
		m[name] = g
		return nil
	})
}

// SetGroupExport: Gruppe in LDAP/Betriebssystem anbieten. Systemgruppen nie.
func (a *Auth) SetGroupExport(ctx context.Context, name string, on bool) error {
	if on && sysGroup(name) {
		return ErrNoExport
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		g, ok := m[name]
		if !ok {
			return ErrNoGroup
		}
		g.Export = on
		m[name] = g
		return nil
	})
}
