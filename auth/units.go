package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"cs-team/store"
)

// Organisationen (Schule, Abteilung, Mandant ...): reine Zuordnung von Gruppen, ohne eigene Rechte.
// Eine Gruppe kann mehreren Organisationen angehören; ohne Angabe gehört sie zu "all".
const (
	DefaultUnit = "all"
	unitsKey    = "users/units.json"
)

var ErrDefaultUnit = errors.New("the unit '" + DefaultUnit + "' cannot be deleted")
var ErrNoUnit = errors.New("no such unit")

func (a *Auth) loadUnits(ctx context.Context) []string {
	b, _, err := a.st.Get(ctx, unitsKey)
	var l []string
	if err == nil {
		json.Unmarshal(b, &l)
	} else if !errors.Is(err, store.ErrNotFound) {
		return []string{DefaultUnit}
	}
	return withDefault(l)
}

func withDefault(l []string) []string {
	out := []string{DefaultUnit}
	for _, x := range l {
		if x != DefaultUnit {
			out = append(out, x)
		}
	}
	sort.Strings(out[1:])
	return uniq(out)
}

func (a *Auth) mutateUnits(ctx context.Context, fn func(l []string) ([]string, error)) error {
	return store.Update(ctx, a.st, unitsKey, func(cur []byte) ([]byte, error) {
		var l []string
		if cur != nil {
			json.Unmarshal(cur, &l)
		}
		l, err := fn(withDefault(l))
		if err != nil {
			return nil, err
		}
		return json.Marshal(l)
	})
}

func (a *Auth) AddUnit(ctx context.Context, name string) error {
	if !okName(name) {
		return ErrBadName
	}
	gs, ok := a.loadGroups(ctx)
	if !ok {
		return errors.New("groups could not be read")
	}
	if _, ex := gs[name]; ex {
		// Sonst wäre neben der Gruppe "schule" auch die Organisation "schule" da: "#schule" und "@schule" wären
		// verwechselbar, und ein Freigabe-Eintrag "g:schule" wäre nicht mehr eindeutig (KISS 0.55).
		return fmt.Errorf("%w: %s", ErrNameUsed, name)
	}
	return a.mutateUnits(ctx, func(l []string) ([]string, error) {
		if contains(l, name) {
			return nil, ErrExists
		}
		return append(l, name), nil
	})
}

// UnitExists: gibt es diese Organisation (ohne "all")? Für Aufrufer, die nur den Namen kennen (z.B. Kalender).
func UnitExists(name string) bool {
	if std == nil || name == "" || name == DefaultUnit {
		return false
	}
	return contains(std.loadUnits(context.Background()), name)
}

// UnitNames: alle Organisationen (ohne "all") - für Aufrufer, die alles auflisten (z.B. Kalender eines Admins).
func UnitNames() []string {
	if std == nil {
		return nil
	}
	var out []string
	for _, u := range std.loadUnits(context.Background()) {
		if u != DefaultUnit {
			out = append(out, u)
		}
	}
	return out
}

// UnitsOf: Organisationen des Benutzers (über seine Gruppen; "all" ist keine echte Zuordnung und zählt nicht).
func UnitsOf(user string) []string {
	if std == nil {
		return nil
	}
	gs := GroupsOf(user)
	std.mu.Lock()
	defer std.mu.Unlock()
	set := map[string]bool{}
	for _, g := range gs {
		for _, u := range std.groups[g].Units {
			if u != "" && u != DefaultUnit {
				set[u] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for u := range set {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// InUnit: gehört der Benutzer über eine seiner Gruppen zu dieser Organisation? Reine Zuordnung (KISS 0.55):
// die Organisation fasst die Mitglieder ihrer Gruppen zusammen; Rechte entstehen daraus nur dort, wo es
// ausdrücklich vorgesehen ist (Freigabeliste "g:<organisation>", Kalender-Zuordnung "#<organisation>").
func InUnit(user, unit string) bool {
	return unit != "" && unit != DefaultUnit && contains(UnitsOf(user), unit)
}

func (a *Auth) DeleteUnit(ctx context.Context, name string) error {
	if name == DefaultUnit {
		return ErrDefaultUnit
	}
	err := a.mutateUnits(ctx, func(l []string) ([]string, error) {
		if !contains(l, name) {
			return nil, ErrNoUnit
		}
		var o []string
		for _, x := range l {
			if x != name {
				o = append(o, x)
			}
		}
		return o, nil
	})
	if err != nil {
		return err
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		for n, g := range m {
			var o []string
			for _, x := range g.Units {
				if x != name {
					o = append(o, x)
				}
			}
			g.Units = o
			m[n] = g
		}
		return nil
	})
}

// SetGroupUnits: leer = "all". Unbekannte Organisationen werden abgelehnt.
func (a *Auth) SetGroupUnits(ctx context.Context, group string, units []string) error {
	known := a.loadUnits(ctx)
	units = uniq(units)
	for _, u := range units {
		if !contains(known, u) {
			return ErrNoUnit
		}
	}
	if len(units) == 1 && units[0] == DefaultUnit {
		units = nil
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		g, ok := m[group]
		if !ok {
			return ErrNoGroup
		}
		g.Units = units
		m[group] = g
		return nil
	})
}

func unitsOf(g Group) []string {
	if len(g.Units) == 0 {
		return []string{DefaultUnit}
	}
	return g.Units
}

func (a *Auth) unitRoutes(mux *http.ServeMux, adm func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/units", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.refresh(r.Context())
		type row struct {
			Name    string   `json:"name"`
			Groups  []string `json:"groups"`
			Members []string `json:"members,omitempty"` // effektive Mitglieder der Organisation (nur für Admins)
			Sources []string `json:"sources,omitempty"` // woher die Mitglieder kommen, "gruppe <- quelle" (nur für Admins)
		}
		out := []row{}
		adm := IsAdmin(r.Context())
		units := a.loadUnits(r.Context())
		a.mu.Lock()
		for _, u := range units {
			rw := row{Name: u, Groups: []string{}}
			for n, g := range a.groups {
				if !contains(unitsOf(g), u) {
					continue
				}
				rw.Groups = append(rw.Groups, n)
				if !adm {
					continue
				}
				for _, src := range append(append(append([]string{}, g.Sub...), g.Unit...), g.Dir...) {
					rw.Sources = append(rw.Sources, n+" <- "+src) // Gruppe n zieht die Mitglieder von src mit
				}
			}
			sort.Strings(rw.Groups)
			if adm { // Mitglieder sieht nur ein Admin (wie in der Gruppenansicht)
				set := map[string]bool{}
				for un, acc := range a.users {
					if acc.Disabled {
						continue
					}
					for _, x := range effGroups(acc) {
						if contains(rw.Groups, x) {
							set[un] = true
							break
						}
					}
				}
				for n := range set {
					rw.Members = append(rw.Members, n)
				}
				sort.Strings(rw.Members)
				sort.Strings(rw.Sources)
			}
			out = append(out, rw)
		}
		a.mu.Unlock()
		json.NewEncoder(w).Encode(out)
	})))
	mux.Handle("POST /api/units", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name string }
		if !body(w, r, &in) {
			return
		}
		if err := a.AddUnit(r.Context(), in.Name); err != nil {
			fail_(w, err)
		}
	}))
	mux.Handle("DELETE /api/units/{name}", adm(func(w http.ResponseWriter, r *http.Request) {
		if err := a.DeleteUnit(r.Context(), r.PathValue("name")); err != nil {
			fail_(w, err)
		}
	}))
}
