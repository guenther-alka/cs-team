package files

import (
	"archive/zip"
	"context"
	"io"
	"sort"
	"strconv"
	"time"

	"cs-team/auth"
)

// Datenauskunft: Grenzen, damit ein Export den Server nicht überlastet (Dateien darüber werden in files/_nicht-enthalten.txt genannt).
const (
	maxExportFiles = 20000
	maxExportBytes = int64(8) << 30
)

// ExportUser schreibt files/<pfad> für die eigenen Dateien des Benutzers (ohne Papierkorb, ohne Gruppenordner und fremde Freigaben).
// Der Inhalt wird gestreamt; Namen werden gegen "../" und ungültige Zeichen bereinigt (zip-slip).
func (s *Svc) ExportUser(ctx context.Context, user string, zw *zip.Writer) error {
	s.invalidate()
	all, err := s.all(ctx)
	if err != nil {
		return err
	}
	var own []Meta
	for _, m := range all {
		if m.Owner == user && !isTrash(&m) && Base(m.Name) != Marker {
			own = append(own, m)
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i].Name < own[j].Name })
	var total int64
	var skipped []string
	used := map[string]bool{}
	for n, m := range own {
		if n >= maxExportFiles || total+m.Size > maxExportBytes {
			skipped = append(skipped, m.Name)
			continue
		}
		rc, err := s.St.GetStream(ctx, dataKey(m.Owner, m.Name))
		if err != nil {
			skipped = append(skipped, m.Name)
			continue
		}
		fn := auth.ZipSafe(m.Name)
		for k := 2; used[fn]; k++ { // zwei Namen können nach der Bereinigung gleich sein
			fn = auth.ZipSafe(m.Name) + "-" + strconv.Itoa(k)
		}
		used[fn] = true
		h := &zip.FileHeader{Name: "files/" + fn, Method: zip.Deflate, Modified: time.Unix(0, m.Mod)}
		w, err := zw.CreateHeader(h)
		if err == nil {
			var c int64
			c, err = io.Copy(w, rc)
			total += c
		}
		rc.Close()
		if err != nil {
			return err
		}
	}
	if len(skipped) > 0 {
		w, err := zw.Create("files/_nicht-enthalten.txt")
		if err != nil {
			return err
		}
		txt := "Nicht enthalten (Größen-/Mengengrenze oder nicht lesbar):\r\n"
		for _, n := range skipped {
			txt += n + "\r\n"
		}
		_, err = w.Write([]byte(txt))
		return err
	}
	return nil
}
