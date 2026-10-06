package files

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"cs-team/store"
)

// Umbenennen/Verschieben (0.60): der öffentliche Link bleibt gültig und zeigt auf den neuen Namen; Ablauf wandert mit.
func TestMoveKeepsPublicLink(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	s := &Svc{St: st, Max: 1 << 20}
	if err := s.saveMeta(ctx, &Meta{Name: "a.txt", Owner: "bob", Size: 5, Type: "text/plain", Mod: time.Now().UnixNano()}); err != nil {
		t.Fatal(err)
	}
	st.Put(ctx, dataKey("bob", "a.txt"), []byte("hallo"), "")
	cur, _ := s.Meta(ctx, "bob", "a.txt")
	cur.Token, cur.Exp = strings.Repeat("ab", 16), time.Now().Add(time.Hour).Unix()
	s.saveMeta(ctx, cur)
	st.Put(ctx, tokKey(cur.Token), []byte("bob/a.txt"), "")
	// Verschieben geht über moveLink, sobald das Ziel angelegt ist (wie in MoveTo)
	st.Put(ctx, dataKey("bob", "b.txt"), []byte("hallo"), "")
	s.saveMeta(ctx, &Meta{Name: "b.txt", Owner: "bob", Size: 5, Type: "text/plain", Mod: time.Now().UnixNano()})
	s.moveLink(ctx, "bob", "a.txt", "b.txt")
	if err := s.removeFrom(ctx, "bob", "bob", "a.txt"); err != nil {
		t.Fatal(err)
	}
	mm, rc, err := s.Public(ctx, cur.Token)
	if err != nil || mm.Name != "b.txt" || mm.Exp != cur.Exp {
		t.Fatalf("Link nach dem Verschieben: %v %+v", err, mm)
	}
	if x, _ := io.ReadAll(rc); string(x) != "hallo" {
		t.Fatal("Inhalt:", string(x))
	}
	rc.Close()
}
