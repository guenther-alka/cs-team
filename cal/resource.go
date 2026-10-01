package cal

import (
	"context"
	"fmt"
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

// fmtSpan: Zeitangabe in der Zeitzone des Termins (TZID), sonst UTC mit Kennzeichnung – nicht in der Zeitzone des Servers.
func fmtSpan(ev *ical.Event, s, e time.Time) string {
	loc := time.UTC
	if p := ev.Props.Get(ical.PropDateTimeStart); p != nil {
		if l := lookupTZ(p.Params.Get(ical.ParamTimezoneID)); l != nil {
			loc = l
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
