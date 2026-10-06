package auth

// Zwischenspeicher (Phase 3): Identity.CacheDays > 0 hält einen bcrypt-Abdruck des Verzeichnispassworts und den
// Zeitpunkt der letzten erfolgreichen Prüfung im Spiegelkonto. Fällt das Verzeichnis aus, gilt dieser Abdruck so lange.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cs-team/store"
)

// offlineSetup: Anwendung mit festem Verzeichnis und einstellbarem Zwischenspeicher.
func offlineSetup(t *testing.T, days int, dir *fakeDir) (*Auth, http.Handler, *putCount) {
	t.Helper()
	ForceChange = false
	st := &putCount{Store: store.NewMem()}
	a := New(st)
	a.SetDirChecker(dir)
	a.SetIdentitySource(fixedIdentity{Realm: "local.de", LocalGroup: "teaching", CacheDays: days})
	h := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(User(r.Context()))) }))
	return a, h, st
}

// clearLogins: den kurzen Anmelde-Zwischenspeicher (authTTL) leeren, damit wieder geprüft wird.
func clearLogins(a *Auth) {
	a.mu.Lock()
	a.cache = map[[32]byte]cacheEnt{}
	a.mu.Unlock()
}

func TestDirCacheOffline(t *testing.T) {
	dir := &fakeDir{users: map[string]string{"anna": "geheim123"},
		info: map[string]DirUser{"anna": {Groups: []string{"lehrer"}}}}
	a, h, st := offlineSetup(t, 14, dir)
	ctx := context.Background()
	if code, body := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.7"); code != 200 || body != "anna@local.de" {
		t.Fatalf("Anmeldung: %d %q", code, body)
	}
	u, ok := a.get(ctx, "anna@local.de")
	if !ok || u.Hash == "" || u.Hash == "!" || u.DirSeen == "" {
		t.Fatalf("kein Zwischenspeicher angelegt: %+v", u)
	}
	puts := st.puts
	// das Verzeichnis fällt aus: die Anmeldung läuft aus dem Zwischenspeicher, ohne den Speicher zu schreiben
	dir.err = ErrDirDown
	clearLogins(a)
	if code, body := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.7"); code != 200 || body != "anna@local.de" {
		t.Fatalf("Anmeldung aus dem Zwischenspeicher: %d %q", code, body)
	}
	if st.puts != puts {
		t.Fatalf("Schreibzugriffe beim Zwischenspeicher-Treffer: %d statt %d", st.puts, puts)
	}
	// falsches Passwort bleibt "Verzeichnis nicht erreichbar" (kein falsches Passwort behaupten)
	clearLogins(a)
	if code, _ := wrapCall(h, "anna@local.de", "falsch123", "192.0.2.7"); code != 503 {
		t.Fatalf("falsches Passwort ohne Verzeichnis: %d", code)
	}
	// unbekanntes Konto: kein Zwischenspeicher vorhanden
	clearLogins(a)
	if code, _ := wrapCall(h, "bert@local.de", "geheim123", "192.0.2.7"); code != 503 {
		t.Fatalf("unbekanntes Konto ohne Verzeichnis: %d", code)
	}
}

func TestDirCacheExpiry(t *testing.T) {
	ctx := context.Background()
	dir := &fakeDir{users: map[string]string{"anna": "geheim123"}}
	a, h, _ := offlineSetup(t, 14, dir)
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.8"); code != 200 {
		t.Fatalf("Anmeldung: %d", code)
	}
	seenAgo := func(d time.Duration) {
		err := a.mutate(ctx, func(m map[string]Account) error {
			u := m["anna@local.de"]
			u.DirSeen = time.Now().Add(-d).UTC().Format(time.RFC3339)
			m["anna@local.de"] = u
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		clearLogins(a)
	}
	dir.err = ErrDirDown
	seenAgo(13 * 24 * time.Hour) // innerhalb der Frist
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.8"); code != 200 {
		t.Fatalf("innerhalb der Frist: %d", code)
	}
	seenAgo(15 * 24 * time.Hour) // abgelaufen
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.8"); code != 503 {
		t.Fatalf("abgelaufener Zwischenspeicher: %d", code)
	}
}

func TestDirCacheOff(t *testing.T) {
	ctx := context.Background()
	dir := &fakeDir{users: map[string]string{"anna": "geheim123"}}
	a, h, _ := offlineSetup(t, 0, dir)
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.9"); code != 200 {
		t.Fatalf("Anmeldung: %d", code)
	}
	if u, _ := a.get(ctx, "anna@local.de"); u.Hash != "!" || u.DirSeen != "" {
		t.Fatalf("ohne Zwischenspeicher wird keiner angelegt: %+v", u)
	}
	dir.err = ErrDirDown
	clearLogins(a)
	if code, _ := wrapCall(h, "anna@local.de", "geheim123", "192.0.2.9"); code != 503 {
		t.Fatalf("abgeschalteter Zwischenspeicher: %d", code)
	}
}

func TestDirCachePasswordChange(t *testing.T) {
	dir := &fakeDir{users: map[string]string{"anna": "altpasswort"}}
	a, h, _ := offlineSetup(t, 14, dir)
	if code, _ := wrapCall(h, "anna@local.de", "altpasswort", "192.0.2.10"); code != 200 {
		t.Fatalf("Anmeldung: %d", code)
	}
	dir.users["anna"] = "neupasswort" // Passwort im Verzeichnis geändert
	clearLogins(a)
	if code, _ := wrapCall(h, "anna@local.de", "neupasswort", "192.0.2.10"); code != 200 {
		t.Fatalf("neues Passwort: %d", code)
	}
	dir.err = ErrDirDown
	clearLogins(a)
	if code, _ := wrapCall(h, "anna@local.de", "neupasswort", "192.0.2.10"); code != 200 {
		t.Fatalf("Zwischenspeicher nach Passwortänderung: %d", code)
	}
	clearLogins(a)
	if code, _ := wrapCall(h, "anna@local.de", "altpasswort", "192.0.2.10"); code != 503 {
		t.Fatalf("alter Abdruck gilt noch: %d", code)
	}
}

func TestDirCacheDrop(t *testing.T) {
	ctx := context.Background()
	a := New(store.NewMem())
	now := time.Now().UTC().Format(time.RFC3339)
	err := a.mutate(ctx, func(m map[string]Account) error {
		h := "$2a$10$0123456789012345678901234567890123456789012345678901"
		m["anna@local.de"] = Account{Hash: h, Realm: "local.de", Source: "dir", DirSeen: now, Groups: []string{DefaultGroup}}
		m["bert@alt.de"] = Account{Hash: h, Realm: "alt.de", Source: "dir", DirSeen: now, Groups: []string{DefaultGroup}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a.dropDirCache(ctx, Identity{Realm: "local.de", CacheDays: 14})
	if u, _ := a.get(ctx, "anna@local.de"); u.Hash == "!" || u.DirSeen == "" {
		t.Fatalf("Zwischenspeicher unnötig entfernt: %+v", u)
	}
	if u, _ := a.get(ctx, "bert@alt.de"); u.Hash != "!" || u.DirSeen != "" {
		t.Fatalf("anderer Namensraum nicht bereinigt: %+v", u)
	}
	a.dropDirCache(ctx, Identity{Realm: "local.de", CacheDays: 0})
	if u, _ := a.get(ctx, "anna@local.de"); u.Hash != "!" || u.DirSeen != "" {
		t.Fatalf("abgeschalteter Zwischenspeicher nicht bereinigt: %+v", u)
	}
}

func TestDirCacheValues(t *testing.T) {
	now := time.Now()
	h, seen := dirCache(Account{}, "geheim123", now)
	if h == "" || h == "!" || seen != now.UTC().Format(time.RFC3339) {
		t.Fatalf("neuer Abdruck: %q %q", h, seen)
	}
	if h2, s2 := dirCache(Account{Hash: h, DirSeen: seen}, "geheim123", now.Add(time.Hour)); h2 != h || s2 != seen {
		t.Fatalf("unverändertes Passwort: %q %q", h2, s2)
	}
	if _, s3 := dirCache(Account{Hash: h, DirSeen: seen}, "geheim123", now.Add(25*time.Hour)); s3 == seen {
		t.Fatal("Zeitpunkt nicht fortgeschrieben")
	}
	if h4, s4 := dirCache(Account{Hash: h, DirSeen: seen}, "neupasswort", now.Add(time.Hour)); h4 == h || s4 == seen {
		t.Fatalf("geändertes Passwort: %q %q", h4, s4)
	}
	if h5, s5 := dirCache(Account{}, strings.Repeat("x", 100), now); h5 != "!" || s5 != "" {
		t.Fatalf("zu langes Passwort: %q %q", h5, s5)
	}
	cur := Account{Source: "dir", Realm: "local.de", Groups: []string{"g"}, Hash: "!", Mail: "m"}
	if dirStale(cur, "local.de", "g", "m", "!", "", nil) {
		t.Fatal("unverändertes Konto gilt als veraltet")
	}
	cur.Mail = "alt"
	if !dirStale(cur, "local.de", "g", "m", "!", "", nil) {
		t.Fatal("geänderte E-Mail wird nicht erkannt")
	}
	if !dirStale(cur, "local.de", "g", "m", "x", "", nil) { // geänderter Zwischenspeicher
		t.Fatal("geänderter Abdruck wird nicht erkannt")
	}
	// Verzeichnisgruppen des Kontos (0.55): geänderte Liste -> Konto neu schreiben
	cur.Mail = "m"
	cur.DirGroups = nil
	if !dirStale(cur, "local.de", "g", "m", "!", "", []string{"lehrer"}) {
		t.Fatal("fehlende Verzeichnisgruppen werden nicht erkannt")
	}
	cur.DirGroups = []string{"lehrer"}
	if dirStale(cur, "local.de", "g", "m", "!", "", []string{"lehrer"}) {
		t.Fatal("unveränderte Verzeichnisgruppen gelten als veraltet")
	}
	if !dirStale(cur, "local.de", "g", "m", "!", "", []string{"lehrer", "schueler"}) {
		t.Fatal("geänderte Verzeichnisgruppen werden nicht erkannt")
	}
}
