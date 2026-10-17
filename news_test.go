package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"cs-team/news"
)

type nRow struct {
	ID, Title, Text, Group, By, Until string
	Can                               bool
}

// Aushaenge: wer darf veroeffentlichen, wer sieht sie, Pruefung der Eingaben, Loeschen, Speicher.
func TestNewsNotices(t *testing.T) {
	srv, st := setup(t)
	defer srv.Close()
	for _, n := range []string{"cara", "dave", "eve"} {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	for _, g := range []string{`{"name":"k5","areas":["files"],"admins":["cara"]}`, `{"name":"k6","areas":["files"],"admins":["anna"]}`} {
		if c, b := req(t, srv, "anna", "POST", "/api/groups", g); c != 200 {
			t.Fatal(c, b)
		}
	}
	req(t, srv, "anna", "POST", "/api/groups/k5/members", `{"add":["dave"]}`)

	type listed struct {
		Items  []nRow
		Can    bool
		All    bool
		Groups []string
	}
	list := func(user string) listed {
		c, b := req(t, srv, user, "GET", "/api/news", "")
		var l listed
		if c != 200 || json.Unmarshal([]byte(b), &l) != nil {
			t.Fatal(user, c, b)
		}
		return l
	}
	post := func(user, body string) (int, string) { return req(t, srv, user, "POST", "/api/news", body) }
	titles := func(l listed) string {
		var s []string
		for _, i := range l.Items {
			s = append(s, i.Title)
		}
		return strings.Join(s, ",")
	}
	idOf := func(l listed, title string) string {
		for _, i := range l.Items {
			if i.Title == title {
				return i.ID
			}
		}
		t.Fatal("Aushang fehlt:", title)
		return ""
	}

	// Wer darf veroeffentlichen
	if l := list("bob"); l.Can || l.All || len(l.Groups) != 0 {
		t.Fatalf("bob: %+v", l)
	}
	if l := list("anna"); !l.Can || !l.All || len(l.Groups) < 2 {
		t.Fatalf("anna: %+v", l)
	}
	if l := list("cara"); !l.Can || l.All || len(l.Groups) != 1 || l.Groups[0] != "k5" {
		t.Fatalf("cara (Gruppen-Admin): %+v", l)
	}
	for _, tc := range []struct {
		who, body string
		want      int
		what      string
	}{
		{"bob", `{"title":"x","text":"y"}`, 403, "Mitglied an alle"},
		{"dave", `{"title":"x","text":"y","group":"k5"}`, 403, "Mitglied an die eigene Gruppe"},
		{"cara", `{"title":"x","text":"y"}`, 403, "Gruppen-Admin an alle"},
		{"cara", `{"title":"x","text":"y","group":"k6"}`, 403, "Gruppen-Admin an fremde Gruppe"},
		{"anna", `{"title":"x","text":"y","group":"gibtsnicht"}`, 400, "unbekannte Gruppe"},
		{"anna", `{"title":"  ","text":"y"}`, 400, "leerer Titel"},
		{"anna", `{"title":"x","text":""}`, 400, "leerer Text"},
		{"anna", `{"title":"` + strings.Repeat("t", 121) + `","text":"y"}`, 400, "Titel zu lang"},
		{"anna", `{"title":"x","text":"` + strings.Repeat("z", 4001) + `"}`, 400, "Text zu lang"},
		{"anna", `{"title":"x","text":"y","until":"2020-01-01"}`, 400, "Datum in der Vergangenheit"},
		{"anna", `{"title":"x","text":"y","until":"morgen"}`, 400, "kein Datum"},
		{"anna", `{"title":"x","text":"y","until":"2099-01-01"}`, 400, "Datum zu weit"},
		{"anna", `{kaputt`, 400, "kaputtes JSON"},
	} {
		if c, b := post(tc.who, tc.body); c != tc.want {
			t.Fatalf("%s: %d %s", tc.what, c, b)
		}
	}

	// Veroeffentlichen und Sichtbarkeit
	until := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	if c, b := post("anna", `{"title":"Allen","text":"Hallo zusammen","until":"`+until+`"}`); c != 200 || !strings.Contains(b, `"id"`) {
		t.Fatal(c, b)
	}
	time.Sleep(1100 * time.Millisecond) // Sortierung nach Sekunden: der zweite Aushang ist sicher neuer
	if c, b := post("cara", `{"title":"Nur k5","text":"Klassenarbeit am Donnerstag","group":"k5"}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := post("anna", `{"title":"Nur k6","text":"intern","group":"k6"}`); c != 200 {
		t.Fatal(c, b)
	}
	if got := titles(list("anna")); got != "Nur k6,Nur k5,Allen" {
		t.Fatalf("anna sieht: %s", got)
	}
	if got := titles(list("dave")); got != "Nur k5,Allen" {
		t.Fatalf("dave (Mitglied k5) sieht: %s", got)
	}
	if got := titles(list("eve")); got != "Allen" {
		t.Fatalf("eve (keine Gruppe) sieht: %s", got)
	}
	if got := titles(list("cara")); got != "Nur k5,Allen" {
		t.Fatalf("cara (Admin k5) sieht: %s", got)
	}
	la := list("anna")
	for _, i := range la.Items {
		if !i.Can {
			t.Fatalf("Admin darf alles loeschen: %+v", i)
		}
	}
	for _, i := range list("dave").Items {
		if i.Can {
			t.Fatalf("Mitglied darf nichts loeschen: %+v", i)
		}
	}
	allen := idOf(la, "Allen")
	k5 := idOf(la, "Nur k5")
	k6 := idOf(la, "Nur k6")
	if r := list("dave").Items; r[1].Until != until || r[0].By != "cara" || r[1].By != "anna" {
		t.Fatalf("Felder: %+v", r)
	}

	// Loeschen: nur Autor, Admin oder Gruppen-Admin; wer es nicht sehen darf, bekommt 404
	del := func(user, id string) int { c, _ := req(t, srv, user, "DELETE", "/api/news/"+id, ""); return c }
	for _, tc := range []struct {
		who, id string
		want    int
		what    string
	}{
		{"dave", allen, 403, "Mitglied loescht Aushang an alle"},
		{"dave", k5, 403, "Mitglied loescht Gruppen-Aushang"},
		{"eve", k5, 404, "fremde Gruppe sieht den Aushang nicht"},
		{"cara", k6, 404, "Gruppen-Admin einer anderen Gruppe"},
		{"cara", allen, 403, "Gruppen-Admin loescht Aushang an alle"},
		{"anna", "gibtsnicht", 404, "unbekannte Nummer"},
		{"cara", k5, 200, "Autor loescht eigenen Aushang"},
		{"cara", k5, 404, "zweites Loeschen"},
	} {
		if c := del(tc.who, tc.id); c != tc.want {
			t.Fatalf("%s: %d", tc.what, c)
		}
	}
	if got := titles(list("anna")); got != "Nur k6,Allen" {
		t.Fatalf("nach dem Loeschen: %s", got)
	}
	if c := del("anna", allen); c != 200 { // globaler Admin
		t.Fatal("Admin loescht", c)
	}

	// Speicher und Haken fuer Gruppen umbenennen / Benutzer loeschen: neue Instanz liest aus dem Speicher
	ns := news.New(st)
	ctx := context.Background()
	if n := ns.Count(ctx, "anna"); n != 1 || ns.Count(ctx, "cara") != 0 {
		t.Fatal("aus dem Speicher geladen:", n)
	}
	if err := ns.RenameGroup(ctx, "k6", "k7"); err != nil {
		t.Fatal(err)
	}
	if n, err := ns.Anon(ctx, "anna", "[geloescht]"); err != nil || n != 1 || ns.Count(ctx, "anna") != 0 || ns.Count(ctx, "[geloescht]") != 1 {
		t.Fatal("anonymisieren:", n, err)
	}
	if err := ns.Purge(ctx, "[geloescht]"); err != nil || ns.Count(ctx, "[geloescht]") != 0 {
		t.Fatal("loeschen:", err)
	}
	if got := news.New(st).Count(ctx, "[geloescht]"); got != 0 {
		t.Fatal("Speicher nach dem Loeschen:", got)
	}
}
