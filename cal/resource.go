package cal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"

	"cs-team/auth"
)

// Ressourcen-Kalender (Raum, Beamer, Fahrzeug): überschneidende Termine werden abgelehnt (Ende exklusiv).
// Prüfung und Speichern laufen je Kalender unter einer Sperre (lockCal), damit zwei gleichzeitige Buchungen sich nicht beide
// durchsetzen. Serientermine (RRULE, EXDATE, RECURRENCE-ID) werden für den betroffenen Zeitraum aufgelöst. Gilt je Prozess;
// mehrere cs-team-Instanzen auf demselben Speicher sind dadurch nicht abgedeckt.

const (
	conflictHorizon = 2 * 366 * 24 * time.Hour // so weit voraus werden Serien aufgelöst
	maxInstances    = 1500                     // je Serie
)

// CheckDelay: nur für Tests – Pause nach bestandener Prüfung, macht das Zeitfenster zwischen Prüfen und Speichern sichtbar.
var CheckDelay time.Duration

var (
	calLocksMu sync.Mutex
	calLocks   = map[string]*sync.Mutex{}
)

// lockCal sperrt einen Kalender (Besitzer/Name) und liefert die Freigabe.
func lockCal(owner, kal string) func() {
	k := owner + "/" + kal
	calLocksMu.Lock()
	l := calLocks[k]
	if l == nil {
		l = &sync.Mutex{}
		calLocks[k] = l
	}
	calLocksMu.Unlock()
	l.Lock()
	return l.Unlock
}

func evSpan(ev *ical.Event) (s, e time.Time, ok bool) {
	s, err := ev.DateTimeStart(time.UTC)
	if err != nil {
		return s, e, false
	}
	e, err = ev.DateTimeEnd(time.UTC)
	if err != nil || !e.After(s) {
		e = s.Add(time.Hour)
		if p := ev.Props.Get(ical.PropDateTimeStart); p != nil && p.ValueType() == ical.ValueDate {
			e = s.AddDate(0, 0, 1)
		}
	}
	return s, e, true
}

type span struct {
	s, e time.Time
	ev   *ical.Event
}

func cancelled(ev *ical.Event) bool {
	st, _ := ev.Props.Text(ical.PropStatus)
	return strings.EqualFold(st, "CANCELLED")
}

// spans löst die Termine eines Kalenderobjekts im Zeitraum [from, to) in einzelne Vorkommen auf
// (Serien mit RRULE/EXDATE, Ausnahmen mit RECURRENCE-ID, abgesagte Termine entfallen).
func spans(c *ical.Calendar, from, to time.Time) []span {
	evs := c.Events()
	over := map[string]map[time.Time]bool{} // UID -> ersetzte Vorkommen (RECURRENCE-ID)
	for i := range evs {
		if p := evs[i].Props.Get(ical.PropRecurrenceID); p != nil {
			if t, err := p.DateTime(time.UTC); err == nil {
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
		s, e, ok := evSpan(ev)
		if !ok {
			continue
		}
		if ev.Props.Get(ical.PropRecurrenceRule) != nil && ev.Props.Get(ical.PropRecurrenceID) == nil {
			set, err := ev.Component.RecurrenceSet(time.UTC)
			if err == nil && set != nil {
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
						out = append(out, span{t, te, ev})
					}
				}
				continue
			}
		}
		if e.After(from) && s.Before(to) {
			out = append(out, span{s, e, ev})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].s.Before(out[j].s) })
	return out
}

// fmtSpan: Zeitangabe in der Zeitzone des Termins (TZID), sonst UTC mit Kennzeichnung – nicht in der Zeitzone des Servers.
func fmtSpan(ev *ical.Event, s, e time.Time) string {
	loc := time.UTC
	if p := ev.Props.Get(ical.PropDateTimeStart); p != nil {
		if tz := p.Params.Get(ical.ParamTimezoneID); tz != "" {
			if l, err := time.LoadLocation(tz); err == nil {
				loc = l
			}
		}
	}
	return s.In(loc).Format("02.01. 15:04") + " - " + e.In(loc).Format("15:04 MST")
}

// conflict: liefert eine Meldung, wenn ein Termin aus c mit einem vorhandenen (außer skipObj) kollidiert.
func (b *Backend) conflict(ctx context.Context, ci *calRef, c *ical.Calendar, skipObj string) string {
	if !ci.m.Resource {
		return ""
	}
	// Zeitraum der neuen Vorkommen
	now := time.Now().UTC()
	from := now.AddDate(-1, 0, 0)
	mine := spans(c, from, now.Add(conflictHorizon))
	if len(mine) == 0 {
		return ""
	}
	lo, hi := mine[0].s, mine[0].e
	for _, m := range mine {
		if m.e.After(hi) {
			hi = m.e
		}
	}
	objs, err := b.ListCalendarObjects(ctx, davPath(auth.User(ctx), ci.cid, ""), nil)
	if err != nil {
		return ""
	}
	for _, o := range objs {
		if skipObj != "" && strings.HasSuffix(o.Path, "/"+skipObj) {
			continue
		}
		for _, x := range spans(o.Data, lo, hi) {
			uid2, _ := x.ev.Props.Text(ical.PropUID)
			for _, m := range mine { // nach Beginn sortiert: ab einem Beginn nach dem Ende von x kann nichts mehr überschneiden
				if !m.s.Before(x.e) {
					break
				}
				uid1, _ := m.ev.Props.Text(ical.PropUID)
				if uid1 != "" && uid1 == uid2 {
					continue
				}
				if m.s.Before(x.e) && x.s.Before(m.e) {
					sum, _ := x.ev.Props.Text(ical.PropSummary)
					return fmt.Sprintf("double booked: %q %s", sum, fmtSpan(x.ev, x.s, x.e))
				}
			}
		}
	}
	if CheckDelay > 0 {
		time.Sleep(CheckDelay)
	}
	return ""
}
