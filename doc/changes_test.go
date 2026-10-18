package doc

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cs-team/store"
)

func TestMergeChange(t *testing.T) {
	var l []Change
	l = mergeChange(l, Change{T0: 100, T: 110, By: "a", Op: "edit", Src: "user", Set: 2, Keys: []string{"p1", "p2"}})
	l = mergeChange(l, Change{T0: 200, T: 220, By: "a", Op: "edit", Src: "user", Set: 1, Del: 1, Keys: []string{"p2", "p3"}})
	if len(l) != 1 || l[0].Set != 3 || l[0].Del != 1 || l[0].T != 220 || l[0].T0 != 100 || len(l[0].Keys) != 3 {
		t.Fatalf("gleicher Benutzer dicht hintereinander = ein Eintrag: %+v", l)
	}
	l = mergeChange(l, Change{T0: 230, T: 230, By: "b", Op: "edit", Src: "user", Set: 1})
	l = mergeChange(l, Change{T0: 240, T: 240, By: "b", Op: "edit", Src: "ai", Set: 1})
	l = mergeChange(l, Change{T0: 250 + mergeWindow + 5, T: 250 + mergeWindow + 5, By: "b", Op: "edit", Src: "ai", Set: 1})
	if len(l) != 4 {
		t.Fatalf("anderer Benutzer, andere Quelle und grosser Abstand = neue Eintraege: %+v", l)
	}
	l = mergeChange(l, Change{T0: 5000, T: 5000, By: "a", Op: "readonly"})
	l = mergeChange(l, Change{T0: 5001, T: 5001, By: "a", Op: "edit", Src: "user", Set: 1})
	if l[len(l)-1].Op != "edit" || len(l) != 6 {
		t.Fatalf("nach einem anderen Eintrag nie zusammenfassen: %+v", l)
	}
	var keys []string
	for i := 0; i < 40; i++ {
		keys = append(keys, "k"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	l = mergeChange(nil, Change{T0: 1, T: 1, By: "a", Op: "edit", Src: "user", Set: 40, Keys: keys[:maxKeys]})
	l = mergeChange(l, Change{T0: 2, T: 2, By: "a", Op: "edit", Src: "user", Set: 1, Keys: keys[20:]})
	if len(l[0].Keys) != maxKeys {
		t.Fatalf("Schluesselliste begrenzt: %d", len(l[0].Keys))
	}
}

func TestMergeChangeCap(t *testing.T) {
	var l []Change
	for i := 0; i < maxChanges+50; i++ {
		l = mergeChange(l, Change{T0: int64(i), T: int64(i), By: "a", Op: "readonly"})
	}
	if len(l) != maxChanges || l[0].T0 != 50 {
		t.Fatalf("hoechstens %d, aelteste fallen weg: len=%d first=%d", maxChanges, len(l), l[0].T0)
	}
}

func TestSummarize(t *testing.T) {
	cr := func(src string) Change { return Change{T: 1, T0: 1, By: "u1", Op: "create", Src: src} }
	ed := func(by, src string) Change { return Change{T: 2, T0: 2, By: by, Op: "edit", Src: src, Set: 1} }
	cases := []struct {
		name string
		l    []Change
		want string
	}{
		{"leer", nil, "unknown"},
		{"nur Bearbeitung, kein create (aelteres Dokument)", []Change{ed("u2", "user")}, "unknown"},
		{"reine KI", []Change{cr("ai")}, "ai"},
		{"KI, nur KI-Funktionen danach", []Change{cr("ai"), ed("u1", "ai")}, "ai"},
		{"KI, dann Mensch", []Change{cr("ai"), ed("u2", "user")}, "ai-edited"},
		{"Mensch", []Change{cr("user")}, "human"},
		{"Import", []Change{cr("import"), ed("u1", "ai")}, "human"},
		{"Schreibschutz aendert nichts", []Change{cr("ai"), {Op: "readonly", By: "u1"}, {Op: "editable", By: "u1"}}, "ai"},
	}
	for _, c := range cases {
		if got := summarize(c.l).Origin; got != c.want {
			t.Errorf("%s: %s, erwartet %s", c.name, got, c.want)
		}
	}
	s := summarize([]Change{cr("ai"), ed("u2", "user"), ed("u3", "user"), ed("u2", "user"), ed("u1", "ai")})
	if s.UserEdits != 3 || s.AIEdits != 1 || s.Editors["u2"] != 2 || s.Editors["u3"] != 1 || s.Created == nil || s.Created.By != "u1" {
		t.Fatalf("Zaehlung: %+v", s)
	}
}

func TestReadonlyLevel(t *testing.T) {
	m := Meta{Owner: "o", Write: []string{"*"}, Read: []string{"*"}, RO: true}
	if rd, wr := m.Level("o"); !rd || wr {
		t.Fatalf("Besitzer liest, schreibt aber nicht: %v %v", rd, wr)
	}
	if rd, wr := m.Level("x"); !rd || wr {
		t.Fatalf("Fremder mit Schreibfreigabe liest nur: %v %v", rd, wr)
	}
}

func TestImportName(t *testing.T) {
	mod := time.Date(2026, 10, 18, 9, 5, 0, 0, time.Local).Unix()
	if got := importName("Recherche", mod); got != "Recherche_2026-10-18_09-05" {
		t.Fatal(got)
	}
	long := strings.Repeat("ä", 100) // 200 Bytes
	got := importName(long, mod)
	if len(got) > 100 || !utf8.ValidString(got) || !strings.HasSuffix(got, "_2026-10-18_09-05") {
		t.Fatalf("Name begrenzt, gueltiges UTF-8: %d %q", len(got), got)
	}
}

// Bearbeitungen werden vorgemerkt und beim Speichern in die Liste geschrieben; KI-Einfuegen ist ein eigener Eintrag.
func TestPersistWritesChanges(t *testing.T) {
	st, err := store.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub(st)
	ctx := context.Background()
	id, err := h.CreateMeta(ctx, Meta{Name: "T", Type: "text", Owner: "o"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Created(ctx, id, "o", "ai", 3)
	l, err := h.open(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	c := &client{user: "bob", write: true, out: make(chan []byte, 16)}
	l.apply(h, c, msgIn{T: "set", K: "p00001", V: "von Hand", Pos: 1})
	l.apply(h, c, msgIn{T: "set", K: "p00002", V: "noch ein Absatz", Pos: 2})
	l.apply(h, c, msgIn{T: "del", K: "p00001"})
	l.persist()
	list := readChanges(ctx, st, id)
	if len(list) != 2 {
		t.Fatalf("create + eine Bearbeitung (zusammengefasst) erwartet: %+v", list)
	}
	if list[0].Op != "create" || list[0].Src != "ai" || list[0].N != 3 {
		t.Fatalf("create: %+v", list[0])
	}
	if e := list[1]; e.Op != "edit" || e.By != "bob" || e.Src != "user" || e.Set != 2 || e.Del != 1 || len(e.Keys) != 2 {
		t.Fatalf("Bearbeitung: %+v", e)
	}
	if strings.Contains(string(mustGet(t, st, changesKey(id))), "von Hand") {
		t.Fatal("die Liste darf nie Inhalte enthalten")
	}
	// zweites Speichern ohne neue Bearbeitung schreibt nichts dazu
	l.persist()
	if n := len(readChanges(ctx, st, id)); n != 2 {
		t.Fatalf("keine doppelten Eintraege: %d", n)
	}
	if s := summarize(list); s.Origin != "ai-edited" {
		t.Fatalf("KI-Dokument, von bob von Hand bearbeitet: %s", s.Origin)
	}
}

func mustGet(t *testing.T, st store.Store, key string) []byte {
	t.Helper()
	b, _, err := st.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
