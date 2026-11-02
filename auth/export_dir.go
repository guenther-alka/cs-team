package auth

import (
	"context"
	"sort"
)

// Verzeichnis fuer den LDAP-Dienst (ldapd, docs/HA_SPEC.md Abschnitt 3): nur lokale Konten mit uid, nur Gruppen mit
// gesetztem Export-Schalter, Mitglieder bereits aufgeloest und flach (Untergruppen, Organisationen, Verzeichnisgruppen).

type ExpUser struct {
	Name string
	Uid  int
	Gid  int // Hauptgruppe: die Standardgruppe (alluser)
	Mail string
}

type ExpGroup struct {
	Name    string
	Gid     int
	Members []string
}

// ExportData liefert die exportierten Konten und Gruppen, nach Name sortiert.
func (a *Auth) ExportData(ctx context.Context) ([]ExpUser, []ExpGroup) {
	a.refresh(ctx)
	a.mu.Lock()
	us, gs := a.users, a.groups
	a.mu.Unlock()
	prim := 0
	if g, ok := gs[DefaultGroup]; ok {
		prim = g.Gid
	}
	var users []ExpUser
	member := map[string][]string{} // Gruppe -> Mitglieder (nur exportierte Konten)
	for n, u := range us {
		if u.Disabled || u.Source == "dir" || u.Uid <= 0 || !validName.MatchString(n) {
			continue
		}
		users = append(users, ExpUser{Name: n, Uid: u.Uid, Gid: prim, Mail: u.Mail})
		for _, g := range effGroups(u) {
			member[g] = append(member[g], n)
		}
	}
	var groups []ExpGroup
	for n, g := range gs {
		if !g.Export || sysGroup(n) || g.Gid <= 0 {
			continue
		}
		m := append([]string{}, member[n]...)
		sort.Strings(m)
		groups = append(groups, ExpGroup{Name: n, Gid: g.Gid, Members: m})
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return users, groups
}

// LDAPBind prueft eine LDAP-Anmeldung (einfache Bindung mit Passwort) eines lokalen Kontos. Nicht erlaubt sind Konten mit
// Zwei-Faktor-Anmeldung (das Passwort allein waere kein zweiter Faktor), Verzeichnis-Spiegelkonten und Konten mit
// OIDC-Verknuepfung. Fehlversuche zaehlen wie bei der Web-Anmeldung (Sperre je Konto und je Adresse).
func (a *Auth) LDAPBind(ctx context.Context, name, pass, ip string) bool {
	uk, ik := "ldap:"+name, "ldap-ip:"+ip
	if a.locked(uk) || a.locked(ik) || pass == "" {
		return false
	}
	u, ok := a.verify(ctx, name, pass)
	if ok && (u.TOTP != "" || u.Link != nil || u.Hash == "") {
		ok = false
	}
	if !ok {
		a.failed(uk)
		a.failed(ik)
		return false
	}
	a.ok(uk)
	a.ok(ik)
	return true
}
