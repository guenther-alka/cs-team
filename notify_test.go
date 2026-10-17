package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"cs-team/chat"
)

// Benachrichtigungen: Erwaehnung im Chat und Aushang mit "Empfaenger benachrichtigen"; Abwahl je Benutzer; nur wer den Kanal/Aushang sehen darf.
func TestNotifications(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	var mu sync.Mutex
	var got []string // "benutzer|betreff|text"
	old := notifySend
	notifySend = func(m *chat.Mailer, user, subj, text string) {
		mu.Lock()
		got = append(got, user+"|"+subj+"|"+text)
		mu.Unlock()
	}
	defer func() { notifySend = old }()
	snap := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, got...) }
	reset := func() { mu.Lock(); got = nil; mu.Unlock() }
	// wartet, bis n Meldungen da sind (die Zustellung laeuft im Hintergrund); danach kurz warten, ob keine weiteren kommen
	waitN := func(n int) []string {
		for i := 0; i < 150 && len(snap()) < n; i++ {
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond)
		return snap()
	}
	users := func(l []string) string {
		var u []string
		for _, x := range l {
			u = append(u, strings.SplitN(x, "|", 2)[0])
		}
		sort.Strings(u)
		return strings.Join(u, ",")
	}

	for _, n := range []string{"cara", "dave", "eve"} {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k5","areas":["files"],"chat":"member","chans":"member","msg":"member","admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/groups/k5/members", `{"add":["bob","dave"]}`)
	say := func(who, text string) {
		if c, b := req(t, srv, who, "POST", "/api/chat/k5/allgemein/upload?name=bild.png&text="+strings.ReplaceAll(strings.ReplaceAll(text, " ", "%20"), "@", "%40"), "\x89PNG\r\n\x1a\nrest"); c != 200 {
			t.Fatal(who, c, b)
		}
	}

	// 1. Erwaehnung: nur dave (Mitglied); anna selbst, cara (kein Mitglied) und ein unbekannter Name nicht
	say("anna", "Hallo @dave, @cara, @anna und @niemand")
	l := waitN(1)
	if len(l) != 1 || users(l) != "dave" || !strings.Contains(l[0], "cs-team [k5 #allgemein] anna @dave") || !strings.Contains(l[0], "anna: Hallo @dave") {
		t.Fatalf("Erwaehnung: %v", l)
	}
	// 2. gleiche Person im selben Kanal innerhalb von 10 Minuten: keine zweite Meldung
	reset()
	say("anna", "noch einmal @dave")
	if l := waitN(1); len(l) != 0 {
		t.Fatalf("Entprellung: %v", l)
	}
	// 3. Satzzeichen am Ende des Namens, Gross-/Kleinschreibung; zwei Personen in einer Nachricht
	reset()
	say("dave", "Danke @Bob. und @anna!")
	l = waitN(2)
	if users(l) != "anna,bob" {
		t.Fatalf("Satzzeichen: %v", l)
	}

	// 4. Abwahl im Konto (nur bekannte Arten, ohne Doppelte), GET /api/me nennt sie
	if c, b := req(t, srv, "cara", "POST", "/api/me/notify", `{"off":["news","bogus","news","mention"]}`); c != 200 {
		t.Fatal(c, b)
	}
	_, me := req(t, srv, "cara", "GET", "/api/me", "")
	var mj struct{ NotifyOff []string }
	if json.Unmarshal([]byte(me), &mj) != nil || strings.Join(mj.NotifyOff, ",") != "mention,news" {
		t.Fatalf("notifyOff: %s", me)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/me/notify", `{"off":["news"]}`); c != 200 {
		t.Fatal("bob")
	}

	// 5. Aushang an die Gruppe k5 mit Benachrichtigung: dave ja, bob hat Aushaenge abgewaehlt, anna ist die Autorin, cara/eve gehoeren nicht dazu
	reset()
	if c, b := req(t, srv, "anna", "POST", "/api/news", `{"title":"Klassenarbeit","text":"am Donnerstag","group":"k5","notify":true}`); c != 200 {
		t.Fatal(c, b)
	}
	l = waitN(1)
	if len(l) != 1 || users(l) != "dave" || !strings.HasPrefix(l[0], "dave|cs-team: Klassenarbeit|anna: am Donnerstag") {
		t.Fatalf("Aushang Gruppe: %v", l)
	}
	// 6. Aushang an alle: alle ausser der Autorin; cara und bob haben abgewaehlt
	reset()
	if c, b := req(t, srv, "anna", "POST", "/api/news", `{"title":"Wartung","text":"Samstag","notify":true}`); c != 200 {
		t.Fatal(c, b)
	}
	l = waitN(2)
	if users(l) != "dave,eve" {
		t.Fatalf("Aushang alle: %v", l)
	}
	// 7. ohne Haken keine Meldung
	reset()
	if c, b := req(t, srv, "anna", "POST", "/api/news", `{"title":"Still","text":"ohne Meldung"}`); c != 200 {
		t.Fatal(c, b)
	}
	if l := waitN(1); len(l) != 0 {
		t.Fatalf("ohne notify: %v", l)
	}
	// 8. ein Mitglied darf keinen Aushang mit Meldung anlegen (Rechte wie bisher)
	if c, _ := req(t, srv, "dave", "POST", "/api/news", `{"title":"x","text":"y","group":"k5","notify":true}`); c != 403 {
		t.Fatal("Mitglied:", c)
	}
}
