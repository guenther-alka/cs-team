package chat

// Benutzer loeschen (Assistent): Teil des Chats, der VOR dem Anonymisieren laeuft (UserHook mit Early).
//   - 1:1-Kanaele, an denen das Konto beteiligt ist, werden samt Verlauf und Anhaengen entfernt (sie gehoeren nur den beiden).
//   - Anhaenge, die das Konto in Gruppen-Kanaelen hochgeladen hat, werden geloescht; die Nachricht bleibt ohne Anhang.
//   - Option "hart loeschen" (auth.HardDelete): die Nachrichten des Kontos in Gruppen-Kanaelen verschwinden ganz,
//     Antworten darauf bleiben als normale Nachrichten. Ohne die Option bleibt der Text und wird danach anonymisiert.

import (
	"context"
	"strings"

	"cs-team/auth"
)

// chatGroups: alle Gruppen, zu denen es Chat-Dateien gibt (Verlaeufe oder Kanalliste).
func (s *Svc) chatGroups(ctx context.Context) []string {
	infos, _ := s.St.List(ctx, "chat/")
	seen := map[string]bool{}
	var out []string
	for _, i := range infos {
		p := strings.Split(strings.TrimPrefix(i.Key, "chat/"), "/")
		if len(p) != 2 || !validG(p[0]) || seen[p[0]] {
			continue
		}
		seen[p[0]] = true
		out = append(out, p[0])
	}
	return out
}

// dmNames: Namen der 1:1-Kanaele der Gruppe, an denen user beteiligt ist.
func (s *Svc) dmNames(ctx context.Context, g, user string) []string {
	l := s.list(ctx, g)
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for name, ci := range l {
		if inDM(ci.Dm, user) {
			out = append(out, name)
		}
	}
	return out
}

// dropChannel entfernt einen Kanal samt Verlauf und Anhaengen ohne Rechtepruefung (nur fuer das Loeschen eines Kontos).
func (s *Svc) dropChannel(ctx context.Context, g, name string) {
	c := s.ch(ctx, g, name)
	c.mu.Lock() // laufende Schreiber abwarten, danach darf der Kanal nie wieder gespeichert werden
	c.gone = true
	c.mu.Unlock()
	l := s.list(ctx, g)
	s.mu.Lock()
	delete(l, name)
	delete(s.chans, g+"/"+name)
	s.mu.Unlock()
	s.saveList(ctx, g)
	s.St.Delete(ctx, chatKey(g, name))
	if infos, err := s.St.List(ctx, "chat/f/"+g+"/"+name+"/"); err == nil {
		for _, i := range infos {
			s.St.Delete(ctx, i.Key)
		}
	}
	s.broadcastChans(ctx, g)
}

// UserCount (Vorschau): 1:1-Kanaele plus eigene Anhaenge in Gruppen-Kanaelen.
func (s *Svc) UserCount(ctx context.Context, user string) int {
	n := 0
	dm := map[string]bool{}
	for _, g := range s.chatGroups(ctx) {
		for _, name := range s.dmNames(ctx, g, user) {
			dm[g+"/"+name] = true
			n++
		}
	}
	for _, k := range s.channelKeys(ctx) {
		if dm[k[0]+"/"+k[1]] {
			continue
		}
		c := s.ch(ctx, k[0], k[1])
		c.mu.Lock()
		for i := range c.msgs {
			if c.msgs[i].By == user && c.msgs[i].Att != nil {
				n++
			}
		}
		c.mu.Unlock()
	}
	return n
}

// MsgCount (Vorschau fuer "hart loeschen"): eigene Nachrichten in Gruppen-Kanaelen (ohne 1:1, die gehen ohnehin).
func (s *Svc) MsgCount(ctx context.Context, user string) int {
	n := 0
	dm := map[string]bool{}
	for _, g := range s.chatGroups(ctx) {
		for _, name := range s.dmNames(ctx, g, user) {
			dm[g+"/"+name] = true
		}
	}
	for _, k := range s.channelKeys(ctx) {
		if dm[k[0]+"/"+k[1]] {
			continue
		}
		c := s.ch(ctx, k[0], k[1])
		c.mu.Lock()
		for i := range c.msgs {
			if c.msgs[i].By == user && !c.msgs[i].Del {
				n++
			}
		}
		c.mu.Unlock()
	}
	return n
}

// PurgeUser: siehe Kopfkommentar. Fehler brechen das Loeschen des Kontos ab (der Assistent meldet sie).
func (s *Svc) PurgeUser(ctx context.Context, user string) error {
	hard := auth.HardDelete(ctx)
	for _, g := range s.chatGroups(ctx) {
		for _, name := range s.dmNames(ctx, g, user) {
			s.dropChannel(ctx, g, name)
		}
	}
	for _, k := range s.channelKeys(ctx) {
		c := s.ch(ctx, k[0], k[1])
		c.mu.Lock()
		if c.gone {
			c.mu.Unlock()
			continue
		}
		changed := false
		keep := make([]Msg, 0, len(c.msgs))
		dropped := map[int64]bool{}
		for _, m := range c.msgs {
			if m.By == user {
				if m.Att != nil {
					s.St.Delete(ctx, fileKey(k[0], k[1], m.Att.ID))
					m.Att = nil
					changed = true
					if m.T == "" && !hard {
						m.T = "(Anhang gelöscht)"
					}
				}
				if hard {
					dropped[m.ID] = true
					changed = true
					continue
				}
			}
			keep = append(keep, m)
		}
		if len(dropped) > 0 {
			for i := range keep {
				if dropped[keep[i].Rp] { // Antwort auf eine entfernte Nachricht: wird eine normale Nachricht
					keep[i].Rp = 0
				}
			}
		}
		if changed {
			c.msgs = keep
			if err := s.save(ctx, c); err != nil {
				c.mu.Unlock()
				return err
			}
		}
		c.mu.Unlock()
	}
	return nil
}
