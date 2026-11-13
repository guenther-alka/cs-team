package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// napCfg muss team.cfg genau so erzeugen wie das napp-it-Menue (cstlib.pl _cst_write_cfg), saveNapp die Felder richtig uebernehmen.
func TestNappCfg(t *testing.T) {
	par := map[string]string{"admin_pass": "start-pw-123", "admin_user": "root", "autostart": "yes", "dir": "/daten1/cs-team",
		"ha_mode": "off", "listen": "9004", "proxy": "no", "s3_bucket": "cs-team", "s3_endpoint": "127.0.0.1:9000", "s3_secret": "x",
		"s3_tls": "no", "snap_mode": "auto", "storage": "dir", "tls_cert": "/c.pem", "tls_key": "/k.pem"}
	want := "CS_LISTEN=:9004\nCS_DIR=/daten1/cs-team\nCS_ADMIN_USER=root\nCS_ADMIN_PASS=start-pw-123\nCS_TLS_CERT=/c.pem\nCS_TLS_KEY=/k.pem\n"
	if got := napCfg(par); got != want {
		t.Fatalf("napCfg:\n%s\nwant:\n%s", got, want)
	}
	c := parToCS(par)
	if c["CS_DIR"] != "/daten1/cs-team" || c["CS_LISTEN"] != ":9004" || c["S3_ENDPOINT"] != "127.0.0.1:9000" {
		t.Fatalf("parToCS: %v", c)
	}
	// Menue: S3 gewaehlt, Port 9010, Snapshot aus, neues Passwort
	dir := t.TempDir()
	cfg := filepath.Join(dir, "team", "team.cfg")
	n := map[string]string{"CS_LISTEN": ":9010", "S3_ENDPOINT": "s3:9000", "S3_KEY": "k", "S3_SECRET": "s", "S3_BUCKET": "b", "S3_TLS": "1",
		"CS_ADMIN_USER": "root", "CS_SNAPSHOT": "off"}
	if err := saveNapp(cfg, par, n, "neu-pw-4567"); err != nil {
		t.Fatal(err)
	}
	p2, _ := parseConf(filepath.Join(dir, "team", "team.par"))
	if p2["storage"] != "s3" || p2["listen"] != "9010" || p2["s3_tls"] != "yes" || p2["admin_pass"] != "neu-pw-4567" || p2["autostart"] != "yes" || p2["dir"] != "/daten1/cs-team" || p2["snap_mode"] != "off" {
		t.Fatalf("team.par: %v", p2)
	}
	c2, _ := parseConf(cfg)
	if c2["S3_ENDPOINT"] != "s3:9000" || c2["S3_TLS"] != "1" || c2["CS_DIR"] != "" || c2["CS_SNAPSHOT"] != "off" || c2["CS_TLS_CERT"] != "" {
		t.Fatalf("team.cfg: %v", c2)
	}
}

// Webcraft: Liste, Weiterleitung auf "/", index.html, wasm-Typ, gzip, immutable bei Hash-Namen, kein Ausbruch aus dem Ordner.
func TestWebcraftServe(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "wordcraft")
	os.MkdirAll(app, 0o755)
	os.WriteFile(filepath.Join(app, "index.html"), []byte("<html>wordcraft</html>"), 0o644)
	big := strings.Repeat("wasm", 1000)
	os.WriteFile(filepath.Join(app, "wordcraft-0123456789abcdef_bg.wasm"), []byte(big), 0o644)
	os.MkdirAll(filepath.Join(root, "noapp"), 0o755) // ohne index.html: nicht in der Liste
	os.WriteFile(filepath.Join(filepath.Dir(root), "secret.txt"), []byte("x"), 0o644)
	srv := httptest.NewServer(webcraftHandler(root))
	defer srv.Close()
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p, enc string) *http.Response {
		req, _ := http.NewRequest("GET", srv.URL+p, nil)
		if enc != "" {
			req.Header.Set("Accept-Encoding", enc)
		}
		r, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := get("/webcraft/index.json", "")
	var l []string
	json.NewDecoder(r.Body).Decode(&l)
	if len(l) != 1 || l[0] != "wordcraft" {
		t.Fatalf("index.json: %v", l)
	}
	if r = get("/webcraft/wordcraft", ""); r.StatusCode != 302 || r.Header.Get("Location") != "/webcraft/wordcraft/" {
		t.Fatalf("redirect: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	r = get("/webcraft/wordcraft/", "")
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !strings.Contains(string(b), "wordcraft") || r.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("index.html: %d %q %s", r.StatusCode, b, r.Header.Get("Cache-Control"))
	}
	r = get("/webcraft/wordcraft/wordcraft-0123456789abcdef_bg.wasm", "gzip")
	if r.Header.Get("Content-Type") != "application/wasm" || r.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("wasm headers: %v", r.Header)
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ = io.ReadAll(zr); string(b) != big {
		t.Fatal("wasm gzip content differs")
	}
	if r = get("/webcraft/../secret.txt", ""); r.StatusCode == 200 {
		t.Fatal("path outside the webcraft folder served")
	}
	if r = get("/webcraft/wordcraft/missing.js", ""); r.StatusCode != 404 {
		t.Fatalf("missing file: %d", r.StatusCode)
	}
}
