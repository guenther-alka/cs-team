package share

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mk(t *testing.T, root, rel, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tree: <tmp>/share mit allen Faellen; <tmp>/outside.txt liegt ausserhalb.
func tree(t *testing.T) (*Svc, string) {
	base := t.TempDir()
	root := filepath.Join(base, "share")
	for _, f := range []string{
		"readme.txt", "docs/a.pdf",
		"groups/class1/a.txt", "groups/class2/b.txt", "groups/klasse5@school.de/k.txt",
		"users/anna/x.txt", "users/bob/y.txt", "users/anna@school.de/z.txt",
		".hidden", "~$lock.docx", ".zfs/snapshot/x.txt", "desktop.ini",
	} {
		mk(t, root, f, "data-"+f)
	}
	mk(t, base, "outside.txt", "secret")
	return &Svc{Root: root}, base
}

func names(es []Entry) string {
	var l []string
	for _, e := range es {
		n := e.Name
		if e.Dir {
			n += "/"
		}
		l = append(l, n)
	}
	return strings.Join(l, ",")
}

var (
	peter = Who{User: "peter.pan", Groups: []string{"Class1"}}
	anna  = Who{User: "anna"}
	adAnn = Who{User: "Anna@School.de", Groups: []string{"Klasse5@School.de"}}
	admin = Who{User: "root", Admin: true}
	guest = Who{User: "gast", Guest: true, Groups: []string{"class1"}}
)

func TestListPerUser(t *testing.T) {
	s, _ := tree(t)
	for _, c := range []struct {
		w    Who
		rel  string
		want string
	}{
		{peter, "", "docs/,groups/,readme.txt"}, // users: fuer peter nichts sichtbar
		{peter, "groups", "class1/"},
		{anna, "", "docs/,users/,readme.txt"}, // groups: anna ist in keiner Gruppe (Ordner zuerst)
		{anna, "users", "anna/"},
		{adAnn, "users", "anna@school.de/"},
		{adAnn, "groups", "klasse5@school.de/"},
		{admin, "", "docs/,groups/,users/,readme.txt"},
		{admin, "groups", "class1/,class2/,klasse5@school.de/"},
		{admin, "users", "anna/,anna@school.de/,bob/"},
		{guest, "", "docs/,readme.txt"},
	} {
		es, err := s.List(c.w, c.rel)
		if err != nil {
			t.Fatalf("%s %q: %v", c.w.User, c.rel, err)
		}
		if got := names(es); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.w.User, c.rel, got, c.want)
		}
	}
}

func TestOpenRules(t *testing.T) {
	s, _ := tree(t)
	for _, c := range []struct {
		w   Who
		rel string
		ok  bool
	}{
		{peter, "readme.txt", true},
		{peter, "/readme.txt/", true},
		{peter, "docs/a.pdf", true},
		{peter, "groups/class1/a.txt", true},
		{peter, "groups/class2/b.txt", false},
		{peter, "users/anna/x.txt", false},
		{peter, "GROUPS/class2/b.txt", false},
		{peter, "groups./class2/b.txt", false}, // Windows ignoriert den Punkt am Ende
		{peter, "groups/../users/anna/x.txt", false},
		{peter, "../outside.txt", false},
		{peter, "docs/../../outside.txt", false},
		{peter, "groups//class2/b.txt", false},
		{peter, "groups\\class2\\b.txt", false},
		{peter, "readme.txt:stream", false},
		{peter, ".hidden", false},
		{peter, "~$lock.docx", false},
		{peter, ".zfs/snapshot/x.txt", false},
		{peter, "desktop.ini", false},
		{peter, "", false},     // Ordner, keine Datei
		{peter, "docs", false}, // Ordner
		{anna, "users/anna/x.txt", true},
		{anna, "users/bob/y.txt", false},
		{anna, "groups/class1/a.txt", false},
		{adAnn, "users/anna@school.de/z.txt", true}, // Name@Domain, ohne Gross-/Kleinschreibung
		{adAnn, "users/anna/x.txt", false},
		{adAnn, "groups/klasse5@school.de/k.txt", true},
		{admin, "users/bob/y.txt", true},
		{admin, "groups/class2/b.txt", true},
		{admin, ".zfs/snapshot/x.txt", false}, // auch Admins nie
		{admin, "../outside.txt", false},
		{guest, "readme.txt", true},
		{guest, "groups/class1/a.txt", false},
		{guest, "users/anna/x.txt", false},
	} {
		f, _, err := s.Open(c.w, c.rel)
		if f != nil {
			f.Close()
		}
		if (err == nil) != c.ok {
			t.Errorf("%s %q: err=%v, want ok=%v", c.w.User, c.rel, err, c.ok)
		}
	}
}

func TestSymlinksRefused(t *testing.T) {
	s, base := tree(t)
	if err := os.Symlink(filepath.Join(base, "outside.txt"), filepath.Join(s.Root, "link.txt")); err != nil {
		t.Skip("keine Symlinks moeglich:", err)
	}
	if err := os.Symlink(base, filepath.Join(s.Root, "ldir")); err != nil {
		t.Skip("keine Symlinks moeglich:", err)
	}
	if err := os.Symlink(filepath.Join(s.Root, "users"), filepath.Join(s.Root, "docs", "tousers")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"link.txt", "ldir/outside.txt", "docs/tousers/anna/x.txt"} {
		if f, _, err := s.Open(admin, rel); err == nil {
			f.Close()
			t.Errorf("%q wurde geoeffnet", rel)
		} else if !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", rel, err)
		}
	}
	es, err := s.List(admin, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(es); strings.Contains(got, "link.txt") || strings.Contains(got, "ldir") {
		t.Errorf("Symlinks in der Liste: %s", got)
	}
	if es, _ = s.List(admin, "docs"); strings.Contains(names(es), "tousers") {
		t.Errorf("Symlink in docs: %s", names(es))
	}
}

func TestRootMayBeLink(t *testing.T) { // der Admin darf share selbst als Link/Mountpunkt anlegen
	s, base := tree(t)
	lnk := filepath.Join(base, "lnk")
	if err := os.Symlink(s.Root, lnk); err != nil {
		t.Skip("keine Symlinks moeglich:", err)
	}
	l := &Svc{Root: lnk}
	f, _, err := l.Open(peter, "readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := l.Open(peter, "groups/class2/b.txt"); err == nil {
		t.Error("fremde Gruppe ueber den Link lesbar")
	}
}

func TestDisabled(t *testing.T) {
	var nilSvc *Svc
	if nilSvc.Enabled() || (&Svc{}).Enabled() || (&Svc{Root: filepath.Join(t.TempDir(), "gibtsnicht")}).Enabled() {
		t.Error("ohne Ordner share muss alles aus sein")
	}
	s := &Svc{Root: filepath.Join(t.TempDir(), "gibtsnicht")}
	if _, err := s.List(admin, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("List ohne share: %v", err)
	}
	if _, _, err := s.Open(admin, "x.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open ohne share: %v", err)
	}
}

func TestUniqueName(t *testing.T) {
	have := map[string]bool{"b.docx": true, "b (2).docx": true, "c": true}
	ex := func(n string) bool { return have[n] }
	for in, want := range map[string]string{"a.txt": "a.txt", "b.docx": "b (3).docx", "c": "c (2)"} {
		if got := uniqueName(ex, in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
