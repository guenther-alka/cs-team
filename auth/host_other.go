//go:build !windows

package auth

import "context"

// hostCheck: Anmeldung über das Betriebssystem. Ohne LDAP-Quelle sind Verzeichnisanmeldungen dort nicht möglich
// (Phase 4 setzt LogonUser um, das es nur unter Windows gibt).
func hostCheck(ctx context.Context, realm, user, pass string) (DirUser, error) {
	return DirUser{}, ErrDirDown
}
