package office

import (
	"testing"

	"cs-team/conv"
	"cs-team/doc"
)

// Formate aus fremden Dateien muessen die Whitelist des Servers bestehen (sonst wuerden sie beim Import verworfen).
func TestImportKeepsFormats(t *testing.T) {
	in := []conv.Para{
		{V: "fett und Notiz", F: "l n1", R: "4:b;1:;4:i t;5:m" + "Pr%C3%BCfen"},
		{V: "Text"},
	}
	data, err := conv.ToDOCXRich(in)
	if err != nil {
		t.Fatal(err)
	}
	typ, paras, _, err := parseImport(".docx", data)
	if err != nil || typ != "text" || len(paras) != 2 {
		t.Fatal(typ, paras, err)
	}
	it := items("anna", paras, nil)
	got := it["p00001"]
	if got.F != "l n1" || got.R != paras[0].R || got.R == "" || !doc.ValidRuns(got.R) || !doc.ValidFmt(got.F) {
		t.Fatalf("Absatz 1: %+v", got)
	}

	cells := map[string]conv.FCell{"A1": {V: "5", F: "b t c#ff0000 g#ffff00 d2 pc hc w"}, "B1": {V: "x", F: "m" + "Hallo%20Welt"}, "C1": {V: "", F: "g#00ff00"}}
	xb, err := conv.ToXLSXRich(cells)
	if err != nil {
		t.Fatal(err)
	}
	typ, _, back, err := parseImport(".xlsx", xb)
	if err != nil || typ != "sheet" || len(back) != 3 {
		t.Fatal(typ, back, err)
	}
	ci := items("anna", nil, back)
	for k, c := range cells {
		if ci[k].V != c.V || ci[k].F != c.F {
			t.Fatalf("Zelle %s: %+v statt %+v", k, ci[k], c)
		}
	}
	// ungueltige Formate werden verworfen, der Text bleibt
	bad := items("anna", []conv.Para{{V: "x", F: "evil", R: "1:<b>"}}, map[string]conv.FCell{"A1": {V: "1", F: "javascript"}})
	if bad["p00001"].V != "x" || bad["p00001"].F != "" || bad["p00001"].R != "" || bad["A1"].V != "1" || bad["A1"].F != "" {
		t.Fatalf("%+v", bad)
	}
}
