package files

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"cs-team/store"
)

func putTrash(t *testing.T, st store.Store, owner, id, orig string, at int64, size int) {
	t.Helper()
	ctx := context.Background()
	tn := trashName(id)
	b, _ := json.Marshal(Meta{Name: tn, Owner: owner, Size: int64(size), Trash: &TrashInfo{Name: orig, By: owner, At: at}})
	st.Put(ctx, metaKey(owner, tn), b, "")
	st.Put(ctx, dataKey(owner, tn), make([]byte, size), "")
}

func TestPurgeTrashExpiry(t *testing.T) {
	st := store.NewMem()
	days := 2
	s := &Svc{St: st, TrashDays: func() int { return days }}
	ctx := context.Background()
	now := time.Now().Unix()
	putTrash(t, st, "bob", "aaaaaaaaaaaa", "alt.txt", now-3*86400, 10)   // abgelaufen
	putTrash(t, st, "bob", "bbbbbbbbbbbb", "jung.txt", now-86400, 20)    // noch frisch
	putTrash(t, st, "@g", "cccccccccccc", "gruppe.txt", now-5*86400, 30) // abgelaufen (Gruppenordner)
	if n := s.PurgeTrash(ctx); n != 2 {
		t.Fatal("abgelaufen:", n)
	}
	all, _ := s.all(ctx)
	if len(all) != 1 || all[0].Trash == nil || all[0].Trash.Name != "jung.txt" {
		t.Fatalf("übrig: %+v", all)
	}
	if _, _, err := st.Get(ctx, dataKey("bob", trashName("aaaaaaaaaaaa"))); err == nil {
		t.Fatal("Inhalt des abgelaufenen Eintrags noch da")
	}
	// Papierkorb ausgeschaltet: alles weg
	days = 0
	if n := s.PurgeTrash(ctx); n != 1 {
		t.Fatal("aus:", n)
	}
	if all, _ = s.all(ctx); len(all) != 0 {
		t.Fatal("Rest:", all)
	}
}

func TestEvictTrashOldestFirst(t *testing.T) {
	st := store.NewMem()
	s := &Svc{St: st, TrashDays: func() int { return 30 }}
	ctx := context.Background()
	now := time.Now().Unix()
	putTrash(t, st, "bob", "111111111111", "a", now-300, 100)
	putTrash(t, st, "bob", "222222222222", "b", now-200, 100)
	putTrash(t, st, "bob", "333333333333", "c", now-100, 100)
	putTrash(t, st, "anna", "444444444444", "x", now-1000, 100) // fremder Besitzer bleibt
	if got := s.evictTrash(ctx, "bob", 150); got != 200 {       // 2 Einträge (die ältesten) reichen für 150
		t.Fatal("freigegeben:", got)
	}
	left := map[string]bool{}
	all, _ := s.all(ctx)
	for _, m := range all {
		left[m.Owner+"/"+m.Trash.Name] = true
	}
	if !left["bob/c"] || left["bob/a"] || left["bob/b"] || !left["anna/x"] {
		t.Fatal("falsche Auswahl:", left)
	}
}

func TestValidNameRejectsTrash(t *testing.T) {
	for _, n := range []string{".trash", ".trash/x", ".trash/aaaaaaaaaaaa"} {
		if ValidName(n) {
			t.Fatal("reservierter Name erlaubt:", n)
		}
	}
	if !ValidName("a/.trash") || !ValidName("x.trash") {
		t.Fatal("nur das erste Segment ist reserviert")
	}
}
