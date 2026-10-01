package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// getH: GET mit eigenen Kopfzeilen, liefert Status, Kopfzeilen und Inhalt.
func getH(t *testing.T, base, user, path string, hdr ...string) (int, http.Header, string) {
	t.Helper()
	r, _ := http.NewRequest("GET", base+path, nil)
	r.SetBasicAuth(user, "passwort-"+user)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func TestFileRangeAndETag(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	data := ""
	for i := 0; i < 10; i++ {
		data += "0123456789" // 100 Bytes
	}
	if c, b := req(t, srv, "bob", "POST", "/api/files?name=r.bin", data); c != 200 {
		t.Fatal(c, b)
	}
	p := "/api/files/bob/r.bin"
	c, h, b := getH(t, srv.URL, "bob", p)
	if c != 200 || b != data || h.Get("Accept-Ranges") != "bytes" || h.Get("ETag") == "" {
		t.Fatal("voll:", c, h)
	}
	et := h.Get("ETag")
	cases := []struct {
		rng, want, cr string
	}{
		{"bytes=10-19", data[10:20], "bytes 10-19/100"},
		{"bytes=95-", data[95:], "bytes 95-99/100"},
		{"bytes=-5", data[95:], "bytes 95-99/100"},
		{"bytes=90-500", data[90:], "bytes 90-99/100"},
		{"bytes=0-0", data[:1], "bytes 0-0/100"},
	}
	for _, k := range cases {
		c, h, b := getH(t, srv.URL, "bob", p, "Range", k.rng)
		if c != 206 || b != k.want || h.Get("Content-Range") != k.cr || h.Get("Content-Length") != strconv.Itoa(len(k.want)) {
			t.Fatalf("%s: %d %q %v", k.rng, c, b, h)
		}
	}
	// hinter dem Ende: 416
	if c, h, _ := getH(t, srv.URL, "bob", p, "Range", "bytes=100-"); c != 416 || h.Get("Content-Range") != "bytes */100" {
		t.Fatal("416:", c, h)
	}
	// mehrere Bereiche, Unsinn: ganze Datei
	for _, rg := range []string{"bytes=0-1,5-6", "bytes=x-y", "lines=1-2", "bytes=9-3"} {
		if c, _, b := getH(t, srv.URL, "bob", p, "Range", rg); c != 200 || b != data {
			t.Fatal("ganze Datei erwartet:", rg, c)
		}
	}
	// If-Range: passt -> Teil, passt nicht -> ganze Datei
	if c, _, b := getH(t, srv.URL, "bob", p, "Range", "bytes=0-9", "If-Range", et); c != 206 || b != data[:10] {
		t.Fatal("If-Range passt:", c)
	}
	if c, _, b := getH(t, srv.URL, "bob", p, "Range", "bytes=0-9", "If-Range", `"alt"`); c != 200 || b != data {
		t.Fatal("If-Range alt:", c)
	}
	// 304
	for _, inm := range []string{et, "W/" + et, `"x", ` + et, "*"} {
		if c, _, b := getH(t, srv.URL, "bob", p, "If-None-Match", inm); c != 304 || b != "" {
			t.Fatal("304 erwartet:", inm, c)
		}
	}
	if c, _, _ := getH(t, srv.URL, "bob", p, "If-None-Match", `"anders"`); c != 200 {
		t.Fatal("anderer ETag:", c)
	}
	// keine Rechte: 404, kein 304 (nichts über Existenz verraten)
	if c, _, _ := getH(t, srv.URL, "anna", p, "If-None-Match", et); c != 404 {
		t.Fatal("fremde Datei:", c)
	}
	// nach dem Ersetzen ein neuer ETag
	req(t, srv, "bob", "POST", "/api/files?name=r.bin", data+"x")
	if c, _, _ := getH(t, srv.URL, "bob", p, "If-None-Match", et); c != 200 {
		t.Fatal("alter ETag nach Ersetzen:", c)
	}
}

func wsSend(t *testing.T, c *websocket.Conn, s string) {
	t.Helper()
	if err := c.Write(context.Background(), websocket.MessageText, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

// wsNext liest bis zu einer Nachricht vom Typ typ (andere werden übersprungen).
func wsNext(t *testing.T, c *websocket.Conn, typ string) map[string]any {
	t.Helper()
	for i := 0; i < 20; i++ {
		if m := read(t, c); m["t"] == typ {
			return m
		}
	}
	t.Fatal("keine Nachricht vom Typ", typ)
	return nil
}

func TestChatEditReactRights(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"lehrer","areas":["files"],"chat":"admin","chans":"admin","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/lehrer/members", `{"add":["bob"]}`)
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"6a","areas":["files"],"chat":"member","chans":"member","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/6a/members", `{"add":["bob"]}`)

	anna, bob := chatDial(t, srv, "anna"), chatDial(t, srv, "bob")
	defer settle(anna, bob)
	read(t, anna)
	read(t, bob)
	// "lehrer": nur Admins schreiben -> bob darf lesen, aber weder reagieren noch bearbeiten
	wsSend(t, anna, `{"t":"send","g":"lehrer","c":"allgemein","text":"hallo"}`)
	id := int64(wsNext(t, bob, "msg")["m"].(map[string]any)["id"].(float64))
	react := func(c *websocket.Conn, g string, id float64) {
		b, _ := json.Marshal(map[string]any{"t": "react", "g": g, "c": "allgemein", "id": id, "e": "👍"})
		wsSend(t, c, string(b))
	}
	react(bob, "lehrer", float64(id))
	if m := wsNext(t, bob, "err"); !strings.Contains(m["m"].(string), "admins") {
		t.Fatal("Reaktion im Nur-Lesen-Chat muss scheitern:", m)
	}
	react(anna, "lehrer", float64(id)) // Admin darf
	if m := wsNext(t, bob, "upd"); m["m"] == nil {
		t.Fatal(m)
	}
	// "6a": alle schreiben -> Reaktion ok, aber Tempolimit (20 je 10 s, gemeinsam mit dem Senden)
	wsSend(t, anna, `{"t":"send","g":"6a","c":"allgemein","text":"hi"}`)
	id2 := wsNext(t, bob, "msg")["m"].(map[string]any)["id"].(float64)
	for i := 0; i < 25; i++ {
		react(bob, "6a", id2)
	}
	limited := false
	for i := 0; i < 30 && !limited; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, b, err := bob.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		limited = strings.Contains(string(b), "too many messages")
	}
	if !limited {
		t.Fatal("Tempolimit bei Reaktionen fehlt")
	}
}

func TestChatConnLimit(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	first := chatDial(t, srv, "bob")
	all := []*websocket.Conn{first}
	defer func() { settle(all...) }()
	read(t, first)
	for i := 0; i < 8; i++ { // 9 Verbindungen insgesamt: die erste muss weichen
		c := chatDial(t, srv, "bob")
		all = append(all, c)
		read(t, c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := first.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("älteste Verbindung wurde nicht geschlossen")
			}
			return
		}
	}
}

// settle schließt die Verbindungen und gibt den Server-Goroutinen Zeit zu enden, bevor der nächste Test den globalen Auth-Zustand neu setzt.
func settle(cs ...*websocket.Conn) {
	for _, c := range cs {
		c.CloseNow()
	}
	time.Sleep(300 * time.Millisecond)
}
