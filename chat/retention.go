package chat

import (
	"context"
	"log"
	"time"

	"cs-team/auth"
)

// Aufbewahrung (DSGVO): je Gruppe löscht cs-team Chat-Nachrichten, die älter sind als "Aufbewahrung Chat (Tage)" der Gruppe
// (0 = unbegrenzt, Vorgabe). Das Alter folgt aus der Nachrichten-ID (Mikrosekunden seit 1970). Anhänge werden mit gelöscht.

// purgeOld löscht in allen Kanälen der Gruppe die Nachrichten vor now-days und liefert ihre Zahl.
func (s *Svc) purgeOld(ctx context.Context, g string, days int, now time.Time) int {
	if days <= 0 {
		return 0
	}
	cut := now.Add(-time.Duration(days) * 24 * time.Hour).UnixMicro()
	n := 0
	for _, cn := range s.channelsOf(ctx, g) {
		c := s.ch(ctx, g, cn)
		c.mu.Lock()
		keep := c.msgs[:0:0]
		gone := 0
		for _, m := range c.msgs {
			if m.ID >= cut {
				keep = append(keep, m)
				continue
			}
			if m.Att != nil {
				s.St.Delete(ctx, fileKey(g, cn, m.Att.ID))
			}
			gone++
		}
		if gone > 0 {
			old := c.msgs
			c.msgs = keep
			if err := s.save(ctx, c); err != nil {
				c.msgs = old // Speichern gescheitert: Stand behalten, nächster Lauf versucht es erneut
				gone = 0
			}
		}
		c.mu.Unlock()
		n += gone
	}
	return n
}

// Retention: ein Lauf über alle Gruppen mit eingestellter Aufbewahrung; je Gruppe mit Treffern eine Logzeile. Liefert die Summe.
func (s *Svc) Retention(ctx context.Context, now time.Time) int {
	total := 0
	for _, g := range auth.AllGroupNames() {
		info, ok := auth.GroupInfoOf(g)
		if !ok || info.ChatDays <= 0 {
			continue
		}
		if n := s.purgeOld(ctx, g, info.ChatDays, now); n > 0 {
			log.Printf("retention: group=%s deleted %d messages (older than %d days)", g, n, info.ChatDays)
			total += n
		}
	}
	return total
}

// RunRetention: Lauf beim Start (nach kurzer Pause) und danach stündlich, bis ctx endet.
func (s *Svc) RunRetention(ctx context.Context) {
	go func() {
		select {
		case <-time.After(45 * time.Second):
		case <-ctx.Done():
			return
		}
		tk := time.NewTicker(time.Hour)
		defer tk.Stop()
		for {
			s.Retention(ctx, time.Now())
			select {
			case <-tk.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}
