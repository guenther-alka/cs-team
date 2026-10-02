package cal

// Erinnerungen (VALARM), Teilnehmer (ATTENDEE/ORGANIZER) mit Einladungsmail (iMIP) und "dieser und folgende" bei Serien.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

	"cs-team/auth"
)

// ---- Erinnerung ----

var durRe = regexp.MustCompile(`^([+-]?)P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// alarmMinutes: Erinnerung in Minuten vor dem Beginn (erste Erinnerung relativ zum Beginn); nil = keine.
func alarmMinutes(ev *ical.Event) *int {
	for _, ch := range ev.Children {
		if ch.Name != ical.CompAlarm {
			continue
		}
		p := ch.Props.Get(ical.PropTrigger)
		if p == nil || p.ValueType() == ical.ValueDateTime || strings.EqualFold(p.Params.Get("RELATED"), "END") {
			continue
		}
		m := durRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(p.Value)))
		if m == nil {
			continue
		}
		n := func(s string) int { v, _ := strconv.Atoi(s); return v }
		secs := n(m[2])*7*86400 + n(m[3])*86400 + n(m[4])*3600 + n(m[5])*60 + n(m[6])
		if m[1] != "-" && secs > 0 {
			continue // Erinnerung nach dem Beginn
		}
		min := secs / 60
		return &min
	}
	return nil
}

func triggerStr(min int) string {
	switch {
	case min <= 0:
		return "PT0S"
	case min%10080 == 0:
		return fmt.Sprintf("-P%dW", min/10080)
	case min%1440 == 0:
		return fmt.Sprintf("-P%dD", min/1440)
	case min%60 == 0:
		return fmt.Sprintf("-PT%dH", min/60)
	}
	return fmt.Sprintf("-PT%dM", min)
}

// setAlarm ersetzt alle Erinnerungen des Termins durch eine (min < 0: keine).
func setAlarm(ev *ical.Event, min int) {
	kept := ev.Children[:0]
	for _, ch := range ev.Children {
		if ch.Name != ical.CompAlarm {
			kept = append(kept, ch)
		}
	}
	ev.Children = kept
	if min < 0 {
		return
	}
	sum, _ := ev.Props.Text(ical.PropSummary)
	if sum == "" {
		sum = "Reminder"
	}
	a := ical.NewComponent(ical.CompAlarm)
	a.Props.SetText(ical.PropAction, "DISPLAY")
	a.Props.SetText(ical.PropDescription, sum)
	p := ical.NewProp(ical.PropTrigger)
	p.SetValueType(ical.ValueDuration)
	p.Value = triggerStr(min)
	a.Props.Set(p)
	ev.Children = append(ev.Children, a)
}

func copyAlarms(dst, src *ical.Event) {
	for _, ch := range src.Children {
		if ch.Name == ical.CompAlarm {
			dst.Children = append(dst.Children, ch)
		}
	}
}

// ---- Teilnehmer ----

type attRow struct {
	Name   string `json:"name,omitempty"`
	Mail   string `json:"mail,omitempty"`
	Status string `json:"status,omitempty"` // NEEDS-ACTION | ACCEPTED | DECLINED | TENTATIVE
}

const urnPrefix = "urn:cs-team:"

// key: Vergleichsschlüssel (Adresse, sonst Name)
func (a attRow) key() string {
	if a.Mail != "" {
		return strings.ToLower(a.Mail)
	}
	return "u:" + strings.ToLower(a.Name)
}

func attendeesOf(ev *ical.Event) []attRow {
	var out []attRow
	for _, p := range ev.Props.Values(ical.PropAttendee) {
		v := strings.TrimSpace(p.Value)
		r := attRow{Name: p.Params.Get(ical.ParamCommonName), Status: strings.ToUpper(p.Params.Get(ical.ParamParticipationStatus))}
		switch {
		case len(v) > 7 && strings.EqualFold(v[:7], "mailto:"):
			r.Mail = v[7:]
		case strings.HasPrefix(v, urnPrefix):
			r.Name = v[len(urnPrefix):]
		default:
			continue
		}
		out = append(out, r)
	}
	return out
}

var mailRe = regexp.MustCompile(`^[^\s@<>",;:]+@[^\s@<>",;:]+\.[^\s@<>",;:]+$`)

const maxAttendees = 50

// resolveAttendees: Eingaben (Benutzername oder E-Mail-Adresse) in Teilnehmer umsetzen.
func resolveAttendees(in []string) ([]attRow, error) {
	var out []attRow
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if len(s) > 254 || strings.ContainsAny(s, "\r\n") {
			return nil, errors.New("bad attendee")
		}
		var r attRow
		if strings.Contains(s, "@") {
			if !mailRe.MatchString(s) {
				return nil, errors.New("bad e-mail address: " + s)
			}
			r = attRow{Mail: s}
		} else {
			mail, _, ok := auth.ContactOf(s)
			if !ok {
				return nil, errors.New("unknown user: " + s)
			}
			r = attRow{Name: s, Mail: mail}
		}
		if seen[r.key()] {
			continue
		}
		seen[r.key()] = true
		out = append(out, r)
		if len(out) > maxAttendees {
			return nil, errors.New("too many attendees")
		}
	}
	return out, nil
}

func mkAttendee(a attRow, status string) ical.Prop {
	p := ical.NewProp(ical.PropAttendee)
	p.SetValueType(ical.ValueCalendarAddress)
	if a.Mail != "" {
		p.Value = "mailto:" + a.Mail
	} else {
		p.Value = urnPrefix + a.Name
	}
	if a.Name != "" {
		p.Params.Set(ical.ParamCommonName, a.Name)
	}
	p.Params.Set(ical.ParamRole, "REQ-PARTICIPANT")
	if status == "" {
		status = "NEEDS-ACTION"
	}
	p.Params.Set(ical.ParamParticipationStatus, status)
	p.Params.Set(ical.ParamRSVP, "TRUE")
	return *p
}

// setAttendees ersetzt die Teilnehmerliste (bekannte Zusagen bleiben erhalten) und setzt den Organisator.
func setAttendees(ev *ical.Event, list []attRow, me string) {
	old := map[string]string{}
	for _, a := range attendeesOf(ev) {
		old[a.key()] = a.Status
	}
	ev.Props.Del(ical.PropAttendee)
	if len(list) == 0 {
		ev.Props.Del(ical.PropOrganizer)
		return
	}
	for _, a := range list {
		p := mkAttendee(a, old[a.key()])
		ev.Props.Add(&p)
	}
	if ev.Props.Get(ical.PropOrganizer) == nil {
		o := ical.NewProp(ical.PropOrganizer)
		o.SetValueType(ical.ValueCalendarAddress)
		mail, _, _ := auth.ContactOf(me)
		if mail != "" {
			o.Value = "mailto:" + mail
		} else {
			o.Value = urnPrefix + me
		}
		o.Params.Set(ical.ParamCommonName, me)
		ev.Props.Set(o)
	}
}

func copyAttendees(dst, src *ical.Event) {
	dst.Props.Del(ical.PropAttendee)
	for _, p := range src.Props.Values(ical.PropAttendee) {
		q := p
		dst.Props.Add(&q)
	}
	if o := src.Props.Get(ical.PropOrganizer); o != nil {
		q := *o
		dst.Props.Set(&q)
	}
}

func organizerOf(ev *ical.Event) (name, mail string) {
	o := ev.Props.Get(ical.PropOrganizer)
	if o == nil {
		return
	}
	name = o.Params.Get(ical.ParamCommonName)
	if v := strings.TrimSpace(o.Value); len(v) > 7 && strings.EqualFold(v[:7], "mailto:") {
		mail = v[7:]
	}
	return
}

func bumpSequence(ev *ical.Event) {
	n := 0
	if p := ev.Props.Get(ical.PropSequence); p != nil {
		n, _ = strconv.Atoi(strings.TrimSpace(p.Value))
	}
	p := ical.NewProp(ical.PropSequence)
	p.SetValueType(ical.ValueInt)
	p.Value = strconv.Itoa(n + 1)
	ev.Props.Set(p)
}

// attDiff: Änderung der Teilnehmerliste
type attDiff struct{ added, kept, removed []attRow }

func diffAtt(before, after []attRow) attDiff {
	var d attDiff
	b, a := map[string]bool{}, map[string]bool{}
	for _, x := range before {
		b[x.key()] = true
	}
	for _, x := range after {
		a[x.key()] = true
		if b[x.key()] {
			d.kept = append(d.kept, x)
		} else {
			d.added = append(d.added, x)
		}
	}
	for _, x := range before {
		if !a[x.key()] {
			d.removed = append(d.removed, x)
		}
	}
	return d
}

// sig: Merkmale, deren Änderung eine Terminänderung für die Teilnehmer ist.
func sig(ev *ical.Event) string {
	var b strings.Builder
	for _, n := range []string{ical.PropSummary, ical.PropLocation, ical.PropDateTimeStart, ical.PropDateTimeEnd, ical.PropRecurrenceRule} {
		if p := ev.Props.Get(n); p != nil {
			b.WriteString(n + "=" + p.Value + p.Params.Get(ical.ParamTimezoneID) + "\n")
		}
	}
	return b.String()
}

// ---- Einladungen (iMIP) ----

func cloneEvent(ev *ical.Event) *ical.Event {
	c := ical.NewEvent()
	for k, v := range ev.Props {
		c.Props[k] = append([]ical.Prop(nil), v...)
	}
	return c
}

func icsOf(cal *ical.Calendar, method string, only []attRow) string {
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, "-//cs-team//EN")
	out.Props.SetText(ical.PropMethod, method)
	for _, ch := range cal.Children {
		if only != nil && ch.Name == ical.CompEvent { // Absage: nur die betroffenen Teilnehmer, Termin als abgesagt
			ev := cloneEvent(&ical.Event{Component: ch})
			ev.Props.Del(ical.PropAttendee)
			for _, a := range only {
				p := mkAttendee(a, "")
				ev.Props.Add(&p)
			}
			ev.Props.SetText(ical.PropStatus, "CANCELLED")
			bumpSequence(ev)
			out.Children = append(out.Children, ev.Component)
			continue
		}
		out.Children = append(out.Children, ch)
	}
	var sb strings.Builder
	if ical.NewEncoder(&sb).Encode(out) != nil {
		return ""
	}
	return sb.String()
}

func whenText(ev *ical.Event) string {
	s, e, allDay, _, ok := evTimes(ev)
	if !ok {
		return ""
	}
	loc := time.UTC
	if l := lookupTZ(zoneOf(ev)); l != nil {
		loc = l
	}
	if allDay {
		return s.Format("02.01.2006") + " (ganztägig / all day)"
	}
	z := s.In(loc).Format("MST")
	if loc == time.UTC {
		z = "UTC"
	}
	return s.In(loc).Format("02.01.2006 15:04") + " - " + e.In(loc).Format("15:04") + " " + z
}

func mailsOf(l []attRow) []string {
	var out []string
	for _, a := range l {
		if a.Mail != "" {
			out = append(out, a.Mail)
		}
	}
	return out
}

// Schutz vor Missbrauch als Mail-Relais: je Benutzer höchstens inviteLimit Empfänger pro Stunde.
const inviteLimit = 100

var (
	inviteMu  sync.Mutex
	inviteLog = map[string][]time.Time{}
)

// inviteAllow bucht n Empfänger für den Benutzer; false, wenn das Stundenlimit überschritten würde.
func inviteAllow(user string, n int) bool {
	inviteMu.Lock()
	defer inviteMu.Unlock()
	cut := time.Now().Add(-time.Hour)
	l := inviteLog[user][:0]
	for _, t := range inviteLog[user] {
		if t.After(cut) {
			l = append(l, t)
		}
	}
	if len(l)+n > inviteLimit {
		inviteLog[user] = l
		return false
	}
	for i := 0; i < n; i++ {
		l = append(l, time.Now())
	}
	inviteLog[user] = l
	return true
}

// Mail: Versand einer Einladung (nil = nicht eingerichtet). Rückgabe: ob versendet wurde.
type MailFunc func(to []string, subject, body, ics, method, replyTo string) (bool, error)

// notify verschickt (im Hintergrund) Einladungen an neue, Änderungen an bleibende und Absagen an entfernte Teilnehmer.
// Rückgabe: Zahl der Empfänger mit E-Mail-Adresse, -1 wenn das Stundenlimit erreicht ist (dann wird nichts verschickt).
func (b *Backend) notify(me string, cal *ical.Calendar, d attDiff, changed bool) int {
	m := masterOf(cal)
	if m == nil || b.Mail == nil || !b.mailOn() {
		return 0
	}
	sum, _ := m.Props.Text(ical.PropSummary)
	loc, _ := m.Props.Text(ical.PropLocation)
	desc, _ := m.Props.Text(ical.PropDescription)
	_, orgMail := organizerOf(m)
	from := me
	if orgMail != "" {
		from = me + " <" + orgMail + ">"
	}
	body := func(head string) string {
		s := head + ": " + sum + "\n\nWann / When: " + whenText(m) + "\n"
		if loc != "" {
			s += "Wo / Where: " + loc + "\n"
		}
		if p := m.Props.Get(ical.PropRecurrenceRule); p != nil {
			s += "Serie / Series: " + p.Value + "\n"
		}
		s += "Von / From: " + from + "\n"
		if desc != "" {
			s += "\n" + desc + "\n"
		}
		return s
	}
	type job struct {
		to            []string
		subject, text string
		ics, method   string
	}
	var jobs []job
	if to := mailsOf(d.added); len(to) > 0 {
		jobs = append(jobs, job{to, "Einladung / Invitation: " + sum, body("Einladung / Invitation"), icsOf(cal, "REQUEST", nil), "REQUEST"})
	}
	if to := mailsOf(d.kept); len(to) > 0 && changed {
		jobs = append(jobs, job{to, "Geändert / Updated: " + sum, body("Geändert / Updated"), icsOf(cal, "REQUEST", nil), "REQUEST"})
	}
	if to := mailsOf(d.removed); len(to) > 0 {
		jobs = append(jobs, job{to, "Abgesagt / Cancelled: " + sum, body("Abgesagt / Cancelled"), icsOf(cal, "CANCEL", d.removed), "CANCEL"})
	}
	n := 0
	for _, j := range jobs {
		n += len(j.to)
	}
	if n > 0 && !inviteAllow(me, n) {
		return -1 // Limit erreicht, nichts verschickt
	}
	if n > 0 {
		go func() {
			for _, j := range jobs {
				if j.ics != "" {
					b.Mail(j.to, j.subject, j.text, j.ics, j.method, orgMail)
				}
			}
		}()
	}
	return n
}

// MailOn: ist der Mailversand eingerichtet?
func (b *Backend) mailOn() bool { return b.MailOK != nil && b.MailOK() }

// ---- Serien: "dieser und folgende" ----

// rawBefore: Zahl der Vorkommen laut Regel (ohne Ausnahmen) vor rid.
func rawBefore(master *ical.Event, start, rid time.Time) int {
	p := master.Props.Get(ical.PropRecurrenceRule)
	if p == nil {
		return 0
	}
	opt, err := rrule.StrToROption(p.Value)
	if err != nil {
		return 0
	}
	opt.Dtstart = start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return 0
	}
	return len(r.Between(start.Add(-time.Second), rid.Add(-time.Second), true))
}

// ruleParts: Regel ohne COUNT/UNTIL, dazu die alten Werte.
func ruleParts(v string) (base string, count int) {
	var keep []string
	for _, p := range strings.Split(strings.TrimSpace(v), ";") {
		k, val, _ := strings.Cut(p, "=")
		switch strings.ToUpper(k) {
		case "COUNT":
			count, _ = strconv.Atoi(val)
		case "UNTIL":
		default:
			if p != "" {
				keep = append(keep, p)
			}
		}
	}
	return strings.Join(keep, ";"), count
}

// untilBefore: UNTIL-Wert (in der Darstellung des Beginns) für "bis vor rid".
func untilBefore(master *ical.Event, rid time.Time) string {
	ps := master.Props.Get(ical.PropDateTimeStart)
	switch {
	case isDateProp(ps):
		return rid.UTC().AddDate(0, 0, -1).Format(fmtDate)
	case strings.HasSuffix(strings.TrimSpace(ps.Value), "Z") || ps.Params.Get(ical.ParamTimezoneID) != "":
		return rid.UTC().Add(-time.Second).Format(fmtUTC)
	}
	return rid.UTC().Add(-time.Second).Format(fmtLocal)
}

// truncateSeries beendet die Serie vor rid, entfernt spätere Einzeltermine/Ausnahmen und liefert die Ausnahmen ab rid.
func truncateSeries(cal *ical.Calendar, master *ical.Event, rid time.Time) (oldRule string, count int, exLater []time.Time) {
	p := master.Props.Get(ical.PropRecurrenceRule)
	oldRule = p.Value
	base, cnt := ruleParts(oldRule)
	count = cnt
	setRule(master, base+";UNTIL="+untilBefore(master, rid))
	kept := cal.Children[:0]
	for _, ch := range cal.Children {
		if ch.Name == ical.CompEvent {
			if rp := ch.Props.Get(ical.PropRecurrenceID); rp != nil {
				if t, _, err := propTime(rp); err == nil && !t.Before(rid) {
					continue
				}
			}
		}
		kept = append(kept, ch)
	}
	cal.Children = kept
	var keepEx []ical.Prop
	for _, x := range master.Props.Values(ical.PropExceptionDates) {
		var early []string
		for _, one := range strings.Split(x.Value, ",") {
			q := ical.Prop{Name: x.Name, Value: one, Params: x.Params}
			if t, _, err := propTime(&q); err == nil {
				if t.Before(rid) {
					early = append(early, one)
				} else {
					exLater = append(exLater, t)
				}
			}
		}
		if len(early) > 0 {
			x.Value = strings.Join(early, ",")
			keepEx = append(keepEx, x)
		}
	}
	master.Props.Del(ical.PropExceptionDates)
	for i := range keepEx {
		master.Props.Add(&keepEx[i])
	}
	return
}

// ruleUntil: UNTIL-Wert einer Regel (roh), sonst "".
func ruleUntil(v string) string {
	for _, p := range strings.Split(strings.TrimSpace(v), ";") {
		if k, val, _ := strings.Cut(p, "="); strings.EqualFold(k, "UNTIL") {
			return val
		}
	}
	return ""
}

// ---- Eingabe: Erinnerung und Teilnehmer ----

const maxAlarmMin = 40320 // 4 Wochen

// orgText: Organisator für die Anzeige (Name, sonst Adresse)
func orgText(ev *ical.Event) string {
	n, m := organizerOf(ev)
	if n != "" {
		return n
	}
	return m
}

// applyExtras übernimmt Erinnerung und Teilnehmer aus der Eingabe in ev. Fehlen sie (nil), bleibt alles wie es war.
// Rückgabe: Änderung der Teilnehmerliste (für die Einladungen).
func applyExtras(ev *ical.Event, in evIn, me string) (attDiff, error) {
	if in.Alarm != nil {
		if *in.Alarm > maxAlarmMin {
			return attDiff{}, errors.New("bad reminder")
		}
		setAlarm(ev, *in.Alarm)
	}
	before := attendeesOf(ev)
	if in.Att == nil {
		return attDiff{kept: before}, nil
	}
	list, err := resolveAttendees(*in.Att)
	if err != nil {
		return attDiff{}, err
	}
	setAttendees(ev, list, me)
	return diffAtt(before, attendeesOf(ev)), nil
}

// ---- "dieser und folgende" ----

// putFollowing trennt die Serie vor rid: der alte Teil endet davor, ab rid beginnt eine neue Serie mit den geänderten Angaben.
func (b *Backend) putFollowing(w http.ResponseWriter, r *http.Request, c *calRef, file string, cal *ical.Calendar, etag string,
	master *ical.Event, in evIn, rid, start, end time.Time, loc *time.Location) {
	me := auth.User(r.Context())
	mStart, _, _, _, _ := evTimes(master)
	before := rawBefore(master, mStart, rid)
	old := master.Props.Get(ical.PropRecurrenceRule).Value
	base, cnt := ruleParts(old)
	rs := ""
	if in.Rule != nil {
		var err error
		if rs, err = ruleString(in.Rule, in.AllDay, loc); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if rs != "" && cnt > 0 && in.Rule.Count == cnt { // unverändert übernommene Anzahl: Rest der Serie
			rs, _ = ruleParts(rs)
			rs += ";COUNT=" + strconv.Itoa(cnt-before)
		}
	} else {
		rs = base
		if cnt > 0 {
			rs += ";COUNT=" + strconv.Itoa(cnt-before)
		} else if u := ruleUntil(old); u != "" {
			rs += ";UNTIL=" + u
		}
	}
	nev := ical.NewEvent()
	id := newID()
	nev.Props.SetText(ical.PropUID, id+"@cs-team")
	setTimes(nev, start, end, in.AllDay, loc)
	fillText(nev, in)
	copyAttendees(nev, master)
	copyAlarms(nev, master)
	if rs != "" {
		setRule(nev, rs)
	}
	_, _, exLater := truncateSeries(cal, master, rid)
	if rs != "" && start.UTC().Equal(rid) { // gleiche Uhrzeit: einzelne Ausnahmen bleiben gültig
		for _, t := range exLater {
			nev.Props.Add(framed(nev, ical.PropExceptionDates, t))
		}
	}
	if _, err := applyExtras(nev, in, me); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	ncal := newCal(nev)
	if msg := b.conflict(r.Context(), c, ncal, ""); msg != "" {
		http.Error(w, msg, http.StatusConflict)
		return
	}
	addTimezones(ncal)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(ncal); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	nkey := key(c.owner, c.kal, id+".ics")
	if _, err := b.St.Put(r.Context(), nkey, buf.Bytes(), "*"); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !b.save(w, r, c, file, cal, etag) {
		b.St.Delete(r.Context(), nkey)
		return
	}
	n := b.notify(me, ncal, attDiff{added: attendeesOf(nev)}, false)
	if m := b.notify(me, cal, attDiff{kept: attendeesOf(master)}, true); m < 0 || n < 0 {
		n = -1
	} else {
		n += m
	}
	json.NewEncoder(w).Encode(map[string]any{"file": id + ".ics", "mails": n})
}

// delFollowing beendet die Serie vor rid. false: rid ist der erste Termin (dann ganze Serie löschen).
func (b *Backend) delFollowing(w http.ResponseWriter, r *http.Request, c *calRef, file string, cal *ical.Calendar, etag string, master *ical.Event, rid time.Time) (handled bool) {
	mStart, _, _, _, _ := evTimes(master)
	if rid.Equal(mStart) {
		return false
	}
	truncateSeries(cal, master, rid)
	if b.save(w, r, c, file, cal, etag) {
		b.notify(auth.User(r.Context()), cal, attDiff{kept: attendeesOf(master)}, true)
	}
	return true
}
