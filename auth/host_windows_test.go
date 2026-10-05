//go:build windows

package auth

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestHostLogonError: Windows-Fehler werden richtig übersetzt. Falsche oder gesperrte Konten führen zu ErrBadDir
// (keine Anmeldung, kein Zwischenspeicher), technische Störungen zu ErrDirDown.
func TestHostLogonError(t *testing.T) {
	for _, c := range []struct {
		err  error
		want error
	}{
		{windows.ERROR_LOGON_FAILURE, ErrBadDir},
		{windows.ERROR_NO_SUCH_USER, ErrBadDir},
		{windows.ERROR_ACCOUNT_DISABLED, ErrBadDir},
		{windows.ERROR_ACCOUNT_LOCKED_OUT, ErrBadDir},
		{windows.ERROR_PASSWORD_EXPIRED, ErrBadDir},
		{windows.ERROR_PASSWORD_MUST_CHANGE, ErrBadDir},
		{windows.ERROR_NO_SUCH_DOMAIN, ErrDirDown},
		{windows.ERROR_LOGON_TYPE_NOT_GRANTED, ErrDirDown},
		{syscall.Errno(5), ErrDirDown},
	} {
		if got := logonError(c.err, "anna", "local.de"); !errors.Is(got, c.want) {
			t.Errorf("%v: %v statt %v", c.err, got, c.want)
		}
	}
	if !logonRefused(windows.ERROR_LOGON_TYPE_NOT_GRANTED) || logonRefused(windows.ERROR_LOGON_FAILURE) {
		t.Fatal("logonRefused")
	}
}

// TestHostCheckBadCredentials: unbekannter Benutzer und falsches Passwort werden abgelehnt. ErrBadDir ist das
// erwartete Ergebnis; Rechner, deren Richtlinie die Netzwerkanmeldung generell verbietet, melden ErrDirDown.
// Eine Anmeldung wäre in jedem Fall falsch.
func TestHostCheckBadCredentials(t *testing.T) {
	ctx := context.Background()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, realm := range []string{"", host} {
		_, err := hostCheck(ctx, realm, "gibtsnicht98765", "falsch123")
		if !errors.Is(err, ErrBadDir) && !errors.Is(err, ErrDirDown) {
			t.Fatalf("realm %q: %v", realm, err)
		}
		if errors.Is(err, ErrDirDown) {
			t.Logf("realm %q: %v (Netzwerkanmeldung für lokale Konten nicht erlaubt)", realm, err)
		}
	}
	if _, err := hostCheck(ctx, "local.de", "anna", ""); !errors.Is(err, ErrBadDir) {
		t.Fatalf("leeres Passwort: %v", err)
	}
	if _, err := hostCheck(ctx, "local.de", "", "geheim123"); !errors.Is(err, ErrBadDir) {
		t.Fatalf("leerer Benutzer: %v", err)
	}
}
