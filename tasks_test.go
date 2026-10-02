package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func taskID(t *testing.T, body string) string {
	var r struct{ ID string }
	if json.Unmarshal([]byte(body), &r) != nil || r.ID == "" {
		t.Fatal("keine ID:", body)
	}
	return r.ID
}

func TestTasks(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	for _, n := range []string{"cara", "dave"} {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"t1","areas":["files"],"admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/groups/t1/members", `{"add":["bob","cara"]}`)
	// Gruppenaufgabe von bob (Standard: Mitglieder dürfen anlegen)
	c, b := req(t, srv, "bob", "POST", "/api/tasks", `{"title":"Bericht","group":"t1","prio":2,"due":"2099-01-01","miles":[{"text":"Entwurf","due":"2098-12-01"}]}`)
	if c != 200 {
		t.Fatal(c, b)
	}
	id := taskID(t, b)
	if c, b := req(t, srv, "cara", "GET", "/api/tasks", ""); c != 200 || !strings.Contains(b, `"take":true`) || !strings.Contains(b, "Bericht") {
		t.Fatal("Mitglied sieht die Aufgabe nicht / kann nicht uebernehmen", b)
	}
	if c, _ := req(t, srv, "dave", "GET", "/api/tasks/"+id, ""); c != 404 {
		t.Fatal("Fremder sieht Gruppenaufgabe", c)
	}
	if c, _ := req(t, srv, "dave", "POST", "/api/tasks", `{"title":"x","group":"t1"}`); c != 403 {
		t.Fatal("Nichtmitglied legt Gruppenaufgabe an", c)
	}
	act := func(user, id, body string) int {
		c, _ := req(t, srv, user, "POST", "/api/tasks/"+id+"/act", body)
		return c
	}
	if act("cara", id, `{"a":"take"}`) != 200 || act("bob", id, `{"a":"take"}`) != 403 {
		t.Fatal("take")
	}
	if act("cara", id, `{"a":"mile","mile":1,"done":true}`) != 200 || act("bob", id, `{"a":"comment","text":"Bitte bis Freitag"}`) != 200 || act("cara", id, `{"a":"comment","text":"ok"}`) != 200 {
		t.Fatal("mile/comment")
	}
	_, b = req(t, srv, "cara", "GET", "/api/tasks/"+id, "")
	var d struct {
		Task struct {
			Status, Assignee string
			Log              []struct {
				ID  int64
				By  string
				T   string
				Sys bool
			}
		}
	}
	json.Unmarshal([]byte(b), &d)
	if d.Task.Status != "doing" || d.Task.Assignee != "cara" || len(d.Task.Log) != 5 { // new, take, mile, 2 Kommentare
		t.Fatal(b)
	}
	cid := d.Task.Log[3].ID // bobs Kommentar
	if act("cara", id, `{"a":"edit","cid":`+jsonNum(cid)+`,"text":"x"}`) != 403 {
		t.Fatal("fremden Kommentar bearbeitet")
	}
	if act("bob", id, `{"a":"edit","cid":`+jsonNum(cid)+`,"text":"Bitte bis Montag"}`) != 200 || act("cara", id, `{"a":"delc","cid":`+jsonNum(cid)+`}`) != 403 {
		t.Fatal("edit/delc")
	}
	// Abnahme: Bearbeiter meldet erledigt, nur Auftraggeber/Admin schließt
	if act("cara", id, `{"a":"status","status":"done"}`) != 200 || act("cara", id, `{"a":"status","status":"closed"}`) != 403 || act("bob", id, `{"a":"status","status":"closed"}`) != 200 {
		t.Fatal("status")
	}
	// Bearbeiten der Stammdaten nur Auftraggeber/Gruppen-Admin
	if c, _ := req(t, srv, "cara", "POST", "/api/tasks/"+id, `{"title":"neu"}`); c != 403 {
		t.Fatal("Bearbeiter aendert Stammdaten", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/tasks/"+id, `{"title":"Bericht 2","group":"t1","due":"2099-01-01","assignee":"cara","miles":[{"id":1,"text":"Entwurf","due":"2098-12-01"},{"text":"Final"}]}`); c != 200 {
		t.Fatal(c, b)
	}
	// Wiederholung: nach Abnahme entsteht genau eine Folgeaufgabe
	_, b = req(t, srv, "bob", "POST", "/api/tasks", `{"title":"Backup","group":"t1","assignee":"cara","due":"2099-01-31","repeat":"monthly"}`)
	rid := taskID(t, b)
	if c, _ := req(t, srv, "bob", "POST", "/api/tasks", `{"title":"ohne Datum","repeat":"weekly"}`); c != 400 {
		t.Fatal("Wiederholung ohne Datum", c)
	}
	act("bob", rid, `{"a":"status","status":"closed"}`)
	act("bob", rid, `{"a":"status","status":"doing"}`)
	act("bob", rid, `{"a":"status","status":"closed"}`)
	_, b = req(t, srv, "bob", "GET", "/api/tasks", "")
	if strings.Count(b, `"title":"Backup"`) != 2 || !strings.Contains(b, `"due":"2099-02-28"`) || !strings.Contains(b, `"link":["`+rid+`"]`) {
		t.Fatal("Folgeaufgabe", b)
	}
	// Gruppenmodus: nur Admins dürfen anlegen / aus
	req(t, srv, "anna", "POST", "/api/groups/t1", `{"tasks":"admin"}`)
	// Mitglied in „nur Admins“-Gruppe: Anfrage ohne Zuständigen, für Gruppenmitglieder sichtbar
	if c, b := req(t, srv, "bob", "POST", "/api/tasks", `{"title":"Anfrage","group":"t1","assignee":"bob"}`); c != 200 {
		t.Fatal("Anfrage", c)
	} else {
		id := b[strings.Index(b, `"id":"`)+6:]
		id = id[:strings.Index(id, `"`)]
		if _, g := req(t, srv, "bob", "GET", "/api/tasks/"+id, ""); !strings.Contains(g, `"req":true`) || strings.Contains(g, `"assignee":"bob"`) {
			t.Fatal("Anfrage-Flag", g)
		}
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/tasks", `{"title":"y","group":"t1"}`); c != 200 {
		t.Fatal("Gruppen-Admin darf", c)
	}
	req(t, srv, "anna", "POST", "/api/groups/t1", `{"tasks":"off"}`)
	if c, _ := req(t, srv, "anna", "POST", "/api/tasks", `{"title":"y","group":"t1"}`); c != 403 {
		t.Fatal("Modus off", c)
	}
	if c, _ := req(t, srv, "cara", "GET", "/api/tasks/"+id, ""); c != 200 {
		t.Fatal("Bearbeiter sieht Aufgabe trotz off", c)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/tasks/"+rid, ""); c != 200 {
		t.Fatal(c)
	}
	// Aufgabe ohne Gruppe: nur Beteiligte
	_, b = req(t, srv, "bob", "POST", "/api/tasks", `{"title":"privat","assignee":"cara","watch":["dave"]}`)
	pid := taskID(t, b)
	for u, want := range map[string]int{"bob": 200, "cara": 200, "dave": 200, "anna": 200} {
		if c, _ := req(t, srv, u, "GET", "/api/tasks/"+pid, ""); c != want {
			t.Fatal(u, c)
		}
	}
	req(t, srv, "bob", "POST", "/api/tasks/"+pid, `{"title":"privat","assignee":"cara"}`) // dave als Beteiligter entfernt
	if c, _ := req(t, srv, "dave", "GET", "/api/tasks/"+pid, ""); c != 404 {
		t.Fatal("entfernter Beteiligter sieht noch", c)
	}
	if c, _ := req(t, srv, "cara", "DELETE", "/api/tasks/"+pid, ""); c != 403 {
		t.Fatal("Bearbeiter loescht", c)
	}
	if c, _ := req(t, srv, "bob", "DELETE", "/api/tasks/"+pid, ""); c != 200 {
		t.Fatal("Auftraggeber loescht", c)
	}
}

func jsonNum(n int64) string { b, _ := json.Marshal(n); return string(b) }
