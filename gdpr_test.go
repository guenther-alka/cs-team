package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const anonName = "gelöschter Benutzer"

// Konten und eine Gruppe mit Chat/Aufgaben für die DSGVO-Tests.
func gdprGroup(t *testing.T, srv *httptest.Server, grp string, users ...string) {
	t.Helper()
	for _, n := range users {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"`+grp+`","areas":["files"],"admins":["anna"],"msg":"member"}`); c != 200 {
		t.Fatal(c, b)
	}
	add, _ := json.Marshal(append([]string{"bob"}, users...))
	if c, b := req(t, srv, "anna", "POST", "/api/groups/"+grp+"/members", `{"add":`+string(add)+`}`); c != 200 {
		t.Fatal(c, b)
	}
}

func TestUserPurgeAnonymize(t *testing.T) {
	srv, st := setup(t)
	defer srv.Close()
	gdprGroup(t, srv, "k8", "carl", "dora")
	// Inhalte von carl: Chat-Nachricht, Aufgabe (mit Kommentar von bob), Aufgabe von bob an carl, Termin mit carl als Teilnehmer/Organisator
	if c, b := req(t, srv, "carl", "POST", "/api/message", `{"group":"k8","body":"Hallo zusammen","chat":true}`); c != 200 {
		t.Fatal(c, b)
	}
	_, b := req(t, srv, "carl", "POST", "/api/tasks", `{"title":"Erste Aufgabe","group":"k8","assignee":"bob"}`)
	id1 := taskID(t, b)
	req(t, srv, "bob", "POST", "/api/tasks/"+id1+"/act", `{"a":"comment","text":"danke"}`)
	_, b = req(t, srv, "bob", "POST", "/api/tasks", `{"title":"Zweite Aufgabe","group":"k8","assignee":"carl","watch":["dora"]}`)
	id2 := taskID(t, b)
	ev := strings.Replace(ics, "SUMMARY:Test", "SUMMARY:Test\r\nORGANIZER;CN=carl:urn:cs-team:carl\r\nATTENDEE;CN=carl;PARTSTAT=ACCEPTED:urn:cs-team:carl\r\nATTENDEE;CN=bob:urn:cs-team:bob", 1)
	if c, b := req(t, srv, "anna", "PUT", "/dav/anna/cal/default/e9.ics", ev, "Content-Type", "text/calendar", "If-None-Match", "*"); c != 201 && c != 204 {
		t.Fatal(c, b)
	}
	// Vorschau nennt die Zahlen (und die Ersatzbezeichnung)
	c, b := req(t, srv, "anna", "GET", "/api/users/carl/delete/preview", "")
	var pv struct {
		Anon        map[string]int
		Replacement string
	}
	json.Unmarshal([]byte(b), &pv)
	if c != 200 || pv.Anon["chat"] < 1 || pv.Anon["tasks"] != 2 || pv.Anon["events"] != 1 || pv.Replacement != anonName {
		t.Fatal("Vorschau:", c, b)
	}
	// Standard: anonymisieren
	c, b = req(t, srv, "anna", "POST", "/api/users/carl/delete", `{"password":"passwort-anna","noSnapshot":true}`)
	var del struct{ Anon map[string]int }
	json.Unmarshal([]byte(b), &del)
	if c != 200 || del.Anon["chat"] < 1 || del.Anon["tasks"] != 2 || del.Anon["events"] != 1 {
		t.Fatal("löschen:", c, b)
	}
	_, tb := req(t, srv, "bob", "GET", "/api/tasks", "")
	if strings.Contains(tb, `"carl"`) || strings.Count(tb, anonName) < 2 {
		t.Fatal("Aufgaben:", tb)
	}
	_, tb = req(t, srv, "bob", "GET", "/api/tasks/"+id1, "")
	if strings.Contains(tb, "carl") || !strings.Contains(tb, `"by":"`+anonName+`"`) || !strings.Contains(tb, "danke") {
		t.Fatal("Verlauf:", tb)
	}
	_, tb = req(t, srv, "bob", "GET", "/api/tasks/"+id2, "")
	if strings.Contains(tb, "carl") || !strings.Contains(tb, `"assignee":"`+anonName+`"`) || !strings.Contains(tb, `"dora"`) {
		t.Fatal("Bearbeiter:", tb)
	}
	_, ch := req(t, srv, "bob", "GET", "/api/chat/k8/allgemein/history", "")
	if strings.Contains(ch, "carl") || !strings.Contains(ch, anonName) || !strings.Contains(ch, "Hallo zusammen") {
		t.Fatal("Chat:", ch)
	}
	_, e := req(t, srv, "anna", "GET", "/dav/anna/cal/default/e9.ics", "")
	if strings.Contains(e, "carl") || strings.Count(e, anonName) < 2 || !strings.Contains(e, "bob") {
		t.Fatal("Termin:", e)
	}
	// das Löschprotokoll enthält nur Zahlen, keinen Inhalt
	raw, _, _ := st.Get(context.Background(), "users/_delete-log.json")
	if !strings.Contains(string(raw), `"anon"`) || strings.Contains(string(raw), "Hallo") || strings.Contains(string(raw), "Erste Aufgabe") {
		t.Fatal("Löschprotokoll:", string(raw))
	}

	// Ohne Anonymisierung (anonymize:false) bleibt der Name stehen
	if c, b := req(t, srv, "dora", "POST", "/api/message", `{"group":"k8","body":"Hallo alle","chat":true}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/users/dora/delete", `{"password":"passwort-anna","noSnapshot":true,"anonymize":false}`); c != 200 || !strings.Contains(b, `"anon":null`) {
		t.Fatal("löschen ohne Anonymisierung:", c, b)
	}
	_, ch = req(t, srv, "bob", "GET", "/api/chat/k8/allgemein/history", "")
	if !strings.Contains(ch, "dora") {
		t.Fatal("Name ohne Anonymisierung entfernt:", ch)
	}
}

func unzip(t *testing.T, body string) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader([]byte(body)), int64(len(body)))
	if err != nil {
		t.Fatal("kein ZIP:", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, `\`) {
			t.Fatal("unsicherer Name im ZIP:", f.Name)
		}
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestDataExport(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	gdprGroup(t, srv, "k9", "carl")
	req(t, srv, "carl", "POST", "/api/files?name=carl.txt", "inhalt-carl")
	req(t, srv, "anna", "POST", "/api/files?name=anna.txt", "inhalt-anna")
	req(t, srv, "carl", "PUT", "/dav/carl/cal/default/e1.ics", ics, "Content-Type", "text/calendar", "If-None-Match", "*")
	req(t, srv, "carl", "POST", "/api/message", `{"group":"k9","body":"Chat von carl","chat":true}`)
	req(t, srv, "carl", "POST", "/api/tasks", `{"title":"Aufgabe von carl","group":"k9"}`)
	req(t, srv, "bob", "POST", "/api/tasks", `{"title":"Aufgabe von bob","group":"k9"}`)

	r, _ := http.NewRequest("GET", srv.URL+"/api/me/export", nil)
	r.SetBasicAuth("carl", "passwort-carl")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), `attachment; filename="cs-team-daten-carl-`) {
		t.Fatal("Export:", resp.StatusCode, resp.Header)
	}
	z := unzip(t, string(raw))
	var cal string
	for n, v := range z {
		if strings.HasPrefix(n, "calendars/") && strings.HasSuffix(n, ".ics") && strings.Contains(v, "SUMMARY:Test") {
			cal = n
		}
	}
	if z["files/carl.txt"] != "inhalt-carl" || cal == "" || !strings.Contains(z["tasks.json"], "Aufgabe von carl") || strings.Contains(z["tasks.json"], "Aufgabe von bob") ||
		!strings.Contains(z["chat.json"], "Chat von carl") || !strings.Contains(z["README.txt"], "Nicht enthalten") {
		t.Fatalf("Inhalt unvollständig: %v", func() []string {
			var l []string
			for n := range z {
				l = append(l, n)
			}
			return l
		}())
	}
	if _, ok := z["files/anna.txt"]; ok {
		t.Fatal("fremde Datei im Export")
	}
	acc := z["account.json"]
	if !strings.Contains(acc, `"name": "carl"`) || !strings.Contains(acc, "alluser") || strings.Contains(strings.ToLower(acc), "hash") || strings.Contains(acc, "$2") {
		t.Fatal("account.json:", acc)
	}
	for n, v := range z { // nirgends ein Passwort-Hash
		if strings.Contains(v, "$2a$") || strings.Contains(v, "$2b$") {
			t.Fatal("Hash in", n)
		}
	}
	// fremde Daten: Nicht-Admin darf nicht, Admin darf
	if c, _ := req(t, srv, "bob", "GET", "/api/users/carl/export", ""); c != 403 {
		t.Fatal("bob darf carl nicht exportieren:", c)
	}
	if c, b := req(t, srv, "anna", "GET", "/api/users/carl/export", ""); c != 200 || !strings.Contains(unzip(t, b)["account.json"], `"name": "carl"`) {
		t.Fatal("Admin-Export:", c)
	}
	if c, _ := req(t, srv, "anna", "GET", "/api/users/gibtsnicht/export", ""); c != 404 {
		t.Fatal("unbekannter Benutzer:", c)
	}
	// jeder bekommt nur die eigenen Daten
	_, b := req(t, srv, "bob", "GET", "/api/me/export", "")
	zb := unzip(t, b)
	if !strings.Contains(zb["account.json"], `"name": "bob"`) || strings.Contains(zb["chat.json"], "Chat von carl") || zb["files/carl.txt"] != "" {
		t.Fatal("bob sieht Daten von carl")
	}
}

func TestAckAndPrivacy(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	me := func(user string) (ack map[string]bool, addr map[string]string, privacy string) {
		_, b := req(t, srv, user, "GET", "/api/me", "")
		var m struct {
			Ack     map[string]bool
			AckAddr map[string]string
			Privacy string
		}
		if json.Unmarshal([]byte(b), &m) != nil {
			t.Fatal(b)
		}
		return m.Ack, m.AckAddr, m.Privacy
	}
	if a, ad, _ := me("bob"); a["video"] || a["ai"] || len(ad) != 0 {
		t.Fatal("nichts eingerichtet:", a, ad)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/me/ack", `{"key":"video","addr":"x"}`); c != 400 {
		t.Fatal("nicht eingerichtet muss 400 sein:", c)
	}
	video := func(host string) {
		if c, b := req(t, srv, "anna", "POST", "/api/settings/video", `{"options":[{"name":"Meet","url":"https://`+host+`/{room}"}]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	video("meet.example.org")
	a, ad, _ := me("bob")
	if a["video"] || ad["video"] != "meet.example.org" {
		t.Fatal("Video eingerichtet:", a, ad)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/me/ack", `{"key":"video","addr":"anderer.example.org"}`); c != 409 {
		t.Fatal("falsche Adresse muss 409 sein:", c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/me/ack", `{"key":"quatsch","addr":"x"}`); c != 400 {
		t.Fatal("falscher Schlüssel:", c)
	}
	if c, b := req(t, srv, "bob", "POST", "/api/me/ack", `{"key":"video","addr":"meet.example.org"}`); c != 200 {
		t.Fatal(c, b)
	}
	if a, _, _ := me("bob"); !a["video"] {
		t.Fatal("Bestätigung fehlt")
	}
	if a, _, _ := me("anna"); a["video"] {
		t.Fatal("Bestätigung gilt nur für bob")
	}
	video("meet.neu.example.org") // Adresse geändert: Bestätigung verfällt
	if a, ad, _ := me("bob"); a["video"] || ad["video"] != "meet.neu.example.org" {
		t.Fatal("nach Adresswechsel:", a, ad)
	}
	// KI: Anbieter + Rechner
	if c, b := req(t, srv, "anna", "POST", "/api/ai/settings", `{"mode":"provider","provider":"openai","model":"gpt-test"}`); c != 200 {
		t.Fatal(c, b)
	}
	_, ad, _ = me("bob")
	if ad["ai"] != "openai (api.openai.com)" {
		t.Fatal("KI-Adresse:", ad)
	}
	if c, b := req(t, srv, "bob", "POST", "/api/me/ack", `{"key":"ai","addr":"openai (api.openai.com)"}`); c != 200 {
		t.Fatal(c, b)
	}
	if a, _, _ := me("bob"); !a["ai"] || a["video"] {
		t.Fatal("ai bestätigt, video nicht:", a)
	}
	req(t, srv, "anna", "POST", "/api/ai/settings", `{"mode":"provider","provider":"anthropic","model":"m"}`)
	if a, _, _ := me("bob"); a["ai"] {
		t.Fatal("Anbieterwechsel muss die Bestätigung zurücksetzen")
	}
	// zusätzlicher Text des Admins: nur Admin setzt, alle sehen ihn
	if c, _ := req(t, srv, "bob", "POST", "/api/settings/privacy", `{"text":"x"}`); c != 403 {
		t.Fatal("nur Admin:", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/settings/privacy", `{"text":"Ansprechpartner: Datenschutz@Schule"}`); c != 200 {
		t.Fatal(c, b)
	}
	if _, _, p := me("bob"); p != "Ansprechpartner: Datenschutz@Schule" {
		t.Fatal("Datenschutzhinweis:", p)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/settings/privacy", `{"text":"`+strings.Repeat("x", 2001)+`"}`); c != 400 {
		t.Fatal("zu lang:", c)
	}
}

func TestRetentionSettings(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k10","areas":["files"],"admins":["anna"],"chatDays":90}`); c != 200 {
		t.Fatal(c, b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/groups", ""); !strings.Contains(b, `"chatDays":90`) {
		t.Fatal("chatDays beim Anlegen:", b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups/k10", `{"chatDays":30}`); c != 200 {
		t.Fatal(c, b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/groups", ""); !strings.Contains(b, `"chatDays":30`) {
		t.Fatal("chatDays ändern:", b)
	}
	for _, bad := range []string{"-1", "1", "6", "40000"} { // 0 oder mindestens 7 Tage
		if c, _ := req(t, srv, "anna", "POST", "/api/groups/k10", `{"chatDays":`+bad+`}`); c != 400 {
			t.Fatal("chatDays "+bad+" muss 400 sein:", c)
		}
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/groups/k10", `{"chatDays":1}`); c != 403 {
		t.Fatal("nur Admin:", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups/k10", `{"chatDays":7}`); c != 200 { // Untergrenze
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups/k10", `{"chatDays":0}`); c != 200 { // 0 = unbegrenzt
		t.Fatal(c, b)
	}
	// global: abgeschlossene Aufgaben
	if c, b := req(t, srv, "anna", "POST", "/api/settings/closedtasks", `{"days":180}`); c != 200 {
		t.Fatal(c, b)
	}
	if _, b := req(t, srv, "anna", "GET", "/api/settings", ""); !strings.Contains(b, `"closedDays":180`) {
		t.Fatal("closedDays:", b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/settings/closedtasks", `{"days":-5}`); c != 400 {
		t.Fatal("negativ:", c)
	}
	for _, bad := range []string{`{"days":1}`, `{"days":6}`, `null`, `{}`, `{"days":null}`} {
		if c, _ := req(t, srv, "anna", "POST", "/api/settings/closedtasks", bad); c != 400 {
			t.Fatal("closedtasks "+bad+" muss 400 sein:", c)
		}
	}
	if _, b := req(t, srv, "anna", "GET", "/api/settings", ""); !strings.Contains(b, `"closedDays":180`) {
		t.Fatal("abgelehnte Werte dürfen nichts ändern:", b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/settings/closedtasks", `{"days":7}`); c != 200 {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/settings/closedtasks", `{"days":1}`); c != 403 {
		t.Fatal("nur Admin:", c)
	}
}
