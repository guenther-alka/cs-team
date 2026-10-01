package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func aiMk(t *testing.T, srv *httptest.Server, u, g string) {
	t.Helper()
	if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+u+`","password":"passwort-`+u+`","groups":["`+g+`"]}`); c != 200 {
		t.Fatal(c, b)
	}
}

func aiDocs(t *testing.T, srv *httptest.Server, user string) []string {
	t.Helper()
	c, b := req(t, srv, user, "GET", "/api/docs", "")
	if c != 200 {
		t.Fatal(c, b)
	}
	var l []struct{ Name, Owner string }
	json.Unmarshal([]byte(b), &l)
	var n []string
	for _, d := range l {
		n = append(n, d.Name+"@"+d.Owner)
	}
	return n
}

const aiProp = `{"type":"text","name":"Elternbrief","paragraphs":["Liebe Eltern","am Freitag ist frei"],"confirm":true}`

func TestAICreateOffByDefault(t *testing.T) {
	srv, _ := aiSetup(t, &fakeAI{}, "openai", "") // Stufe 2 nicht eingeschaltet
	defer srv.Close()
	if c, _ := req(t, srv, "anna", "POST", "/api/ai/create", aiProp); c != 403 {
		t.Fatal("global aus: auch der Admin darf nicht:", c)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/ai/config", ""); !strings.Contains(b, `"create":false`) {
		t.Fatal(b)
	}
}

func TestAICreate(t *testing.T) {
	f := &fakeAI{}
	srv, _ := aiSetup(t, f, "openai", `,"create":"yes"`)
	defer srv.Close()
	aiMk(t, srv, "dora", "alluser")
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k7","areas":["calc","text"],"admins":["dora"]}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k9","areas":["text"]}`); c != 200 {
		t.Fatal(c, b)
	}
	aiMk(t, srv, "carl", "k7")
	aiMk(t, srv, "fred", "k9")

	// Gruppen-Schalter: Standard aus; Mitglieder dürfen nicht, Gruppen-Admin und globaler Admin schon
	cfg := func(u string) string { _, b := req(t, srv, u, "GET", "/api/ai/config", ""); return b }
	for _, u := range []string{"bob", "carl", "fred"} {
		if !strings.Contains(cfg(u), `"create":false`) {
			t.Fatal(u, cfg(u))
		}
		if c, _ := req(t, srv, u, "POST", "/api/ai/create", aiProp); c != 403 {
			t.Fatal(u, "Gruppe aus:", c)
		}
	}
	for _, u := range []string{"anna", "dora"} {
		if !strings.Contains(cfg(u), `"create":true`) {
			t.Fatal(u, cfg(u))
		}
	}
	// nur wer die Gruppe verwaltet, schaltet
	if c, _ := req(t, srv, "carl", "POST", "/api/groups/k7/ai", `{"mode":"member"}`); c != 403 {
		t.Fatal("Mitglied darf nicht schalten:", c)
	}
	if c, _ := req(t, srv, "dora", "POST", "/api/groups/k9/ai", `{"mode":"member"}`); c != 403 {
		t.Fatal("fremde Gruppe:", c)
	}
	if c, _ := req(t, srv, "dora", "POST", "/api/groups/k7/ai", `{"mode":"alle"}`); c != 400 {
		t.Fatal("ungültiger Wert:", c)
	}
	if c, b := req(t, srv, "dora", "POST", "/api/groups/k7/ai", `{"mode":"member"}`); c != 200 {
		t.Fatal(c, b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/groups", ""); !strings.Contains(b, `"name":"k7"`) || !strings.Contains(b, `"ai":"member"`) {
		t.Fatal(b)
	}
	if !strings.Contains(cfg("carl"), `"create":true`) || !strings.Contains(cfg("bob"), `"create":false`) {
		t.Fatal("carl ja, bob nein")
	}

	// Systemanweisung für den Vorschlagsblock nur, wenn der Benutzer darf
	req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("Schreibe einen Brief", ""))
	if body, _ := f.last(); strings.Contains(body, "csdoc") {
		t.Fatal("bob darf nicht, Anweisung darf nicht erscheinen")
	}
	req(t, srv, "carl", "POST", "/api/ai/chat", chatBody("Schreibe einen Brief", ""))
	if body, _ := f.last(); !strings.Contains(body, "csdoc") {
		t.Fatal("carl darf, Anweisung fehlt")
	}

	// Vorschau legt nichts an und liefert die geprüfte Fassung
	pre := strings.Replace(aiProp, `"confirm":true`, `"confirm":false`, 1)
	c, b := req(t, srv, "carl", "POST", "/api/ai/create", pre)
	if c != 200 || !strings.Contains(b, "Liebe Eltern") || strings.Contains(b, `"id"`) {
		t.Fatal("Vorschau:", c, b)
	}
	if n := aiDocs(t, srv, "carl"); len(n) != 0 {
		t.Fatal("Vorschau darf nichts anlegen:", n)
	}
	// Anlegen: neues Dokument im Besitz von carl, sonst nichts
	c, b = req(t, srv, "carl", "POST", "/api/ai/create", aiProp)
	if c != 200 || !strings.Contains(b, `"id"`) {
		t.Fatal(c, b)
	}
	if n := aiDocs(t, srv, "carl"); len(n) != 1 || n[0] != "Elternbrief@carl" {
		t.Fatal(n)
	}
	if n := aiDocs(t, srv, "bob"); len(n) != 0 {
		t.Fatal("kein Zugriff für andere:", n)
	}
	// Tabelle mit erlaubter Formel
	c, b = req(t, srv, "carl", "POST", "/api/ai/create", `{"type":"sheet","name":"Punkte","rows":[["Name","Punkte"],["Anna","12"],["Bert","8"],["Summe","=SUM(B2:B3)"]],"confirm":true}`)
	if c != 200 {
		t.Fatal(c, b)
	}
	var d struct{ ID string }
	json.Unmarshal([]byte(b), &d)
	if len(d.ID) < 8 {
		t.Fatal(b)
	}

	// Rechte des Benutzers gelten weiter: fred hat nur Text
	req(t, srv, "anna", "POST", "/api/groups/k9/ai", `{"mode":"member"}`)
	if c, _ := req(t, srv, "fred", "POST", "/api/ai/create", `{"type":"sheet","name":"x","rows":[["1"]],"confirm":true}`); c != 403 {
		t.Fatal("fred ohne Calc-Recht:", c)
	}
	if c, b := req(t, srv, "fred", "POST", "/api/ai/create", aiProp); c != 200 {
		t.Fatal(c, b)
	}

	// Prüfungen
	long := make([]string, 301)
	for i := range long {
		long[i] = "x"
	}
	lj, _ := json.Marshal(map[string]any{"type": "text", "name": "lang", "paragraphs": long, "confirm": true})
	bad := map[string]string{
		"Typ":              `{"type":"html","name":"x","paragraphs":["a"],"confirm":true}`,
		"Name leer":        `{"type":"text","name":"  ","paragraphs":["a"],"confirm":true}`,
		"leer":             `{"type":"text","name":"x","paragraphs":["","  "],"confirm":true}`,
		"gemischt":         `{"type":"text","name":"x","paragraphs":["a"],"rows":[["b"]],"confirm":true}`,
		"HYPERLINK":        `{"type":"sheet","name":"x","rows":[["=HYPERLINK(\"http://evil\",\"klick\")"]],"confirm":true}`,
		"Formelzeichen":    `{"type":"sheet","name":"x","rows":[["=A1&B1"]],"confirm":true}`,
		"zu viele Spalten": `{"type":"sheet","name":"x","rows":[["1","2","3","4","5","6","7","8","9","10","11","12","13","14","15","16","17","18","19","20","21","22","23","24","25","26","27"]],"confirm":true}`,
		"zu lang":          string(lj),
	}
	for k, v := range bad {
		if c, b := req(t, srv, "dora", "POST", "/api/ai/create", v); c != 400 {
			t.Fatal(k, c, b)
		}
	}
	// Steuerzeichen und Zeilenumbrüche werden bereinigt, die Vorschau zeigt die bereinigte Fassung
	_, b = req(t, srv, "dora", "POST", "/api/ai/create", `{"type":"text","name":"a\u0000b\u0007","paragraphs":["eins\nzwei\u0000"],"confirm":false}`)
	var pv struct{ Proposal Proposal0 }
	json.Unmarshal([]byte(b), &pv)
	if pv.Proposal.Name != "ab" || len(pv.Proposal.Paragraphs) != 2 || pv.Proposal.Paragraphs[1] != "zwei" {
		t.Fatal("Bereinigung:", b)
	}
	// ohne Anmeldung / falscher Weg
	if c, _ := req(t, srv, "bob", "GET", "/api/ai/create", ""); c == 200 {
		t.Fatal("GET darf nichts liefern")
	}
}

type Proposal0 struct {
	Type, Name string
	Paragraphs []string
}
