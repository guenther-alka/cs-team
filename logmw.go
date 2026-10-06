package main

import (
	"bufio"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// logMW (0.58.1): Protokoll für den Betrieb.
//   - Serverfehler (5xx) und abgefangene Abstürze (panic) mit Methode, Pfad und Benutzer,
//   - Audit: schreibende Zugriffe auf Benutzer, Gruppen, Einstellungen und Passwörter (wer, was, Ergebnis),
//   - CS_LOG_ACCESS=1: jede Anfrage (Fehlersuche), ohne Statik und ohne WebSocket-Aufbau.
//
// Es werden nie Anfrageinhalte, Passwörter oder Tokens protokolliert; Pfade enthalten höchstens Namen.
func logMW(next http.Handler) http.Handler {
	all := os.Getenv("CS_LOG_ACCESS") == "1"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: 200}
		start := time.Now()
		defer func() {
			if e := recover(); e != nil {
				if e == http.ErrAbortHandler {
					panic(e)
				}
				log.Printf("http: PANIC %s %s user=%q ip=%s: %v", r.Method, cleanLog(r.URL.Path), logUser(r), logIP(r), e)
				if !sw.wrote {
					http.Error(sw, "internal error", http.StatusInternalServerError)
				}
				return
			}
			p := r.URL.Path
			switch {
			case sw.code >= 500:
				log.Printf("http: %d %s %s user=%q ip=%s", sw.code, r.Method, cleanLog(p), logUser(r), logIP(r))
			case audited(r) && r.Method != http.MethodGet && r.Method != http.MethodHead:
				verb := "audit"
				if sw.code >= 400 {
					verb = "audit-denied"
				}
				log.Printf("%s: %s %s -> %d user=%q ip=%s", verb, r.Method, cleanLog(p), sw.code, logUser(r), logIP(r))
			case all && !strings.HasPrefix(p, "/lang/") && p != "/" && sw.code != 101:
				log.Printf("access: %s %s -> %d %dms user=%q ip=%s", r.Method, cleanLog(p), sw.code, time.Since(start).Milliseconds(), logUser(r), logIP(r))
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// audited: Pfade, deren Änderungen nachvollziehbar sein müssen (Rechte, Konten, Einstellungen).
func audited(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/api/users") || strings.HasPrefix(p, "/api/groups") || strings.HasPrefix(p, "/api/settings") ||
		p == "/api/me/password" || strings.HasPrefix(p, "/api/year") || strings.HasPrefix(p, "/api/routine")
}

func logUser(r *http.Request) string {
	if u, _, ok := r.BasicAuth(); ok {
		return cleanLog(u)
	}
	return ""
}

func logIP(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// cleanLog: keine Steuerzeichen, begrenzte Länge (Log-Injektion).
func cleanLog(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return '?'
		}
		return r
	}, s)
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

// statusWriter merkt den Statuscode und reicht Hijack/Flush (WebSocket, Streaming) durch.
type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (s *statusWriter) WriteHeader(c int) {
	if !s.wrote {
		s.code, s.wrote = c, true
	}
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		s.code, s.wrote = 101, true
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
