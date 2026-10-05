package auth

import (
	"testing"

	"cs-team/store"
)

// TestDirCheckSource: ohne LDAP-Adresse prüft das Betriebssystem (dirDefault, mit dem Namensraum als Domäne);
// mit Adresse prüft LDAP; eine fest gesetzte Prüfung (Tests) hat immer Vorrang.
func TestDirCheckSource(t *testing.T) {
	a := New(store.NewMem())
	a.SetIdentitySource(fixedIdentity{Realm: "Local.DE"})
	d, ok := a.dirCheck().(dirDefault)
	if !ok || d.realm != "local.de" {
		t.Fatalf("ohne LDAP-Adresse: %#v", a.dirCheck())
	}
	a.SetIdentitySource(fixedIdentity{Realm: "local.de", URL: "ldaps://dc.schule.de:636"})
	if _, ok := a.dirCheck().(ldapCheck); !ok {
		t.Fatalf("mit LDAP-Adresse muss LDAP prüfen: %#v", a.dirCheck())
	}
	fake := &fakeDir{}
	a.SetDirChecker(fake)
	if a.dirCheck() != DirChecker(fake) {
		t.Fatal("fest gesetzte Prüfung hat Vorrang")
	}
}
