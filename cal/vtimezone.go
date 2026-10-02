package cal

// VTIMEZONE: Termine mit TZID verlangen laut RFC 5545 die Zeitzonen-Definition in derselben Datei. Thunderbird kennt viele Zonen
// selbst, Apple-Kalender und Outlook brauchen sie aber oft (sonst "schwebend" oder falsche Stunde). Beim Anlegen/Ändern aus der
// Oberfläche wird sie aus der eingebetteten Zeitzonendatenbank erzeugt: mit Regel (RRULE) für die üblichen jährlichen Umstellungen
// (EU, USA ...), ohne Umstellung als einzelner Block, sonst als Liste von Umstellungen (RDATE).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

type trans struct {
	at          time.Time // Zeitpunkt der Umstellung (UTC)
	from, to    int       // Offsets in Sekunden
	dst         bool
	abbr        string
	wall        time.Time // Ortszeit unmittelbar vor der Umstellung als "schwebende" UTC-Zeit
	month       time.Month
	wd          time.Weekday
	nth         int // 1..4, -1 = letzter
	hour, min   int
	sec         int
	fromH, toH  string
	yearOfTrans int
}

func offStr(sec int) string {
	sign := "+"
	if sec < 0 {
		sign, sec = "-", -sec
	}
	return fmt.Sprintf("%s%02d%02d", sign, sec/3600, sec%3600/60)
}

// transitions: Umstellungen im Jahr y. Gefunden werden sie durch Abtasten (täglich, dann Halbierung auf die Sekunde),
// nicht über Location.ZoneBounds: bei den schlanken Zeitzonendaten (z.B. unter Windows) liefert ZoneBounds an Jahresgrenzen
// falsche Werte und bleibt hängen.
func transitions(loc *time.Location, y int) []trans {
	var out []trans
	state := func(t time.Time) (int, bool) {
		l := t.In(loc)
		_, off := l.Zone()
		return off, l.IsDST()
	}
	day := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
	for ; day.Before(end); day = day.AddDate(0, 0, 1) {
		next := day.AddDate(0, 0, 1)
		o1, d1 := state(day)
		o2, d2 := state(next)
		if o1 == o2 && d1 == d2 {
			continue
		}
		lo, hi := day.Unix(), next.Unix() // lo: alter Zustand, hi: neuer Zustand
		for hi-lo > 1 {
			mid := lo + (hi-lo)/2
			if o, d := state(time.Unix(mid, 0)); o == o1 && d == d1 {
				lo = mid
			} else {
				hi = mid
			}
		}
		at := time.Unix(hi, 0).UTC()
		a := at.In(loc)
		abbr, offA := a.Zone()
		w := at.Add(time.Duration(o1) * time.Second)
		tr := trans{at: at, from: o1, to: offA, dst: a.IsDST(), abbr: abbr, wall: w, month: w.Month(), wd: w.Weekday(), hour: w.Hour(), min: w.Minute(), sec: w.Second(), yearOfTrans: y}
		nth := (w.Day()-1)/7 + 1
		if w.AddDate(0, 0, 7).Month() != w.Month() {
			nth = -1
		}
		tr.nth = nth
		out = append(out, tr)
	}
	return out
}

func sameTrans(a, b trans) bool {
	return a.month == b.month && a.wd == b.wd && a.nth == b.nth && a.hour == b.hour && a.min == b.min && a.from == b.from && a.to == b.to && a.dst == b.dst
}

func tzComp(kind string, t trans, start time.Time, rule string, rdates []time.Time) *ical.Component {
	c := ical.NewComponent(kind)
	c.Props.SetText(ical.PropTimezoneName, t.abbr)
	set := func(name, v string) {
		p := ical.NewProp(name)
		p.Value = v
		c.Props.Set(p)
	}
	set(ical.PropTimezoneOffsetFrom, offStr(t.from))
	set(ical.PropTimezoneOffsetTo, offStr(t.to))
	p := ical.NewProp(ical.PropDateTimeStart)
	p.SetValueType(ical.ValueDateTime)
	p.Value = start.Format(fmtLocal)
	c.Props.Set(p)
	if rule != "" {
		r := ical.NewProp(ical.PropRecurrenceRule)
		r.SetValueType(ical.ValueRecurrence)
		r.Value = rule
		c.Props.Set(r)
	}
	for _, d := range rdates {
		r := ical.NewProp(ical.PropRecurrenceDates)
		r.SetValueType(ical.ValueDateTime)
		r.Value = d.Format(fmtLocal)
		c.Props.Add(r)
	}
	return c
}

// buildTimezone erzeugt die VTIMEZONE-Komponente; nil bei unbekannter Zone.
func buildTimezone(tzid string) *ical.Component {
	loc := lookupTZ(tzid)
	if loc == nil {
		return nil
	}
	vt := ical.NewComponent(ical.CompTimezone)
	vt.Props.SetText(ical.PropTimezoneID, tzid)
	const y0, y1 = 2007, 2030
	byYear := map[int][]trans{}
	n := 0
	for y := y0; y <= y1; y++ {
		byYear[y] = transitions(loc, y)
		n += len(byYear[y])
	}
	_, offNow := time.Date(y0, 1, 1, 0, 0, 0, 0, loc).Zone()
	abbrNow, _ := time.Date(y0, 1, 1, 0, 0, 0, 0, loc).Zone()
	if n == 0 { // keine Umstellung: ein Block
		t := trans{from: offNow, to: offNow, abbr: abbrNow}
		vt.Children = append(vt.Children, tzComp(ical.CompTimezoneStandard, t, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), "", nil))
		return vt
	}
	// Regelmuster: je Jahr gleich viele Umstellungen, jede mit gleicher Regel wie im Startjahr
	pattern := byYear[y0]
	if len(pattern) == 0 {
		pattern = byYear[y0+1]
	}
	regular := len(pattern) > 0
	for y := y0; regular && y <= y1; y++ {
		if len(byYear[y]) != len(pattern) {
			regular = false
			break
		}
		for i := range pattern {
			if !sameTrans(byYear[y][i], pattern[i]) {
				regular = false
				break
			}
		}
	}
	days := []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}
	if regular {
		for _, p := range pattern {
			kind := ical.CompTimezoneStandard
			if p.dst {
				kind = ical.CompTimezoneDaylight
			}
			rule := fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYDAY=%d%s", int(p.month), p.nth, days[p.wd])
			start := time.Date(p.yearOfTrans, p.month, p.wall.Day(), p.hour, p.min, p.sec, 0, time.UTC)
			vt.Children = append(vt.Children, tzComp(kind, p, start, rule, nil))
		}
		sort.SliceStable(vt.Children, func(i, j int) bool { return vt.Children[i].Name < vt.Children[j].Name })
		return vt
	}
	// unregelmäßig: Umstellungen einzeln auflisten
	var all []trans
	for y := y0; y <= y1; y++ {
		all = append(all, byYear[y]...)
	}
	for _, dst := range []bool{false, true} {
		var rd []time.Time
		var first *trans
		for i := range all {
			if all[i].dst == dst {
				if first == nil {
					first = &all[i]
				}
				rd = append(rd, all[i].wall)
			}
		}
		if first == nil {
			continue
		}
		kind := ical.CompTimezoneStandard
		if dst {
			kind = ical.CompTimezoneDaylight
		}
		vt.Children = append(vt.Children, tzComp(kind, *first, rd[0], "", rd[1:]))
	}
	return vt
}

// zoneIDs: alle TZID-Angaben in den Terminen.
func zoneIDs(cal *ical.Calendar) []string {
	seen := map[string]bool{}
	var out []string
	for _, ev := range cal.Events() {
		for _, ps := range ev.Props {
			for _, p := range ps {
				if tz := strings.Trim(p.Params.Get(ical.ParamTimezoneID), `"`); tz != "" && !seen[tz] {
					seen[tz] = true
					out = append(out, tz)
				}
			}
		}
	}
	return out
}

// addTimezones ergänzt fehlende VTIMEZONE-Definitionen (vorhandene bleiben unverändert).
func addTimezones(cal *ical.Calendar) {
	have := map[string]bool{}
	for _, c := range cal.Children {
		if c.Name == ical.CompTimezone {
			if id, _ := c.Props.Text(ical.PropTimezoneID); id != "" {
				have[id] = true
			}
		}
	}
	var add []*ical.Component
	for _, tz := range zoneIDs(cal) {
		if have[tz] {
			continue
		}
		if vt := buildTimezone(tz); vt != nil {
			add = append(add, vt)
		}
	}
	if len(add) > 0 {
		cal.Children = append(add, cal.Children...) // VTIMEZONE steht vor den Terminen
	}
}
