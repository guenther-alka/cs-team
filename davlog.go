package main

import (
	"log"
	"net"
	"net/http"
)

// davLog (0.69.2): fehlgeschlagene CalDAV-Anmeldungen ins Log schreiben (Benutzername, Adresse, Pfad; nie das Passwort),
// damit sich Probleme mit Handy-/Thunderbird-Konten nachvollziehen lassen. Erfolgreiche Anfragen werden nicht protokolliert.
type davStatus struct {
	http.ResponseWriter
	code int
}

func (w *davStatus) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }

func (w *davStatus) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func davLog(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &davStatus{ResponseWriter: w, code: http.StatusOK}
		h.ServeHTTP(sw, r)
		if sw.code == http.StatusUnauthorized || sw.code == http.StatusForbidden {
			user, _, ok := r.BasicAuth()
			if !ok && sw.code == http.StatusUnauthorized {
				return // erste Anfrage ohne Anmeldung: normal, der Client meldet sich danach an
			}
			ip, _, _ := net.SplitHostPort(r.RemoteAddr)
			log.Printf("caldav: %d %s %s user=%q ip=%s agent=%q", sw.code, r.Method, r.URL.Path, user, ip, r.UserAgent())
		}
	})
}
