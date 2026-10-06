package cal

// ICS-Export (ganzer Kalender als eine .ics-Datei) und -Import (Termine aus einer .ics-Datei in einen Kalender).

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/emersion/go-ical"

	"cs-team/auth"
)

const maxImportEvents = 5000

// GET /api/cal/{kal}/export.ics
func (b *Backend) apiExport(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	objs, err := b.ListCalendarObjects(r.Context(), davPath(me, c.cid, ""), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Path < objs[j].Path })
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, "-//cs-team//EN")
	out.Props.SetText("X-WR-CALNAME", c.m.Name)
	zones := map[string]bool{}
	var events []*ical.Component
	for _, o := range objs {
		for _, ch := range o.Data.Children {
			switch ch.Name {
			case ical.CompTimezone:
				id, _ := ch.Props.Text(ical.PropTimezoneID)
				if !zones[id] {
					zones[id] = true
					out.Children = append(out.Children, ch)
				}
			case ical.CompEvent:
				events = append(events, ch)
			}
		}
	}
	out.Children = append(out.Children, events...)
	var buf bytes.Buffer
	if len(out.Children) == 0 { // leerer Kalender: der Encoder verlangt mindestens einen Eintrag, ein leeres VCALENDAR ist aber gültig
		nm := strings.NewReplacer("\r", "", "\n", " ", "\\", "\\\\", ",", "\\,", ";", "\\;").Replace(c.m.Name)
		buf.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//cs-team//EN\r\nX-WR-CALNAME:" + nm + "\r\nEND:VCALENDAR\r\n")
	} else if err := ical.NewEncoder(&buf).Encode(out); err != nil {
		http.Error(w, "export failed", 500)
		return
	}
	name := slug(c.m.Name)
	if name == "" {
		name = "calendar"
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.ics"`)
	w.Write(buf.Bytes())
}

// POST /api/cal/{kal}/import (Body: .ics) -> {"added":n,"updated":n,"skipped":n}
// Termine mit bekannter UID werden aktualisiert, neue angelegt (wiederholtes Importieren erzeugt keine Dubletten).
func (b *Backend) apiImport(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if !c.write || c.m.URL != "" {
		http.Error(w, "read-only calendar", 403)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, subMaxSize))
	if err != nil {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	src, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		http.Error(w, "no valid iCalendar", 400)
		return
	}
	var tz []*ical.Component
	byUID := map[string][]*ical.Component{}
	var order []string
	for _, ch := range src.Children {
		switch ch.Name {
		case ical.CompTimezone:
			tz = append(tz, ch)
		case ical.CompEvent:
			uid, _ := ch.Props.Text(ical.PropUID)
			if uid == "" {
				uid = newID() + "@cs-team"
				ch.Props.SetText(ical.PropUID, uid)
			}
			dropFineRRule(ch)
			if _, ok := byUID[uid]; !ok {
				order = append(order, uid)
			}
			byUID[uid] = append(byUID[uid], ch)
		}
	}
	if len(order) == 0 {
		http.Error(w, "no events found", 400)
		return
	}
	if len(order) > maxImportEvents {
		http.Error(w, "too many events", 400)
		return
	}
	defer lockCal(c.owner, c.kal)()
	existing := map[string]string{} // UID -> Datei
	if objs, err := b.ListCalendarObjects(r.Context(), davPath(me, c.cid, ""), nil); err == nil {
		for _, o := range objs {
			if m := masterOf(o.Data); m != nil {
				uid, _ := m.Props.Text(ical.PropUID)
				existing[uid] = o.Path[strings.LastIndex(o.Path, "/")+1:]
			}
		}
	}
	var added, updated, skipped int
	for _, uid := range order {
		file, known := existing[uid]
		if !known {
			sum := sha1.Sum([]byte(uid))
			file = hex.EncodeToString(sum[:10]) + ".ics"
		}
		one := ical.NewCalendar()
		one.Props.SetText(ical.PropVersion, "2.0")
		one.Props.SetText(ical.PropProductID, "-//cs-team//EN")
		one.Children = append(one.Children, tz...)
		one.Children = append(one.Children, byUID[uid]...)
		if masterOf(one) == nil || b.conflict(r.Context(), c, one, file) != "" {
			skipped++
			continue
		}
		var buf bytes.Buffer
		if ical.NewEncoder(&buf).Encode(one) != nil {
			skipped++
			continue
		}
		if _, err := b.St.Put(r.Context(), key(c.owner, c.kal, file), buf.Bytes(), ""); err != nil {
			skipped++
			continue
		}
		if known {
			updated++
		} else {
			added++
		}
	}
	json.NewEncoder(w).Encode(map[string]int{"added": added, "updated": updated, "skipped": skipped})
}
