package conv

import (
	"strings"
	"testing"
)

func TestParasFromMarkdown(t *testing.T) {
	in := "# Ueberschrift\r\n\r\nEin **fetter** Satz.\n\n\n\n- Punkt eins\n* Punkt zwei\n  - eingerückt\n1. erste\n2) zweite\n\nSchluss\n\n\n"
	got := ParasFromMarkdown(in)
	want := []Para{
		BoldPara("Ueberschrift"),
		{},
		{V: "Ein fetter Satz.", R: "4:;6:b;6:"},
		{},
		{V: "Punkt eins", F: "l"},
		{V: "Punkt zwei", F: "l"},
		{V: "eingerückt", F: "l n1"},
		{V: "erste", F: "o"},
		{V: "zweite", F: "o"},
		{},
		{V: "Schluss"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d Absaetze statt %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Absatz %d: %+v statt %+v", i, got[i], want[i])
		}
	}
	if p := ParasFromMarkdown("  \n\n"); len(p) != 1 || p[0].V != "" {
		t.Fatal("leer:", p)
	}
	if p := ParasFromMarkdown(strings.Repeat("zeile\n", MaxParas+10)); len(p) != MaxParas {
		t.Fatal("Obergrenze:", len(p))
	}
	// die Formate bestehen die Pruefung des Servers (Reihenfolge und Zeichen der Laeufe)
	if g := BoldPara("x"); g.R != "1:b" {
		t.Fatal(g.R)
	}
}
