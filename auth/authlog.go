package auth

import (
	"log"
	"strings"
	"sync"
	"time"
)

// Protokoll für Anmeldefehler und Sperren (0.58.1): damit Angriffe und Fehlbedienung im Log sichtbar sind.
// Die Zeilen enthalten nur Benutzername (bereinigt, gekürzt) und Adresse, nie Passwörter. Die Ausgabe ist begrenzt
// (höchstens logBurst Zeilen je Minute), damit ein Angriff das Log nicht füllt.
const logBurst = 30

// logQuota: ein Minutenkontingent. Gesperrt-Zeilen und Fehlversuch-Zeilen haben je ein eigenes,
// damit ein Angriff mit vielen Fehlversuchen die Sperr-Meldungen nicht verdrängt (und umgekehrt).
type logQuota struct {
	sync.Mutex
	label   string
	burst   int
	span    time.Duration // Länge des Fensters (Minute; im Test kürzer)
	win     time.Time
	n       int
	dropped int
	timer   *time.Timer
}

var (
	failQuota   = &logQuota{label: "failed-login", burst: logBurst, span: time.Minute}
	lockedQuota = &logQuota{label: "locked", burst: logBurst, span: time.Minute}
)

// allow: true, solange das Kontingent der laufenden Minute reicht. Beim ersten Verwerfen wird ein Timer
// gestartet, der am Ende des Fensters die Zusammenfassung schreibt, auch wenn keine Meldung mehr folgt.
func (q *logQuota) allow() bool {
	q.Lock()
	defer q.Unlock()
	now := time.Now()
	if now.Sub(q.win) >= q.span {
		q.summary()
		q.win, q.n = now, 0
	}
	q.n++
	if q.n <= q.burst {
		return true
	}
	q.dropped++
	if q.dropped == 1 {
		q.timer = time.AfterFunc(q.win.Add(q.span).Sub(now), func() {
			q.Lock()
			q.summary()
			q.Unlock()
		})
	}
	return false
}

// summary schreibt die Zusammenfassung der verworfenen Zeilen (Aufrufer hält die Sperre).
func (q *logQuota) summary() {
	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	if q.dropped > 0 {
		log.Printf("auth: %d further %s messages suppressed in the last minute", q.dropped, q.label)
		q.dropped = 0
	}
}

// logLimited schreibt eine Fehlversuch-Zeile, solange das Minutenkontingent reicht.
func logLimited(format string, args ...any) {
	if failQuota.allow() {
		log.Printf(format, args...)
	}
}

// logLocked schreibt eine Sperr-Zeile (eigenes Kontingent).
func logLocked(format string, args ...any) {
	if lockedQuota.allow() {
		log.Printf(format, args...)
	}
}

// safeName: Name für das Log (keine Steuerzeichen, höchstens 40 Zeichen).
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return '?'
		}
		return r
	}, s)
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return s
}
