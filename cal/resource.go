package cal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"cs-team/auth"
)

// Ressourcen-Kalender (Raum, Beamer, Fahrzeug): überschneidende Termine werden abgelehnt (Ende exklusiv).

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

// conflict: liefert eine Meldung, wenn ein Termin aus c mit einem vorhandenen (außer skipObj) kollidiert.
func (b *Backend) conflict(ctx context.Context, ci *calRef, c *ical.Calendar, skipObj string) string {
	if !ci.m.Resource {
		return ""
	}
	objs, err := b.ListCalendarObjects(ctx, davPath(auth.User(ctx), ci.cid, ""), nil)
	if err != nil {
		return ""
	}
	for _, n := range c.Events() {
		s1, e1, ok := evSpan(&n)
		if !ok {
			continue
		}
		uid1, _ := n.Props.Text(ical.PropUID)
		for _, o := range objs {
			if skipObj != "" && strings.HasSuffix(o.Path, "/"+skipObj) {
				continue
			}
			for _, ev := range o.Data.Events() {
				uid2, _ := ev.Props.Text(ical.PropUID)
				if uid2 == uid1 && uid1 != "" {
					continue
				}
				s2, e2, ok := evSpan(&ev)
				if ok && s1.Before(e2) && s2.Before(e1) {
					sum, _ := ev.Props.Text(ical.PropSummary)
					return fmt.Sprintf("double booked: %q %s - %s", sum, s2.Local().Format("02.01. 15:04"), e2.Local().Format("15:04"))
				}
			}
		}
	}
	return ""
}
