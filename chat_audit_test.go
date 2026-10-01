package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestChatAudit(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	if c, b := req(t, srv, "anna", "POST", "/api/groups", `{"name":"k6","areas":["files"],"chat":"member","chans":"member","msg":"member","admins":["anna"]}`); c != 200 {
		t.Fatal(c, b)
	}
	req(t, srv, "anna", "POST", "/api/users", `{"name":"carl","password":"passwort-carl","groups":["alluser"]}`)
	req(t, srv, "anna", "POST", "/api/groups/k6/members", `{"add":["bob","carl"]}`)
	w := func(c *websocket.Conn, s string) { c.Write(t.Context(), websocket.MessageText, []byte(s)) }
	for _, m := range []struct{ u, t string }{{"bob", "du bist dumm"}, {"bob", "hallo zusammen"}, {"carl", "doof und dumm"}, {"carl", "Mathe morgen?"}} {
		c := chatDial(t, srv, m.u)
		read(t, c)
		w(c, `{"t":"send","g":"k6","c":"allgemein","text":"`+m.t+`"}`)
		read(t, c)
		c.CloseNow()
		time.Sleep(2 * time.Millisecond)
	}
	if c, b := req(t, srv, "bob", "POST", "/api/chat/k6/allgemein/upload?name=notiz.txt&text=Beweis", "geheimer Anhang"); c != 200 {
		t.Fatal(c, b)
	}
	type res struct {
		Hits []struct {
			G, C string
			ID   int64
			By   string
			T    string
			Ctx  bool
			Att  *struct{ ID string }
		}
		Total int
		Users map[string]int
	}
	search := func(user, body string) (int, res) {
		c, b := req(t, srv, user, "POST", "/api/routines/chat/search", body)
		var r res
		json.Unmarshal([]byte(b), &r)
		return c, r
	}
	// nur globale Admins, Anlass nötig
	if c, _ := search("bob", `{"reason":"Akte 1","filter":{}}`); c != 403 {
		t.Fatal("nur Admin:", c)
	}
	if c, _ := search("anna", `{"filter":{}}`); c != 400 {
		t.Fatal("Anlass nötig:", c)
	}
	c, r := search("anna", `{"reason":"Akte 1","filter":{"words":["DUMM"],"ctx":1}}`)
	if c != 200 || r.Total != 2 || r.Users["bob"] != 1 || r.Users["carl"] != 1 {
		t.Fatal("Suche Wort:", c, r)
	}
	ctxN := 0
	for _, h := range r.Hits {
		if h.Ctx {
			ctxN++
		}
	}
	if ctxN == 0 || len(r.Hits) <= 2 {
		t.Fatal("Zusammenhang fehlt:", r.Hits)
	}
	if _, r = search("anna", `{"reason":"Akte 1","filter":{"users":["bob"],"groups":["k6"],"channel":"allgemein"}}`); r.Total != 3 {
		t.Fatal("Suche Autor:", r.Total)
	}
	if c, _ = search("anna", `{"reason":"Akte 1","filter":{"from":"2999-01-01"}}`); c != 200 {
		t.Fatal(c)
	}
	if _, r = search("anna", `{"reason":"Akte 1","filter":{"from":"2999-01-01"}}`); r.Total != 0 {
		t.Fatal("Datum:", r.Total)
	}
	var refs []string
	attID := ""
	_, r = search("anna", `{"reason":"Akte 1","filter":{"users":["bob"]}}`)
	for _, h := range r.Hits {
		refs = append(refs, `{"g":"`+h.G+`","c":"`+h.C+`","id":`+strconv.FormatInt(h.ID, 10)+`}`)
		if h.Att != nil {
			attID = h.Att.ID
		}
	}
	sel := `[` + strings.Join(refs, ",") + `]`
	// Export: Passwort nötig, ZIP mit Prüfsumme
	if c, _ := req(t, srv, "anna", "POST", "/api/routines/chat/export", `{"reason":"Akte 1","refs":`+sel+`,"password":"falsch"}`); c != 403 {
		t.Fatal("Export ohne Passwort:", c)
	}
	rq, _ := http.NewRequest("POST", srv.URL+"/api/routines/chat/export", strings.NewReader(`{"reason":"Akte 1","refs":`+sel+`,"password":"passwort-anna"}`))
	rq.SetBasicAuth("anna", "passwort-anna")
	resp, err := http.DefaultClient.Do(rq)
	if err != nil {
		t.Fatal(err)
	}
	zb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	sum := sha256.Sum256(zb)
	if resp.StatusCode != 200 || resp.Header.Get("X-Content-SHA256") != hex.EncodeToString(sum[:]) {
		t.Fatal("Export:", resp.StatusCode, resp.Header.Get("X-Content-SHA256"))
	}
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(b)
	}
	att := ""
	for n, b := range files {
		if strings.HasPrefix(n, "attachments/k6/allgemein/") && b == "geheimer Anhang" {
			att = n
		}
	}
	if att == "" || !strings.Contains(files["messages.csv"], "du bist dumm") || !strings.Contains(files["manifest.txt"], "Akte 1") || !strings.Contains(files["manifest.txt"], "messages.json") || strings.Contains(files["messages.csv"], "doof") {
		t.Fatal("ZIP-Inhalt:", len(files), files["manifest.txt"])
	}
	// Löschen: Vorschau -> Hash, Anlass, Snapshot-Hinweis, Passwort
	del := `{"filter":{"users":["bob"]},"mode":"clear","reason":"Löschanforderung DSB 7"`
	_, b := req(t, srv, "anna", "POST", "/api/routines/chat/delete/preview", del+`}`)
	var pl struct {
		Count, Atts int
		Hash        string
	}
	json.Unmarshal([]byte(b), &pl)
	if pl.Count != 3 || pl.Atts != 1 || pl.Hash == "" {
		t.Fatal("Vorschau:", b)
	}
	run := func(extra string) (int, string) {
		return req(t, srv, "anna", "POST", "/api/routines/chat/delete/run", del+`,`+extra+`}`)
	}
	if c, _ := run(`"hash":"` + pl.Hash + `","password":"passwort-anna"`); c != 400 {
		t.Fatal("ohne Snapshot nur mit Bestätigung:", c)
	}
	if c, _ := run(`"hash":"` + pl.Hash + `","password":"x","noSnapshot":true`); c != 403 {
		t.Fatal("Passwort:", c)
	}
	if c, _ := run(`"hash":"0000","password":"passwort-anna","noSnapshot":true`); c != 409 {
		t.Fatal("Hash:", c)
	}
	if c, b := run(`"hash":"` + pl.Hash + `","password":"passwort-anna","noSnapshot":true`); c != 200 || !strings.Contains(b, `"deleted":3`) {
		t.Fatal("Löschen:", c, b)
	}
	_, hist := req(t, srv, "carl", "GET", "/api/chat/k6/allgemein/history", "")
	if strings.Contains(hist, "du bist dumm") || strings.Contains(hist, "hallo zusammen") || !strings.Contains(hist, "doof und dumm") || !strings.Contains(hist, `"del":true`) {
		t.Fatal("Verlauf nach Löschen:", hist)
	}
	if c, _ := req(t, srv, "carl", "GET", "/api/chat/k6/allgemein/file/"+attID, ""); c == 200 {
		t.Fatal("Anhang noch abrufbar")
	}
	// zweite Vorschau: nichts mehr da
	if c, _ := req(t, srv, "anna", "POST", "/api/routines/chat/delete/preview", del+`}`); c != 400 {
		t.Fatal("zweimal löschen:", c)
	}
	// Modus remove: Eintrag verschwindet ganz
	rem := `{"filter":{"users":["carl"],"words":["doof"]},"mode":"remove","reason":"Löschanforderung DSB 8"`
	_, b = req(t, srv, "anna", "POST", "/api/routines/chat/delete/preview", rem+`}`)
	json.Unmarshal([]byte(b), &pl)
	if c, b := req(t, srv, "anna", "POST", "/api/routines/chat/delete/run", rem+`,"hash":"`+pl.Hash+`","password":"passwort-anna","noSnapshot":true}`); c != 200 {
		t.Fatal(c, b)
	}
	_, hist = req(t, srv, "carl", "GET", "/api/chat/k6/allgemein/history", "")
	if strings.Contains(hist, "doof") || !strings.Contains(hist, "Mathe morgen") {
		t.Fatal("remove:", hist)
	}
	// Protokoll: Anlass, Zahlen, keine Inhalte
	_, lg := req(t, srv, "anna", "GET", "/api/routines/chat/log", "")
	if !strings.Contains(lg, "Löschanforderung DSB 7") || !strings.Contains(lg, `"action":"export"`) || !strings.Contains(lg, `"action":"search"`) || !strings.Contains(lg, `"snapshot":"none"`) {
		t.Fatal("Protokoll:", lg)
	}
	for _, s := range []string{"du bist dumm", "doof und", "Mathe", "geheimer", "hallo zusammen"} {
		if strings.Contains(lg, s) {
			t.Fatal("Inhalt im Protokoll:", s)
		}
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/routines/chat/log", ""); c != 403 {
		t.Fatal("Protokoll nur Admin:", c)
	}
	// KI-Auswertung ist ohne Freigabe nicht möglich
	if c, _ := req(t, srv, "anna", "POST", "/api/ai/review", `{"reason":"Akte 1","refs":`+sel+`,"confirm":true}`); c != 503 {
		t.Fatal("KI-Auswertung ohne Freigabe:", c)
	}
}
