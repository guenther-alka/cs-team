package conv

import "testing"

func TestToCSVSparse(t *testing.T) { // F2: weit entfernte Zellen dürfen nur leere Felder kosten
	b := ToCSV(map[string]string{"A1": "x", "ZZ300": "y"})
	if len(b) > 1<<20 {
		t.Fatalf("zu gross: %d", len(b))
	}
	if _, _, ok := ColRow("ZZZ99999"); ok {
		t.Fatal("3 Buchstaben duerfen keine gueltige Zelle mehr sein")
	}
}
