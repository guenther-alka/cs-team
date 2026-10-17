package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func unzipAll(t *testing.T, b string) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader([]byte(b)), int64(len(b)))
	if err != nil {
		t.Fatalf("kein ZIP: %v (%.80q)", err, b)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		d, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(d)
	}
	return out
}

func hasKey(m map[string]string, prefix, suffix string) bool {
	for k := range m {
		if strings.HasPrefix(k, prefix) && strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// Datenauskunft vollstaendig (Dateien, Dokumente, Chat samt Anhaengen und 1:1, Aushaenge, Konto) und Loeschen vollstaendig
// (1:1-Kanaele, Anhaenge, Aushaenge, auf Wunsch die Nachrichten).
func TestDataExportAndDeleteComplete(t *testing.T) {
	srv, st := setup(t)
	defer srv.Close()
	ctx := context.Background()
	for _, n := range []string{"dora", "eddy"} {
		if c, b := req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`); c != 200 {
			t.Fatal(c, b)
		}
	}
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k7","areas":["files"],"chat":"member","chans":"member","msg":"member","admins":["anna","dora"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/groups/k7/members", `{"add":["bob","dora","eddy"]}`)
	if c, b := req(t, srv, "anna", "POST", "/api/groups/k7/dm", `{"mode":"member"}`); c != 200 {
		t.Fatal(c, b)
	}
	png := "\x89PNG\r\n\x1a\nrest"
	post := func(user, cn, name, text string) int {
		c, _ := req(t, srv, user, "POST", "/api/chat/k7/"+cn+"/upload?name="+name+"&text="+strings.ReplaceAll(text, " ", "%20"), png)
		return c
	}
	// Daten von dora
	if c, b := req(t, srv, "dora", "POST", "/api/files?name=d.txt", "inhalt"); c != 200 {
		t.Fatal(c, b)
	}
	for _, d := range []string{`{"name":"tabelle","type":"sheet"}`, `{"name":"brief","type":"text"}`} {
		if c, b := req(t, srv, "dora", "POST", "/api/docs", d); c != 200 {
			t.Fatal(c, b)
		}
	}
	if c, b := req(t, srv, "dora", "POST", "/api/news", `{"title":"Infotitel","text":"Infotext","group":"k7"}`); c != 200 {
		t.Fatal(c, b)
	}
	if c := post("dora", "allgemein", "doras.png", "hallo welt"); c != 200 {
		t.Fatal("upload dora:", c)
	}
	if c := post("eddy", "allgemein", "eddys.png", "von eddy"); c != 200 {
		t.Fatal("upload eddy:", c)
	}
	c, b := req(t, srv, "dora", "POST", "/api/chat/k7/dm", `{"with":"bob"}`)
	var dm struct{ Name string }
	json.Unmarshal([]byte(b), &dm)
	if c != 200 || !strings.HasPrefix(dm.Name, "dm-") {
		t.Fatal("1:1 oeffnen:", c, b)
	}
	if c := post("dora", dm.Name, "privat.png", "geheim fuer bob"); c != 200 {
		t.Fatal("upload 1:1:", c)
	}

	// 1. Datenauskunft: alles drin, nichts von anderen
	c, b = req(t, srv, "anna", "GET", "/api/users/dora/export", "")
	if c != 200 {
		t.Fatal("export:", c, b)
	}
	z := unzipAll(t, b)
	for _, k := range []string{"README.txt", "account.json", "chat.json", "notices.json", "files/d.txt"} {
		if _, ok := z[k]; !ok {
			t.Fatalf("%s fehlt im ZIP", k)
		}
	}
	if !hasKey(z, "documents/", ".xlsx") || !hasKey(z, "documents/", ".docx") {
		t.Fatalf("Dokumente fehlen: %v", keys(z))
	}
	if !hasKey(z, "chat-anhaenge/k7/allgemein/", "doras.png") || !hasKey(z, "chat-anhaenge/k7/"+dm.Name+"/", "privat.png") {
		t.Fatalf("Chat-Anhaenge fehlen: %v", keys(z))
	}
	if hasKey(z, "chat-anhaenge/", "eddys.png") || strings.Contains(z["chat.json"], "von eddy") {
		t.Fatal("Inhalt anderer Personen im ZIP")
	}
	if !strings.Contains(z["notices.json"], "Infotitel") || !strings.Contains(z["account.json"], `"notifyOff"`) || !strings.Contains(z["account.json"], `"guest"`) {
		t.Fatalf("Aushang/Konto: %s %s", z["notices.json"], z["account.json"])
	}
	if strings.Contains(z["README.txt"], "bitte beim Administrator anfragen") {
		t.Fatal("README nennt Dokumente noch als fehlend")
	}
	if !strings.Contains(z["chat.json"], "geheim fuer bob") {
		t.Fatal("eigene 1:1-Nachricht fehlt in chat.json")
	}

	// 2. Vorschau und Loeschen ohne "hart": 1:1 und Anhaenge weg, Text bleibt anonymisiert
	c, b = req(t, srv, "anna", "GET", "/api/users/dora/delete/preview", "")
	var pv struct {
		Counts   map[string]int
		Messages int
	}
	json.Unmarshal([]byte(b), &pv)
	if c != 200 || pv.Counts["chat"] != 2 || pv.Messages != 1 || pv.Counts["news"] != 1 {
		t.Fatalf("Vorschau: %d %s", c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/users/dora/delete", `{"password":"passwort-anna","noSnapshot":true}`); c != 200 {
		t.Fatal(c, b)
	}
	_, h := req(t, srv, "bob", "GET", "/api/chat/k7/allgemein/history", "")
	if !strings.Contains(h, "hallo welt") || !strings.Contains(h, "gelöschter Benutzer") || strings.Contains(h, "doras.png") || !strings.Contains(h, "eddys.png") {
		t.Fatalf("Gruppen-Chat nach dem Loeschen: %s", h)
	}
	if _, g := req(t, srv, "bob", "GET", "/api/chat/groups", ""); strings.Contains(g, "dm-") {
		t.Fatalf("1:1-Kanal ist noch in der Kanalliste: %s", g)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/chat/k7/"+dm.Name+"/history", ""); c == 200 {
		t.Fatal("1:1-Verlauf ist noch lesbar")
	}
	if _, nb := req(t, srv, "bob", "GET", "/api/news", ""); strings.Contains(nb, "Infotitel") {
		t.Fatal("Aushang ist noch da")
	}
	if infos, _ := st.List(ctx, "chat/f/k7/"+dm.Name+"/"); len(infos) != 0 {
		t.Fatalf("Anhaenge des 1:1-Kanals liegen noch: %d", len(infos))
	}

	// 3. Option "hart loeschen": die Nachrichten verschwinden ganz (Anhang inklusive), fremde bleiben
	c, b = req(t, srv, "anna", "GET", "/api/users/eddy/delete/preview", "")
	json.Unmarshal([]byte(b), &pv)
	if c != 200 || pv.Messages != 1 {
		t.Fatalf("Vorschau eddy: %d %s", c, b)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/users/eddy/delete", `{"password":"passwort-anna","noSnapshot":true,"hardDelete":true}`); c != 200 {
		t.Fatal(c, b)
	}
	_, h = req(t, srv, "bob", "GET", "/api/chat/k7/allgemein/history", "")
	if strings.Contains(h, "von eddy") || strings.Contains(h, "eddys.png") || !strings.Contains(h, "hallo welt") {
		t.Fatalf("hart loeschen: %s", h)
	}
	if infos, _ := st.List(ctx, "chat/f/k7/"); len(infos) != 0 {
		t.Fatalf("Anhaenge liegen noch: %d", len(infos))
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
