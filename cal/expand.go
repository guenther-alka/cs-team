package cal

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
)

// Zeiten und Serien in iCalendar-Objekten: Zeitzonen (IANA, übliche Windows-Namen, schwebende Zeiten), Auflösung von
// Serien (RRULE, EXDATE, RDATE, RECURRENCE-ID, STATUS:CANCELLED) in einzelne Vorkommen für Anzeige und Ressourcenprüfung.

// winTZ: Windows-Zeitzonennamen (Outlook, Exchange), wie sie in .ics-Dateien vorkommen, auf IANA-Namen.
var winTZ = map[string]string{
	"UTC": "UTC", "GMT Standard Time": "Europe/London", "Greenwich Standard Time": "Atlantic/Reykjavik",
	"W. Europe Standard Time": "Europe/Berlin", "Central Europe Standard Time": "Europe/Budapest",
	"Romance Standard Time": "Europe/Paris", "Central European Standard Time": "Europe/Warsaw",
	"E. Europe Standard Time": "Europe/Chisinau", "GTB Standard Time": "Europe/Bucharest",
	"FLE Standard Time": "Europe/Kiev", "Russian Standard Time": "Europe/Moscow", "Turkey Standard Time": "Europe/Istanbul",
	"Israel Standard Time": "Asia/Jerusalem", "Arab Standard Time": "Asia/Riyadh", "Arabic Standard Time": "Asia/Baghdad",
	"Egypt Standard Time": "Africa/Cairo", "South Africa Standard Time": "Africa/Johannesburg",
	"W. Central Africa Standard Time": "Africa/Lagos", "E. Africa Standard Time": "Africa/Nairobi",
	"Iran Standard Time": "Asia/Tehran", "Arabian Standard Time": "Asia/Dubai", "Pakistan Standard Time": "Asia/Karachi",
	"India Standard Time": "Asia/Kolkata", "Bangladesh Standard Time": "Asia/Dhaka", "SE Asia Standard Time": "Asia/Bangkok",
	"China Standard Time": "Asia/Shanghai", "Singapore Standard Time": "Asia/Singapore", "W. Australia Standard Time": "Australia/Perth",
	"Tokyo Standard Time": "Asia/Tokyo", "Korea Standard Time": "Asia/Seoul", "AUS Eastern Standard Time": "Australia/Sydney",
	"E. Australia Standard Time": "Australia/Brisbane", "New Zealand Standard Time": "Pacific/Auckland",
	"Eastern Standard Time": "America/New_York", "Central Standard Time": "America/Chicago",
	"Mountain Standard Time": "America/Denver", "US Mountain Standard Time": "America/Phoenix",
	"Pacific Standard Time": "America/Los_Angeles", "Alaskan Standard Time": "America/Anchorage",
	"Hawaiian Standard Time": "Pacific/Honolulu", "Atlantic Standard Time": "America/Halifax",
	"Canada Central Standard Time": "America/Regina", "Mexico Standard Time": "America/Mexico_City",
	"SA Pacific Standard Time": "America/Bogota", "E. South America Standard Time": "America/Sao_Paulo",
	"Argentina Standard Time": "America/Argentina/Buenos_Aires", "SA Western Standard Time": "America/La_Paz",
}

var tzCache sync.Map // Name -> *time.Location (nil-Wert als typisierter nil gespeichert: tzMiss)

type tzMiss struct{}

// lookupTZ: Ortszeit zu einer TZID (IANA oder Windows-Name); nil = unbekannt.
func lookupTZ(name string) *time.Location {
	name = strings.Trim(strings.TrimSpace(name), `"`)
	if name == "" || len(name) > 80 || strings.EqualFold(name, "local") {
		return nil
	}
	if v, ok := tzCache.Load(name); ok {
		l, _ := v.(*time.Location)
		return l
	}
	var loc *time.Location
	if l, err := time.LoadLocation(name); err == nil {
		loc = l
	} else if w, ok := winTZ[name]; ok {
		loc, _ = time.LoadLocation(w)
	}
	if loc != nil {
		tzCache.Store(name, loc)
	} else {
		tzCache.Store(name, tzMiss{})
	}
	return loc
}

const (
	fmtDate  = "20060102"
	fmtLocal = "20060102T150405"
	fmtUTC   = "20060102T150405Z"
)

// propTime liest ein DATE / DATE-TIME. Schwebende Zeiten (ohne Z und TZID) und unbekannte Zeitzonen werden als Uhrzeit in UTC
// gelesen und mit floating=true gekennzeichnet (die Oberfläche zeigt sie als Ortszeit des Betrachters).
func propTime(p *ical.Prop) (t time.Time, floating bool, err error) {
	v := strings.TrimSpace(p.Value)
	if p.ValueType() == ical.ValueDate || (p.ValueType() == ical.ValueDefault && len(v) == len(fmtDate)) {
		t, err = time.ParseInLocation(fmtDate, v, time.UTC)
		return t, false, err
	}
	if strings.HasSuffix(v, "Z") {
		t, err = time.ParseInLocation(fmtUTC, v, time.UTC)
		return t, false, err
	}
	if tz := p.Params.Get(ical.ParamTimezoneID); tz != "" {
		if loc := lookupTZ(tz); loc != nil {
			t, err = time.ParseInLocation(fmtLocal, v, loc)
			return t, false, err
		}
	}
	t, err = time.ParseInLocation(fmtLocal, v, time.UTC)
	return t, true, err
}

func isDateProp(p *ical.Prop) bool {
	return p != nil && (p.ValueType() == ical.ValueDate || (p.ValueType() == ical.ValueDefault && len(strings.TrimSpace(p.Value)) == len(fmtDate)))
}

// evTimes liefert Beginn, Ende (exklusiv) und die Art der Zeitangabe eines Termins.
func evTimes(ev *ical.Event) (s, e time.Time, allDay, floating bool, ok bool) {
	ps := ev.Props.Get(ical.PropDateTimeStart)
	if ps == nil {
		return s, e, false, false, false
	}
	var err error
	if s, floating, err = propTime(ps); err != nil {
		return s, e, false, false, false
	}
	allDay = isDateProp(ps)
	if pe := ev.Props.Get(ical.PropDateTimeEnd); pe != nil {
		if e, _, err = propTime(pe); err != nil {
			e = time.Time{}
		}
	} else if pd := ev.Props.Get(ical.PropDuration); pd != nil {
		if d, err := pd.Duration(); err == nil {
			e = s.Add(d)
		}
	}
	if !e.After(s) {
		e = s.Add(time.Hour)
		if allDay {
			e = s.AddDate(0, 0, 1)
		}
	}
	return s, e, allDay, floating, true
}

// zoneOf: IANA-Name der Zeitzone des Beginns (leer bei UTC, schwebend, ganztägig oder unbekannt).
func zoneOf(ev *ical.Event) string {
	p := ev.Props.Get(ical.PropDateTimeStart)
	if p == nil || isDateProp(p) || strings.HasSuffix(strings.TrimSpace(p.Value), "Z") {
		return ""
	}
	if l := lookupTZ(p.Params.Get(ical.ParamTimezoneID)); l != nil {
		return l.String()
	}
	return ""
}

type span struct {
	s, e time.Time
	ev   *ical.Event
	rec  bool      // Teil einer Serie
	ovr  bool      // einzeln geänderter Termin einer Serie (RECURRENCE-ID)
	rid  time.Time // Vorkommen innerhalb der Serie (Beginn laut Regel); nur bei rec
}

func cancelled(ev *ical.Event) bool {
	st, _ := ev.Props.Text(ical.PropStatus)
	return strings.EqualFold(st, "CANCELLED")
}

// ruleSet: Menge der Vorkommen einer Serie (RRULE, RDATE, EXDATE) mit der Zeitzone des Beginns; nil ohne gültige Regel.
func ruleSet(ev *ical.Event, start time.Time) *rrule.Set {
	p := ev.Props.Get(ical.PropRecurrenceRule)
	if p == nil {
		return nil
	}
	opt, err := rrule.StrToROption(p.Value)
	if err != nil {
		return nil
	}
	opt.Dtstart = start
	r, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil
	}
	set := &rrule.Set{}
	set.RRule(r)
	set.DTStart(start)
	for _, x := range ev.Props.Values(ical.PropExceptionDates) {
		for _, one := range strings.Split(x.Value, ",") {
			q := ical.Prop{Name: x.Name, Value: one, Params: x.Params}
			if t, _, err := propTime(&q); err == nil {
				set.ExDate(t)
			}
		}
	}
	for _, x := range ev.Props.Values(ical.PropRecurrenceDates) {
		for _, one := range strings.Split(x.Value, ",") {
			q := ical.Prop{Name: x.Name, Value: one, Params: x.Params}
			if t, _, err := propTime(&q); err == nil {
				set.RDate(t)
			}
		}
	}
	return set
}

// spans löst die Termine eines Kalenderobjekts im Zeitraum [from, to) in einzelne Vorkommen auf
// (Serien mit RRULE/EXDATE, Ausnahmen mit RECURRENCE-ID, abgesagte Termine entfallen).
func spans(c *ical.Calendar, from, to time.Time) []span {
	evs := c.Events()
	over := map[string]map[time.Time]bool{} // UID -> ersetzte Vorkommen (RECURRENCE-ID)
	for i := range evs {
		if p := evs[i].Props.Get(ical.PropRecurrenceID); p != nil {
			if t, _, err := propTime(p); err == nil {
				uid, _ := evs[i].Props.Text(ical.PropUID)
				if over[uid] == nil {
					over[uid] = map[time.Time]bool{}
				}
				over[uid][t.UTC()] = true
			}
		}
	}
	var out []span
	for i := range evs {
		ev := &evs[i]
		if cancelled(ev) {
			continue
		}
		s, e, _, _, ok := evTimes(ev)
		if !ok {
			continue
		}
		rid := ev.Props.Get(ical.PropRecurrenceID)
		if ev.Props.Get(ical.PropRecurrenceRule) != nil && rid == nil {
			if set := ruleSet(ev, s); set != nil {
				uid, _ := ev.Props.Text(ical.PropUID)
				d := e.Sub(s)
				n := 0
				for _, t := range set.Between(from.Add(-d), to, true) {
					if n++; n > maxInstances {
						break
					}
					if over[uid][t.UTC()] {
						continue
					}
					if te := t.Add(d); te.After(from) && t.Before(to) {
						out = append(out, span{s: t, e: te, ev: ev, rec: true, rid: t.UTC()})
					}
				}
				continue
			}
		}
		if e.After(from) && s.Before(to) {
			sp := span{s: s, e: e, ev: ev}
			if rid != nil {
				sp.rec, sp.ovr = true, true
				if t, _, err := propTime(rid); err == nil {
					sp.rid = t.UTC()
				}
			}
			out = append(out, sp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].s.Before(out[j].s) })
	return out
}

// masterOf: der Haupttermin (ohne RECURRENCE-ID) eines Objekts, nil wenn es keinen gibt.
func masterOf(c *ical.Calendar) *ical.Event {
	evs := c.Events()
	for i := range evs {
		if evs[i].Props.Get(ical.PropRecurrenceID) == nil {
			return &evs[i]
		}
	}
	return nil
}
