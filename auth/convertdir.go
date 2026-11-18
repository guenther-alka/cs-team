package auth

// Spiegelkonten in lokale Konten umwandeln (0.72): fuer den dauerhaften Abschied vom Verzeichnis (AD/LDAP).
// Name, uid, Gruppen (auch die aus Verzeichnisgruppen abgeleiteten) und Rechte bleiben; der Zwischenspeicher-Abdruck des
// zuletzt im Verzeichnis gueltigen Passworts wird zum lokalen Passwort. Konten ohne Abdruck (Zwischenspeicher war aus)
// bekommen kein Passwort und brauchen ein Startpasswort (Benutzerliste/Initialpasswort oder Passwort setzen).
// Ein abgelaufener Abdruck gilt trotzdem: die Umwandlung ist eine ausdrueckliche Entscheidung eines globalen Admins.

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
)

type ConvertResult struct {
	Converted []string `json:"converted"`         // umgewandelt, Passwort bleibt
	NoPw      []string `json:"nopw"`              // umgewandelt, braucht ein Startpasswort
	Skipped   []string `json:"skipped,omitempty"` // nicht umwandelbar (Name ist kein gueltiger lokaler Kontoname)
}

// ConvertDir wandelt Spiegelkonten (Source "dir") in lokale Konten um; names leer = alle.
func (a *Auth) ConvertDir(ctx context.Context, names []string) (ConvertResult, error) {
	var res ConvertResult
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	// Gruppen aus dem berechneten Stand (Account.Member aus Group.Dir/Group.Sub) als feste Handliste uebernehmen
	a.refresh(ctx)
	a.mu.Lock()
	eff := map[string][]string{}
	for n, u := range a.users {
		if u.Source == "dir" && (len(want) == 0 || want[n]) {
			eff[n] = noSysGroups(effGroups(u))
		}
	}
	a.mu.Unlock()
	err := a.mutate(ctx, func(m map[string]Account) error {
		res = ConvertResult{}
		for n, u := range m {
			gs, ok := eff[n]
			if !ok || u.Source != "dir" {
				continue
			}
			if !validAcct.MatchString(n) {
				res.Skipped = append(res.Skipped, n)
				continue
			}
			if len(gs) == 0 {
				gs = []string{DefaultGroup}
			}
			if u.Mail == "" { // die Verzeichnisadresse name@domain wird zur Kontakt-Mail (wie bei lokalen Konten mit Adresse)
				if strings.Contains(n, "@") {
					u.Mail = n
				} else if u.Realm != "" && strings.Contains(u.Realm, ".") {
					u.Mail = n + "@" + u.Realm
				}
			}
			u.Source, u.Realm, u.DirSeen, u.DirGroups = "", "", "", nil
			u.Groups = gs
			if u.Hash == "" || u.Hash == "!" {
				u.Hash, u.Must = "", true
				res.NoPw = append(res.NoPw, n)
			} else {
				res.Converted = append(res.Converted, n)
			}
			m[n] = u
		}
		return nil
	})
	sort.Strings(res.Converted)
	sort.Strings(res.NoPw)
	sort.Strings(res.Skipped)
	return res, err
}

func (a *Auth) convertRoutes(mux *http.ServeMux, adm func(http.HandlerFunc) http.Handler) {
	// POST /api/users/convertdir {"names":[...]} (leer = alle Spiegelkonten), nur globale Admins
	mux.Handle("POST /api/users/convertdir", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Names []string }
		if !body(w, r, &in) {
			return
		}
		res, err := a.ConvertDir(r.Context(), in.Names)
		if err != nil {
			fail_(w, err)
			return
		}
		log.Printf("audit: mirror accounts converted to local by %q: %d with password, %d need a start password, %d skipped",
			safeName(User(r.Context())), len(res.Converted), len(res.NoPw), len(res.Skipped))
		jsonOut(w, res)
	}))
}
