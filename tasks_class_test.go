package main

import (
	"encoding/json"
	"strings"
	"testing"
)

type tkRow struct {
	ID, Title, Assignee, Of, Status string
	Class                           bool
	Kt, Kd                          int
	Labels                          []string
}

func tkList(t *testing.T, user string, srv interface{ Close() }, get func(string) (int, string)) []tkRow {
	t.Helper()
	c, b := get(user)
	if c != 200 {
		t.Fatal(user, c, b)
	}
	var r struct{ Tasks []tkRow }
	if json.Unmarshal([]byte(b), &r) != nil {
		t.Fatal("json:", b)
	}
	return r.Tasks
}

// Labels, Klassenaufgabe (an alle Mitglieder verteilen), private Kopien.
func TestTaskLabelsAndClass(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	for _, n := range []string{"cara", "dave", "eve"} {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k5","areas":["files"],"admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/groups/k5/members", `{"add":["bob","cara","dave"]}`)
	list := func(user string) []tkRow {
		return tkList(t, user, srv, func(u string) (int, string) { return req(t, srv, u, "GET", "/api/tasks", "") })
	}

	// Labels: getrimmt, Leerzeichen zusammengefasst, Doppelte (ohne Gross-/Kleinschreibung) entfallen
	c, b := req(t, srv, "anna", "POST", "/api/tasks", `{"title":"Referat","group":"k5","due":"2099-05-01","labels":["Mathe"," Haus  aufgabe ","mathe","x"],"miles":[{"text":"Gliederung"}]}`)
	if c != 200 {
		t.Fatal(c, b)
	}
	id := taskID(t, b)
	if c, b := req(t, srv, "anna", "GET", "/api/tasks/"+id, ""); c != 200 || !strings.Contains(b, `"labels":["Mathe","Haus aufgabe","x"]`) {
		t.Fatal("Labels:", c, b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/tasks", `{"title":"viele","labels":["1","2","3","4","5","6","7","8","9"]}`); c != 400 {
		t.Fatal("mehr als 8 Labels", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/tasks", `{"title":"lang","labels":["`+strings.Repeat("a", 25)+`"]}`); c != 400 {
		t.Fatal("Label ueber 24 Zeichen", c)
	}

	// verteilen: nur der Verantwortliche, nur freie offene Gruppenaufgaben
	if c, _ := req(t, srv, "bob", "POST", "/api/tasks/"+id+"/distribute", ""); c != 403 && c != 404 {
		t.Fatal("Mitglied darf nicht verteilen", c)
	}
	c, b = req(t, srv, "anna", "POST", "/api/tasks/"+id+"/distribute", "")
	if c != 200 || !strings.Contains(b, `"n":3`) {
		t.Fatal("verteilen:", c, b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/tasks/"+id+"/distribute", ""); c != 400 {
		t.Fatal("zweites Verteilen", c)
	}

	// jedes Mitglied sieht nur die eigene Kopie, Fremde nichts
	mine := func(user string) tkRow {
		var out []tkRow
		for _, x := range list(user) {
			if x.Title == "Referat" {
				out = append(out, x)
			}
		}
		if len(out) != 1 || out[0].Assignee != user || out[0].Of != id || len(out[0].Labels) != 3 {
			t.Fatalf("%s sieht: %+v", user, out)
		}
		return out[0]
	}
	bobT, caraT := mine("bob"), mine("cara")
	if n := len(list("eve")); n != 0 {
		t.Fatal("Nichtmitglied sieht Aufgaben:", n)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/tasks/"+caraT.ID, ""); c != 404 {
		t.Fatal("bob sieht die Kopie von cara", c)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/tasks/"+id, ""); c != 404 {
		t.Fatal("bob sieht die Klassenaufgabe", c)
	}

	// Abgabe (erledigt melden) und Abnahme
	act := func(user, tid, body string) int {
		c, _ := req(t, srv, user, "POST", "/api/tasks/"+tid+"/act", body)
		return c
	}
	if act("bob", bobT.ID, `{"a":"comment","text":"abgegeben: Ordner/Referat.docx"}`) != 200 || act("bob", bobT.ID, `{"a":"mile","mile":1,"done":true}`) != 200 || act("bob", bobT.ID, `{"a":"status","status":"done"}`) != 200 {
		t.Fatal("Abgabe")
	}
	if act("cara", bobT.ID, `{"a":"status","status":"done"}`) != 404 {
		t.Fatal("cara bearbeitet die Kopie von bob")
	}
	c, b = req(t, srv, "anna", "GET", "/api/tasks/"+id, "")
	var d struct {
		Task struct {
			Class  bool
			Kt, Kd int
		}
		Kids []struct{ ID, Assignee, Status string }
	}
	if c != 200 || json.Unmarshal([]byte(b), &d) != nil || !d.Task.Class || d.Task.Kt != 3 || d.Task.Kd != 1 || len(d.Kids) != 3 {
		t.Fatal("Fortschritt der Klassenaufgabe:", c, b)
	}
	if act("anna", bobT.ID, `{"a":"status","status":"closed"}`) != 200 {
		t.Fatal("Abnahme")
	}
	// die Kopien erscheinen in der Liste des Auftraggebers weiterhin (die Oberflaeche blendet sie aus), die Klassenaufgabe traegt kein "take"
	for _, x := range list("anna") {
		if x.ID == id && (!x.Class || x.Kt != 3 || x.Kd != 1) {
			t.Fatalf("Listenansicht: %+v", x)
		}
	}
}
