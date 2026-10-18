// snapflow: Handtest fuer Verlauf/Snapshot-Import (0.63.0). Ablauf steuert ein Shellskript (zfs snapshot dazwischen).
//   snapflow URL USER:PASS create          -> legt Doc an, schreibt "Version EINS", gibt id aus
//   snapflow URL USER:PASS edit ID         -> schreibt "Version ZWEI"
//   snapflow URL USER:PASS check ID        -> Versionen, Import, readonly, Inhalt
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
)

var base, u, p string
var fails int

func call(m, path string, body string) (int, []byte) {
	r, _ := http.NewRequest(m, base+path, bytes.NewReader([]byte(body)))
	r.SetBasicAuth(u, p)
	x, err := http.DefaultClient.Do(r)
	if err != nil {
		return -1, []byte(err.Error())
	}
	defer x.Body.Close()
	b, _ := io.ReadAll(x.Body)
	return x.StatusCode, b
}

func check(name string, ok bool, info ...any) {
	if ok {
		fmt.Println("PASS ", name)
		return
	}
	fails++
	fmt.Println("FAIL ", name, info)
}

func open(id string) *websocket.Conn {
	h := http.Header{}
	r, _ := http.NewRequest("GET", "/", nil)
	r.SetBasicAuth(u, p)
	h.Set("Authorization", r.Header.Get("Authorization"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws/"+id, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		fmt.Println("ws:", err)
		os.Exit(2)
	}
	return c
}

func initMsg(c *websocket.Conn) (string, map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		return "", nil
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return string(b), m
}

func setP(id, text string) {
	c := open(id)
	initMsg(c)
	c.Write(context.Background(), websocket.MessageText, []byte(fmt.Sprintf(`{"t":"set","k":"p00001","v":%q,"pos":1}`, text)))
	time.Sleep(3500 * time.Millisecond) // Persist verzoegert 2 s
	c.CloseNow()
}

func main() {
	base = os.Args[1]
	up := strings.SplitN(os.Args[2], ":", 2)
	u, p = up[0], up[1]
	if os.Args[3] == "create" { // Startpasswort: Aenderung erzwungen; danach gilt Passwort+"-sf"
		if c, _ := call("POST", "/api/me/password", fmt.Sprintf(`{"old":%q,"new":%q}`, p, p+"-sf")); c == 200 {
			p += "-sf"
		}
	}
	switch os.Args[3] {
	case "create":
		code, b := call("POST", "/api/docs", `{"name":"snapflow","type":"text"}`)
		var d struct{ ID string }
		json.Unmarshal(b, &d)
		if code != 200 || d.ID == "" {
			fmt.Println("create failed", code, string(b))
			os.Exit(2)
		}
		setP(d.ID, "Version EINS")
		fmt.Println(d.ID)
	case "edit":
		setP(os.Args[4], "Version ZWEI")
	case "check":
		id := os.Args[4]
		code, b := call("GET", "/api/docs/"+id+"/versions", "")
		var v struct {
			Supported bool `json:"supported"`
			Versions  []struct {
				Snap string `json:"snap"`
			} `json:"versions"`
		}
		json.Unmarshal(b, &v)
		fmt.Println("versions:", string(b))
		check("Versionen: ZFS erkannt", code == 200 && v.Supported, code, string(b))
		check("Versionen: mindestens 1 Stand", len(v.Versions) >= 1, string(b))
		if len(v.Versions) == 0 {
			os.Exit(1)
		}
		old := v.Versions[len(v.Versions)-1].Snap // aeltester = Stand EINS
		code, b = call("POST", "/api/docs/"+id+"/fromversion", fmt.Sprintf(`{"snap":%q}`, old))
		var n struct {
			ID, Name string
			RO       bool
		}
		json.Unmarshal(b, &n)
		fmt.Println("import:", string(b))
		check("Import: neues Dokument, schreibgeschuetzt, Name mit Datum", code == 200 && n.ID != "" && n.RO && strings.HasPrefix(n.Name, "snapflow_20"), code, string(b))
		c := open(n.ID)
		raw, m := initMsg(c)
		c.CloseNow()
		check("Import: Inhalt = Stand EINS", strings.Contains(raw, "Version EINS") && !strings.Contains(raw, "Version ZWEI"), raw)
		check("Import: nur lesbar", m != nil && m["rw"] != true, raw)
		code, b = call("POST", "/api/docs/"+n.ID+"/readonly", `{"ro":false}`)
		check("Import: freigeben fuer Bearbeitung", code == 200, code, string(b))
		c = open(n.ID)
		_, m = initMsg(c)
		c.CloseNow()
		check("Import: nach Freigabe schreibbar", m != nil && m["rw"] == true)
		code, b = call("GET", "/api/docs/"+id+"/changes", "")
		check("Aenderungsliste Original: edit-Eintraege", code == 200 && strings.Contains(string(b), `"op":"edit"`), string(b))
		code, b = call("GET", "/api/docs/"+n.ID+"/changes", "")
		check("Aenderungsliste Import: Quelle snapshot", code == 200 && strings.Contains(string(b), `"src":"snapshot"`) && strings.Contains(string(b), old), string(b))
		call("DELETE", "/api/docs/"+n.ID, "")
		call("DELETE", "/api/docs/"+id, "")
		fmt.Printf("== %d Fehler\n", fails)
		if fails > 0 {
			os.Exit(1)
		}
	}
}
