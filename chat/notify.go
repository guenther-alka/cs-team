package chat

// Meldungen aus dem Chat ueber die externen Adressen der Benutzer (E-Mail, Chat-Adresse/ntfy; Svc.Notify, Svc.NotifyDM von main gesetzt; Abwahl im Konto):
//  - Erwaehnung mit @name und Antwort auf eine Nachricht (Art "mention"),
//  - Nachricht in einem 1:1-Kanal an den anderen Teilnehmer (Art "dm").
// Nur wer den Kanal lesen darf; hoechstens eine Meldung je Person und Kanal in 10 Minuten.

import (
	"regexp"
	"strings"
	"time"

	"cs-team/auth"
)

var reMention = regexp.MustCompile(`(?i)(^|[^a-z0-9._-])@([a-z0-9][a-z0-9._-]*)`)

const mentionGap = 10 * time.Minute

// userOf: kanonischer Benutzername zu "@name" (auch mit Satzzeichen am Ende, z. B. "@anna." oder "@anna,").
func userOf(raw string) (string, bool) {
	for _, c := range []string{raw, strings.TrimRight(raw, ".-_"), strings.ToLower(raw), strings.ToLower(strings.TrimRight(raw, ".-_"))} {
		if c == "" {
			continue
		}
		if _, _, ok := auth.ContactOf(c); ok {
			return c, true
		}
	}
	return "", false
}

// due: false, wenn dieselbe Person fuer diesen Schluessel in den letzten 10 Minuten schon gemeldet wurde.
func (s *Svc) due(key string) bool {
	now := time.Now()
	s.nmu.Lock()
	defer s.nmu.Unlock()
	if s.mnote == nil || len(s.mnote) > 4000 {
		s.mnote = map[string]time.Time{}
	}
	if t, seen := s.mnote[key]; seen && now.Sub(t) < mentionGap {
		return false
	}
	s.mnote[key] = now
	return true
}

func (s *Svc) link(body string) string {
	if s.Base != nil {
		if b := s.Base(); b != "" {
			return body + "\n\n" + b
		}
	}
	return body
}

// mentionOne meldet name (Erwaehnung oder Antwort), wenn er den Kanal lesen darf und nicht zu frisch gemeldet wurde.
func (s *Svc) mentionOne(name, g, cn, by, text string) {
	if s.Notify == nil || name == by {
		return
	}
	if r, _ := access(name, g); !r { // nur wer den Kanal lesen darf: nichts ueber fremde Gruppen verraten
		return
	}
	if !s.due(name + "|" + g + "|" + cn) {
		return
	}
	go s.Notify(name, "cs-team ["+g+" #"+cn+"] "+by+" @"+name, s.link(by+": "+clipRunes(text, 300)))
}

func (s *Svc) notifyMentions(g, cn, by, text string) {
	if s.Notify == nil || !strings.Contains(text, "@") {
		return
	}
	done := map[string]bool{}
	for _, m := range reMention.FindAllStringSubmatch(text, 20) {
		name, ok := userOf(m[2])
		if !ok || done[name] {
			continue
		}
		done[name] = true
		s.mentionOne(name, g, cn, by, text)
	}
}

// notifyAfterPost: Antwort an den Verfasser der Nachricht, auf die geantwortet wurde; 1:1-Kanal: Meldung an den anderen Teilnehmer.
func (s *Svc) notifyAfterPost(g, cn, by, parentBy, text string, dm []string) {
	if dm != nil {
		other := otherOf(dm, by)
		if s.NotifyDM != nil && other != "" && other != by && s.due("dm|"+other+"|"+g+"|"+cn) {
			go s.NotifyDM(other, "cs-team ["+g+"] "+by+" (1:1)", s.link(by+": "+clipRunes(text, 300)))
		}
		return
	}
	if parentBy != "" && parentBy != by {
		s.mentionOne(parentBy, g, cn, by, text)
	}
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
