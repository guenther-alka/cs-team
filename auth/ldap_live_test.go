package auth

// Live-Tests gegen ein echtes Verzeichnis. Sie laufen nur, wenn die Umgebung gesetzt ist:
//
//	CS_TEST_DIR_URL=ldap://192.168.2.124:389
//	CS_TEST_DIR_BASE=DC=local,DC=de
//	CS_TEST_DIR_REALM=local.de
//	CS_TEST_DIR_USER=anna   CS_TEST_DIR_PASS=...
//	CS_TEST_DIR_GROUPS=lehrer        (Gruppen im Verzeichnis, die zur Anmeldung berechtigen)
//	CS_TEST_DIR_LOCALGROUP=teaching  (cs-team-Gruppe der Verzeichnisbenutzer)
//	CS_TEST_DIR_BINDDN=... CS_TEST_DIR_BINDPW=... CS_TEST_DIR_STARTTLS=1   (optional)
//
// Beispiel (PowerShell):
//
//	$env:CS_TEST_DIR_URL='ldap://192.168.2.124:389'; $env:CS_TEST_DIR_BASE='DC=local,DC=de'; ...
//	go test ./auth/ -run TestLive -v

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"cs-team/store"
)

func liveCheck(t *testing.T) ldapCheck {
	t.Helper()
	if os.Getenv("CS_TEST_DIR_URL") == "" || os.Getenv("CS_TEST_DIR_USER") == "" || os.Getenv("CS_TEST_DIR_PASS") == "" {
		t.Skip("CS_TEST_DIR_URL/USER/PASS nicht gesetzt")
	}
	return ldapCheck{url: os.Getenv("CS_TEST_DIR_URL"), base: os.Getenv("CS_TEST_DIR_BASE"),
		realm: os.Getenv("CS_TEST_DIR_REALM"), bindDN: os.Getenv("CS_TEST_DIR_BINDDN"),
		bindPW: os.Getenv("CS_TEST_DIR_BINDPW"), startTLS: os.Getenv("CS_TEST_DIR_STARTTLS") == "1"}
}

// TestLiveDirCheck: richtige Zugangsdaten liefern den Eintrag samt Gruppen, falsche ein falsches Passwort.
func TestLiveDirCheck(t *testing.T) {
	c := liveCheck(t)
	user, pass := os.Getenv("CS_TEST_DIR_USER"), os.Getenv("CS_TEST_DIR_PASS")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	du, err := c.CheckDir(ctx, user, pass)
	if err != nil {
		t.Fatalf("Anmeldung: %v", err)
	}
	t.Logf("Verzeichnis: Name=%q DN=%q Mail=%q Gruppen=%v", du.Name, du.DN, du.Mail, du.Groups)
	if du.Name == "" {
		t.Fatal("kein Name")
	}
	for _, g := range strings.Split(os.Getenv("CS_TEST_DIR_GROUPS"), ",") {
		if g = strings.TrimSpace(g); g != "" && !admitted(Identity{AdmitGroups: []string{g}}, du) {
			t.Fatalf("Gruppe %s fehlt in %v", g, du.Groups)
		}
	}
	if _, err := c.CheckDir(ctx, user, pass+"x"); !errors.Is(err, ErrBadDir) {
		t.Fatalf("falsches Passwort: %v", err)
	}
	if _, err := c.CheckDir(ctx, "gibtsnicht12345", "irgendwas123"); !errors.Is(err, ErrBadDir) {
		t.Fatalf("unbekannter Benutzer: %v", err)
	}
}

// TestLiveDirEndToEnd: vollständige Anmeldung "name@realm" über die Anwendung (Prüfung, Aufnahme, Spiegelkonto).
func TestLiveDirEndToEnd(t *testing.T) {
	c := liveCheck(t)
	ForceChange = false
	user, pass := os.Getenv("CS_TEST_DIR_USER"), os.Getenv("CS_TEST_DIR_PASS")
	realm := strings.ToLower(c.realm)
	var admit []string
	for _, g := range strings.Split(os.Getenv("CS_TEST_DIR_GROUPS"), ",") {
		if g = strings.TrimSpace(g); g != "" {
			admit = append(admit, g)
		}
	}
	ctx := context.Background()
	a := New(store.NewMem())
	a.SetIdentitySource(fixedIdentity{Mode: "dir", Realm: realm, URL: c.url, Base: c.base, BindDN: c.bindDN,
		BindPW: c.bindPW, StartTLS: c.startTLS, AdmitGroups: admit, LocalGroup: os.Getenv("CS_TEST_DIR_LOCALGROUP")})
	h := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(User(r.Context()))) }))
	key := user + "@" + realm
	if code, body := wrapCall(h, key, pass, "192.0.2.77"); code != 200 || body != key {
		t.Fatalf("Anmeldung %s: %d %q", key, code, body)
	}
	u, ok := a.get(ctx, key)
	if !ok || u.Source != "dir" || u.Hash != "!" || u.Realm != realm {
		t.Fatalf("Spiegelkonto: %+v", u)
	}
	want := a.identity().Group()
	if len(u.Groups) != 1 || u.Groups[0] != want {
		t.Fatalf("Gruppen: %v (erwartet %s)", u.Groups, want)
	}
	// lokale Anmeldung ist bei Mode "dir" abgeschaltet, unbekannte Namensräume werden abgelehnt
	if code, _ := wrapCall(h, user, pass, "192.0.2.77"); code != 403 {
		t.Fatalf("lokale Anmeldung: %d", code)
	}
	if code, _ := wrapCall(h, user+"@fremd.example", pass, "192.0.2.77"); code != 401 {
		t.Fatalf("fremder Namensraum: %d", code)
	}
	if code, _ := wrapCall(h, key, pass+"x", "192.0.2.77"); code != 401 {
		t.Fatalf("falsches Passwort: %d", code)
	}
}
