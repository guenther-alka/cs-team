package store

// Fruehere Staende eines Objekts aus ZFS-Snapshots (0.63.0, fuer Texte und Calc). Liegt der Ordner-Speicher (FS) auf einem
// ZFS-Dataset, enthalten dessen Snapshots frueheren Staende unter <Mountpunkt>/.zfs/snapshot/<snap>/<Pfad der Speicherwurzel>/<Schluessel>.
// cs-team liest dieses Verzeichnis nur (kein zfs-Befehl, keine Schreibzugriffe). Der Mountpunkt ist der naechste uebergeordnete
// Ordner der Speicherwurzel mit einem Unterordner ".zfs"; CS_SNAP_ROOT legt ihn fest. S3-Betrieb und Ordner ohne Snapshots:
// supported=false. Snapshot-Namen sind ein einzelnes Pfadsegment; Symlinks werden im Snapshot nie verfolgt.
// (Die Dateiversionen in package files haben eine eigene, gleichartige Fassung.)

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxSnapVersions = 50       // Eintraege der Liste
	maxSnapScan     = 300      // neueste Snapshots, die je Anfrage angesehen werden (jeder Zugriff kann einen Snapshot einhaengen)
	maxSnapRead     = 64 << 20 // groesster Stand, der gelesen wird
)

type pathStore interface {
	Root() string
	Path(key string) (string, error)
}

// SnapVersion: ein frueherer Stand des Objekts.
type SnapVersion struct {
	Snap string `json:"snap"`
	Size int64  `json:"size"`
	Mod  int64  `json:"mod"` // Aenderungszeit der Datei im Snapshot (unix s)
}

func validSnapName(n string) bool {
	if n == "" || len(n) > 200 || n == "." || n == ".." || !utf8.ValidString(n) || strings.ContainsAny(n, "/\\") {
		return false
	}
	return !strings.ContainsFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// snapMountOf: Mountpunkt des Datasets (CS_SNAP_ROOT oder der naechste Vorfahre von root mit Ordner .zfs); "" = keiner.
func snapMountOf(root string) string {
	if v := os.Getenv("CS_SNAP_ROOT"); v != "" {
		a, err := filepath.Abs(v)
		if err != nil {
			return ""
		}
		return a
	}
	for d := root; ; {
		if fi, err := os.Lstat(filepath.Join(d, ".zfs")); err == nil && fi.IsDir() {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			return ""
		}
		d = p
	}
}

// snapLocOf: Snapshot-Verzeichnis, Pfadsegmente des Objekts unterhalb eines Snapshots und Pfad der aktuellen Datei.
func snapLocOf(st Store, key string) (snapDir string, rel []string, cur string, ok bool) {
	ps, isFS := st.(pathStore)
	if !isFS {
		return
	}
	root := ps.Root()
	mount := snapMountOf(root)
	if mount == "" {
		return
	}
	rr, err := filepath.Rel(mount, root)
	if err != nil || rr == ".." || strings.HasPrefix(rr, ".."+string(filepath.Separator)) {
		return
	}
	cur, err = ps.Path(key)
	if err != nil {
		return
	}
	fr, err := filepath.Rel(root, cur)
	if err != nil {
		return
	}
	if rr != "." {
		rel = strings.Split(rr, string(filepath.Separator))
	}
	rel = append(rel, strings.Split(fr, string(filepath.Separator))...)
	for _, sg := range rel {
		if sg == "" || sg == "." || sg == ".." {
			return
		}
	}
	return filepath.Join(mount, ".zfs", "snapshot"), rel, cur, true
}

// lstatInSnap: Pfad base/segs... ohne Symlinks zu folgen: Zwischenstuecke muessen Ordner, das Ende eine normale Datei sein.
func lstatInSnap(base string, segs ...string) (os.FileInfo, string, error) {
	p := base
	var fi os.FileInfo
	for i, sg := range segs {
		p = filepath.Join(p, sg)
		var err error
		if fi, err = os.Lstat(p); err != nil {
			return nil, "", ErrNotFound
		}
		if (i < len(segs)-1 && !fi.IsDir()) || (i == len(segs)-1 && !fi.Mode().IsRegular()) {
			return nil, "", ErrNotFound
		}
	}
	return fi, p, nil
}

// SnapVersions: fruehere Staende des Objekts key aus den Snapshots, neueste zuerst; aufeinanderfolgende Snapshots mit gleicher
// Groesse und Zeit zaehlen einmal (der aelteste bleibt), Staende wie das aktuelle Objekt entfallen.
func SnapVersions(st Store, key string) (supported bool, out []SnapVersion) {
	out = []SnapVersion{}
	snapDir, rel, cur, ok := snapLocOf(st, key)
	if !ok {
		return false, out
	}
	ents, err := os.ReadDir(snapDir)
	if err != nil {
		return false, out
	}
	type snap struct {
		name string
		t    time.Time
	}
	var snaps []snap
	for _, e := range ents {
		if !e.IsDir() || !validSnapName(e.Name()) {
			continue
		}
		if fi, err := e.Info(); err == nil {
			snaps = append(snaps, snap{e.Name(), fi.ModTime()})
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].t.Equal(snaps[j].t) {
			return snaps[i].t.Before(snaps[j].t)
		}
		return snaps[i].name < snaps[j].name
	})
	if len(snaps) > maxSnapScan {
		snaps = snaps[len(snaps)-maxSnapScan:]
	}
	var curSize, curMod int64 = -1, 0
	if fi, err := os.Stat(cur); err == nil {
		curSize, curMod = fi.Size(), fi.ModTime().UnixNano()
	}
	lastSize, lastMod := int64(-1), int64(0)
	for _, sn := range snaps { // alt -> neu
		fi, _, err := lstatInSnap(snapDir, append([]string{sn.name}, rel...)...)
		if err != nil {
			lastSize = -1 // in diesem Snapshot gab es das Objekt nicht
			continue
		}
		size, mod := fi.Size(), fi.ModTime().UnixNano()
		same := size == lastSize && mod == lastMod
		lastSize, lastMod = size, mod
		if same || (size == curSize && mod == curMod) {
			continue
		}
		out = append(out, SnapVersion{sn.name, size, fi.ModTime().Unix()})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > maxSnapVersions {
		out = out[:maxSnapVersions]
	}
	return true, out
}

// ReadSnap liest den Stand des Objekts key im Snapshot snap (Inhalt und Aenderungszeit, unix s).
func ReadSnap(st Store, key, snap string) ([]byte, int64, error) {
	if !validSnapName(snap) {
		return nil, 0, ErrNotFound
	}
	snapDir, rel, _, ok := snapLocOf(st, key)
	if !ok {
		return nil, 0, ErrNotFound
	}
	fi, p, err := lstatInSnap(snapDir, append([]string{snap}, rel...)...)
	if err != nil {
		return nil, 0, err
	}
	if fi.Size() > maxSnapRead {
		return nil, 0, errors.New("version too large")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, ErrNotFound
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSnapRead+1))
	if err != nil {
		return nil, 0, err
	}
	return b, fi.ModTime().Unix(), nil
}
