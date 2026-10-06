package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// 0.60.0 Umfragen im Chat: Anlegen (Schreibrecht), Abstimmen (Mitglied), Anonymitaet, Beenden, Nichtmitglied.
func TestChatPoll(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/users", `{"name":"carl","password":"passwort-carl","groups":["alluser"]}`)
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"6a","areas":["files"],"chat":"member","chans":"member","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/6a/members", `{"add":["bob"]}`)
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"lehrer","areas":["files"],"chat":"admin","chans":"admin","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/lehrer/members", `{"add":["bob"]}`)

	anna, bob, carl := chatDial(t, srv, "anna"), chatDial(t, srv, "bob"), chatDial(t, srv, "carl")
	defer settle(anna, bob, carl)
	read(t, anna)
	read(t, bob)
	read(t, carl)
	poll := func(g, extra string) string {
		return `{"t":"poll","g":"` + g + `","c":"allgemein","poll":{"q":"Ausflug?","opts":["Zoo","Museum","Park"]` + extra + `}}`
	}
	// Validierung: eine Option, Frage fehlt
	wsSend(t, bob, `{"t":"poll","g":"6a","c":"allgemein","poll":{"q":"x","opts":["a"]}}`)
	if m := wsNext(t, bob, "err"); !strings.Contains(m["m"].(string), "poll") {
		t.Fatal("zu wenige Optionen:", m)
	}
	// Nur-Lesen-Gruppe: bob darf keine Umfrage anlegen; Nichtmitglied carl auch nicht
	wsSend(t, bob, poll("lehrer", ""))
	if m := wsNext(t, bob, "err"); !strings.Contains(m["m"].(string), "admins") {
		t.Fatal("Nur-Lesen:", m)
	}
	wsSend(t, carl, poll("6a", ""))
	wsNext(t, carl, "err")

	// bob legt eine benannte und eine anonyme Umfrage in 6a an; anna sieht sie live
	wsSend(t, bob, poll("6a", `,"multi":true`))
	named := wsNext(t, anna, "msg")["m"].(map[string]any)
	wsSend(t, bob, poll("6a", `,"anon":true`))
	anon := wsNext(t, anna, "msg")["m"].(map[string]any)
	nid, aid := named["id"].(float64), anon["id"].(float64)
	send := func(user string, id float64, sel string) {
		b, _ := json.Marshal(map[string]any{"t": "vote", "g": "6a", "c": "allgemein", "id": id, "sel": json.RawMessage(sel)})
		c := map[string]*websocket.Conn{"anna": anna, "bob": bob, "carl": carl}[user]
		wsSend(t, c, string(b))
	}
	// upd: naechstes Update genau dieser Nachricht (Updates anderer Umfragen werden uebersprungen)
	upd := func(c *websocket.Conn, id float64) map[string]any {
		for i := 0; i < 10; i++ {
			if m := wsNext(t, c, "upd")["m"].(map[string]any); m["id"].(float64) == id {
				return m["poll"].(map[string]any)
			}
		}
		t.Fatal("kein Update")
		return nil
	}
	send("anna", nid, `[0,2]`)
	u := upd(bob, nid)
	if u["n"].(float64) != 1 || u["votes"].(map[string]any)["anna"] == nil {
		t.Fatal("Live-Update benannt:", u)
	}
	send("bob", nid, `[1]`)
	send("bob", nid, `[0]`) // Stimme aendern
	upd(anna, nid)
	upd(anna, nid)
	// Nichtmitglied darf nicht abstimmen und bekommt keine Umfrage zu sehen
	send("carl", nid, `[0]`)
	if m := wsNext(t, carl, "err"); m["m"] == nil {
		t.Fatal(m)
	}
	// anonym: Stimme zaehlt, Aenderung abgelehnt, nichts davon im Verlauf
	send("anna", aid, `[2]`)
	// Regression (Audit H1): bei offener anonymer Umfrage zeigt keine Meldung Namen oder Zaehler je Antwort, nur die Teilnehmerzahl;
	// zwei aufeinanderfolgende Meldungen lassen also keinen Schluss auf die Wahl zu
	au := upd(bob, aid)
	if au["n"].(float64) != 1 || au["cnt"] != nil || au["done"] != nil || au["votes"] != nil || au["me"] != nil {
		t.Fatal("anonym offen (bob, hat nicht abgestimmt):", au)
	}
	if am := upd(anna, aid); am["me"] != true || am["cnt"] != nil || am["done"] != nil || am["n"].(float64) != 1 {
		t.Fatal("anonym offen (anna, eigene Marke):", am)
	}
	send("bob", aid, `[1]`)
	if au = upd(bob, aid); au["n"].(float64) != 2 || au["me"] != true || au["cnt"] != nil || au["done"] != nil || au["votes"] != nil {
		t.Fatal("anonym offen nach zweiter Stimme (bob):", au)
	}
	if a2 := upd(anna, aid); a2["n"].(float64) != 2 || a2["cnt"] != nil || a2["done"] != nil {
		t.Fatal("anonym offen nach zweiter Stimme (anna):", a2)
	}
	send("anna", aid, `[0]`)
	if m := wsNext(t, anna, "err"); !strings.Contains(m["m"].(string), "anonymous") {
		t.Fatal("anonyme Aenderung:", m)
	}
	_, hist := req(t, srv, "bob", "GET", "/api/chat/6a/allgemein/history", "")
	var h struct{ Msgs []struct{ Poll map[string]any } }
	json.Unmarshal([]byte(hist), &h)
	if len(h.Msgs) != 2 || h.Msgs[0].Poll["votes"] == nil || h.Msgs[1].Poll["votes"] != nil || strings.Contains(hist, `"anna":[2]`) {
		t.Fatal("Verlauf:", hist)
	}
	if p := h.Msgs[1].Poll; p["cnt"] != nil || p["done"] != nil || p["me"] != true || p["n"].(float64) != 2 {
		t.Fatal("Verlauf anonym offen:", p)
	}
	if c, _ := req(t, srv, "carl", "GET", "/api/chat/6a/allgemein/history", ""); c != 404 {
		t.Fatal("Nichtmitglied liest Verlauf:", c)
	}
	// Beenden: bob (Ersteller) darf, carl nicht; danach keine Stimmen mehr
	b2, _ := json.Marshal(map[string]any{"t": "pollclose", "g": "6a", "c": "allgemein", "id": nid})
	wsSend(t, carl, string(b2))
	wsNext(t, carl, "err")
	wsSend(t, bob, string(b2))
	if c := upd(anna, nid)["closed"]; c != true {
		t.Fatal("nicht geschlossen")
	}
	send("anna", nid, `[1]`)
	if m := wsNext(t, anna, "err"); !strings.Contains(m["m"].(string), "closed") {
		t.Fatal("geschlossen:", m)
	}
	// anonyme Umfrage beenden: jetzt erscheinen die Zaehler (anna=Park, bob=Museum), Namen nie
	b5, _ := json.Marshal(map[string]any{"t": "pollclose", "g": "6a", "c": "allgemein", "id": aid})
	wsSend(t, bob, string(b5))
	if cl := upd(anna, aid); cl["closed"] != true || cl["done"] != nil || cl["votes"] != nil || fmt.Sprint(cl["cnt"]) != "[0 1 1]" {
		t.Fatal("anonym beendet:", cl)
	}
	_, hist = req(t, srv, "anna", "GET", "/api/chat/6a/allgemein/history", "")
	if !strings.Contains(hist, `"cnt":[0,1,1]`) || strings.Contains(hist, `"done"`) {
		t.Fatal("Verlauf nach Ende:", hist)
	}
	// Nur-Lesen-Gruppe: Admin legt an, bob (nur Leser) darf abstimmen
	wsSend(t, anna, poll("lehrer", ""))
	lid := wsNext(t, bob, "msg")["m"].(map[string]any)["id"].(float64)
	b3, _ := json.Marshal(map[string]any{"t": "vote", "g": "lehrer", "c": "allgemein", "id": lid, "sel": []int{1}})
	wsSend(t, bob, string(b3))
	if p := upd(anna, lid); p["n"].(float64) != 1 {
		t.Fatal("Leser stimmt ab:", p)
	}
	// Bearbeiten einer Umfrage ist nicht moeglich
	b4, _ := json.Marshal(map[string]any{"t": "edit", "g": "6a", "c": "allgemein", "id": nid, "text": "neu"})
	wsSend(t, bob, string(b4))
	wsNext(t, bob, "err")
}
