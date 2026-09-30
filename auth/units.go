package auth

import (
	"context"
	"encoding/json"
	"errors"
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
	if !validName.MatchString(name) {
		return ErrBadName
	}
	return a.mutateUnits(ctx, func(l []string) ([]string, error) {
		if contains(l, name) {
			return nil, ErrExists
		}
		return append(l, name), nil
	})
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
			Name   string   `json:"name"`
			Groups []string `json:"groups"`
		}
		out := []row{}
		units := a.loadUnits(r.Context())
		a.mu.Lock()
		for _, u := range units {
			rw := row{Name: u, Groups: []string{}}
			for n, g := range a.groups {
				if contains(unitsOf(g), u) {
					rw.Groups = append(rw.Groups, n)
				}
			}
			sort.Strings(rw.Groups)
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
