package store

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func testStore(t *testing.T, s Store) {
	ctx := context.Background()
	if _, _, err := s.Get(ctx, "a/b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	e1, err := s.Put(ctx, "a/b", []byte("1"), "*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "a/b", []byte("x"), "*"); !errors.Is(err, ErrConflict) {
		t.Fatalf("create twice: %v", err)
	}
	e2, err := s.Put(ctx, "a/b", []byte("22"), e1)
	if err != nil || e2 == e1 {
		t.Fatalf("update: %v %s %s", err, e1, e2)
	}
	if _, err := s.Put(ctx, "a/b", []byte("z"), e1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale etag: %v", err)
	}
	if _, err := s.Put(ctx, "a/nix", []byte("z"), "e-x"); !errors.Is(err, ErrConflict) {
		t.Fatalf("match on missing: %v", err)
	}
	b, e, err := s.Get(ctx, "a/b")
	if err != nil || string(b) != "22" || e != e2 {
		t.Fatalf("get: %q %s %v", b, e, err)
	}
	// schnelle Folgeänderung gleicher Größe -> anderer ETag
	e3, _ := s.Put(ctx, "a/b", []byte("33"), "")
	if e3 == e2 {
		t.Fatal("etag unchanged")
	}
	s.Put(ctx, "a/c/d", []byte("d"), "")
	s.Put(ctx, "files/anna/Über: uns?.txt", []byte("sonder"), "")
	s.Put(ctx, "files/anna/x.", []byte("punkt"), "")
	if err := s.PutStream(ctx, "files/anna/big", strings.NewReader(strings.Repeat("x", 100000)), -1, ""); err != nil {
		t.Fatal(err)
	}
	l, _ := s.List(ctx, "a/")
	if len(l) != 2 || l[0].Key != "a/b" || l[1].Key != "a/c/d" {
		t.Fatalf("list a/: %+v", l)
	}
	l, _ = s.List(ctx, "files/anna/")
	keys := map[string]int64{}
	for _, i := range l {
		keys[i.Key] = i.Size
	}
	if len(keys) != 3 || keys["files/anna/Über: uns?.txt"] != 6 || keys["files/anna/x."] != 5 || keys["files/anna/big"] != 100000 {
		t.Fatalf("list files: %v", keys)
	}
	if l, _ = s.List(ctx, "a/b"); len(l) != 1 {
		t.Fatalf("list exact: %+v", l)
	}
	if l, _ = s.List(ctx, "nope/"); len(l) != 0 {
		t.Fatalf("list none: %+v", l)
	}
	rc, err := s.GetStream(ctx, "files/anna/big")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, rc)
	rc.Close()
	if n != 100000 {
		t.Fatalf("stream %d", n)
	}
	if _, err := s.GetStream(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stream missing: %v", err)
	}
	s.Delete(ctx, "a/c/d")
	if _, _, err := s.Get(ctx, "a/c/d"); !errors.Is(err, ErrNotFound) {
		t.Fatal("delete")
	}
	s.Delete(ctx, "a/c/d") // idempotent
}

func TestMemStore(t *testing.T) { testStore(t, NewMem()) }

func TestFSStore(t *testing.T) {
	s, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	testStore(t, s)
	for _, k := range []string{"../x", "a/../b", "/abs", "a//b", "a\\b", ""} {
		if _, err := s.Put(context.Background(), k, []byte("x"), ""); err == nil {
			t.Fatalf("bad key accepted: %q", k)
		}
	}
	// Neustart: Daten bleiben
	s2, _ := NewFS(s.root)
	if b, _, err := s2.Get(context.Background(), "a/b"); err != nil || string(b) != "33" {
		t.Fatalf("persist: %q %v", b, err)
	}
}

func TestMove(t *testing.T) {
	ctx := context.Background()
	fs, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Store{"mem": NewMem(), "fs": fs} {
		if err := s.PutStream(ctx, "files/u/a\x1fb", strings.NewReader("inhalt"), 6, "text/plain"); err != nil {
			t.Fatal(name, err)
		}
		if err := Move(ctx, s, "files/u/a\x1fb", "files/u/.trash\x1fxyz"); err != nil {
			t.Fatal(name, "move:", err)
		}
		if _, err := s.GetStream(ctx, "files/u/a\x1fb"); !errors.Is(err, ErrNotFound) {
			t.Fatal(name, "Quelle noch da:", err)
		}
		rc, err := s.GetStream(ctx, "files/u/.trash\x1fxyz")
		if err != nil {
			t.Fatal(name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != "inhalt" {
			t.Fatal(name, "Inhalt:", string(b))
		}
		if err := Move(ctx, s, "files/u/gibtsnicht", "files/u/x"); !errors.Is(err, ErrNotFound) {
			t.Fatal(name, "fehlende Quelle:", err)
		}
	}
}
