
package chat

import (
	"errors"
	"log"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Umfragen (0.60.0): eine Umfrage ist eine ganz normale Chat-Nachricht mit dem Feld "poll" (gleicher Speicher, gleicher
// Verlauf, gleiche Aufbewahrung, gleiche Anonymisierung, gleicher WebSocket-Weg).
//   - Benannt: Votes (Name -> Optionen) ist fuer alle Leser des Kanals sichtbar.
//   - Anonym: der Server speichert nur die Zaehler (Cnt) und je Person die Marke "hat abgestimmt" (Done), NIE die Wahl.
//     Stimmen koennen deshalb nicht geaendert werden. Ein Votes-Feld gibt es bei anonymen Umfragen nicht. Im Netz (WebSocket
//     und Verlauf, siehe wire) gehen bei offener anonymer Umfrage weder Namen noch Zaehler je Antwort hinaus, nur die Zahl
//     der Teilnehmer und je Empfaenger "Me" (habe ich abgestimmt); die Ergebnisse folgen erst nach dem Ende. So verraet
//     auch der Vergleich zweier aufeinanderfolgender Meldungen nichts ueber die Wahl. Rest-Risiko: wer Zugriff auf den
//     Speicher selbst hat und die Datei vor/nach einer Stimme vergleicht, sieht, welcher Zaehler stieg.
//   - Ende: optional (End, Unix-Millisekunden) oder von Hand (Closed); abgelaufene Umfragen lehnen Stimmen ab.

const (
	maxPollQ    = 200
	maxPollOpt  = 100
	minPollOpts = 2
	maxPollOpts = 10
	maxPollDays = 366 // Ende hoechstens ein Jahr voraus
)

type Poll struct {
	Q      string           `json:"q"`
	Opts   []string         `json:"opts"`
	Multi  bool             `json:"multi,omitempty"`
	Anon   bool             `json:"anon,omitempty"`
	End    int64            `json:"end,omitempty"` // Unix-Millisekunden; 0 = kein Ende
	Closed bool             `json:"closed,omitempty"`
	Cnt    []int            `json:"cnt,omitempty"`   // Stimmen je Option (anonym und offen: nie im Netz, siehe wire)
	N      int              `json:"n"`               // Zahl der Teilnehmer
	Me     bool             `json:"me,omitempty"`    // nur im Netz, anonym: habe ich abgestimmt? (je Empfänger, nie gespeichert)
	Votes  map[string][]int `json:"votes,omitempty"` // nur benannt: Name -> gewaehlte Optionen
	Done   []string         `json:"done,omitempty"`  // nur anonym: Namen der Teilnehmer ohne Wahl
}

type pollIn struct {
	Q     string   `json:"q"`
	Opts  []string `json:"opts"`
	Multi bool     `json:"multi"`
	Anon  bool     `json:"anon"`
	End   int64    `json:"end"`
}

var (
	ErrPollBad    = errors.New("bad poll")
	ErrPollClosed = errors.New("poll closed")
	ErrPollVoted  = errors.New("anonymous votes cannot be changed")
	ErrPollChoice = errors.New("bad choice")
)

func oneLine(s string) string { return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)) }

// newPoll prueft die Eingabe (Frage <= 200, 2-10 verschiedene Optionen <= 100 Zeichen, Ende in der Zukunft).
func newPoll(in *pollIn, now time.Time) (*Poll, error) {
	if in == nil {
		return nil, ErrPollBad
	}
	q := oneLine(in.Q)
	if q == "" || utf8.RuneCountInString(q) > maxPollQ || len(in.Opts) < minPollOpts || len(in.Opts) > maxPollOpts {
		return nil, ErrPollBad
	}
	p := &Poll{Q: q, Multi: in.Multi, Anon: in.Anon, Cnt: make([]int, len(in.Opts))}
	seen := map[string]bool{}
	for _, o := range in.Opts {
		o = oneLine(o)
		if o == "" || utf8.RuneCountInString(o) > maxPollOpt || seen[o] {
			return nil, ErrPollBad
		}
		seen[o] = true
		p.Opts = append(p.Opts, o)
	}
	if in.End != 0 {
		if in.End <= now.UnixMilli() || in.End > now.Add(maxPollDays*24*time.Hour).UnixMilli() {
			return nil, ErrPollBad
		}
		p.End = in.End
	}
	if !p.Anon {
		p.Votes = map[string][]int{}
	}
	return p, nil
}

// clone: Aenderungen immer an einer Kopie (die Nachricht wird nach dem Entsperren noch an Clients gesendet/gelesen).
func (p *Poll) clone() *Poll {
	c := *p
	c.Cnt = append([]int(nil), p.Cnt...)
	c.Done = append([]string(nil), p.Done...)
	if p.Votes != nil {
		c.Votes = make(map[string][]int, len(p.Votes))
		for k, v := range p.Votes {
			c.Votes[k] = append([]int(nil), v...)
		}
	}
	return &c
}

// wire: die Umfrage, wie sie user sehen darf. Benannt: unveraendert. Anonym: nie die Namensliste, Me = hat user abgestimmt,
// Zaehler je Antwort erst wenn die Umfrage beendet ist.
func (p *Poll) wire(user string, now time.Time) *Poll {
	if !p.Anon {
		return p
	}
	w := *p
	w.Me, w.Done = p.done(user), nil
	if p.open(now) {
		w.Cnt = nil
	}
	return &w
}

func (p *Poll) open(now time.Time) bool { return !p.Closed && (p.End == 0 || now.UnixMilli() < p.End) }

func (p *Poll) done(user string) bool {
	for _, u := range p.Done {
		if u == user {
			return true
		}
	}
	return false
}

// vote traegt die Stimme von user ein. Benannt: eine neue Stimme ersetzt die alte, leere Auswahl nimmt sie zurueck.
// Anonym: genau eine Stimme, keine Aenderung.
func (p *Poll) vote(user string, sel []int, now time.Time) error {
	if !p.open(now) {
		return ErrPollClosed
	}
	seen := map[int]bool{}
	for _, i := range sel {
		if i < 0 || i >= len(p.Opts) || seen[i] {
			return ErrPollChoice
		}
		seen[i] = true
	}
	if len(sel) > 1 && !p.Multi {
		return ErrPollChoice
	}
	if p.Anon {
		if len(sel) == 0 {
			return ErrPollChoice
		}
		if p.done(user) {
			return ErrPollVoted
		}
		for _, i := range sel {
			p.Cnt[i]++
		}
		p.Done = append(p.Done, user)
		sort.Strings(p.Done) // Reihenfolge der Abstimmung verraet nichts
		p.N++
		return nil
	}
	old, had := p.Votes[user]
	if len(sel) == 0 && !had {
		return ErrPollChoice
	}
	for _, i := range old {
		p.Cnt[i]--
	}
	for _, i := range sel {
		p.Cnt[i]++
	}
	sort.Ints(sel)
	switch {
	case len(sel) == 0:
		delete(p.Votes, user)
		p.N--
	case !had:
		p.Votes[user] = sel
		p.N++
	default:
		p.Votes[user] = sel
	}
	return nil
}

// choices: alle Optionen, die user in einer benannten Umfrage gewaehlt hat (Datenauskunft).
func (p *Poll) choices(user string) []string {
	var out []string
	for _, i := range p.Votes[user] {
		if i >= 0 && i < len(p.Opts) {
			out = append(out, p.Opts[i])
		}
	}
	return out
}

// voted: hat user an der Umfrage teilgenommen (benannt oder anonym)?
func (p *Poll) voted(user string) bool {
	_, ok := p.Votes[user]
	return ok || p.done(user)
}

// rename ersetzt den Namen user durch repl (Konto geloescht, Anonymisierung); Zaehler und Teilnehmerzahl bleiben.
func (p *Poll) rename(user, repl string) bool {
	hit := false
	if l, ok := p.Votes[user]; ok {
		delete(p.Votes, user)
		if o, ok := p.Votes[repl]; ok { // zwei geloeschte Konten: Wahl zusammenfuehren, Zaehler bleiben
			m := map[int]bool{}
			for _, i := range append(o, l...) {
				m[i] = true
			}
			l = l[:0:0]
			for i := range m {
				l = append(l, i)
			}
			sort.Ints(l)
		}
		p.Votes[repl] = l
		hit = true
	}
	for i, u := range p.Done {
		if u == user {
			p.Done[i], hit = repl, true
		}
	}
	return hit
}

// pollClose: Ersteller oder Gruppen-Admin beendet die Umfrage vorzeitig (Aufruf unter c.mu).
func pollClose(x *Msg, user string, admin bool) bool {
	if x.Poll == nil || x.Poll.Closed || (x.By != user && !admin) {
		return false
	}
	x.Poll = x.Poll.clone()
	x.Poll.Closed = true
	if x.By != user {
		log.Printf("audit: poll closed by group admin user=%q author=%q", cleanLogName(user), cleanLogName(x.By))
	}
	return true
}

func cleanLogName(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return '?'
		}
		return r
	}, s)
}
