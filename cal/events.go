package cal

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"cs-team/auth"
	"cs-team/store"
)

// Termine für die Web-UI: Anzeige eines Zeitraums (Serien aufgelöst), Anlegen, Bearbeiten ("nur dieser" / "alle") und Löschen.
// Zeiten werden mit der Zeitzone des Browsers (TZID) gespeichert, damit Serien über die Sommerzeit-Umstellung gleich bleiben.

const (
	defPast   = 31 * 24 * time.Hour
	defFuture = 400 * 24 * time.Hour
	maxWindow = 800 * 24 * time.Hour
	maxText   = 4000
)

type ruleOut struct {
	Freq     string `json:"freq"`
	Interval int    `json:"interval,omitempty"`
	Count    int    `json:"count,omitempty"`
	Until    string `json:"until,omitempty"` // YYYY-MM-DD (in der Zeitzone des Termins)
	Simple   bool   `json:"simple"`          // Regel lässt sich in der Oberfläche vollständig bearbeiten
	Text     string `json:"text,omitempty"`  // Originalregel bei komplexen Regeln (nur zur Anzeige)
}

type evRow struct {
	UID      string   `json:"uid"`
	File     string   `json:"file"`
	Summary  string   `json:"summary"`
	Location string   `json:"location,omitempty"`
	Desc     string   `json:"desc,omitempty"`
	Start    string   `json:"start"`
	End      string   `json:"end,omitempty"`
	AllDay   bool     `json:"allDay,omitempty"`
	TZ       string   `json:"tz,omitempty"`    // Zeitzone des Termins (IANA), leer = UTC/schwebend
	Float    bool     `json:"float,omitempty"` // schwebende Zeit: start/end ohne Zonenangabe (Ortszeit des Betrachters)
	Rec      bool     `json:"rec,omitempty"`   // Teil einer Serie
	Ovr      bool     `json:"ovr,omitempty"`   // einzeln geänderter Termin einer Serie
	Rid      string   `json:"rid,omitempty"`   // Vorkommen innerhalb der Serie (für Bearbeiten/Löschen "nur dieser")
	Rule     *ruleOut `json:"rule,omitempty"`  // Regel der Serie
	Alarm    *int     `json:"alarm,omitempty"` // Erinnerung: Minuten vor Beginn
	Att      []attRow `json:"att,omitempty"`   // Teilnehmer
	Org      string   `json:"org,omitempty"`   // Organisator
}

type ruleIn struct {
	Freq     string // "" = keine Wiederholung
	Interval int
	Count    int
	Until    string // YYYY-MM-DD
}

type evIn struct {
	Summary, Location, Description, Start, End, TZ string
	AllDay                                         bool
	Rule                                           *ruleIn // nil = unverändert (Bearbeiten) bzw. keine (Anlegen)
	Scope                                          string  // Bearbeiten/Löschen: "all" (Standard) | "one" | "following"
	Rid                                            string
	Alarm                                          *int      // Minuten vor Beginn; nil = unverändert, <0 = keine
	Att                                            *[]string // Teilnehmer (Benutzername oder E-Mail); nil = unverändert
}

const tsLocal = "2006-01-02T15:04:05"

// tstr: Zeit für die Oberfläche (RFC 3339 in UTC; schwebende Zeiten ohne Zone).
func tstr(t time.Time, floating bool) string {
	if floating {
		return t.Format(tsLocal)
	}
	return t.UTC().Format(time.RFC3339)
}

// parseTS liest RFC 3339, eine schwebende Zeit (Uhrzeit in UTC) oder ein Datum.
func parseTS(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.ParseInLocation(tsLocal, s, time.UTC); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02", s, time.UTC)
}

func parseRange(r *http.Request) (from, to time.Time, err error) {
	now := time.Now().UTC()
	from, to = now.Add(-defPast), now.Add(defFuture)
	if v := r.URL.Query().Get("from"); v != "" {
		if from, err = parseTS(v); err != nil {
			return
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if to, err = parseTS(v); err != nil {
			return
		}
	}
	if !to.After(from) {
		return from, to, errors.New("bad range")
	}
	if to.Sub(from) > maxWindow {
		to = from.Add(maxWindow)
	}
	return
}

// ruleInfo: Regel eines Haupttermins für die Oberfläche.
func ruleInfo(master *ical.Event) *ruleOut {
	p := master.Props.Get(ical.PropRecurrenceRule)
	if p == nil {
		return nil
	}
	out := &ruleOut{Simple: true, Text: p.Value}
	loc := time.UTC
	if l := lookupTZ(master.Props.Get(ical.PropDateTimeStart).Params.Get(ical.ParamTimezoneID)); l != nil {
		loc = l
	}
	for _, part := range strings.Split(p.Value, ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch strings.ToUpper(k) {
		case "FREQ":
			out.Freq = strings.ToUpper(v)
		case "INTERVAL":
			out.Interval, _ = strconv.Atoi(v)
		case "COUNT":
			out.Count, _ = strconv.Atoi(v)
		case "UNTIL":
			q := ical.Prop{Name: "UNTIL", Value: v}
			if t, _, err := propTime(&q); err == nil {
				if len(v) == len(fmtDate) {
					out.Until = t.Format("2006-01-02")
				} else {
					out.Until = t.In(loc).Format("2006-01-02")
				}
			}
		case "WKST", "":
		default:
			out.Simple = false
		}
	}
	switch out.Freq {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
	default:
		out.Simple = false
	}
	return out
}

func rowOf(file string, cal *ical.Calendar, sp span) evRow {
	ev := sp.ev
	uid, _ := ev.Props.Text(ical.PropUID)
	sum, _ := ev.Props.Text(ical.PropSummary)
	loc, _ := ev.Props.Text(ical.PropLocation)
	desc, _ := ev.Props.Text(ical.PropDescription)
	_, _, allDay, fl, _ := evTimes(ev)
	row := evRow{UID: uid, File: file, Summary: sum, Location: loc, Desc: desc, AllDay: allDay, Float: fl && !allDay, TZ: zoneOf(ev)}
	row.Start, row.End = tstr(sp.s, row.Float), tstr(sp.e, row.Float)
	row.Alarm, row.Att = alarmMinutes(ev), attendeesOf(ev)
	row.Org, _ = organizerOf(ev)
	if m := masterOf(cal); m != nil && sp.ovr {
		if row.Alarm == nil {
			row.Alarm = alarmMinutes(m)
		}
		if len(row.Att) == 0 {
			row.Att = attendeesOf(m)
			row.Org, _ = organizerOf(m)
		}
	}
	if sp.rec {
		row.Rec, row.Ovr = true, sp.ovr
		if m := masterOf(cal); m != nil {
			_, _, mAll, mFl, _ := evTimes(m)
			row.Rid = tstr(sp.rid, mFl && !mAll)
			row.Rule = ruleInfo(m)
		}
	}
	return row
}

func (b *Backend) apiEvents(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	from, to, err := parseRange(r)
	if err != nil {
		http.Error(w, "bad range (from/to as RFC 3339 or YYYY-MM-DD)", 400)
		return
	}
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	b.autoRefresh(r.Context(), c)
	objs, err := b.ListCalendarObjects(r.Context(), davPath(me, c.cid, ""), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	out := []evRow{}
	for _, o := range objs {
		file := o.Path[strings.LastIndex(o.Path, "/")+1:]
		for _, sp := range spans(o.Data, from, to) {
			out = append(out, rowOf(file, o.Data, sp))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	json.NewEncoder(w).Encode(out)
}

// ---- Eingabe prüfen und Termine bauen ----

func readEv(w http.ResponseWriter, r *http.Request) (in evIn, start, end time.Time, loc *time.Location, ok bool) {
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
		http.Error(w, "bad json", 400)
		return
	}
	in.Summary = strings.TrimSpace(in.Summary)
	if in.Summary == "" {
		http.Error(w, "summary required", 400)
		return
	}
	if len(in.Summary) > 500 || len(in.Location) > 500 || len(in.Description) > maxText {
		http.Error(w, "text too long", 400)
		return
	}
	var err error
	if start, err = time.Parse(time.RFC3339, in.Start); err != nil {
		http.Error(w, "bad start (RFC3339)", 400)
		return
	}
	end = start.Add(time.Hour)
	if in.AllDay {
		end = start.AddDate(0, 0, 1)
	}
	if in.End != "" {
		if end, err = time.Parse(time.RFC3339, in.End); err != nil || end.Before(start) {
			http.Error(w, "bad end", 400)
			return
		}
	}
	loc = time.UTC
	if in.TZ != "" {
		if loc = lookupTZ(in.TZ); loc == nil {
			http.Error(w, "unknown time zone", 400)
			return
		}
	}
	return in, start, end, loc, true
}

// ruleString baut die Regel selbst (nie eine Regel aus der Anfrage übernehmen: keine Last durch fremde Regeln).
func ruleString(r *ruleIn, allDay bool, loc *time.Location) (string, error) {
	f := strings.ToUpper(strings.TrimSpace(r.Freq))
	if f == "" {
		return "", nil
	}
	switch f {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
	default:
		return "", errors.New("bad frequency")
	}
	if r.Interval < 0 || r.Interval > 99 || r.Count < 0 || r.Count > 999 {
		return "", errors.New("bad interval or count")
	}
	if r.Count > 0 && r.Until != "" {
		return "", errors.New("count and until exclude each other")
	}
	s := "FREQ=" + f
	if r.Interval > 1 {
		s += ";INTERVAL=" + strconv.Itoa(r.Interval)
	}
	if r.Count > 0 {
		s += ";COUNT=" + strconv.Itoa(r.Count)
	}
	if r.Until != "" {
		d, err := time.ParseInLocation("2006-01-02", r.Until, loc)
		if err != nil {
			return "", errors.New("bad until date")
		}
		if allDay {
			s += ";UNTIL=" + d.Format(fmtDate)
		} else {
			s += ";UNTIL=" + d.AddDate(0, 0, 1).Add(-time.Second).UTC().Format(fmtUTC)
		}
	}
	return s, nil
}

func setRule(ev *ical.Event, rule string) {
	if rule == "" {
		ev.Props.Del(ical.PropRecurrenceRule)
		return
	}
	p := ical.NewProp(ical.PropRecurrenceRule)
	p.SetValueType(ical.ValueRecurrence)
	p.Value = rule
	ev.Props.Set(p)
}

func setTimes(ev *ical.Event, start, end time.Time, allDay bool, loc *time.Location) {
	ev.Props.Del(ical.PropDuration)
	if allDay { // DATE-Werte (Ende exklusiv); start/end kommen als UTC-Mitternacht des Datums
		for _, d := range []struct {
			name string
			t    time.Time
		}{{ical.PropDateTimeStart, start}, {ical.PropDateTimeEnd, end}} {
			p := ical.NewProp(d.name)
			p.SetValueType(ical.ValueDate)
			p.Value = d.t.UTC().Format(fmtDate)
			ev.Props.Set(p)
		}
		return
	}
	if loc == nil {
		loc = time.UTC
	}
	ev.Props.SetDateTime(ical.PropDateTimeStart, start.In(loc))
	ev.Props.SetDateTime(ical.PropDateTimeEnd, end.In(loc))
}

func setText(ev *ical.Event, name, v string) {
	if strings.TrimSpace(v) == "" {
		ev.Props.Del(name)
		return
	}
	ev.Props.SetText(name, v)
}

func fillText(ev *ical.Event, in evIn) {
	ev.Props.SetText(ical.PropSummary, in.Summary)
	setText(ev, ical.PropLocation, in.Location)
	setText(ev, ical.PropDescription, in.Description)
	ev.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
}

func newCal(ev *ical.Event) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//cs-team//EN")
	cal.Children = append(cal.Children, ev.Component)
	return cal
}

func newID() string { x := make([]byte, 12); rand.Read(x); return hex.EncodeToString(x) }

func (b *Backend) apiAddEvent(w http.ResponseWriter, r *http.Request) {
	in, start, end, loc, ok := readEv(w, r)
	if !ok {
		return
	}
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "no such calendar", 404)
		return
	}
	if !c.write {
		http.Error(w, "read-only calendar", 403)
		return
	}
	id := newID()
	ev := ical.NewEvent()
	ev.Props.SetText(ical.PropUID, id+"@cs-team")
	setTimes(ev, start, end, in.AllDay, loc)
	fillText(ev, in)
	if in.Rule != nil {
		rs, err := ruleString(in.Rule, in.AllDay, loc)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		setRule(ev, rs)
	}
	me := auth.User(r.Context())
	d, err := applyExtras(ev, in, me)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	cal := newCal(ev)
	defer lockCal(c.owner, c.kal)()
	if msg := b.conflict(r.Context(), c, cal, ""); msg != "" {
		http.Error(w, msg, http.StatusConflict)
		return
	}
	var buf bytes.Buffer
	addTimezones(cal)
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if _, err := b.St.Put(r.Context(), key(c.owner, c.kal, id+".ics"), buf.Bytes(), "*"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"file": id + ".ics", "mails": b.notify(me, cal, d, false)})
}

// ---- Bearbeiten und Löschen ----

func fileArg(w http.ResponseWriter, r *http.Request) (string, bool) {
	file := r.PathValue("file")
	if !strings.HasSuffix(file, ".ics") || strings.ContainsAny(file, `/\`) {
		http.Error(w, "bad file", 400)
		return "", false
	}
	return file, true
}

// framed: Zeitangabe t in der Darstellung des Beginns des Haupttermins (DATE, UTC, TZID oder schwebend) - für RECURRENCE-ID und EXDATE.
func framed(master *ical.Event, name string, t time.Time) *ical.Prop {
	p := ical.NewProp(name)
	ps := master.Props.Get(ical.PropDateTimeStart)
	switch {
	case isDateProp(ps):
		p.SetValueType(ical.ValueDate)
		p.Value = t.UTC().Format(fmtDate)
	case strings.HasSuffix(strings.TrimSpace(ps.Value), "Z"):
		p.SetValueType(ical.ValueDateTime)
		p.Value = t.UTC().Format(fmtUTC)
	default:
		p.SetValueType(ical.ValueDateTime)
		loc := time.UTC
		if tz := ps.Params.Get(ical.ParamTimezoneID); tz != "" {
			p.Params.Set(ical.ParamTimezoneID, tz)
			if l := lookupTZ(tz); l != nil {
				loc = l
			}
		}
		p.Value = t.In(loc).Format(fmtLocal)
	}
	return p
}

func sameInstant(p *ical.Prop, t time.Time) bool {
	x, _, err := propTime(p)
	return err == nil && x.UTC().Equal(t.UTC())
}

// dropOverride entfernt den Einzeltermin zu rid; meldet, ob es ihn gab.
func dropOverride(cal *ical.Calendar, rid time.Time) bool {
	found := false
	kept := cal.Children[:0]
	for _, ch := range cal.Children {
		if ch.Name == ical.CompEvent {
			if p := ch.Props.Get(ical.PropRecurrenceID); p != nil && sameInstant(p, rid) {
				found = true
				continue
			}
		}
		kept = append(kept, ch)
	}
	cal.Children = kept
	return found
}

func hasInstance(master *ical.Event, rid time.Time) bool {
	s, _, _, _, ok := evTimes(master)
	if !ok {
		return false
	}
	set := ruleSet(master, s)
	if set == nil {
		return false
	}
	for _, t := range set.Between(rid.Add(-time.Second), rid.Add(time.Second), true) {
		if t.UTC().Equal(rid.UTC()) {
			return true
		}
	}
	return false
}

func sameRule(a, b string) bool {
	norm := func(s string) string {
		var l []string
		for _, p := range strings.Split(strings.ToUpper(strings.TrimSpace(s)), ";") {
			if p != "" && !strings.HasPrefix(p, "WKST=") {
				l = append(l, p)
			}
		}
		sort.Strings(l)
		return strings.Join(l, ";")
	}
	return norm(a) == norm(b)
}

func clearSeries(cal *ical.Calendar, master *ical.Event) {
	kept := cal.Children[:0]
	for _, ch := range cal.Children {
		if ch.Name == ical.CompEvent && ch.Props.Get(ical.PropRecurrenceID) != nil {
			continue
		}
		kept = append(kept, ch)
	}
	cal.Children = kept
	master.Props.Del(ical.PropExceptionDates)
	master.Props.Del(ical.PropRecurrenceDates)
}

// load liest ein Objekt samt ETag.
func (b *Backend) load(r *http.Request, c *calRef, file string) (*ical.Calendar, string, int, error) {
	data, etag, err := b.St.Get(r.Context(), key(c.owner, c.kal, file))
	if errors.Is(err, store.ErrNotFound) {
		return nil, "", 404, errors.New("no such event")
	} else if err != nil {
		return nil, "", 500, err
	}
	cal, err := decode(data)
	if err != nil || masterOf(cal) == nil {
		return nil, "", 400, errors.New("event cannot be edited here")
	}
	return cal, etag, 0, nil
}

func (b *Backend) save(w http.ResponseWriter, r *http.Request, c *calRef, file string, cal *ical.Calendar, etag string) bool {
	if msg := b.conflict(r.Context(), c, cal, file); msg != "" {
		http.Error(w, msg, http.StatusConflict)
		return false
	}
	var buf bytes.Buffer
	addTimezones(cal)
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		http.Error(w, err.Error(), 400)
		return false
	}
	if _, err := b.St.Put(r.Context(), key(c.owner, c.kal, file), buf.Bytes(), etag); errors.Is(err, store.ErrConflict) {
		http.Error(w, "event was changed meanwhile, reload", http.StatusConflict)
		return false
	} else if err != nil {
		http.Error(w, err.Error(), 500)
		return false
	}
	return true
}

func (b *Backend) apiPutEvent(w http.ResponseWriter, r *http.Request) {
	file, ok := fileArg(w, r)
	if !ok {
		return
	}
	in, start, end, loc, ok := readEv(w, r)
	if !ok {
		return
	}
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if !c.write {
		http.Error(w, "read-only calendar", 403)
		return
	}
	defer lockCal(c.owner, c.kal)()
	cal, etag, code, err := b.load(r, c, file)
	if err != nil {
		http.Error(w, err.Error(), code)
		return
	}
	master := masterOf(cal)
	mStart, _, mAll, mFl, _ := evTimes(master)
	series := master.Props.Get(ical.PropRecurrenceRule) != nil
	me := auth.User(r.Context())
	sigBefore := sig(master)
	if in.TZ == "" { // ohne Angabe die Zeitzone des Haupttermins beibehalten
		if l := lookupTZ(zoneOf(master)); l != nil {
			loc = l
		}
	}
	if in.Scope == "one" && series {
		rid, err := parseTS(in.Rid)
		if err != nil || !hasInstance(master, rid) {
			http.Error(w, "no such occurrence", 404)
			return
		}
		dropOverride(cal, rid)
		ov := ical.NewEvent()
		uid, _ := master.Props.Text(ical.PropUID)
		ov.Props.SetText(ical.PropUID, uid)
		ov.Props.Set(framed(master, ical.PropRecurrenceID, rid))
		setTimes(ov, start, end, in.AllDay, loc)
		fillText(ov, in)
		copyAttendees(ov, master) // Teilnehmer gelten für die ganze Serie
		in.Att = nil
		if _, err := applyExtras(ov, in, me); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		cal.Children = append(cal.Children, ov.Component)
		if b.save(w, r, c, file, cal, etag) {
			n := b.notify(me, cal, attDiff{kept: attendeesOf(master)}, true)
			json.NewEncoder(w).Encode(map[string]any{"file": file, "mails": n})
		}
		return
	}
	if in.Scope == "following" && series {
		rid, err := parseTS(in.Rid)
		if err != nil || !hasInstance(master, rid) {
			http.Error(w, "no such occurrence", 404)
			return
		}
		if !rid.Equal(mStart.UTC()) {
			b.putFollowing(w, r, c, file, cal, etag, master, in, rid, start, end, loc)
			return
		}
	}
	// ganze Serie bzw. Einzeltermin: Haupttermin ändern
	keyChanged := false
	// Beginn: unverändert lassen, wenn dieselbe Ortszeit/Zone (erhält schwebende Zeiten und fremde Zonennamen)
	tmp := ical.NewEvent()
	setTimes(tmp, start, end, in.AllDay, loc)
	oldS, newS := master.Props.Get(ical.PropDateTimeStart), tmp.Props.Get(ical.PropDateTimeStart)
	sameStart := false
	switch {
	case mAll != in.AllDay:
	case mAll:
		sameStart = strings.TrimSpace(oldS.Value) == newS.Value
	case mFl:
		sameStart = strings.TrimSpace(oldS.Value) == start.In(loc).Format(fmtLocal) // Ortszeit des Browsers (tz) gleich der gespeicherten
	default:
		x, _, _ := propTime(newS)
		sameStart = x.UTC().Equal(mStart.UTC())
	}
	if !sameStart {
		keyChanged = true
		master.Props.Set(newS)
	}
	// Ende: aus der Dauer, damit schwebende/fremde Zonen konsistent bleiben
	if sameStart && !mAll && mFl {
		d := end.Sub(start)
		p := ical.NewProp(ical.PropDateTimeEnd)
		p.SetValueType(ical.ValueDateTime)
		p.Value = mStart.Add(d).Format(fmtLocal)
		if tz := oldS.Params.Get(ical.ParamTimezoneID); tz != "" {
			p.Params.Set(ical.ParamTimezoneID, tz)
		}
		master.Props.Set(p)
		master.Props.Del(ical.PropDuration)
	} else {
		master.Props.Set(tmp.Props.Get(ical.PropDateTimeEnd))
		master.Props.Del(ical.PropDuration)
	}
	fillText(master, in)
	if in.Rule != nil {
		rs, err := ruleString(in.Rule, in.AllDay, loc)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		old := ""
		if p := master.Props.Get(ical.PropRecurrenceRule); p != nil {
			old = p.Value
		}
		if rs == "" && old != "" || rs != "" && !sameRule(rs, old) {
			keyChanged = true
			setRule(master, rs)
		}
	}
	if keyChanged { // Einzeländerungen und Ausnahmen passen nicht mehr zu den neuen Vorkommen
		clearSeries(cal, master)
	}
	d, err := applyExtras(master, in, me)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	changed := sig(master) != sigBefore
	if changed && len(attendeesOf(master)) > 0 {
		bumpSequence(master)
	}
	if b.save(w, r, c, file, cal, etag) {
		json.NewEncoder(w).Encode(map[string]any{"file": file, "mails": b.notify(me, cal, d, changed)})
	}
}

func (b *Backend) apiDelEvent(w http.ResponseWriter, r *http.Request) {
	file, ok := fileArg(w, r)
	if !ok {
		return
	}
	me := auth.User(r.Context())
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "one" || scope == "following" {
		if !c.write {
			http.Error(w, "read-only calendar", 403)
			return
		}
		defer lockCal(c.owner, c.kal)()
		cal, etag, code, err := b.load(r, c, file)
		if err != nil {
			http.Error(w, err.Error(), code)
			return
		}
		master := masterOf(cal)
		if master.Props.Get(ical.PropRecurrenceRule) != nil {
			rid, err := parseTS(r.URL.Query().Get("rid"))
			if err != nil || !hasInstance(master, rid) {
				http.Error(w, "no such occurrence", 404)
				return
			}
			if scope == "following" {
				if b.delFollowing(w, r, c, file, cal, etag, master, rid) {
					return
				}
			} else {
				dropOverride(cal, rid)
				master.Props.Add(framed(master, ical.PropExceptionDates, rid))
				if b.save(w, r, c, file, cal, etag) {
					b.notify(me, cal, attDiff{kept: attendeesOf(master)}, true)
				}
				return
			}
		}
	}
	var snap *ical.Calendar // Teilnehmer bekommen eine Absage
	var gone []attRow
	if b.mailOn() && c.write {
		if cal, _, _, err := b.load(r, c, file); err == nil {
			snap, gone = cal, attendeesOf(masterOf(cal))
		}
	}
	if err := b.DeleteCalendarObject(r.Context(), davPath(me, c.cid, file)); err != nil {
		code := 500
		if strings.Contains(err.Error(), "read-only") {
			code = 403
		}
		http.Error(w, err.Error(), code)
		return
	}
	if len(gone) > 0 {
		b.notify(me, snap, attDiff{removed: gone}, false)
	}
}
