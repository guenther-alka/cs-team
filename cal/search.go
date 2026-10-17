package cal

import (
	"context"

	"github.com/emersion/go-ical"

	"cs-team/auth"
	"cs-team/hit"
)

// Search: Termine (Titel, Ort, Beschreibung) in den Kalendern, die der Benutzer lesen darf; eine Serie zaehlt einmal (Starttermin).
func (b *Backend) Search(ctx context.Context, q string, limit int) []hit.Hit {
	me := auth.User(ctx)
	l, err := b.list(ctx)
	if err != nil {
		return nil
	}
	var out []hit.Hit
	for _, c := range l {
		if !c.read {
			continue
		}
		objs, err := b.ListCalendarObjects(ctx, davPath(me, c.cid, ""), nil)
		if err != nil {
			continue
		}
		for _, o := range objs {
			ev := masterOf(o.Data)
			if ev == nil {
				continue
			}
			sum, _ := ev.Props.Text(ical.PropSummary)
			loc, _ := ev.Props.Text(ical.PropLocation)
			desc, _ := ev.Props.Text(ical.PropDescription)
			sn, ok := "", false
			for _, f := range []string{sum, loc, desc} {
				if s2, o2 := hit.Snip(f, q); o2 {
					ok = true
					if f != sum {
						sn = s2
					}
					break
				}
			}
			if !ok {
				continue
			}
			when := ""
			if s, _, _, _, tok := evTimes(ev); tok {
				when = s.Format("2006-01-02") + " · "
			}
			out = append(out, hit.Hit{Kind: "event", ID: c.cid, Title: sum, Snippet: sn, Where: when + c.m.Name})
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}
