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

var logRate struct {
	sync.Mutex
	win     time.Time
	n       int
	dropped int
}

// logLimited schreibt eine Zeile, solange das Minutenkontingent reicht; danach wird nur gezählt.
func logLimited(format string, args ...any) {
	logRate.Lock()
	now := time.Now()
	if now.Sub(logRate.win) >= time.Minute {
		if logRate.dropped > 0 {
			log.Printf("auth: %d further messages suppressed in the last minute", logRate.dropped)
		}
		logRate.win, logRate.n, logRate.dropped = now, 0, 0
	}
	logRate.n++
	over := logRate.n > logBurst
	if over {
		logRate.dropped++
	}
	logRate.Unlock()
	if !over {
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
