package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"cs-team/chat"
)

// 1:1-Nachrichten je Gruppe schaltbar (off | admin | member), privat nur fuer die beiden; Antworten (Threads).
func TestDirectMessagesAndThreads(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	var mu sync.Mutex
	var got []string
	old := notifySend
	notifySend = func(m *chat.Mailer, user, subj, text string) {
		mu.Lock()
		got = append(got, user+"|"+subj+"|"+text)
		mu.Unlock()
	}
	defer func() { notifySend = old }()
	snap := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, got...) }
	for _, n := range []string{"cara", "dave", "eve"} {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k5","areas":["files"],"chat":"member","chans":"member","msg":"member","admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/groups/k5/members", `{"add":["bob","cara","dave"]}`)
	people := func(user string) (int, []string, string) {
		c, b := req(t, srv, user, "GET", "/api/chat/k5/dm", "")
		var r struct {
			People []string
			Mode   string
		}
		json.Unmarshal([]byte(b), &r)
		return c, r.People, r.Mode
	}
	open := func(user, with string) (int, string) {
		c, b := req(t, srv, user, "POST", "/api/chat/k5/dm", `{"with":"`+with+`"}`)
		var r struct{ Name string }
		json.Unmarshal([]byte(b), &r)
		return c, r.Name
	}
	say := func(user, cn, text string) int {
		c, _ := req(t, srv, user, "POST", "/api/chat/k5/"+cn+"/upload?name=bild.png&text="+strings.ReplaceAll(text, " ", "%20"), "\x89PNG\r\n\x1a\nrest")
		return c
	}
	hist := func(user, cn string) (int, string) { return req(t, srv, user, "GET", "/api/chat/k5/"+cn+"/history", "") }
	rows := func(user string) string { // Kanaele der Gruppe k5 aus Sicht des Benutzers
		_, b := req(t, srv, user, "GET", "/api/chat/groups", "")
		return b
	}

	// 1. Standard: aus. Wer nicht Mitglied ist, sieht nichts.
	if c, p, mode := people("cara"); c != 200 || len(p) != 0 || mode != "off" {
		t.Fatal("Standard:", c, p, mode)
	}
	if c, _ := open("cara", "bob"); c != 403 {
		t.Fatal("1:1 aus:", c)
	}
	if c, _, _ := people("eve"); c != 404 {
		t.Fatal("Nichtmitglied:", c)
	}
	// 2. Umschalten: nur Gruppen-/globale Admins, nur bekannte Modi
	if c, _ := req(t, srv, "cara", "POST", "/api/groups/k5/dm", `{"mode":"member"}`); c != 403 {
		t.Fatal("Mitglied schaltet:", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/groups/k5/dm", `{"mode":"alle"}`); c != 400 {
		t.Fatal("unbekannter Modus:", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups/k5/dm", `{"mode":"admin"}`); c != 200 {
		t.Fatal(c, b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/groups", ""); !strings.Contains(b, `"dm":"admin"`) {
		t.Fatalf("Gruppenliste nennt den Modus nicht: %s", b)
	}

	// 3. Modus admin: Mitglied <-> Mitglied nein, Gruppen-Admin <-> Mitglied ja
	if c, _ := open("cara", "bob"); c != 403 {
		t.Fatal("admin-Modus, Mitglied an Mitglied:", c)
	}
	if c, p, mode := people("cara"); c != 200 || strings.Join(p, ",") != "anna" || mode != "admin" {
		t.Fatalf("Auswahl im admin-Modus: %v %q", p, mode)
	}
	if c, _ := open("cara", "cara"); c != 403 {
		t.Fatal("an sich selbst:", c)
	}
	if c, _ := open("cara", "eve"); c != 403 {
		t.Fatal("an Nichtmitglied:", c)
	}
	c, cn := open("cara", "anna")
	if c != 200 || !strings.HasPrefix(cn, "dm-") || len(cn) != 15 {
		t.Fatal("anlegen:", c, cn)
	}
	if c2, cn2 := open("anna", "cara"); c2 != 200 || cn2 != cn { // gleicher Kanal aus Sicht des anderen
		t.Fatal("stabiler Name:", c2, cn2)
	}
	// 4. nur die beiden lesen/schreiben; Verlauf, Kanalliste, Suche, Meldung
	bobWS := chatDial(t, srv, "bob")
	defer bobWS.CloseNow()
	read(t, bobWS) // hello
	if c := say("cara", cn, "geheim eins"); c != 200 {
		t.Fatal("cara schreibt:", c)
	}
	if c := say("anna", cn, "antwort zwei"); c != 200 {
		t.Fatal("anna schreibt:", c)
	}
	for _, u := range []string{"cara", "anna"} {
		if c, b := hist(u, cn); c != 200 || !strings.Contains(b, "geheim eins") {
			t.Fatalf("%s liest: %d %s", u, c, b)
		}
	}
	for _, u := range []string{"bob", "dave"} {
		if c, _ := hist(u, cn); c != 404 {
			t.Fatalf("Mitglied %s darf den 1:1-Kanal nicht lesen: %d", u, c)
		}
		if c := say(u, cn, "mitlesen"); c != 403 {
			t.Fatalf("Mitglied %s darf nicht schreiben: %d", u, c)
		}
	}
	if c, _ := hist("eve", cn); c != 404 {
		t.Fatal("Nichtmitglied liest:", c)
	}
	if b := rows("bob"); strings.Contains(b, cn) || strings.Contains(b, `"dm":"anna"`) || strings.Contains(b, `"dm":"cara"`) {
		t.Fatalf("bob sieht den 1:1-Kanal: %s", b)
	}
	if b := rows("cara"); !strings.Contains(b, cn) || !strings.Contains(b, `"dm":"anna"`) {
		t.Fatalf("cara sieht ihren 1:1-Kanal nicht: %s", b)
	}
	if b := rows("anna"); !strings.Contains(b, `"dm":"cara"`) {
		t.Fatalf("anna sieht ihren 1:1-Kanal nicht: %s", b)
	}
	if _, b := req(t, srv, "bob", "GET", "/api/search?q=geheim", ""); strings.Contains(b, "geheim eins") {
		t.Fatalf("Suche findet fremde 1:1-Nachricht: %s", b)
	}
	if _, b := req(t, srv, "cara", "GET", "/api/search?q=geheim", ""); !strings.Contains(b, "geheim eins") {
		t.Fatalf("Suche findet eigene 1:1-Nachricht nicht: %s", b)
	}
	// ein Mitleser per WebSocket bekommt weder die Nachricht noch die Kanalliste
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	_, data, err := bobWS.Read(ctx)
	cancel()
	if err == nil {
		t.Fatalf("bob bekam ueber den WebSocket: %s", data)
	}
	// Meldung an den anderen Teilnehmer, nicht doppelt innerhalb von 10 Minuten
	time.Sleep(300 * time.Millisecond)
	var dmn []string
	for _, x := range snap() {
		if strings.Contains(x, "(1:1)") {
			dmn = append(dmn, x)
		}
	}
	if len(dmn) != 2 || !strings.HasPrefix(dmn[0], "anna|cs-team [k5] cara (1:1)|cara: geheim eins") || !strings.HasPrefix(dmn[1], "cara|cs-team [k5] anna (1:1)|anna: antwort zwei") {
		t.Fatalf("1:1-Meldungen: %v", dmn)
	}
	// Abwahl "dm" im Konto
	if c, _ := req(t, srv, "anna", "POST", "/api/me/notify", `{"off":["dm"]}`); c != 200 {
		t.Fatal("Abwahl")
	}

	// 5. Modus member: auch Mitglied <-> Mitglied; der Gruppen-Admin liest fremde 1:1-Kanaele NICHT (auch nicht als globaler Admin)
	if c, _ := req(t, srv, "anna", "POST", "/api/groups/k5/dm", `{"mode":"member"}`); c != 200 {
		t.Fatal(c)
	}
	c, cn2 := open("cara", "bob")
	if c != 200 || cn2 == cn {
		t.Fatal("member-Modus:", c, cn2)
	}
	if c := say("bob", cn2, "unter uns"); c != 200 {
		t.Fatal("bob schreibt:", c)
	}
	if c, _ := hist("anna", cn2); c != 404 {
		t.Fatalf("Gruppen-/globaler Admin darf fremde 1:1-Kanaele nicht lesen: %d", c)
	}
	if b := rows("anna"); strings.Contains(b, cn2) {
		t.Fatalf("Admin sieht fremden 1:1-Kanal: %s", b)
	}
	// 1:1-Kanaele lassen sich nicht wie Kanaele loeschen oder neu anlegen
	cw := chatDial(t, srv, "cara")
	defer cw.CloseNow()
	read(t, cw)
	cw.Write(context.Background(), websocket.MessageText, []byte(`{"t":"rmchan","g":"k5","name":"`+cn2+`"}`))
	if m := read(t, cw); m["t"] != "err" {
		t.Fatalf("rmchan 1:1: %v", m)
	}
	cw.Write(context.Background(), websocket.MessageText, []byte(`{"t":"mkchan","g":"k5","name":"dm-abcdef123456"}`))
	if m := read(t, cw); m["t"] != "err" {
		t.Fatalf("mkchan dm-: %v", m)
	}

	// 6. Modus aus: der Verlauf bleibt lesbar, schreiben nicht mehr
	if c, _ := req(t, srv, "anna", "POST", "/api/groups/k5/dm", `{"mode":"off"}`); c != 200 {
		t.Fatal(c)
	}
	if c, b := hist("cara", cn); c != 200 || !strings.Contains(b, "geheim eins") {
		t.Fatalf("Verlauf nach dem Abschalten: %d", c)
	}
	if c := say("cara", cn, "geht nicht mehr"); c != 403 {
		t.Fatalf("schreiben nach dem Abschalten: %d", c)
	}
	if c, _ := open("cara", "anna"); c != 403 {
		t.Fatal("neu anlegen nach dem Abschalten:", c)
	}

	// 7. Antworten (Threads): Wurzel, Antwort, Antwort auf eine Antwort zeigt auf die Wurzel, unbekannte Nummer -> Fehler
	wsA, wsB := chatDial(t, srv, "anna"), chatDial(t, srv, "bob")
	defer wsA.CloseNow()
	defer wsB.CloseNow()
	read(t, wsA)
	read(t, wsB)
	send := func(c *websocket.Conn, v string) { c.Write(context.Background(), websocket.MessageText, []byte(v)) }
	idOf := func(m map[string]any) int64 { return int64(m["m"].(map[string]any)["id"].(float64)) }
	send(wsB, `{"t":"send","g":"k5","c":"allgemein","text":"Frage an alle"}`)
	m := read(t, wsB)
	read(t, wsA)
	root := idOf(m)
	send(wsA, fmt.Sprintf(`{"t":"send","g":"k5","c":"allgemein","text":"erste Antwort","rp":%d}`, root))
	m = read(t, wsA)
	read(t, wsB)
	reply := idOf(m)
	if int64(m["m"].(map[string]any)["rp"].(float64)) != root {
		t.Fatalf("Antwort: %v", m)
	}
	send(wsB, fmt.Sprintf(`{"t":"send","g":"k5","c":"allgemein","text":"Antwort auf die Antwort","rp":%d}`, reply))
	m = read(t, wsB)
	read(t, wsA)
	if int64(m["m"].(map[string]any)["rp"].(float64)) != root {
		t.Fatalf("Antwort auf Antwort zeigt auf die Wurzel: %v", m)
	}
	send(wsA, `{"t":"send","g":"k5","c":"allgemein","text":"ins Leere","rp":1}`)
	if m := read(t, wsA); m["t"] != "err" {
		t.Fatalf("unbekannte Nachricht: %v", m)
	}
	// die Antwort steht im Verlauf mit rp; eine Antwort meldet den Verfasser der Wurzel (bob, Art mention)
	if _, b := hist("bob", "allgemein"); !strings.Contains(b, fmt.Sprintf(`"rp":%d`, root)) {
		t.Fatalf("Verlauf ohne rp: %s", b)
	}
	time.Sleep(300 * time.Millisecond)
	found := false
	for _, x := range snap() {
		if strings.HasPrefix(x, "bob|cs-team [k5 #allgemein] anna @bob|anna: erste Antwort") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Meldung an den Verfasser der Wurzel fehlt: %v", snap())
	}
}
