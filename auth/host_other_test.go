//go:build !windows

package auth

import (
	"context"
	"errors"
	"testing"
)

// TestHostCheckOther: außerhalb von Windows gibt es die Anmeldung über das Betriebssystem nicht - ohne LDAP-Quelle
// gilt das Verzeichnis als nicht erreichbar (keine Anmeldung, aber auch kein falsches Passwort).
func TestHostCheckOther(t *testing.T) {
	if _, err := hostCheck(context.Background(), "local.de", "anna", "geheim123"); !errors.Is(err, ErrDirDown) {
		t.Fatalf("hostCheck: %v", err)
	}
}
