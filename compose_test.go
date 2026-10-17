package main

import (
	"encoding/json"
	"strings"
	"testing"

	"cs-team/conv"
)

// Zusammenfuegen (ohne KI) und Text speichern: Ziel PDF oder cs-text, es gelten die Rechte des Benutzers.
func TestComposeMergeAndFromText(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	up := func(name, body string) {
		if c, b := req(t, srv, "anna", "POST", "/api/files?name="+name, body); c != 200 {
			t.Fatal("upload", name, c, b)
		}
	}
	up("a.pdf", string(conv.ToPDFRich("A", []conv.Para{{V: "ALPHA erste Datei"}})))
	up("b.pdf", string(conv.ToPDFRich("B", []conv.Para{{V: "BETA zweite Datei"}})))
	up("notiz.txt", "GAMMA dritte Datei\nzweite Zeile")
	up("bild.png", "kein bild")
	src := func(names ...string) string {
		var l []string
		for _, n := range names {
			l = append(l, `{"kind":"file","owner":"anna","name":"`+n+`"}`)
		}
		return `[` + strings.Join(l, ",") + `]`
	}
	idOf := func(b string) string {
		var r struct{ ID, Name string }
		if json.Unmarshal([]byte(b), &r) != nil {
			t.Fatal("json:", b)
		}
		return r.ID
	}

	// Ziel PDF: PDFs direkt verbunden, die Textdatei als PDF gesetzt und angehaengt
	c, b := req(t, srv, "anna", "POST", "/api/docs/merge", `{"sources":`+src("a.pdf", "b.pdf", "notiz.txt")+`,"target":"pdf","name":"alles"}`)
	if c != 200 || !strings.Contains(b, `"name":"alles.pdf"`) {
		t.Fatal("merge pdf:", c, b)
	}
	c, pdf := req(t, srv, "anna", "GET", "/api/files/anna/alles.pdf", "")
	if c != 200 || !strings.HasPrefix(pdf, "%PDF-") {
		t.Fatal("Ergebnis-PDF:", c)
	}
	txt, err := conv.FromPDF([]byte(pdf))
	if err != nil {
		t.Fatal(err)
	}
	i, j, k := strings.Index(txt, "ALPHA"), strings.Index(txt, "BETA"), strings.Index(txt, "GAMMA")
	if i < 0 || j < i || k < j {
		t.Fatalf("Reihenfolge im PDF: %d %d %d", i, j, k)
	}

	// Ziel cs-text: neues Mehrbenutzer-Dokument mit Titelzeilen
	c, b = req(t, srv, "anna", "POST", "/api/docs/merge", `{"sources":`+src("a.pdf", "notiz.txt", "b.pdf")+`,"target":"text","name":"Sammlung"}`)
	if c != 200 {
		t.Fatal("merge text:", c, b)
	}
	id := idOf(b)
	c, b = req(t, srv, "anna", "GET", "/api/docs/"+id+"/export", "")
	if c != 200 || !strings.HasPrefix(b, "a\n") {
		t.Fatalf("Textdokument: %d %q", c, b)
	}
	i, j, k = strings.Index(b, "ALPHA"), strings.Index(b, "GAMMA"), strings.Index(b, "BETA")
	if i < 0 || j < i || k < j || !strings.Contains(b, "\nnotiz\n") || !strings.Contains(b, "\nb\n") {
		t.Fatalf("Reihenfolge/Titel im Text: %q", b)
	}
	if c, b := req(t, srv, "anna", "GET", "/api/docs", ""); c != 200 || !strings.Contains(b, "Sammlung") {
		t.Fatal("Dokumentliste", c)
	}

	// Fehler und Rechte
	for _, tc := range []struct {
		who, body string
		want      int
		what      string
	}{
		{"anna", `{"sources":` + src("a.pdf") + `,"target":"pdf"}`, 400, "nur eine Quelle"},
		{"anna", `{"sources":` + src("a.pdf", "b.pdf") + `,"target":"odt"}`, 400, "unbekanntes Ziel"},
		{"anna", `{"sources":` + src("a.pdf", "bild.png") + `,"target":"pdf"}`, 400, "Dateityp"},
		{"anna", `{"sources":` + src("a.pdf", "fehlt.pdf") + `,"target":"pdf"}`, 404, "Datei fehlt"},
		{"bob", `{"sources":` + src("a.pdf", "b.pdf") + `,"target":"pdf"}`, 404, "fremde Dateien"},
		{"anna", `{"sources":[{"kind":"doc","id":"gibtsnicht"},{"kind":"doc","id":"auch-nicht"}],"target":"text"}`, 404, "fremde Dokumente"},
		{"anna", `{kaputt`, 400, "kaputtes JSON"},
	} {
		if c, b := req(t, srv, tc.who, "POST", "/api/docs/merge", tc.body); c != tc.want {
			t.Fatalf("%s: %d %s", tc.what, c, b)
		}
	}
	// ein Textdokument als Quelle (ohne Rechte von bob nicht lesbar), Ziel PDF
	c, b = req(t, srv, "anna", "POST", "/api/docs/merge", `{"sources":[{"kind":"doc","id":"`+id+`"},{"kind":"file","owner":"anna","name":"b.pdf"}],"target":"pdf","name":"mix"}`)
	if c != 200 || !strings.Contains(b, "mix.pdf") {
		t.Fatal("Dokument + PDF:", c, b)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/docs/merge", `{"sources":[{"kind":"doc","id":"`+id+`"},{"kind":"doc","id":"`+id+`"}],"target":"text"}`); c != 404 {
		t.Fatal("bob liest Annas Dokument", c)
	}

	// Text speichern (KI-Zusammenfassung): Formate aus der Schreibweise
	c, b = req(t, srv, "anna", "POST", "/api/docs/fromtext", `{"text":"# Titel\n\n- eins\n- **zwei**\n1. erste","target":"text","name":"Zusammenfassung"}`)
	if c != 200 {
		t.Fatal("fromtext text:", c, b)
	}
	if c, b := req(t, srv, "anna", "GET", "/api/docs/"+idOf(b)+"/export?format=cstext", ""); c != 200 || !strings.Contains(b, `"f": "l"`) || !strings.Contains(b, `"r": "4:b"`) {
		t.Fatalf("Formate im cs-text: %d %s", c, b)
	}
	c, b = req(t, srv, "anna", "POST", "/api/docs/fromtext", `{"text":"Zusammenfassung\n- eins","target":"pdf","name":"Zus"}`)
	if c != 200 || !strings.Contains(b, "Zus.pdf") {
		t.Fatal("fromtext pdf:", c, b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/docs/fromtext", `{"text":"   ","target":"pdf"}`); c != 400 {
		t.Fatal("leerer Text", c)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/docs/fromtext", `{"text":"x","target":"calc"}`); c != 400 {
		t.Fatal("falsches Ziel", c)
	}
}
