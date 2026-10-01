package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"cs-team/chat"
)

func TestRTC(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/users", `{"name":"carl","password":"passwort-carl","groups":["alluser"]}`)
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"6a","areas":["files"],"chat":"member","chans":"member","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/6a/members", `{"add":["bob"]}`)
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"lehrer","areas":["files"],"chat":"admin","chans":"admin","admins":["anna"]}`)
	req(t, srv, "anna", "POST", "/api/groups/lehrer/members", `{"add":["bob"]}`)

	set := func(user, body string) int { c, _ := req(t, srv, user, "POST", "/api/settings/rtc", body); return c }
	vid := func(user, g string, slot int) (int, string) {
		return req(t, srv, user, "POST", "/api/chat/"+g+"/allgemein/video", `{"slot":`+string(rune('0'+slot))+`}`)
	}
	// ohne Einstellung: aus
	if c, _ := vid("anna", "6a", 3); c != 404 {
		t.Fatal("ohne Einstellung muss Platz 4 aus sein:", c)
	}
	ok := `{"on":true,"stun":["stun:stun.example.org:3478"],"turn":["turn:turn.example.org:3478?transport=udp"],"secret":"geheim123"}`
	if set("bob", ok) != 403 {
		t.Fatal("nur Admin")
	}
	for _, bad := range []string{
		`{"on":true,"stun":["http://x.example.org"]}`,
		`{"on":true,"stun":["stun:a b"]}`,
		`{"on":true,"turn":["turn:turn.example.org:3478"]}`, // ohne Geheimnis
		`{"on":true,"stun":["stun:a:1","stun:b:1","stun:c:1","stun:d:1","stun:e:1"]}`,
		`{"on":true,"turn":["stun:x:1"],"secret":"s"}`,
	} {
		if set("anna", bad) != 400 {
			t.Fatal("muss abgelehnt werden:", bad)
		}
	}
	if set("anna", ok) != 200 {
		t.Fatal("speichern")
	}
	if _, b := req(t, srv, "anna", "GET", "/api/settings", ""); !strings.Contains(b, `"on":true`) || !strings.Contains(b, "stun.example.org") || !strings.Contains(b, `"secretSet":true`) || strings.Contains(b, "geheim123") {
		t.Fatal("settings:", b)
	}
	// Geheimnis bleibt, wenn nicht mitgeschickt
	if set("anna", `{"on":true,"stun":["stun:stun.example.org:3478"],"turn":["turn:turn.example.org:3478?transport=udp"]}`) != 200 {
		t.Fatal("ohne Geheimnis neu speichern")
	}

	type vrow struct {
		Slot int
		Name string
		Mode string
		Can  bool
	}
	rows := func(user, g string) (r []vrow) {
		_, b := req(t, srv, user, "GET", "/api/chat/groups", "")
		var x struct {
			Groups []struct {
				Name  string
				Video []vrow
			}
		}
		json.Unmarshal([]byte(b), &x)
		for _, gr := range x.Groups {
			if gr.Name == g {
				return gr.Video
			}
		}
		return nil
	}
	if r := rows("bob", "6a"); len(r) != 1 || r[0].Slot != 3 || r[0].Mode != "rtc" || !r[0].Can {
		t.Fatal("bob 6a:", r)
	}
	if r := rows("bob", "lehrer"); len(r) != 1 || r[0].Can {
		t.Fatal("bob lehrer (nur lesen):", r)
	}
	if r := rows("anna", "lehrer"); len(r) != 1 || !r[0].Can {
		t.Fatal("anna lehrer:", r)
	}

	// Einladung
	c1, b1 := vid("bob", "6a", 3)
	var inv struct {
		RTC bool
		ID  int64
	}
	json.Unmarshal([]byte(b1), &inv)
	if c1 != 200 || !inv.RTC || inv.ID == 0 || strings.Contains(b1, "http") {
		t.Fatal("start:", c1, b1)
	}
	sid := strconv.FormatInt(inv.ID, 10)
	if _, b := vid("bob", "6a", 3); !strings.Contains(b, `"id":`+sid) {
		t.Fatal("zweiter Klick muss denselben Raum liefern:", b)
	}
	if c, _ := vid("carl", "6a", 3); c != 403 {
		t.Fatal("carl ist nicht in 6a:", c)
	}
	if c, _ := vid("bob", "lehrer", 3); c != 403 {
		t.Fatal("bob darf in lehrer nicht schreiben:", c)
	}
	_, hist := req(t, srv, "bob", "GET", "/api/chat/6a/allgemein/history", "")
	if strings.Count(hist, `"mode":"rtc"`) != 1 || !strings.Contains(hist, "WebRTC") {
		t.Fatal("genau eine Einladung erwartet:", hist)
	}
	idURL := "/api/chat/6a/allgemein/video/" + sid
	if c, b := req(t, srv, "bob", "GET", idURL, ""); c != 200 || !strings.Contains(b, `"rtc":true`) {
		t.Fatal("annehmen:", c, b)
	}
	if c, _ := req(t, srv, "carl", "GET", idURL, ""); c != 404 {
		t.Fatal("carl darf nicht:", c)
	}

	// Signalisierung
	next := func(c *websocket.Conn, typ string) map[string]any {
		t.Helper()
		for i := 0; i < 10; i++ {
			m := read(t, c)
			if m["t"] == typ {
				return m
			}
			if m["t"] == "err" {
				t.Fatalf("erwartet %s, Fehler: %v", typ, m["m"])
			}
		}
		t.Fatal("keine Nachricht", typ)
		return nil
	}
	w := func(c *websocket.Conn, s string) { c.Write(t.Context(), websocket.MessageText, []byte(s)) }
	dial := func(user string) *websocket.Conn {
		c := chatDial(t, srv, user)
		read(t, c) // hello
		return c
	}
	join := `{"t":"rtcjoin","g":"6a","c":"allgemein","id":` + sid + `}`
	bob, anna := dial("bob"), dial("anna")
	w(bob, join)
	jb := next(bob, "rtcjoined")
	if len(jb["peers"].([]any)) != 0 || jb["pid"] == "" || jb["max"].(float64) != 6 {
		t.Fatal(jb)
	}
	ice := jb["ice"].([]any)
	if len(ice) != 2 {
		t.Fatal("stun + turn erwartet:", ice)
	}
	tu := ice[1].(map[string]any)
	if !strings.HasSuffix(tu["username"].(string), ":bob") || tu["credential"].(string) == "" || strings.Contains(tu["credential"].(string), "geheim") {
		t.Fatal("turn:", tu)
	}
	w(anna, join)
	ja := next(anna, "rtcjoined")
	peers := ja["peers"].([]any)
	if len(peers) != 1 || peers[0].(map[string]any)["user"] != "bob" || peers[0].(map[string]any)["pid"] != jb["pid"] {
		t.Fatal("anna sieht bob:", ja)
	}
	if p := next(bob, "rtcpeer"); p["pid"] != ja["pid"] || p["user"] != "anna" {
		t.Fatal("bob sieht anna:", p)
	}
	// Signal: nur an bekannte Teilnehmer, mit Absender
	w(anna, `{"t":"rtcsig","to":"gibtesnicht","data":{"x":1}}`)
	w(anna, `{"t":"rtcsig","to":"`+jb["pid"].(string)+`","data":{"sdp":"v=0"}}`)
	s := next(bob, "rtcsig")
	if s["from"] != ja["pid"] || s["user"] != "anna" || s["data"].(map[string]any)["sdp"] != "v=0" {
		t.Fatal("signal:", s)
	}
	// zu großes Signal
	w(bob, `{"t":"rtcsig","to":"`+ja["pid"].(string)+`","data":"`+strings.Repeat("x", 15<<10)+`"}`)
	if e := next(bob, "err"); e == nil {
		t.Fatal("zu groß")
	}
	// Fremder (nicht in 6a) kann nicht beitreten
	carl := dial("carl")
	w(carl, join)
	next(carl, "err")
	// Verlassen
	w(anna, `{"t":"rtcleave"}`)
	if l := next(bob, "rtcleft"); l["pid"] != ja["pid"] {
		t.Fatal("left:", l)
	}
	w(anna, `{"t":"rtcsig","to":"`+jb["pid"].(string)+`","data":{"a":1}}`) // nicht mehr im Raum: still verworfen
	// Voll: bob + 5 weitere = 6, der siebte wird abgewiesen
	var extra []*websocket.Conn
	for i := 0; i < 5; i++ {
		c := dial("anna")
		w(c, join)
		next(c, "rtcjoined")
		extra = append(extra, c)
	}
	w(anna, join)
	if e := next(anna, "err"); !strings.Contains(e["m"].(string), "full") {
		t.Fatal("voll:", e)
	}
	// Verbindung schließen = verlassen
	extra[0].CloseNow()
	next(bob, "rtcleft")
	// Rate-Limit
	for i := 0; i < 320; i++ {
		w(bob, `{"t":"rtcsig","to":"`+ja["pid"].(string)+`","data":{"i":1}}`)
	}
	next(bob, "err")
	if c, _ := vid("bob", "6a", 3); c != 200 { // läuft noch -> dieselbe Einladung
		t.Fatal(c)
	}
	_, hist = req(t, srv, "bob", "GET", "/api/chat/6a/allgemein/history", "")
	if strings.Count(hist, `"mode":"rtc"`) != 1 {
		t.Fatal("laufender Raum: keine zweite Einladung")
	}

	// Ablauf
	old := chat.AdhocTTL
	chat.AdhocTTL = -time.Hour
	defer func() { chat.AdhocTTL = old }()
	c, b := vid("anna", "lehrer", 3)
	if c != 200 {
		t.Fatal(c, b)
	}
	var inv2 struct{ ID int64 }
	json.Unmarshal([]byte(b), &inv2)
	id := strconv.FormatInt(inv2.ID, 10)
	if c, _ := req(t, srv, "anna", "GET", "/api/chat/lehrer/allgemein/video/"+id, ""); c != 410 {
		t.Fatal("abgelaufen:", c)
	}
	w(anna, `{"t":"rtcjoin","g":"lehrer","c":"allgemein","id":`+id+`}`)
	if e := next(anna, "err"); !strings.Contains(e["m"].(string), "expired") {
		t.Fatal(e)
	}

	// Ausschalten
	if set("anna", `{"on":false}`) != 200 {
		t.Fatal("aus")
	}
	if r := rows("bob", "6a"); len(r) != 0 {
		t.Fatal("aus:", r)
	}
	if c, _ := req(t, srv, "bob", "GET", idURL, ""); c != 404 {
		t.Fatal("aus: Einladung:", c)
	}
	w(carl, join)
	next(carl, "err")
	if _, b := req(t, srv, "anna", "GET", "/api/settings", ""); strings.Contains(b, "turn.example.org") && !strings.Contains(b, `"turn":null`) {
		// Server und Geheimnis werden beim Ausschalten ohne TURN-Angabe geleert
		t.Fatal("turn nicht geleert:", b)
	}
}
