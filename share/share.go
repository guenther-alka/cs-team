// Package share: schreibgeschuetzte Sicht auf den lokalen Ordner <CS_DIR>/share (TODO G).
//
// Admins legen Dokumente per SMB/Explorer in "share" ab, cs-team zeigt sie nur an. Bearbeiten = Import in den
// eigenen Bereich (Kopie); der Benutzer kann die Kopie danach wie jede eigene Datei fuer Gruppen freigeben
// (lesen oder rw). Gibt es den Ordner nicht, ist alles aus (kein Marker, keine Einstellung).
//
// cs-team laeuft als root/admin: die SMB-ACL spielt hier keine Rolle, die Sichtbarkeit bestimmt allein die
// Ordnerkonvention:
//
//	share/*                 alle angemeldeten Benutzer (Gaeste ebenfalls)
//	share/groups/<gruppe>/  Mitglieder der Gruppe (und globale Admins)
//	share/users/<name>/     nur dieser Benutzer (und globale Admins)
//
// Es gibt keine Schreib-API. Jeder Pfad wird Segment fuer Segment geprueft (kein "..", keine Symlinks/Junctions,
// keine versteckten Namen) und nach dem Aufloesen noch einmal gegen die kanonischen Namen der Platte.
package share

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cs-team/files"
)

var (
	ErrNotFound = errors.New("share: not found")
	ErrNotFile  = errors.New("share: not a file")
)

// Svc: Root = <CS_DIR>/share (leer = aus); Files wird nur fuer den Import gebraucht.
type Svc struct {
	Root  string
	Files *files.Svc
}

// Who: wer fragt (aus der Anmeldung, siehe http.go).
type Who struct {
	User   string
	Groups []string // cs-team-Gruppen des Benutzers (lokal: gruppe, AD: gruppe@domain)
	Admin  bool     // globaler Admin
	Guest  bool
}

// Entry: ein Eintrag der Auflistung. Scope: "all", "group:<gruppe>" oder "user".
type Entry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	Mod   int64  `json:"mod"`
	Scope string `json:"scope"`
}

// Enabled: gibt es den Ordner share? Wird bei jedem Aufruf geprueft, der Ordner darf also spaeter angelegt werden.
func (s *Svc) Enabled() bool {
	if s == nil || s.Root == "" {
		return false
	}
	fi, err := os.Stat(s.Root)
	return err == nil && fi.IsDir()
}

// hiddenName: Namen, die nie angezeigt und nie geoeffnet werden (versteckt, .zfs, Office-Sperrdateien, Windows-Tricks).
func hiddenName(n string) bool {
	if n == "" || !utf8.ValidString(n) {
		return true
	}
	if n[0] == '.' || strings.HasPrefix(n, "~$") { // "." ".." ".zfs" und alles Versteckte
		return true
	}
	if strings.HasSuffix(n, ".") || strings.HasSuffix(n, " ") { // Windows ignoriert diese: "groups." waere "groups"
		return true
	}
	if strings.EqualFold(n, "desktop.ini") || strings.EqualFold(n, "thumbs.db") {
		return true
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f || r == '\\' || r == ':' || r == '/' { // ":" = Windows-Datenstrom
			return true
		}
	}
	return false
}

func reserved(n string) bool { return strings.EqualFold(n, "groups") || strings.EqualFold(n, "users") }

// splitRel: "a/b/c" -> Segmente; leer = Wurzel. Jedes unzulaessige Segment verwirft den ganzen Pfad.
func splitRel(rel string) ([]string, error) {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return nil, nil
	}
	segs := strings.Split(rel, "/")
	if len(segs) > 12 {
		return nil, ErrNotFound
	}
	for _, sg := range segs {
		if hiddenName(sg) {
			return nil, ErrNotFound
		}
	}
	return segs, nil
}

// allowed: darf der Benutzer diesen Pfad sehen? (reine Regel, ohne Dateizugriff). Beim Eintrag "groups" bzw. "users"
// selbst ist es erlaubt - die Auflistung filtert dann je Unterordner.
func (w Who) allowed(segs []string) bool {
	if len(segs) == 0 || !reserved(segs[0]) {
		return true
	}
	if w.Guest {
		return false
	}
	if len(segs) == 1 || w.Admin {
		return true
	}
	if strings.EqualFold(segs[0], "groups") {
		for _, g := range w.Groups {
			if strings.EqualFold(g, segs[1]) { // AD ist nicht gross-/kleinschreibungsabhaengig
				return true
			}
		}
		return false
	}
	return strings.EqualFold(segs[1], w.User)
}

func scopeOf(segs []string) string {
	if len(segs) >= 2 {
		switch {
		case strings.EqualFold(segs[0], "groups"):
			return "group:" + segs[1]
		case strings.EqualFold(segs[0], "users"):
			return "user"
		}
	}
	return "all"
}

// plain: nur normale Dateien und Ordner (keine Symlinks, Junctions, Geraete, Pipes).
func plain(fi os.FileInfo) bool { return fi.Mode().IsRegular() || fi.IsDir() }

// resolve: prueft Recht und Pfad und liefert den echten Pfad. Die Wurzel selbst darf ein Symlink/Mountpunkt sein
// (Admin-Entscheidung), darunter ist keiner erlaubt.
func (s *Svc) resolve(w Who, rel string) (string, os.FileInfo, []string, error) {
	segs, err := splitRel(rel)
	if err != nil || !s.Enabled() || !w.allowed(segs) {
		return "", nil, nil, ErrNotFound
	}
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil {
		return "", nil, nil, ErrNotFound
	}
	fi, err := os.Stat(root)
	if err != nil {
		return "", nil, nil, ErrNotFound
	}
	cur := root
	for _, sg := range segs {
		cur = filepath.Join(cur, sg)
		if fi, err = os.Lstat(cur); err != nil || !plain(fi) {
			return "", nil, nil, ErrNotFound
		}
	}
	if len(segs) > 0 { // zweite Pruefung mit den Namen, wie sie auf der Platte stehen (Gross-/Kleinschreibung, 8.3-Namen)
		real, err := filepath.EvalSymlinks(cur)
		if err != nil {
			return "", nil, nil, ErrNotFound
		}
		r, err := filepath.Rel(root, real)
		if err != nil {
			return "", nil, nil, ErrNotFound
		}
		csegs := strings.Split(filepath.ToSlash(r), "/")
		if len(csegs) != len(segs) {
			return "", nil, nil, ErrNotFound
		}
		for _, c := range csegs {
			if hiddenName(c) {
				return "", nil, nil, ErrNotFound
			}
		}
		if !w.allowed(csegs) {
			return "", nil, nil, ErrNotFound
		}
	}
	return cur, fi, segs, nil
}

// List: der Ordner rel (leer = Wurzel), gefiltert auf das, was der Benutzer sehen darf. "groups" und "users" erscheinen
// nur, wenn darin fuer ihn etwas sichtbar ist.
func (s *Svc) List(w Who, rel string) ([]Entry, error) {
	abs, fi, segs, err := s.resolve(w, rel)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, ErrNotFound
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return nil, ErrNotFound
	}
	out := []Entry{}
	for _, de := range des {
		name := de.Name()
		if hiddenName(name) {
			continue
		}
		info, err := de.Info()
		if err != nil || !plain(info) {
			continue
		}
		child := append(append([]string{}, segs...), name)
		if !w.allowed(child) {
			continue
		}
		if info.IsDir() && len(child) == 1 && reserved(name) {
			if l, err := s.List(w, name); err != nil || len(l) == 0 {
				continue
			}
		}
		e := Entry{Name: name, Dir: info.IsDir(), Mod: info.ModTime().Unix(), Scope: scopeOf(child)}
		if !e.Dir {
			e.Size = info.Size()
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Open: oeffnet eine Datei zum Lesen. Nach dem Oeffnen wird geprueft, dass es noch dieselbe Datei ist.
func (s *Svc) Open(w Who, rel string) (*os.File, os.FileInfo, error) {
	abs, fi, _, err := s.resolve(w, rel)
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, ErrNotFile
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	fi2, err := f.Stat()
	if err != nil || !os.SameFile(fi, fi2) {
		f.Close()
		return nil, nil, ErrNotFound
	}
	return f, fi2, nil
}

// uniqueName: "bericht.docx" -> "bericht (2).docx" ..., nie ueberschreiben.
func uniqueName(exists func(string) bool, name string) string {
	if !exists(name) {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < 1000; i++ {
		if n := fmt.Sprintf("%s (%d)%s", stem, i, ext); !exists(n) {
			return n
		}
	}
	return fmt.Sprintf("%s (%d)%s", stem, time.Now().Unix(), ext)
}

// Import: kopiert die Datei in den eigenen Bereich (Wurzel von "Meine Dateien") und liefert den neuen Namen.
// Danach ist die Kopie unabhaengig vom Original. Groesse, Kontingent und Schreibrecht prueft files.Svc.Put.
func (s *Svc) Import(ctx context.Context, w Who, rel string) (string, error) {
	if s.Files == nil {
		return "", ErrNotFound
	}
	f, fi, err := s.Open(w, rel)
	if err != nil {
		return "", err
	}
	defer f.Close()
	name := uniqueName(func(n string) bool {
		_, err := s.Files.Meta(ctx, w.User, n)
		return err == nil
	}, fi.Name())
	if _, err := s.Files.Put(ctx, w.User, w.User, name, f, fi.Size()); err != nil {
		return "", err
	}
	return name, nil
}
