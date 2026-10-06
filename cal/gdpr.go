package cal

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/emersion/go-ical"

	"cs-team/auth"
)

// Datenschutz: Namen gelöschter Konten in Teilnehmern ersetzen und Datenauskunft (Export) der persönlichen Kalender.

const maxExportObjs = 20000

// anonEvent ersetzt Teilnehmer/Organisator mit dem Namen (CN oder urn:cs-team:<name>) durch repl; true = geändert.
func anonEvent(ev *ical.Component, user, repl string) bool {
	ch := false
	for _, pn := range []string{ical.PropAttendee, ical.PropOrganizer} {
		for i := range ev.Props[pn] {
			p := &ev.Props[pn][i]
			if p.Params.Get(ical.ParamCommonName) == user || strings.TrimSpace(p.Value) == urnPrefix+user {
				p.Value = urnPrefix + repl
				p.Params.Set(ical.ParamCommonName, repl)
				ch = true
			}
		}
	}
	return ch
}

// anonScan geht über alle Termine fremder Kalender (Gruppen, Organisationen, global, andere Benutzer); fix = ersetzen.
// Liefert die Zahl der betroffenen Termin-Objekte.
func (b *Backend) anonScan(ctx context.Context, user, repl string, fix bool) (int, error) {
	infos, err := b.St.List(ctx, "cal/")
	if err != nil {
		return 0, err
	}
	own := "cal/" + user + "/"
	n := 0
	for _, i := range infos {
		if !strings.HasSuffix(i.Key, ".ics") || strings.HasPrefix(i.Key, own) {
			continue
		}
		data, etag, err := b.St.Get(ctx, i.Key)
		if err != nil || !bytes.Contains(data, []byte(user)) { // schneller Vorfilter ohne Parsen
			continue
		}
		c, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
		if err != nil {
			continue
		}
		hit := false
		for _, ch := range c.Children {
			if (ch.Name == ical.CompEvent || ch.Name == ical.CompToDo) && anonEvent(ch, user, repl) {
				hit = true
			}
		}
		if !hit {
			continue
		}
		n++
		if !fix {
			continue
		}
		var buf bytes.Buffer
		if err := ical.NewEncoder(&buf).Encode(c); err != nil {
			return n, err
		}
		if _, err := b.St.Put(ctx, i.Key, buf.Bytes(), etag); err != nil {
			return n, err
		}
	}
	return n, nil
}

// AnonCount: Zahl der Termine anderer Kalender, in denen der Name als Teilnehmer/Organisator steht (Vorschau).
func (b *Backend) AnonCount(ctx context.Context, user string) int {
	n, _ := b.anonScan(ctx, user, "", false)
	return n
}

// Anon ersetzt den Namen durch repl; liefert die Zahl der geänderten Termine.
func (b *Backend) Anon(ctx context.Context, user, repl string) (int, error) {
	return b.anonScan(ctx, user, repl, true)
}

// ExportUser schreibt calendars/<name>.ics je persönlichem Kalender (Abos ausgenommen: ihr Inhalt stammt aus fremden Feeds).
func (b *Backend) ExportUser(ctx context.Context, user string, zw *zip.Writer) error {
	infos, err := b.St.List(ctx, "cal/"+user+"/")
	if err != nil {
		return err
	}
	byKal := map[string][][]byte{}
	names := map[string]string{}
	skip := map[string]bool{}
	total := 0
	sort.Slice(infos, func(i, j int) bool { return infos[i].Key < infos[j].Key })
	for _, i := range infos {
		rest := strings.TrimPrefix(i.Key, "cal/"+user+"/")
		kal, obj, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		if obj == "_meta.json" {
			raw, _, err := b.St.Get(ctx, i.Key)
			var m meta
			if err == nil && json.Unmarshal(raw, &m) == nil {
				if m.Name != "" {
					names[kal] = m.Name
				}
				skip[kal] = m.URL != ""
			}
			continue
		}
		if !strings.HasSuffix(obj, ".ics") || total >= maxExportObjs {
			continue
		}
		data, _, err := b.St.Get(ctx, i.Key)
		if err != nil {
			continue
		}
		byKal[kal] = append(byKal[kal], data)
		total++
	}
	for kal := range names {
		if _, ok := byKal[kal]; !ok {
			byKal[kal] = nil
		}
	}
	var kals []string
	for k := range byKal {
		if !skip[k] {
			kals = append(kals, k)
		}
	}
	sort.Strings(kals)
	used := map[string]bool{}
	for _, kal := range kals {
		nm := names[kal]
		if nm == "" {
			nm = kal
		}
		fn := auth.ZipSafe(nm)
		for k := 2; used[fn]; k++ {
			fn = auth.ZipSafe(nm) + "-" + strconv.Itoa(k)
		}
		used[fn] = true
		w, err := zw.Create("calendars/" + fn + ".ics")
		if err != nil {
			return err
		}
		if _, err := w.Write(mergeICS(nm, byKal[kal])); err != nil {
			return err
		}
	}
	return nil
}

// mergeICS fasst die Objekte eines Kalenders zu einer .ics-Datei zusammen (Zeitzonen nur einmal).
func mergeICS(name string, datas [][]byte) []byte {
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, "-//cs-team//EN")
	out.Props.SetText("X-WR-CALNAME", name)
	zones := map[string]bool{}
	var rest []*ical.Component
	for _, d := range datas {
		c, err := ical.NewDecoder(bytes.NewReader(d)).Decode()
		if err != nil {
			continue
		}
		for _, ch := range c.Children {
			if ch.Name == ical.CompTimezone {
				id, _ := ch.Props.Text(ical.PropTimezoneID)
				if !zones[id] {
					zones[id] = true
					out.Children = append(out.Children, ch)
				}
			} else {
				rest = append(rest, ch)
			}
		}
	}
	out.Children = append(out.Children, rest...)
	var buf bytes.Buffer
	if len(out.Children) == 0 { // leerer Kalender: der Encoder verlangt mindestens einen Eintrag, ein leeres VCALENDAR ist aber gültig
		nm := strings.NewReplacer("\r", "", "\n", " ", "\\", "\\\\", ",", "\\,", ";", "\\;").Replace(name)
		return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//cs-team//EN\r\nX-WR-CALNAME:" + nm + "\r\nEND:VCALENDAR\r\n")
	}
	if err := ical.NewEncoder(&buf).Encode(out); err != nil {
		return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n")
	}
	return buf.Bytes()
}
