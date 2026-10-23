package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShareFolder: Ordner "share" (TODO G) ueber die echte API: Sichtbarkeit je Benutzer/Gruppe, nur lesen, Import in die eigene Ablage.
func TestShareFolder(t *testing.T) {
	dir := t.TempDir()
	for f, d := range map[string]string{
		"readme.txt": "alle", "groups/kl/a.txt": "klasse", "groups/other/o.txt": "andere",
		"users/lisa/l.txt": "lisa", "users/fred/f.txt": "fred", ".hidden": "x",
	} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(d), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CS_SHARE", dir)
	srv, _ := setup(t) // anna = Admin
	defer srv.Close()
	do := func(user, pw, method, path string) (int, string) {
		r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(""))
		r.SetBasicAuth(user, pw)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	post := func(user, pw, path, body string) int {
		r, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		r.SetBasicAuth(user, pw)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	pa := "passwort-anna"
	if c := post("anna", pa, "/api/groups", `{"name":"kl","areas":["files"],"folder":"ro"}`); c != 200 {
		t.Fatalf("create group kl: %d", c)
	}
	post("anna", pa, "/api/groups", `{"name":"other","areas":["files"]}`)
	post("anna", pa, "/api/users/import", "lisa;lisalisa1;kl\nfred;fredfred1;other\n")

	// Wurzel: lisa sieht readme.txt und groups (nur kl), nicht .hidden
	c, b := do("lisa", "lisalisa1", "GET", "/api/share")
	if c != 200 || !strings.Contains(b, `"enabled":true`) || !strings.Contains(b, `"readme.txt"`) || !strings.Contains(b, `"groups"`) || strings.Contains(b, ".hidden") {
		t.Fatalf("lisa root: %d %s", c, b)
	}
	if _, b = do("lisa", "lisalisa1", "GET", "/api/share?path=groups"); !strings.Contains(b, `"kl"`) || strings.Contains(b, `"other"`) {
		t.Fatalf("lisa groups: %s", b)
	}
	if _, b = do("anna", pa, "GET", "/api/share?path=groups"); !strings.Contains(b, `"kl"`) || !strings.Contains(b, `"other"`) {
		t.Fatalf("admin groups: %s", b)
	}

	// Lesen: eigene Gruppe/eigener Ordner ja, fremde nein, Pfad-Tricks nein
	for _, c := range []struct {
		user, pw, path string
		code           int
		body           string
	}{
		{"lisa", "lisalisa1", "readme.txt", 200, "alle"},
		{"lisa", "lisalisa1", "groups/kl/a.txt", 200, "klasse"},
		{"lisa", "lisalisa1", "groups/other/o.txt", 404, ""},
		{"lisa", "lisalisa1", "users/lisa/l.txt", 200, "lisa"},
		{"lisa", "lisalisa1", "users/fred/f.txt", 404, ""},
		{"fred", "fredfred1", "groups/kl/a.txt", 404, ""},
		{"fred", "fredfred1", "users/fred/f.txt", 200, "fred"},
		{"anna", pa, "users/fred/f.txt", 200, "fred"},
		{"lisa", "lisalisa1", "../outside.txt", 404, ""},
		{"lisa", "lisalisa1", "groups/../users/fred/f.txt", 404, ""},
		{"lisa", "lisalisa1", ".hidden", 404, ""},
	} {
		cc, bb := do(c.user, c.pw, "GET", "/api/share/file?path="+c.path)
		if cc != c.code || (c.body != "" && bb != c.body) {
			t.Errorf("%s %s: %d %q, want %d %q", c.user, c.path, cc, bb, c.code, c.body)
		}
	}

	// share ist nur lesen: keine Schreib-API
	if c := post("anna", pa, "/api/share?path=neu.txt", "x"); c == 200 {
		t.Errorf("POST /api/share darf es nicht geben: %d", c)
	}
	if c, _ := do("anna", pa, "DELETE", "/api/share/file?path=readme.txt"); c == 200 {
		t.Errorf("DELETE darf es nicht geben: %d", c)
	}
	if _, err := os.Stat(filepath.Join(dir, "readme.txt")); err != nil {
		t.Errorf("readme.txt ist weg: %v", err)
	}

	// Import: Kopie in die eigene Ablage, bei gleichem Namen mit Zaehler; fremde Datei nicht importierbar
	if c := post("lisa", "lisalisa1", "/api/share/import?path=groups/kl/a.txt", ""); c != 200 {
		t.Fatalf("import: %d", c)
	}
	if c, b := do("lisa", "lisalisa1", "GET", "/api/files/lisa/a.txt"); c != 200 || b != "klasse" {
		t.Fatalf("Kopie: %d %q", c, b)
	}
	if c, b := do("lisa", "lisalisa1", "POST", "/api/share/import?path=groups/kl/a.txt"); c != 200 || !strings.Contains(b, `a (2).txt`) {
		t.Fatalf("zweiter Import: %d %s", c, b)
	}
	if c := post("fred", "fredfred1", "/api/share/import?path=groups/kl/a.txt", ""); c != 404 {
		t.Errorf("fred importiert fremde Gruppe: %d", c)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "groups", "kl", "a.txt")); string(b) != "klasse" {
		t.Errorf("Original veraendert: %q", b)
	}
}

// Ohne Ordner share (weder CS_SHARE noch CS_DIR) bleibt die Funktion aus.
func TestShareOff(t *testing.T) {
	t.Setenv("CS_SHARE", "")
	t.Setenv("CS_DIR", "")
	srv, _ := setup(t)
	defer srv.Close()
	if c, _ := req(t, srv, "anna", "GET", "/api/share", ""); c == 200 {
		t.Errorf("/api/share ohne share: %d", c)
	}
	// Ordner angegeben, aber nicht vorhanden: aus, kein Fehler
	t.Setenv("CS_SHARE", filepath.Join(t.TempDir(), "gibtsnicht"))
	srv2, _ := setup(t)
	defer srv2.Close()
	c, b := req(t, srv2, "anna", "GET", "/api/share", "")
	if c != 200 || !strings.Contains(b, `"enabled":false`) {
		t.Errorf("share fehlt: %d %s", c, b)
	}
}
