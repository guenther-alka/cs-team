// Universeller Export/Import (0.61): JSON-Rundumsicherung von Benutzern und Gruppen mit allen cs-team-Settings
// (Oberflaechensprache, Kontakt, DSGVO-Bestaetigungen, Gruppenrechte, Aufbewahrung usw.). Startup-Einstellungen
// (Umgebungsvariablen: Port, Zertifikat, Datenpfad ...) gehoeren nicht dazu, das sind Servereinstellungen.
//
// Zwei getrennte Schalter fuer sensible Daten: PW (Passwort-Hash) und Secrets (TOTP-Schluessel, Wiederherstellungs-
// codes, App-Passwoerter). Beide Standard aus, damit ein Export ohne Nachdenken niemals Zugangsdaten enthaelt.
//
// Fehlt beim Import ein Passwort-Hash (z.B. Export von einem Windows-Host oder aus LDAP/AD, siehe Kapitel 15.4/15.5
// im Handbuch), erzeugt GenPW ein Zufalls-Startpasswort, das beim ersten Login geaendert werden muss; die vergebenen
// Passwoerter kommen nur in der HTTP-Antwort zurueck, nie in users.json im Klartext und nie ins Protokoll.
package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"time"
)

// BulkDump: vollstaendiger oder gefilterter Bestand an Benutzern und Gruppen.
type BulkDump struct {
	Version int                `json:"version"`
	Users   map[string]Account `json:"users"`
	Groups  map[string]Group   `json:"groups,omitempty"`
}

// BulkExportOpts steuert, was in den Export darf.
type BulkExportOpts struct {
	PW      bool     // Passwort-Hash mitnehmen (nur dort vorhanden, wo cs-team ihn selbst haelt)
	Secrets bool     // TOTP-Schluessel, Wiederherstellungscodes, App-Passwoerter mitnehmen
	Users   []string // leer = alle (nach Rechten erlaubten)
	Groups  []string // leer = alle (nur globaler Admin bekommt Gruppen)
}

// ExportBulk: Benutzer- und Gruppenbestand als BulkDump. Rechte wie beim bestehenden CSV-Export (S-09): globaler
// Admin sieht alles, Gruppen-Admin nur Mitglieder seiner Gruppen und nie deren Chat-Webhook-Adresse.
func (a *Auth) ExportBulk(ctx context.Context, o BulkExportOpts) BulkDump {
	a.refresh(ctx)
	admin := IsAdmin(ctx)
	ao := AdminOf(ctx)
	me := User(ctx)
	wantU := setOf(o.Users)
	wantG := setOf(o.Groups)

	a.mu.Lock()
	defer a.mu.Unlock()

	users := map[string]Account{}
	for n, u := range a.users {
		if !admin && (u.Admin || !shares(effGroups(u), ao)) {
			continue
		}
		if len(wantU) > 0 && !wantU[n] {
			continue
		}
		cp := u
		cp.Groups = append([]string{}, handGroups(u)...) // Handliste, nicht aus Verzeichnis/Untergruppe abgeleitete Mitgliedschaft
		cp.Member = nil
		cp.DirGroups = append([]string{}, u.DirGroups...)
		cp.Recovery = append([]string{}, u.Recovery...)
		cp.AppPw = append([]AppPass{}, u.AppPw...)
		if !o.PW {
			cp.Hash = ""
		}
		if !o.Secrets {
			cp.TOTP = ""
			cp.Recovery = nil
			cp.AppPw = nil
		}
		if !admin && n != me { // Webhook-Adresse enthaelt ein Token: nur Besitzer oder globaler Admin (S-09)
			cp.Chat = ""
		}
		users[n] = cp
	}

	groups := map[string]Group{}
	if admin {
		for n, g := range a.groups {
			if len(wantG) > 0 && !wantG[n] {
				continue
			}
			groups[n] = g
		}
	}
	return BulkDump{Version: 1, Users: users, Groups: groups}
}

// BulkImportOpts steuert den Import.
type BulkImportOpts struct {
	Create bool // unbekannte Gruppen anlegen (nur globaler Admin)
	Update bool // vorhandene Benutzer/Gruppen ueberschreiben
	GenPW  bool // fehlt ein Passwort-Hash: Zufalls-Startpasswort statt Fehler
}

// BulkImportResult: wie ImportResult, zusaetzlich die bei GenPW neu vergebenen Startpasswoerter (nur zur Anzeige/
// Weitergabe an die Benutzer, werden nirgends gespeichert).
type BulkImportResult struct {
	Created int               `json:"created"`
	Updated int               `json:"updated"`
	Errors  []string          `json:"errors"`
	GenPW   map[string]string `json:"genpw,omitempty"`
}

// ImportBulk: vollstaendiger Import eines BulkDump. Nur globaler Admin, weil ein BulkDump Gruppenrechte und
// Datenschutz-relevante Felder (Ack) jedes Benutzers setzen kann, nicht nur Mitgliedschaften wie beim CSV-Import.
func (a *Auth) ImportBulk(ctx context.Context, d BulkDump, o BulkImportOpts) BulkImportResult {
	res := BulkImportResult{Errors: []string{}}
	if !IsAdmin(ctx) {
		res.Errors = append(res.Errors, "admin only")
		return res
	}
	a.refresh(ctx)

	if len(d.Groups) > 0 {
		if err := a.mutateGroups(ctx, func(m map[string]Group) error {
			for n, g := range d.Groups {
				if !okName(n) {
					res.Errors = append(res.Errors, fmt.Sprintf("group %s: %v", n, ErrBadName))
					continue
				}
				if _, ok := m[n]; ok && !o.Update {
					continue // vorhandene Gruppe ohne Update-Option unberuehrt lassen
				}
				if _, ok := m[n]; !ok && !o.Create {
					res.Errors = append(res.Errors, fmt.Sprintf("group %s: unknown (create not set)", n))
					continue
				}
				m[n] = g
			}
			return nil
		}); err != nil {
			res.Errors = append(res.Errors, "groups: "+err.Error())
			return res
		}
	}

	names := make([]string, 0, len(d.Users))
	for n := range d.Users {
		names = append(names, n)
	}
	sort.Strings(names)

	now := time.Now().UTC().Format(time.RFC3339)
	err := a.mutate(ctx, func(m map[string]Account) error {
		res.Created, res.Updated = 0, 0
		for _, n := range names {
			u := d.Users[n]
			if !okName(n) {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", n, ErrBadName))
				continue
			}
			ex, had := m[n]
			switch {
			case had && !o.Update:
				res.Errors = append(res.Errors, fmt.Sprintf("%s: exists", n))
			case had && ex.Sys && u.Hash != "" && n != User(ctx):
				res.Errors = append(res.Errors, fmt.Sprintf("%s: the sysadmin password can only be changed by the sysadmin", n))
			case had:
				nh, must := ex.Hash, ex.Must
				if u.Hash != "" {
					nh, must = u.Hash, true
				} else if o.GenPW {
					pw := genPassword()
					h, err := hash(pw)
					if err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", n, err))
						continue
					}
					nh, must = h, true
					if res.GenPW == nil {
						res.GenPW = map[string]string{}
					}
					res.GenPW[n] = pw
				}
				u.Hash, u.Must = nh, must
				if u.Groups == nil {
					u.Groups = ex.Groups
				}
				m[n] = u
				res.Updated++
			default:
				nh := u.Hash
				if nh == "" {
					if !o.GenPW {
						res.Errors = append(res.Errors, fmt.Sprintf("%s: password required for new user", n))
						continue
					}
					pw := genPassword()
					h, err := hash(pw)
					if err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", n, err))
						continue
					}
					nh = h
					if res.GenPW == nil {
						res.GenPW = map[string]string{}
					}
					res.GenPW[n] = pw
				}
				u.Hash = nh
				u.Must = true
				u.Created = now
				m[n] = u
				res.Created++
			}
		}
		return nil
	})
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
	}
	return res
}

func setOf(l []string) map[string]bool {
	if len(l) == 0 {
		return nil
	}
	m := make(map[string]bool, len(l))
	for _, x := range l {
		m[x] = true
	}
	return m
}

// genPassword: zufaelliges Startpasswort (16 Zeichen, Gross/Klein/Ziffer), besteht minPass/maxPass und weakPass.
func genPassword() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	out := make([]byte, 16)
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// kryptographischer Zufall nicht verfuegbar: Zeitstempel als Notbehelf, damit der Import nicht abbricht
		for i := range buf {
			buf[i] = byte(time.Now().UnixNano() >> (i % 8))
		}
	}
	for i, c := range buf {
		out[i] = alphabet[int(c)%len(alphabet)]
	}
	return string(out)
}
