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

// DirChecker: prüft Anmeldedaten gegen eine Verzeichnisquelle. Umsetzungen: hostCheck (Betriebssystem, Phase 4)
// und LDAP/AD (ldap.go).
type DirChecker interface {
	CheckDir(ctx context.Context, user, pass string) (DirUser, error)
}

// dirDefault: Prüfung über das Betriebssystem des Servers, wenn keine LDAP-Quelle eingerichtet ist.
type dirDefault struct{}

func (dirDefault) CheckDir(ctx context.Context, user, pass string) (DirUser, error) {
	return hostCheck(ctx, user, pass)
}

// hostCheck: Anmeldung über das Betriebssystem (Phase 4: LogonUser unter Windows). Solange das nicht eingerichtet
// ist, gilt die Quelle als nicht verfügbar - Anmeldungen über das Verzeichnis sind dann nicht möglich.
func hostCheck(ctx context.Context, user, pass string) (DirUser, error) {
	return DirUser{}, ErrDirDown
}

// dirCheck: die für die aktuelle Einstellung passende Prüfung. Eine fest gesetzte Prüfung (Tests) hat Vorrang, sonst
// prüft LDAP, wenn eine Adresse eingerichtet ist, sonst die Quelle selbst (derzeit: das Betriebssystem).
func (a *Auth) dirCheck() DirChecker {
	a.mu.Lock()
	d := a.dir
	a.mu.Unlock()
	if d != nil {
		return d
	}
	if c := ldapFrom(a.identity()); c != nil {
		return c
	}
	return dirDefault{}
}
