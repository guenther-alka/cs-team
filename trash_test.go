package main

import (
	"encoding/json"
	"strings"
	"testing"
)

type trItem struct {
	ID, Owner, Name, By string
	Size, At, Expires   int64
}

func trashOf(t *testing.T, get func(m, p, b string) (int, string), user string) (days int, items []trItem) {
	t.Helper()
	c, b := get("GET", "/api/trash", "")
	if c != 200 {
		t.Fatal("trash:", c, b)
	}
	var x struct {
		Days  int
		Items []trItem
	}
	if err := json.Unmarshal([]byte(b), &x); err != nil {
		t.Fatal(err, b)
	}
	return x.Days, x.Items
}

func TestTrash(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	bob := func(m, p, b string) (int, string) { return req(t, srv, "bob", m, p, b) }
	anna := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	data := strings.Repeat("x", 1000)
	if c, _ := bob("POST", "/api/files?name=a.txt", data); c != 200 {
		t.Fatal("upload", c)
	}
	// Löschen -> Papierkorb
	if c, _ := bob("DELETE", "/api/files/bob/a.txt", ""); c != 200 {
		t.Fatal("delete", c)
	}
	if c, _ := bob("GET", "/api/files/bob/a.txt", ""); c != 404 {
		t.Fatal("gelöschte Datei darf nicht mehr abrufbar sein:", c)
	}
	days, it := trashOf(t, bob, "bob")
	if days != 30 || len(it) != 1 || it[0].Name != "a.txt" || it[0].Size != 1000 || it[0].By != "bob" || it[0].Owner != "bob" || it[0].Expires != it[0].At+30*86400 {
		t.Fatal("Papierkorb:", days, it)
	}
	// belegt Kontingent (used), taucht in der Dateiliste nicht auf
	_, b := bob("GET", "/api/files", "")
	var l struct {
		Own         []struct{ Name string }
		Used, Trash int64
	}
	json.Unmarshal([]byte(b), &l)
	if len(l.Own) != 0 || l.Used != 1000 || l.Trash != 1000 {
		t.Fatal("Liste/belegt:", b)
	}
	// fremde Benutzer sehen und berühren ihn nicht
	if _, ai := trashOf(t, anna, "anna"); len(ai) != 0 {
		t.Fatal("anna sieht fremden Papierkorb:", ai)
	}
	if c, _ := anna("POST", "/api/trash/bob/"+it[0].ID+"/restore", ""); c != 404 {
		t.Fatal("fremdes Wiederherstellen:", c)
	}
	if c, _ := anna("DELETE", "/api/trash/bob/"+it[0].ID, ""); c != 404 {
		t.Fatal("fremdes Löschen:", c)
	}
	if c, _ := bob("POST", "/api/trash/bob/../restore", ""); c == 200 {
		t.Fatal("Pfadtrick")
	}
	if c, _ := bob("POST", "/api/trash/bob/nichtda12345/restore", ""); c != 404 {
		t.Fatal("unbekannte ID:", c)
	}
	// Wiederherstellen
	c, b2 := bob("POST", "/api/trash/bob/"+it[0].ID+"/restore", "")
	if c != 200 || !strings.Contains(b2, `"a.txt"`) {
		t.Fatal("restore:", c, b2)
	}
	if c, got := bob("GET", "/api/files/bob/a.txt", ""); c != 200 || got != data {
		t.Fatal("Inhalt nach Wiederherstellung:", c, len(got))
	}
	if _, it = trashOf(t, bob, "bob"); len(it) != 0 {
		t.Fatal("Papierkorb nicht leer:", it)
	}
	// Name belegt: "a (2).txt"
	bob("DELETE", "/api/files/bob/a.txt", "")
	bob("POST", "/api/files?name=a.txt", "neu")
	_, it = trashOf(t, bob, "bob")
	c, b2 = bob("POST", "/api/trash/bob/"+it[0].ID+"/restore", "")
	if c != 200 || !strings.Contains(b2, `"a (2).txt"`) {
		t.Fatal("alternativer Name:", c, b2)
	}
	if c, got := bob("GET", "/api/files/bob/a.txt", ""); got != "neu" {
		t.Fatal("neue Datei überschrieben:", c, got)
	}
	// Ordner löschen: Dateien einzeln im Papierkorb, Ordner-Marker nicht
	bob("POST", "/api/filesdir?owner=bob&name=Mathe", "")
	bob("POST", "/api/files?name=Mathe/b.txt", "bbb")
	bob("POST", "/api/files?name=Mathe/c.txt", "ccc")
	if c, _ := bob("DELETE", "/api/files/bob/Mathe?dir=1", ""); c != 200 {
		t.Fatal("Ordner löschen:", c)
	}
	_, it = trashOf(t, bob, "bob")
	names := map[string]string{}
	for _, x := range it {
		names[x.Name] = x.ID
	}
	if len(it) != 2 || names["Mathe/b.txt"] == "" || names["Mathe/c.txt"] == "" {
		t.Fatal("Ordner im Papierkorb:", it)
	}
	bob("POST", "/api/trash/bob/"+names["Mathe/b.txt"]+"/restore", "")
	if c, got := bob("GET", "/api/files/bob/Mathe/b.txt", ""); c != 200 || got != "bbb" {
		t.Fatal("Datei im Ordner wiederhergestellt:", c, got)
	}
	// Einzeln endgültig löschen
	if c, _ := bob("DELETE", "/api/trash/bob/"+names["Mathe/c.txt"], ""); c != 200 {
		t.Fatal("endgültig:", c)
	}
	// Leeren
	bob("DELETE", "/api/files/bob/a.txt", "")
	bob("DELETE", "/api/files/bob/a (2).txt", "")
	if _, it = trashOf(t, bob, "bob"); len(it) != 2 {
		t.Fatal("vor dem Leeren:", it)
	}
	if c, b3 := bob("DELETE", "/api/trash", ""); c != 200 || !strings.Contains(b3, `"deleted":2`) {
		t.Fatal("leeren:", c, b3)
	}
	if _, it = trashOf(t, bob, "bob"); len(it) != 0 {
		t.Fatal("nach dem Leeren:", it)
	}
	// WebDAV-Löschen landet ebenfalls im Papierkorb; der Papierkorb ist per WebDAV nicht erreichbar
	bob("PUT", "/webdav/d.txt", "dav")
	if c, _ := bob("DELETE", "/webdav/d.txt", ""); c != 204 {
		t.Fatal("DAV delete:", c)
	}
	if _, it = trashOf(t, bob, "bob"); len(it) != 1 || it[0].Name != "d.txt" {
		t.Fatal("DAV-Papierkorb:", it)
	}
	if c, _ := bob("PROPFIND", "/webdav/.trash", ""); c != 404 {
		t.Fatal("Papierkorb per WebDAV sichtbar:", c)
	}
	if c, _ := bob("POST", "/api/files?name=.trash/x", "x"); c != 400 {
		t.Fatal("reservierter Name:", c)
	}
	// Verschieben/Umbenennen füllt den Papierkorb nicht
	bob("POST", "/api/files?name=m.txt", "mmm")
	bob("POST", "/api/filesmove?owner=bob&from=m.txt&to=n.txt", "")
	if _, it = trashOf(t, bob, "bob"); len(it) != 1 {
		t.Fatal("Umbenennen im Papierkorb:", it)
	}
	// leere Dateien: sofort weg
	bob("POST", "/api/files?name=leer.txt", "")
	bob("DELETE", "/api/files/bob/leer.txt", "")
	if _, it = trashOf(t, bob, "bob"); len(it) != 1 {
		t.Fatal("leere Datei im Papierkorb:", it)
	}
}

func TestTrashGroupAndSettings(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	bob := func(m, p, b string) (int, string) { return req(t, srv, "bob", m, p, b) }
	anna := func(m, p, b string) (int, string) { return req(t, srv, "anna", m, p, b) }
	// Gruppenordner mit Schreibrecht: bob löscht, Mitglied ohne Verwaltung sieht den Papierkorb des Ordners
	anna("POST", "/api/groups", `{"name":"t1","areas":["files"],"folder":"rw"}`)
	anna("POST", "/api/users/import?update=1", "bob;;t1\n")
	bob("POST", "/api/files?name=g.txt&owner=@t1", "gruppe")
	if c, _ := bob("DELETE", "/api/files/@t1/g.txt", ""); c != 200 {
		t.Fatal("delete:", c)
	}
	_, it := trashOf(t, bob, "bob")
	if len(it) != 1 || it[0].Owner != "@t1" || it[0].Name != "g.txt" {
		t.Fatal("Gruppen-Papierkorb:", it)
	}
	if c, _ := bob("POST", "/api/trash/@t1/"+it[0].ID+"/restore", ""); c != 200 {
		t.Fatal("restore:", c)
	}
	if c, got := bob("GET", "/api/files/@t1/g.txt", ""); c != 200 || got != "gruppe" {
		t.Fatal("Gruppendatei:", c, got)
	}
	// Einstellung: nur Admin, 0 = aus (löscht sofort endgültig)
	if c, _ := bob("POST", "/api/settings/trash", `{"days":5}`); c != 403 {
		t.Fatal("bob darf nicht:", c)
	}
	if c, _ := anna("POST", "/api/settings/trash", `{"days":-1}`); c != 400 {
		t.Fatal("negativ:", c)
	}
	if c, b := anna("POST", "/api/settings/trash", `{"days":0}`); c != 200 {
		t.Fatal(c, b)
	}
	if _, b := anna("GET", "/api/settings", ""); !strings.Contains(b, `"trashDays":0`) {
		t.Fatal("GET settings:", b)
	}
	bob("POST", "/api/files?name=x.txt", "xxx")
	bob("DELETE", "/api/files/bob/x.txt", "")
	if days, it := trashOf(t, bob, "bob"); days != 0 || len(it) != 0 {
		t.Fatal("Papierkorb aus:", days, it)
	}
	anna("POST", "/api/settings/trash", `{"days":7}`)
	bob("POST", "/api/files?name=y.txt", "yyy")
	bob("DELETE", "/api/files/bob/y.txt", "")
	if days, it := trashOf(t, bob, "bob"); days != 7 || len(it) != 1 {
		t.Fatal("7 Tage:", days, it)
	}
}

func TestTrashQuotaEvicts(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	bob := func(m, p, b string) (int, string) { return req(t, srv, "bob", m, p, b) }
	req(t, srv, "anna", "POST", "/api/settings/quota", `{"mb":1}`)
	blob := strings.Repeat("x", 600<<10)
	bob("POST", "/api/files?name=a.bin", blob)
	bob("DELETE", "/api/files/bob/a.bin", "") // 600 KB im Papierkorb
	if c, _ := bob("POST", "/api/files?name=b.bin", blob); c != 200 {
		t.Fatal("Upload muss den Papierkorb räumen:", c)
	}
	if _, it := trashOf(t, bob, "bob"); len(it) != 0 {
		t.Fatal("Papierkorb nach dem Räumen:", it)
	}
	// aktive Dateien werden nie geräumt
	if c, _ := bob("POST", "/api/files?name=c.bin", blob); c != 507 {
		t.Fatal("zweite aktive Datei:", c)
	}
}
