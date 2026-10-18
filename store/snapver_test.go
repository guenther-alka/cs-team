package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// snapFixture: Speicher unter <mount>/data mit nachgebautem .zfs/snapshot (zwei Staende + ein Snapshot ohne das Objekt).
func snapFixture(t *testing.T) (*FS, string) {
	t.Helper()
	mount := t.TempDir()
	t.Setenv("CS_SNAP_ROOT", mount)
	root := filepath.Join(mount, "data")
	st, err := NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(context.Background(), "doc/a1/snapshot.json", []byte(`{"v":"jetzt"}`), ""); err != nil {
		t.Fatal(err)
	}
	cur, _ := st.Path("doc/a1/snapshot.json")
	rel, _ := filepath.Rel(mount, cur)
	put := func(snap, body string, age time.Duration) {
		p := filepath.Join(mount, ".zfs", "snapshot", snap, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if body != "" {
			os.WriteFile(p, []byte(body), 0o644)
			mt := time.Now().Add(-age)
			os.Chtimes(p, mt, mt)
		}
		// Snapshot-Verzeichnis selbst bekommt die Zeit (Reihenfolge)
		sd := filepath.Join(mount, ".zfs", "snapshot", snap)
		mt := time.Now().Add(-age)
		os.Chtimes(sd, mt, mt)
	}
	put("auto-1", `{"v":"alt1"}`, 3*time.Hour)
	put("auto-2", `{"v":"alt1"}`, 2*time.Hour) // gleicher Stand wie auto-1 (gleiche Zeit/Groesse): zaehlt einmal -> hier andere Zeit
	put("auto-0", "", 4*time.Hour)             // Snapshot ohne Objekt
	return st, mount
}

func TestSnapVersionsListAndRead(t *testing.T) {
	st, _ := snapFixture(t)
	ok, vs := SnapVersions(st, "doc/a1/snapshot.json")
	if !ok {
		t.Fatal("not supported")
	}
	if len(vs) == 0 {
		t.Fatal("no versions")
	}
	if vs[0].Mod < vs[len(vs)-1].Mod {
		t.Fatalf("neueste zuerst erwartet: %+v", vs)
	}
	b, mod, err := ReadSnap(st, "doc/a1/snapshot.json", vs[0].Snap)
	if err != nil || len(b) == 0 || mod == 0 {
		t.Fatalf("read: %v %q %d", err, b, mod)
	}
}

func TestSnapVersionsRejects(t *testing.T) {
	st, _ := snapFixture(t)
	for _, bad := range []string{"", "..", "a/b", "a\\b", "x\x00y", "."} {
		if _, _, err := ReadSnap(st, "doc/a1/snapshot.json", bad); err == nil {
			t.Fatalf("snap %q muss abgelehnt werden", bad)
		}
	}
	if _, _, err := ReadSnap(st, "doc/a1/snapshot.json", "gibts-nicht"); err == nil {
		t.Fatal("unbekannter Snapshot")
	}
}

func TestSnapVersionsNoZfs(t *testing.T) {
	t.Setenv("CS_SNAP_ROOT", "")
	st, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ok, vs := SnapVersions(st, "doc/a1/snapshot.json"); ok || len(vs) != 0 {
		t.Fatalf("ohne .zfs: supported=false erwartet, got %v %v", ok, vs)
	}
}

func TestSnapNoSymlinkFollow(t *testing.T) {
	st, mount := snapFixture(t)
	cur, _ := st.Path("doc/a1/snapshot.json")
	rel, _ := filepath.Rel(mount, cur)
	secret := filepath.Join(t.TempDir(), "geheim.json")
	os.WriteFile(secret, []byte("geheim"), 0o644)
	p := filepath.Join(mount, ".zfs", "snapshot", "evil", rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.Symlink(secret, p); err != nil {
		t.Skip("symlink nicht moeglich:", err)
	}
	if _, _, err := ReadSnap(st, "doc/a1/snapshot.json", "evil"); err == nil {
		t.Fatal("Symlink im Snapshot darf nicht gelesen werden")
	}
}
