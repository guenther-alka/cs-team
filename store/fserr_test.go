package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWinBad(t *testing.T) {
	for _, n := range []string{"con", "NUL", "com1", "Lpt9", "aux.txt", "a:b", "x::$DATA", "name.", "x "} {
		if !WinBad(n) {
			t.Error("muss abgelehnt werden:", n)
		}
	}
	for _, n := range []string{"anna", "console", "com10", "com", "nulla", "lpt0", "a.b"} {
		if WinBad(n) {
			t.Error("muss erlaubt sein:", n)
		}
	}
}

// Fehler des Dateisystems enthalten nie den Serverpfad.
func TestFSErrorHasNoPath(t *testing.T) {
	root := t.TempDir()
	s, err := NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blk"), []byte("x"), 0o644); err != nil { // Datei statt Ordner
		t.Fatal(err)
	}
	_, err = s.Put(context.Background(), "blk/x", []byte("y"), "")
	if err == nil {
		t.Fatal("Fehler erwartet")
	}
	if err != ErrStorage || strings.Contains(err.Error(), root) {
		t.Fatal("Pfad im Fehler:", err)
	}
}
