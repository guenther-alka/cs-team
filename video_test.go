package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"cs-team/chat"
)

func TestVideo(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/users", `{"name":"carl","password":"passwort-carl","groups":["alluser"]}`)
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"6a","areas":["files"],"chat":"member","chans":"member","admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/groups/6a/members", `{"add":["bob"]}`)
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"lehrer","areas":["files"],"chat":"admin","chans":"admin","admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/groups/lehrer/members", `{"add":["bob"]}`)

	set := func(user, body string) int { c, _ := req(t, srv, user, "POST", "/api/settings/video", body); return c }
	// Platz 1 fester Raum, Platz 2 Ad-hoc nur Gruppen-Admins, Platz 3 Ad-hoc für alle; Art und Recht kommen vom Platz, nicht aus der Eingabe
	ok := `{"options":[{"name":"MiroTalk","url":"https://mt.example.org/join/{room}","mode":"adhoc","who":"admin"},{"name":"Jitsi Lehrer","url":"https://admin.example.org/{room}"},{"name":"Jitsi","url":"https://meet.example.org/{room}","mode":"group","who":"admin"}]}`
	if set("bob", ok) != 403 {
		t.Fatal("nur Admin")
	}
	for _, bad := range []string{
		`{"options":[{"name":"x","url":"https://a.example.org/room"}]}`,                                                             // kein {room}
		`{"options":[{"name":"x","url":"https://a.example.org/{room}/{room}"}]}`,                                                    // zweimal
		`{"options":[{"name":"x","url":"javascript:alert({room})"}]}`,                                                               // Schema
		`{"options":[{"name":"x","url":"https://u:p@a.example.org/{room}"}]}`,                                                       // Zugangsdaten
		`{"options":[{"url":"https://a/{room}"},{"url":"https://b/{room}"},{"url":"https://c/{room}"},{"url":"https://d/{room}"}]}`, // 4 Plätze
	} {
		if set("anna", bad) != 400 {
			t.Fatal("muss abgelehnt werden:", bad)
		}
	}
	if set("anna", ok) != 200 {
		t.Fatal("speichern")
	}
	if _, b := req(t, srv, "anna", "GET", "/api/settings", ""); !strings.Contains(b, "mt.example.org") || strings.Contains(b, "vsecret") || !strings.Contains(b, `"mode":"group"`) {
		t.Fatal("settings:", b)
	}

	type vrow struct {
		Slot int
		Name string
		Mode string
		Can  bool
	}
	type grp struct {
		Name  string
		Video []vrow
	}
	groups := func(user string) map[string]grp {
		_, b := req(t, srv, user, "GET", "/api/chat/groups", "")
		var r struct{ Groups []grp }
		json.Unmarshal([]byte(b), &r)
		m := map[string]grp{}
		for _, g := range r.Groups {
			m[g.Name] = g
		}
		return m
	}
	can := func(g grp) string {
		s := ""
		for _, v := range g.Video {
			if v.Can {
				s += strconv.Itoa(v.Slot)
			}
		}
		return s
	}
	if g := groups("bob")["6a"]; len(g.Video) != 3 || can(g) != "02" || g.Video[0].Mode != "group" || g.Video[2].Mode != "adhoc" {
		t.Fatalf("bob 6a: %+v", g)
	}
	if g := groups("anna")["6a"]; can(g) != "012" {
		t.Fatalf("anna 6a: %+v", g)
	}
	if g := groups("bob")["lehrer"]; len(g.Video) != 3 || can(g) != "" {
		t.Fatalf("bob lehrer (nur lesen): %+v", g)
	}
	if _, in := groups("carl")["6a"]; in {
		t.Fatal("carl ist nicht in 6a")
	}

	post := func(user, g string, slot string) (int, string) {
		c, b := req(t, srv, user, "POST", "/api/chat/"+g+"/allgemein/video", `{"slot":`+slot+`}`)
		var r struct{ URL string }
		json.Unmarshal([]byte(b), &r)
		if c != 200 {
			return c, b
		}
		return c, r.URL
	}
	type hm struct {
		ID  int64
		T   string
		Vid *struct {
			Slot int
			Mode string
			Exp  int64
		}
	}
	history := func(user, g string) ([]hm, string) {
		_, b := req(t, srv, user, "GET", "/api/chat/"+g+"/allgemein/history", "")
		var r struct{ Msgs []hm }
		json.Unmarshal([]byte(b), &r)
		return r.Msgs, b
	}
	// Platz 3 (Ad-hoc für alle): neue Einladung, Raumname nicht erratbar und nicht in der Nachricht gespeichert
	c, u1 := post("bob", "6a", "2")
	if c != 200 || !regexp.MustCompile(`^https://meet\.example\.org/6a-[0-9a-f]{12}$`).MatchString(u1) {
		t.Fatal("adhoc:", c, u1)
	}
	msgs, raw := history("bob", "6a")
	if len(msgs) != 1 || msgs[0].Vid == nil || msgs[0].Vid.Mode != "adhoc" || msgs[0].Vid.Exp < time.Now().Add(23*time.Hour).UnixMilli() || msgs[0].T != "📹 Videochat: Jitsi" {
		t.Fatal("Einladung:", raw)
	}
	if strings.Contains(raw, "meet.example.org") || strings.Contains(raw, u1[len(u1)-12:]) {
		t.Fatal("Raumname darf nicht in der Nachricht stehen:", raw)
	}
	if _, u := post("bob", "6a", "2"); u != u1 { // zweiter Klick kurz danach: gleicher Raum
		t.Fatal("Doppelklick:", u, u1)
	}
	if m, _ := history("bob", "6a"); len(m) != 1 {
		t.Fatal("nur eine Einladung:", len(m))
	}
	// Beitreten über die Einladung: Mitglied ja, Fremder nein
	join := func(user, g string, id int64) (int, string) {
		c, b := req(t, srv, user, "GET", "/api/chat/"+g+"/allgemein/video/"+strconv.FormatInt(id, 10), "")
		var r struct{ URL string }
		json.Unmarshal([]byte(b), &r)
		return c, r.URL
	}
	if c, u := join("anna", "6a", msgs[0].ID); c != 200 || u != u1 {
		t.Fatal("join:", c, u)
	}
	if c, _ := join("carl", "6a", msgs[0].ID); c != 404 {
		t.Fatal("Fremder darf nicht:", c)
	}
	if c, _ := join("anna", "6a", 12345); c != 404 {
		t.Fatal("falsche Nachricht:", c)
	}
	// Platz 2 (Ad-hoc nur Gruppen-Admins)
	if c, _ := post("bob", "6a", "1"); c != 403 {
		t.Fatal("bob darf Platz 2 nicht:", c)
	}
	c, ua := post("anna", "6a", "1")
	if c != 200 || !regexp.MustCompile(`^https://admin\.example\.org/6a-[0-9a-f]{12}$`).MatchString(ua) || ua[len(ua)-12:] == u1[len(u1)-12:] {
		t.Fatal("admin-adhoc:", c, ua)
	}
	// Platz 1 (fester Raum): immer derselbe Name, Einladung nicht erneut innerhalb 30 min, Mitglieder dürfen
	c, g1 := post("bob", "6a", "0")
	if c != 200 || !regexp.MustCompile(`^https://mt\.example\.org/join/6a-[0-9a-f]{10}$`).MatchString(g1) {
		t.Fatal("group:", c, g1)
	}
	if _, g2 := post("anna", "6a", "0"); g2 != g1 {
		t.Fatal("fester Raum wechselt:", g2, g1)
	}
	if m, _ := history("bob", "6a"); len(m) != 3 {
		t.Fatal("drei Einladungen (je Platz eine):", len(m))
	}
	// andere Gruppe: anderer Raum; nur lesen und Nichtmitglied dürfen nicht starten, leerer/ungültiger Platz
	if _, x := post("anna", "lehrer", "0"); x == g1 || !strings.Contains(x, "/join/lehrer-") {
		t.Fatal("Raum je Gruppe:", x)
	}
	if c, _ := post("bob", "lehrer", "2"); c != 403 {
		t.Fatal("nur lesen darf nicht starten:", c)
	}
	if c, _ := post("carl", "6a", "2"); c != 403 {
		t.Fatal("Nichtmitglied:", c)
	}
	if c, _ := post("bob", "6a", "3"); c != 404 {
		t.Fatal("ungültiger Platz:", c)
	}
	// Platz abschalten: Eintrag verschwindet, alte Einladung führt ins Leere
	if set("anna", `{"options":[{"name":"MiroTalk","url":"https://mt.example.org/join/{room}"}]}`) != 200 {
		t.Fatal("abschalten")
	}
	if g := groups("bob")["6a"]; len(g.Video) != 1 || can(g) != "0" {
		t.Fatalf("nach Abschalten: %+v", g)
	}
	if c, _ := join("anna", "6a", msgs[0].ID); c != 404 {
		t.Fatal("abgeschalteter Platz:", c)
	}
	if c, _ := post("bob", "6a", "2"); c != 404 {
		t.Fatal("abgeschaltet startet nicht:", c)
	}
	// Ablauf: eine bereits abgelaufene Einladung liefert 410
	if set("anna", ok) != 200 {
		t.Fatal("wieder an")
	}
	chat.AdhocTTL = -time.Hour
	defer func() { chat.AdhocTTL = 24 * time.Hour }()
	if c, _ := post("anna", "lehrer", "2"); c != 200 {
		t.Fatal("anna startet in lehrer:", c)
	}
	lm, _ := history("anna", "lehrer")
	var inv int64
	for _, m := range lm {
		if m.Vid != nil && m.Vid.Slot == 2 {
			inv = m.ID
		}
	}
	if inv == 0 {
		t.Fatal("Einladung fehlt")
	}
	if c, _ := join("anna", "lehrer", inv); c != 410 {
		t.Fatal("abgelaufen muss 410 sein:", c)
	}
}
