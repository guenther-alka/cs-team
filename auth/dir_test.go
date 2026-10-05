package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	ldap "github.com/go-ldap/ldap/v3"

	"cs-team/store"
)

func TestDirHelpers(t *testing.T) {
	if got := ntlmDomains("local.de"); strings.Join(got, ",") != "local.de,local,LOCAL" {
		t.Fatalf("ntlmDomains: %v", got)
	}
	if got := ntlmDomains(""); len(got) != 1 || got[0] != "" {
		t.Fatalf("ntlmDomains ohne Namensraum: %v", got)
	}
	if got := ntlmDomains("LOCAL"); len(got) != 1 || got[0] != "LOCAL" {
		t.Fatalf("ntlmDomains Großschreibung: %v", got)
	}
	if got := bindNames("local.de", "anna"); strings.Join(got, ",") != `anna@local.de,LOCAL\anna,anna` {
		t.Fatalf("bindNames: %v", got)
	}
	if got := bindNames("local.de", "anna@local.de"); len(got) != 1 || got[0] != "anna@local.de" {
		t.Fatalf("bindNames mit Namensraum: %v", got)
	}
	if got := userFilters("a*b"); !strings.Contains(got[0], `sAMAccountName=a\2ab`) || strings.Contains(got[0], "*") {
		t.Fatalf("Filter nicht maskiert: %v", got)
	}
	if got := hostOnly("ldap://192.168.2.124:389"); got != "192.168.2.124:389" {
		t.Fatalf("hostOnly: %q", got)
	}
	if got := hostOnly("kaputt"); got != "kaputt" {
		t.Fatalf("hostOnly ohne Adresse: %q", got)
	}
	for _, c := range []struct {
		c ldapCheck
		s bool
	}{
		{ldapCheck{url: "ldaps://dc:636"}, true},
		{ldapCheck{url: "ldap://dc:389", startTLS: true}, true},
		{ldapCheck{url: "ldap://dc:389"}, false},
	} {
		if ldapSecure(c.c) != c.s {
			t.Fatalf("ldapSecure %+v: %v", c.c, ldapSecure(c.c))
		}
	}
	if firstOf("", " ", "x", "y") != "x" || firstOf("", "") != "" {
		t.Fatal("firstOf")
	}
	if got := dedup([]string{"b", "", "a", "b", " c "}); strings.Join(got, ",") != "b,a,c" {
		t.Fatalf("dedup: %v", got)
	}
}

func TestDirErrMapping(t *testing.T) {
	if dirErr(nil) != nil {
		t.Fatal("kein Fehler")
	}
	if !errors.Is(dirErr(&ldap.Error{ResultCode: ldap.LDAPResultInvalidCredentials}), ErrBadDir) {
		t.Fatal("Code 49 muss ein falsches Passwort sein")
	}
	if !errors.Is(dirErr(&ldap.Error{ResultCode: ldap.LDAPResultTimeLimitExceeded}), ErrDirDown) {
		t.Fatal("andere Codes sind Störungen")
	}
	if !errors.Is(dirErr(context.DeadlineExceeded), ErrDirDown) || !errors.Is(dirErr(errors.New("Netz weg")), ErrDirDown) {
		t.Fatal("Störungen müssen ErrDirDown sein")
	}
	e := down(errors.New("connection refused"))
	if !errors.Is(e, ErrDirDown) || !strings.Contains(e.Error(), "connection refused") {
		t.Fatalf("down: %v", e)
	}
	if !errors.Is(down(nil), ErrDirDown) {
		t.Fatal("down ohne Ursache")
	}
}

func TestLdapFromAndDirCheck(t *testing.T) {
	if ldapFrom(Identity{}) != nil {
		t.Fatal("ohne Adresse keine LDAP-Prüfung")
	}
	c, ok := ldapFrom(Identity{URL: " ldap://dc:389 ", Base: "DC=local,DC=de", Realm: "Local.DE", StartTLS: true}).(ldapCheck)
	if !ok || c.url != "ldap://dc:389" || c.realm != "local.de" || !c.startTLS {
		t.Fatalf("ldapFrom: %+v", c)
	}
	a := New(store.NewMem())
	if _, ok := a.dirCheck().(dirDefault); !ok {
		t.Fatalf("ohne Einrichtung: %T", a.dirCheck())
	}
	a.SetIdentitySource(fixedIdentity{Realm: "local.de", URL: "ldap://dc:389"})
	if _, ok := a.dirCheck().(ldapCheck); !ok {
		t.Fatalf("mit Adresse: %T", a.dirCheck())
	}
	fake := &fakeDir{}
	a.SetDirChecker(fake) // feste Prüfung (Tests) hat Vorrang
	if a.dirCheck() != DirChecker(fake) {
		t.Fatal("feste Prüfung hat keinen Vorrang")
	}
	// unverschlüsselt: ldap:// ohne StartTLS
	if !(Identity{URL: "ldap://dc:389"}).Unencrypted() || (Identity{URL: "ldap://dc:389", StartTLS: true}).Unencrypted() {
		t.Fatal("Unencrypted")
	}
}

// TestDirDown: nicht erreichbare Adresse -> ErrDirDown (kein Passwortfehler).
func TestDirDown(t *testing.T) {
	c := ldapCheck{url: "ldap://127.0.0.1:1", base: "DC=local,DC=de", realm: "local.de"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := c.CheckDir(ctx, "anna", "geheim123"); !errors.Is(err, ErrDirDown) {
		t.Fatalf("nicht erreichbar: %v", err)
	}
	// leeres Passwort wird ohne Verbindungsaufbau abgelehnt
	if _, err := c.CheckDir(ctx, "anna", ""); !errors.Is(err, ErrBadDir) {
		t.Fatalf("leeres Passwort: %v", err)
	}
	if _, err := c.CheckDir(ctx, "  ", "geheim123"); !errors.Is(err, ErrBadDir) {
		t.Fatalf("leerer Name: %v", err)
	}
}

func TestDirAddr(t *testing.T) {
	for _, c := range []struct {
		in     string
		host   string
		secure bool
		bad    bool
	}{
		{"ldap://192.168.2.124:389", "192.168.2.124:389", false, false},
		{"ldap://192.168.2.124", "192.168.2.124:389", false, false},
		{" ldap://dc:1389 ", "dc:1389", false, false},
		{"ldaps://dc", "dc:636", true, false},
		{"LDAPS://dc:1636", "dc:1636", true, false},
		{"ldap://dc/", "dc:389", false, false},
		{"http://dc:389", "", false, true},
		{"ldap://", "", false, true},
		{"", "", false, true},
	} {
		host, secure, err := dirAddr(c.in)
		if c.bad {
			if err == nil {
				t.Fatalf("%q: erwartet Fehler", c.in)
			}
			continue
		}
		if err != nil || host != c.host || secure != c.secure {
			t.Fatalf("%q: %q %v %v", c.in, host, secure, err)
		}
	}
}

// TestWarmDir: das Warmhalten läuft nur mit einer eingerichteten LDAP-Prüfung und lässt sich beenden.
func TestWarmDir(t *testing.T) {
	ForceChange = false
	a := New(store.NewMem())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.SetIdentitySource(fixedIdentity{Realm: "local.de", URL: "ldap://127.0.0.1:1"})
	a.SetDirChecker(&fakeDir{}) // feste Prüfung: es wird nicht wirklich verbunden
	a.WarmDir(ctx)
	a.WarmDir(ctx) // beendet den ersten Lauf und startet neu
	a.mu.Lock()
	running := a.warm != nil
	a.mu.Unlock()
	if !running {
		t.Fatal("Warmhalten läuft nicht")
	}
}
