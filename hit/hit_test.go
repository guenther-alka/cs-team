package hit

import (
	"strings"
	"testing"
)

func TestQueryAndSnip(t *testing.T) {
	if _, ok := Query(" a "); ok {
		t.Fatal("ein Zeichen")
	}
	if q, ok := Query("  Müller  "); !ok || q != "müller" {
		t.Fatal(q, ok)
	}
	if _, ok := Query(strings.Repeat("x", 81)); ok {
		t.Fatal("zu lang")
	}
	long := strings.Repeat("alt ", 40) + "Das Referat über Äpfel ist fällig " + strings.Repeat("neu ", 40)
	sn, ok := Snip(long, "äpfel")
	if !ok || !strings.Contains(sn, "Äpfel") || !strings.HasPrefix(sn, "…") || !strings.HasSuffix(sn, "…") || len([]rune(sn)) > 140 {
		t.Fatalf("Ausschnitt: %q", sn)
	}
	if sn, ok := Snip("kurz", "ur"); !ok || sn != "kurz" {
		t.Fatal(sn, ok)
	}
	if _, ok := Snip("nichts", "xyz"); ok {
		t.Fatal("kein Treffer erwartet")
	}
	if _, ok := Snip("", "ab"); ok {
		t.Fatal("leerer Text")
	}
}
