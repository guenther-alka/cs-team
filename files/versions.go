package files

// Dateiversionen (0.60.0): liegt der Ordner-Speicher (CS_DIR) auf einem ZFS-Dataset, enthalten dessen Snapshots frühere Stände der
// Dateien unter <Mountpunkt>/.zfs/snapshot/<snap>/<Pfad der Speicherwurzel>/files/<owner>/<name>. cs-team liest dieses Verzeichnis
// nur (kein zfs-Befehl, keine Schreibzugriffe): Liste der Versionen, Herunterladen und Wiederherstellen (kopiert die Version als
// aktuelle Datei zurück; der bisherige Stand kommt vorher als Kopie in den Papierkorb).
//
// Der Mountpunkt ist der nächste übergeordnete Ordner der Speicherwurzel mit einem Unterordner ".zfs"; CS_SNAP_ROOT legt ihn
// fest. S3-/Speicher-Betrieb und Ordner ohne Snapshots: "supported": false.
// Rechte: Liste und Herunterladen nur für den Besitzer oder mit Schreibrecht (Gruppenordner: Schreibrecht oder Gruppen-Admin); wer
// die Datei nur lesen darf, sieht frühere Stände nicht (sie können von vor der Freigabe stammen): 403, ohne jedes Recht 404.
// Wiederherstellen braucht Schreibrecht. (Zum Vergleich Umfragen: Mitglieder mit nur Leserecht im Chat dürfen abstimmen, aber
// keine Umfrage anlegen.)
// Snapshot-Namen sind ein einzelnes Pfadsegment; Symlinks werden im Snapshot nie verfolgt.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cs-team/auth"
)

const (
	maxVersions = 50  // Einträge der Liste
	maxSnapScan = 300 // neueste Snapshots, die je Anfrage angesehen werden (jeder Zugriff kann einen Snapshot einhängen)
)

// pathStore: Speicher, der seine Dateien in einem lokalen Ordner hält (store.FS).
type pathStore interface {
	Root() string
	Path(key string) (string, error)
}

type Version struct {
	Snap string `json:"snap"`
	Size int64  `json:"size"`
	Mod  int64  `json:"mod"` // Änderungszeit der Datei im Snapshot (unix s)
}

type VersionList struct {
	Supported bool      `json:"supported"`
	Versions  []Version `json:"versions"`
}

func validSnap(n string) bool {
	if n == "" || len(n) > 200 || n == "." || n == ".." || !utf8.ValidString(n) || strings.ContainsAny(n, "/\\") {
		return false
	}
	return !strings.ContainsFunc(n, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// snapMount: Mountpunkt des Datasets (CS_SNAP_ROOT oder der nächste Vorfahre von root mit Ordner .zfs); "" = keiner.
func snapMount(root string) string {
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

// snapLoc: Snapshot-Verzeichnis, Pfadsegmente der Datei unterhalb eines Snapshots und Pfad der aktuellen Datei.
func (s *Svc) snapLoc(owner, name string) (snapDir string, rel []string, cur string, ok bool) {
	ps, isFS := s.St.(pathStore)
	if !isFS {
		return
	}
	root := ps.Root()
	mount := snapMount(root)
	if mount == "" {
		return
	}
	rr, err := filepath.Rel(mount, root)
	if err != nil || rr == ".." || strings.HasPrefix(rr, ".."+string(filepath.Separator)) {
		return
	}
	cur, err = ps.Path(dataKey(owner, name))
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

// lstatIn: Pfad base/segs... ohne Symlinks zu folgen: Zwischenstücke müssen Ordner, das Ende eine normale Datei sein.
func lstatIn(base string, segs ...string) (os.FileInfo, string, error) {
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

// verAccess: Recht auf frühere Stände (write: zum Wiederherstellen). Gibt es die Datei nicht mehr, entscheidet der Besitz:
// eigene Datei bzw. Gruppenordner. Liefert die aktuellen Metadaten (nil = Datei gibt es nicht).
func (s *Svc) verAccess(ctx context.Context, actor, owner, name string, write bool) (*Meta, error) {
	if !ValidName(name) {
		return nil, ErrNotFound
	}
	m, err := s.Meta(ctx, owner, name)
	if err == nil {
		r, w := m.Level(actor)
		if !r {
			return nil, ErrNotFound
		}
		if g, isG := GroupOf(owner); isG && !write && auth.IsGroupAdmin(actor, g) {
			w = true
		}
		if !w {
			return nil, ErrDenied
		}
		return m, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if g, isG := GroupOf(owner); isG {
		r, w := auth.FolderAccess(actor, g)
		if !r {
			return nil, ErrNotFound
		}
		if w = w && auth.WriteArea(actor, "files"); !w && !write && auth.IsGroupAdmin(actor, g) {
			w = true
		}
		if !w {
			return nil, ErrDenied
		}
	} else if actor != owner {
		return nil, ErrNotFound
	}
	return nil, nil
}

// Versions: frühere Stände aus den Snapshots, neueste zuerst; aufeinanderfolgende Snapshots mit gleicher Größe und Zeit
// zählen einmal (der älteste bleibt), Stände wie die aktuelle Datei entfallen.
func (s *Svc) Versions(ctx context.Context, actor, owner, name string) (*VersionList, error) {
	if _, err := s.verAccess(ctx, actor, owner, name, false); err != nil {
		return nil, err
	}
	out := &VersionList{Versions: []Version{}}
	snapDir, rel, cur, ok := s.snapLoc(owner, name)
	if !ok {
		return out, nil
	}
	ents, err := os.ReadDir(snapDir)
	if err != nil {
		return out, nil
	}
	out.Supported = true
	type snap struct {
		name string
		t    time.Time
	}
	var snaps []snap
	for _, e := range ents {
		if !e.IsDir() || !validSnap(e.Name()) {
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
		fi, _, err := lstatIn(snapDir, append([]string{sn.name}, rel...)...)
		if err != nil {
			lastSize = -1 // in diesem Snapshot gab es die Datei nicht
			continue
		}
		size, mod := fi.Size(), fi.ModTime().UnixNano()
		same := size == lastSize && mod == lastMod
		lastSize, lastMod = size, mod
		if same || (size == curSize && mod == curMod) {
			continue
		}
		out.Versions = append(out.Versions, Version{sn.name, size, fi.ModTime().Unix()})
	}
	for i, j := 0, len(out.Versions)-1; i < j; i, j = i+1, j-1 {
		out.Versions[i], out.Versions[j] = out.Versions[j], out.Versions[i]
	}
	if len(out.Versions) > maxVersions {
		out.Versions = out.Versions[:maxVersions]
	}
	return out, nil
}

// OpenVersion öffnet die Datei im Snapshot zum Herunterladen (Metadaten nur für Typ/Name/ETag).
func (s *Svc) OpenVersion(ctx context.Context, actor, owner, name, snap string) (*Meta, io.ReadCloser, error) {
	if _, err := s.verAccess(ctx, actor, owner, name, false); err != nil {
		return nil, nil, err
	}
	if !validSnap(snap) {
		return nil, nil, ErrNotFound
	}
	snapDir, rel, _, ok := s.snapLoc(owner, name)
	if !ok {
		return nil, nil, ErrNotFound
	}
	fi, p, err := lstatIn(snapDir, append([]string{snap}, rel...)...)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	return &Meta{Name: name, Owner: owner, Size: fi.Size(), Type: contentType(name), Mod: fi.ModTime().UnixNano()}, f, nil
}

// RestoreVersion stellt eine Version als aktuelle Datei wieder her. Die bisherige Datei kommt (bei eingeschaltetem Papierkorb)
// vorher als Kopie in den Papierkorb; Freigaben und öffentlicher Link der Datei bleiben erhalten.
func (s *Svc) RestoreVersion(ctx context.Context, actor, owner, name, snap string) (*Meta, error) {
	cur, err := s.verAccess(ctx, actor, owner, name, true)
	if err != nil {
		return nil, err
	}
	m, f, err := s.OpenVersion(ctx, actor, owner, name, snap)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if s.lockedByOther(owner, name, actor) {
		return nil, ErrLocked
	}
	var tm *Meta
	if cur != nil && s.trashDays() > 0 && cur.Size > 0 && Base(cur.Name) != Marker {
		if tm, err = s.trashCopy(ctx, actor, cur); err != nil {
			return nil, err
		}
	}
	nm, err := s.Put(ctx, actor, owner, name, f, m.Size)
	if err != nil && tm != nil { // nichts geändert: die Kopie wieder entfernen
		s.dropTrash(ctx, tm)
		s.invalidate()
	}
	return nm, err
}

// trashCopy legt eine Kopie der aktuellen Datei als Papierkorb-Eintrag an (die Datei selbst bleibt samt Freigaben bestehen).
func (s *Svc) trashCopy(ctx context.Context, actor string, m *Meta) (*Meta, error) {
	tn := trashName(newTrashID())
	rc, err := s.St.GetStream(ctx, dataKey(m.Owner, m.Name))
	if err != nil {
		return nil, ErrNotFound
	}
	defer rc.Close()
	if err := s.St.PutStream(ctx, dataKey(m.Owner, tn), rc, m.Size, m.Type); err != nil {
		return nil, err
	}
	tm := &Meta{Name: tn, Owner: m.Owner, Size: m.Size, Type: m.Type, Mod: m.Mod, Trash: &TrashInfo{Name: m.Name, By: actor, At: time.Now().Unix()}}
	if err := s.saveMeta(ctx, tm); err != nil {
		s.St.Delete(ctx, dataKey(m.Owner, tn))
		return nil, err
	}
	return tm, nil
}
