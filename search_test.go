package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Globale Suche: Treffer nur dort, wo der Benutzer lesen darf.
func TestGlobalSearch(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, b := req(t, srv, "anna", "POST", "/api/docs", `{"name":"Quartalsbericht","type":"text"}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/tasks", `{"title":"Quartalsbericht pruefen","labels":["Quartal"]}`); c != 200 {
		t.Fatal(c, b)
	}
	type res struct {
		Tasks, Events, Docs, Files, Chat []struct{ Kind, ID, Title, Snippet, Where string }
	}
	search := func(user, q string) (int, res) {
		c, b := req(t, srv, user, "GET", "/api/search?q="+q, "")
		var r res
		if c == 200 && json.Unmarshal([]byte(b), &r) != nil {
			t.Fatal("json:", b)
		}
		if c == 200 && !strings.Contains(b, `"events":[]`) {
			t.Fatal("alle Bereiche muessen als Liste vorkommen:", b)
		}
		return c, r
	}
	if c, r := search("anna", "quartals"); c != 200 || len(r.Tasks) != 1 || len(r.Docs) != 1 || r.Docs[0].Kind != "text" || r.Docs[0].Title != "Quartalsbericht" {
		t.Fatal("anna findet:", c, r)
	}
	if c, r := search("bob", "quartals"); c != 200 || len(r.Tasks)+len(r.Docs) != 0 {
		t.Fatal("bob findet fremde Inhalte:", c, r)
	}
	// Label wird gefunden
	if _, r := search("anna", "quartal"); len(r.Tasks) != 1 {
		t.Fatal("Label-Suche", r)
	}
	if c, _ := search("anna", "q"); c != 400 {
		t.Fatal("zu kurze Suche", c)
	}
	if c, _ := req(t, srv, "", "GET", "/api/search?q=abc", ""); c == 200 {
		t.Fatal("ohne Anmeldung")
	}
}
