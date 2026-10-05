package auth

import (
	"context"
	"errors"
)

// DirUser: Ergebnis einer Anmeldung gegen eine Verzeichnisquelle (Betriebssystem oder LDAP).
type DirUser struct {
	Name   string   // Anmeldename im Verzeichnis
	DN     string   // vollständiger Verzeichnisname (LDAP), sonst leer
	Mail   string   // E-Mail, falls das Verzeichnis sie liefert
	Groups []string // Gruppen (Kurzname oder DN) - für die Aufnahme-Prüfung
}

var (
	// ErrBadDir: Anmeldename oder Passwort ist falsch.
	ErrBadDir = errors.New("directory: wrong credentials")
	// ErrDirDown: das Verzeichnis ist nicht erreichbar (Anmeldung derzeit nicht möglich).
	ErrDirDown = errors.New("directory unreachable")
)

// DirChecker: prüft Anmeldedaten gegen eine Verzeichnisquelle. Umsetzungen: das Betriebssystem des Servers
// (hostCheck, siehe host_windows.go/host_other.go) und LDAP/AD (ldap.go).
type DirChecker interface {
	CheckDir(ctx context.Context, user, pass string) (DirUser, error)
}

// dirDefault: Prüfung über das Betriebssystem des Servers, wenn keine LDAP-Quelle eingerichtet ist.
type dirDefault struct {
	realm string // Namensraum = Domäne (leer: nur die lokale Kontendatenbank)
}

func (d dirDefault) CheckDir(ctx context.Context, user, pass string) (DirUser, error) {
	return hostCheck(ctx, d.realm, user, pass)
}

// dirCheck: die für die aktuelle Einstellung passende Prüfung. Eine fest gesetzte Prüfung (Tests) hat Vorrang, sonst
// prüft LDAP, wenn eine Adresse eingerichtet ist, sonst die Quelle selbst (das Betriebssystem).
func (a *Auth) dirCheck() DirChecker {
	a.mu.Lock()
	d := a.dir
	a.mu.Unlock()
	if d != nil {
		return d
	}
	id := a.identity()
	if c := ldapFrom(id); c != nil {
		return c
	}
	return dirDefault{realm: id.DirRealm()}
}
