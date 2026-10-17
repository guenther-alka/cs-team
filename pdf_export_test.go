package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// PDF-Export ueber die Schnittstelle: Text per GET, Calc per POST mit den im Browser angezeigten Werten; Rechte wie beim Export insgesamt.
func TestPDFExportAPI(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	mk := func(name, typ string) string {
		c, b := req(t, srv, "anna", "POST", "/api/docs", `{"name":"`+name+`","type":"`+typ+`"}`)
		var r struct{ ID string }
		if c != 200 || json.Unmarshal([]byte(b), &r) != nil || r.ID == "" {
			t.Fatal(c, b)
		}
		return r.ID
	}
	txt, sheet := mk("Bericht", "text"), mk("Zahlen", "sheet")
	if c, b := req(t, srv, "anna", "GET", "/api/docs/"+txt+"/export?format=pdf", ""); c != 200 || !strings.HasPrefix(b, "%PDF-1.4") || !strings.HasSuffix(b, "%%EOF\n") {
		t.Fatal("Text als PDF:", c, len(b))
	}
	if c, b := req(t, srv, "anna", "POST", "/api/docs/"+sheet+"/export?format=pdf", `{"vals":{"A1":"1.234,50 €","B2":"x"}}`); c != 200 || !strings.HasPrefix(b, "%PDF-1.4") {
		t.Fatal("Calc als PDF (POST):", c, len(b))
	}
	if c, b := req(t, srv, "anna", "GET", "/api/docs/"+sheet+"/export?format=pdf", ""); c != 200 || !strings.HasPrefix(b, "%PDF-1.4") {
		t.Fatal("Calc als PDF (GET):", c, len(b))
	}
	// fremde Dokumente: kein Zugriff
	if c, _ := req(t, srv, "bob", "GET", "/api/docs/"+txt+"/export?format=pdf", ""); c != 404 {
		t.Fatal("bob darf das Dokument nicht exportieren", c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/docs/"+sheet+"/export?format=pdf", `{"vals":{"A1":"x"}}`); c != 404 {
		t.Fatal("bob darf die Tabelle nicht exportieren", c)
	}
	// unbekanntes Format bleibt ein Fehler; kaputter Body bei POST fuehrt zu den gespeicherten Werten, nicht zum Absturz
	if c, _ := req(t, srv, "anna", "GET", "/api/docs/"+txt+"/export?format=odt", ""); c != 400 {
		t.Fatal("unbekanntes Format", c)
	}
	if c, b := req(t, srv, "anna", "POST", "/api/docs/"+sheet+"/export?format=pdf", `{kaputt`); c != 200 || !strings.HasPrefix(b, "%PDF-1.4") {
		t.Fatal("kaputter Body", c)
	}
}
