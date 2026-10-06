package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cs-team/auth"
	"cs-team/store"
)

// fsServer: Server auf dem Ordner-Speicher <mount>/.csteam; mount imitiert den Mountpunkt eines ZFS-Datasets
// (<mount>/.zfs/snapshot/<snap>/.csteam/... wird vom Test angelegt, kein ZFS nötig).
func fsServer(t *testing.T, mount string) (*httptest.Server, *store.FS) {
	st, err := store.NewFS(filepath.Join(mount, ".csteam"))
	if err != nil {
		t.Fatal(err)
	}
	auth.ForceChange = false
	a := auth.New(st)
	for _, u := range []string{"anna", "bob"} {
		if err := a.SetUser(context.Background(), u, "passwort-"+u, u == "anna"); err != nil {
			t.Fatal(err)
		}
		a.SetPassword(context.Background(), u, "passwort-"+u)
	}
	return httptest.NewServer(routes(st, a)), st
}

// putSnap legt den Stand einer Datei in einem imitierten Snapshot ab (Snapshot-Ordner und Datei bekommen die Zeit mt).
func putSnap(t *testing.T, mount string, st *store.FS, snap, owner, name, content string, mt time.Time) string {
	t.Helper()
	p, err := st.Path("files/" + owner + "/" + strings.ReplaceAll(name, "/", "\x1f"))
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(mount, p)
	dst := filepath.Join(mount, ".zfs", "snapshot", snap, rel)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(dst, mt, mt)
	sd := filepath.Join(mount, ".zfs", "snapshot", snap)
	os.Chtimes(sd, mt, mt)
	return dst
}

func TestFileVersions(t *testing.T) {
	mount := t.TempDir()
	srv, st := fsServer(t, mount)
	defer srv.Close()
	req(t, srv, "anna", "POST", "/api/users", `{"name":"carl","password":"passwort-carl","groups":["alluser"]}`)
	if c, b := req(t, srv, "bob", "POST", "/api/files?name=a.txt", "aktuell"); c != 200 {
		t.Fatal(c, b)
	}
	if c, b := req(t, srv, "bob", "POST", "/api/files?name=Mathe/b.txt", "ordner-aktuell"); c != 200 {
		t.Fatal(c, b)
	}
	cur, _ := st.Path("files/bob/a.txt")
	fi, _ := os.Stat(cur)
	T := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	putSnap(t, mount, st, "s1", "bob", "a.txt", "v1", T)
	putSnap(t, mount, st, "s2", "bob", "a.txt", "v1", T)                             // gleicher Stand (Größe+Zeit): zählt einmal
	putSnap(t, mount, st, "s3", "bob", "a.txt", "version zwei", T.Add(2*time.Hour)) // anderer Stand
	putSnap(t, mount, st, "s4", "bob", "a.txt", "aktuell", fi.ModTime())             // wie die aktuelle Datei: entfällt
	putSnap(t, mount, st, "s0", "bob", "x.txt", "andere Datei", T)                   // Snapshot ohne a.txt: überspringen
	putSnap(t, mount, st, "m1", "bob", "Mathe/b.txt", "ordner-alt", T)
	os.MkdirAll(filepath.Join(mount, ".zfs", "snapshot", "leer"), 0o755)

	type list struct {
		Supported bool
		Versions  []struct {
			Snap string
			Size int64
		}
	}
	get := func(user, name string) (int, list) {
		c, b := req(t, srv, user, "GET", "/api/filesversions/bob/"+name, "")
		var l list
		json.Unmarshal([]byte(b), &l)
		return c, l
	}
	c, l := get("bob", "a.txt")
	if c != 200 || !l.Supported || len(l.Versions) != 2 || l.Versions[0].Snap != "s3" || l.Versions[0].Size != 12 || l.Versions[1].Snap != "s1" {
		t.Fatalf("Liste: %d %+v", c, l)
	}
	if _, l := get("bob", "Mathe/b.txt"); len(l.Versions) != 1 || l.Versions[0].Snap != "m1" { // Ordnername im Schlüssel
		t.Fatalf("Datei im Ordner: %+v", l)
	}
	// Herunterladen: Inhalt, Typ und Disposition wie beim normalen Download
	c, b := req(t, srv, "bob", "GET", "/api/filesversions/bob/a.txt?snap=s1", "")
	if c != 200 || b != "v1" {
		t.Fatal("Download:", c, b)
	}
	// Rechte wie beim Lesen: carl sieht nichts, mit Leserecht Liste und Download, Wiederherstellen erst mit Schreibrecht
	if c, _ := get("carl", "a.txt"); c != 404 {
		t.Fatal("carl ohne Recht:", c)
	}
	if c, _ := req(t, srv, "carl", "GET", "/api/filesversions/bob/a.txt?snap=s1", ""); c != 404 {
		t.Fatal("carl Download:", c)
	}
	if c, _ := req(t, srv, "carl", "POST", "/api/filesrestore/bob/a.txt?snap=s1", ""); c != 404 {
		t.Fatal("carl Restore ohne Recht:", c)
	}
	req(t, srv, "bob", "POST", "/api/filesshare/bob/a.txt", `{"read":["carl"]}`)
	// nur Leserecht: weder Liste noch Download noch Wiederherstellen (frühere Stände können von vor der Freigabe stammen)
	if c, _ := get("carl", "a.txt"); c != 403 {
		t.Fatal("carl nur lesen darf die Liste nicht sehen:", c)
	}
	if c, b := req(t, srv, "carl", "GET", "/api/filesversions/bob/a.txt?snap=s3", ""); c != 403 || strings.Contains(b, "version zwei") {
		t.Fatal("carl nur lesen darf keine Version laden:", c, b)
	}
	if c, _ := req(t, srv, "carl", "POST", "/api/filesrestore/bob/a.txt?snap=s1", ""); c != 403 {
		t.Fatal("carl nur lesen darf nicht wiederherstellen:", c)
	}
	// Pfadtricks im Snapshot-Namen und im Dateinamen
	os.WriteFile(filepath.Join(mount, "geheim.txt"), []byte("GEHEIM"), 0o644)
	for _, sn := range []string{"..", "../x", "%2e%2e", "..%2F..", "a%2Fb", "a/b", ".", "%5C", "x%00y", "s1/../s3", "../../.csteam/files/bob/a.txt"} {
		for _, call := range [][2]string{{"GET", "/api/filesversions/bob/a.txt?snap=" + sn}, {"POST", "/api/filesrestore/bob/a.txt?snap=" + sn}} {
			c, b := req(t, srv, "bob", call[0], call[1], "")
			if c == 200 || strings.Contains(b, "GEHEIM") || strings.Contains(b, "version zwei") {
				t.Fatal("Snapshot-Name nicht abgelehnt:", sn, call, c, b)
			}
		}
	}
	for _, nm := range []string{"..%2F..%2Fgeheim.txt", "%2e%2e/a.txt", "shared/a.txt", ".trash/x", "a%5Cb"} {
		if c, b := req(t, srv, "bob", "GET", "/api/filesversions/bob/"+nm+"?snap=s1", ""); c == 200 || strings.Contains(b, "GEHEIM") {
			t.Fatal("Dateiname nicht abgelehnt:", nm, c)
		}
	}
	// Symlink im Snapshot (auf eine Datei außerhalb) wird nie gefolgt
	link := putSnap(t, mount, st, "s5", "bob", "a.txt", "x", T.Add(3*time.Hour))
	os.Remove(link)
	if os.Symlink(filepath.Join(mount, "geheim.txt"), link) == nil {
		if c, b := req(t, srv, "bob", "GET", "/api/filesversions/bob/a.txt?snap=s5", ""); c != 404 || strings.Contains(b, "GEHEIM") {
			t.Fatal("Symlink gefolgt:", c, b)
		}
		if _, l := get("bob", "a.txt"); len(l.Versions) != 2 {
			t.Fatalf("Symlink in der Liste: %+v", l)
		}
	}
	os.RemoveAll(filepath.Join(mount, ".zfs", "snapshot", "s5"))

	// Wiederherstellen: carl mit Schreibrecht; der bisherige Stand kommt in den Papierkorb, Freigabe bleibt
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	req(t, srv, "bob", "POST", "/api/filesshare/bob/a.txt", `{"read":["carl"],"write":["carl"]}`)
	if c, b := req(t, srv, "carl", "POST", "/api/filesrestore/bob/a.txt?snap=s1", ""); c != 200 {
		t.Fatal("Restore:", c, b)
	}
	if c, b := req(t, srv, "bob", "GET", "/api/files/bob/a.txt", ""); c != 200 || b != "v1" {
		t.Fatal("nach Restore:", c, b)
	}
	if _, b := req(t, srv, "bob", "GET", "/api/trash", ""); !strings.Contains(b, `"name":"a.txt"`) || !strings.Contains(b, `"size":7`) {
		t.Fatal("alter Stand nicht im Papierkorb:", b)
	}
	if _, b := req(t, srv, "bob", "GET", "/api/files", ""); !strings.Contains(b, `"read":["carl"]`) {
		t.Fatal("Freigabe verloren:", b)
	}
	if !strings.Contains(logs.String(), `audit: file restored file="bob"/"a.txt" snap="s1" by="carl"`) {
		t.Fatal("Audit-Zeile fehlt:", logs.String())
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/filesrestore/bob/a.txt?snap=gibtsnicht", ""); c != 404 {
		t.Fatal("unbekannter Snapshot:", c)
	}
	// gelöschte Datei: Liste und Wiederherstellen gehen weiter (nur für den Besitzer)
	req(t, srv, "bob", "DELETE", "/api/files/bob/a.txt", "")
	if c, l := get("bob", "a.txt"); c != 200 || len(l.Versions) != 3 {
		t.Fatal("gelöscht:", c, l)
	}
	if c, _ := get("carl", "a.txt"); c != 404 {
		t.Fatal("carl bei gelöschter Datei:", c)
	}
	if c, b := req(t, srv, "bob", "POST", "/api/filesrestore/bob/a.txt?snap=s3", ""); c != 200 {
		t.Fatal("Restore gelöschter Datei:", c, b)
	}
	if c, b := req(t, srv, "bob", "GET", "/api/files/bob/a.txt", ""); c != 200 || b != "version zwei" {
		t.Fatal("wiederhergestellt:", c, b)
	}
}

// Ohne ZFS-Snapshots (Speicher im RAM / S3 / Ordner ohne .zfs): supported=false, nichts zum Herunterladen.
func TestFileVersionsUnsupported(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	req(t, srv, "bob", "POST", "/api/files?name=a.txt", "x")
	c, b := req(t, srv, "bob", "GET", "/api/filesversions/bob/a.txt", "")
	if c != 200 || !strings.Contains(b, `"supported":false`) || !strings.Contains(b, `"versions":[]`) {
		t.Fatal("Speicher ohne Snapshots:", c, b)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/filesversions/bob/a.txt?snap=s1", ""); c != 404 {
		t.Fatal(c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/filesrestore/bob/a.txt?snap=s1", ""); c != 404 {
		t.Fatal(c)
	}
	// Ordner-Speicher ohne .zfs: ebenfalls nicht unterstützt
	dir := t.TempDir()
	fs, fst := fsServer(t, dir)
	defer fs.Close()
	_ = fst
	req(t, fs, "bob", "POST", "/api/files?name=a.txt", "x")
	if _, b := req(t, fs, "bob", "GET", "/api/filesversions/bob/a.txt", ""); !strings.Contains(b, `"supported":false`) {
		t.Fatal("Ordner ohne .zfs:", b)
	}
	// CS_SNAP_ROOT legt den Mountpunkt fest
	t.Setenv("CS_SNAP_ROOT", dir)
	os.MkdirAll(filepath.Join(dir, ".zfs", "snapshot", "s1"), 0o755)
	if _, b := req(t, fs, "bob", "GET", "/api/filesversions/bob/a.txt", ""); !strings.Contains(b, `"supported":true`) {
		t.Fatal("CS_SNAP_ROOT:", b)
	}
}
