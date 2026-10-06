package chat

import (
	"context"
	"encoding/json"
	"strings"

	"cs-team/store"
)

// Benutzer löschen mit Option "anonymize": der Name des Kontos wird in Nachrichten, Reaktionen, Kanallisten und im
// Versandprotokoll durch einen festen Text ersetzt. Der Inhalt der Nachrichten bleibt (er gehört der Gruppe).

// channelKeys: alle Verlaufsdateien (Gruppe, Kanal); Anhänge (chat/f/), Kanallisten und das Protokoll gehören nicht dazu.
func (s *Svc) channelKeys(ctx context.Context) [][2]string {
	infos, _ := s.St.List(ctx, "chat/")
	var out [][2]string
	for _, i := range infos {
		p := strings.Split(strings.TrimPrefix(i.Key, "chat/"), "/")
		if len(p) != 2 || !validG(p[0]) || !strings.HasSuffix(p[1], ".json") || strings.HasPrefix(p[1], "_") {
			continue
		}
		out = append(out, [2]string{p[0], strings.TrimSuffix(p[1], ".json")})
	}
	return out
}

func reacted(m *Msg, user string) bool {
	for _, l := range m.Re {
		for _, u := range l {
			if u == user {
				return true
			}
		}
	}
	return false
}

// AnonCount: Zahl der Nachrichten, Kanäle und Protokolleinträge mit dem Namen (Vorschau).
func (s *Svc) AnonCount(ctx context.Context, user string) int {
	n := 0
	for _, k := range s.channelKeys(ctx) {
		c := s.ch(ctx, k[0], k[1])
		c.mu.Lock()
		for i := range c.msgs {
			if c.msgs[i].By == user || reacted(&c.msgs[i], user) {
				n++
			}
		}
		c.mu.Unlock()
	}
	groups := map[string]bool{}
	for _, k := range s.channelKeys(ctx) {
		groups[k[0]] = true
	}
	for g := range groups {
		for _, ci := range s.list(ctx, g) {
			if ci.By == user {
				n++
			}
		}
	}
	if b, _, err := s.St.Get(ctx, logKey); err == nil {
		var l []logEntry
		if json.Unmarshal(b, &l) == nil {
			for _, e := range l {
				if e.By == user {
					n++
				}
			}
		}
	}
	return n
}

// Anon ersetzt den Namen durch repl und liefert die Zahl der geänderten Einträge.
func (s *Svc) Anon(ctx context.Context, user, repl string) (int, error) {
	n := 0
	groups := map[string]bool{}
	for _, k := range s.channelKeys(ctx) {
		groups[k[0]] = true
		c := s.ch(ctx, k[0], k[1])
		c.mu.Lock()
		ch := 0
		for i := range c.msgs {
			m := &c.msgs[i]
			hit := false
			if m.By == user {
				m.By, hit = repl, true
			}
			for e, l := range m.Re { // Reaktionen: der Name wird ersetzt, doppelte Einträge entfallen
				var out []string
				seen := map[string]bool{}
				for _, u := range l {
					if u == user {
						u, hit = repl, true
					}
					if !seen[u] {
						seen[u] = true
						out = append(out, u)
					}
				}
				m.Re[e] = out
			}
			if hit {
				ch++
			}
		}
		if ch > 0 {
			if err := s.save(ctx, c); err != nil {
				c.mu.Unlock()
				return n, err
			}
			n += ch
		}
		c.mu.Unlock()
	}
	for g := range groups {
		l := s.list(ctx, g)
		s.mu.Lock()
		ch := 0
		for name, ci := range l {
			if ci.By == user {
				ci.By = repl
				l[name] = ci
				ch++
			}
		}
		s.mu.Unlock()
		if ch > 0 {
			s.saveList(ctx, g)
			n += ch
		}
	}
	ch := 0
	if b, _, err := s.St.Get(ctx, logKey); err != nil || !strings.Contains(string(b), `"by":"`+user+`"`) {
		return n, nil
	}
	err := store.Update(ctx, s.St, logKey, func(cur []byte) ([]byte, error) {
		var l []logEntry
		if cur == nil || json.Unmarshal(cur, &l) != nil {
			return cur, nil
		}
		for i := range l {
			if l[i].By == user {
				l[i].By = repl
				ch++
			}
		}
		if ch == 0 {
			return cur, nil
		}
		return json.Marshal(l)
	})
	n += ch
	return n, err
}
