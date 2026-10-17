package office

import (
	"archive/zip"
	"context"
	"strings"

	"cs-team/auth"
)

const maxExportDocs = 2000 // Calc-/Text-Dokumente in der Datenauskunft

// ExportUser schreibt documents/<Name>.docx bzw. .xlsx fuer die eigenen Calc- und Text-Dokumente des Benutzers
// (Datenauskunft). Fremde Dokumente, auch wenn sie mit dem Benutzer geteilt sind, sind nicht enthalten.
func (s *Svc) ExportUser(ctx context.Context, user string, zw *zip.Writer) error {
	ids := s.Hub.OwnedIDs(ctx, user)
	used := map[string]bool{}
	var failed []string
	for i, id := range ids {
		if i >= maxExportDocs {
			failed = append(failed, "(weitere Dokumente nicht enthalten, Grenze "+itoa(maxExportDocs)+")")
			break
		}
		m, d, err := s.Hub.Snapshot(ctx, user, id)
		if err != nil {
			failed = append(failed, id)
			continue
		}
		format := "docx"
		if m.Type == "sheet" {
			format = "xlsx"
		}
		data, ext, err := render(m, d, format, nil)
		if err != nil {
			failed = append(failed, m.Name)
			continue
		}
		fn := auth.ZipSafe(fileName(m.Name, ext))
		if used[fn] { // gleiche Namen: Kennung des Dokuments anhaengen
			fn = strings.TrimSuffix(fn, "."+ext) + "-" + id + "." + ext
		}
		used[fn] = true
		w, err := zw.Create("documents/" + fn)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if len(failed) > 0 {
		w, err := zw.Create("documents/_nicht-enthalten.txt")
		if err != nil {
			return err
		}
		_, err = w.Write([]byte("Nicht exportiert:\r\n" + strings.Join(failed, "\r\n") + "\r\n"))
		return err
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
