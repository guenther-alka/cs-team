package chat

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"

	"cs-team/store"
)

// fakeSMTP: minimaler SMTP-Server auf localhost (ohne TLS/Anmeldung), zählt angenommene Mails und Empfänger.
func fakeSMTP(t *testing.T) (port string, got func() (int, []string)) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	n, rcpt := 0, []string{}
	lastFrom, lastHdr = "", ""
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				w := func(s string) { c.Write([]byte(s + "\r\n")) }
				w("220 fake")
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(l))
					switch {
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						w("250 fake")
					case strings.HasPrefix(cmd, "MAIL FROM:"):
						mu.Lock()
						lastFrom = strings.Trim(strings.TrimSpace(l)[10:], "<> ")
						mu.Unlock()
						w("250 ok")
					case strings.HasPrefix(cmd, "RCPT TO:"):
						mu.Lock()
						rcpt = append(rcpt, strings.Trim(strings.TrimSpace(l)[8:], "<> "))
						mu.Unlock()
						w("250 ok")
					case cmd == "DATA":
						w("354 go")
						for {
							d, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if d == ".\r\n" {
								break
							}
							if strings.HasPrefix(d, "From: ") {
								mu.Lock()
								lastHdr = strings.TrimSpace(d[6:])
								mu.Unlock()
							}
						}
						mu.Lock()
						n++
						mu.Unlock()
						w("250 queued")
					case cmd == "QUIT":
						w("221 bye")
						return
					default:
						w("250 ok")
					}
				}
			}(c)
		}
	}()
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, func() (int, []string) { mu.Lock(); defer mu.Unlock(); return n, append([]string(nil), rcpt...) }
}

// lastFrom/lastHdr: Absender (MAIL FROM) und From-Kopfzeile der letzten Sitzung eines fakeSMTP.
var lastFrom, lastHdr string

// 0.71: Server 2 muss seinen eigenen Absender benutzen (Envelope und Kopfzeile), über die Einstellungen wie im Betrieb.
func TestSMTPAltFrom(t *testing.T) {
	p2, _ := fakeSMTP(t)
	st := store.NewMem()
	s := NewSettings(st, SMTP{}, false)
	if err := s.set(settingsIn{Host: "127.0.0.1", Port: deadPort(t), TLS: "none", From: "eins@example.org",
		Alt: &altIn{Host: "127.0.0.1", Port: p2, TLS: "none", From: "zwei@example.org"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SMTP().Send([]string{"x@example.org"}, "s", "b", ""); err != nil { // Ausweichen
		t.Fatal(err)
	}
	if lastFrom != "zwei@example.org" || lastHdr != "zwei@example.org" {
		t.Fatalf("Ausweichen: MAIL FROM %q, From: %q, erwartet zwei@example.org", lastFrom, lastHdr)
	}
	c := *s.SMTP().Alt // wie Testmail Server 2
	c.Alt = nil
	if err := c.Send([]string{"x@example.org"}, "s", "b", ""); err != nil || lastFrom != "zwei@example.org" || lastHdr != "zwei@example.org" {
		t.Fatalf("Testmail Server 2: MAIL FROM %q, From: %q, err %v", lastFrom, lastHdr, err)
	}
}

// deadPort: Port, auf dem nichts lauscht (Verbindung wird abgelehnt).
func deadPort(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	return p
}

// 0.71: scheitert der erste Mailserver, geht die Mail über den zweiten; arbeitet der erste, wird der zweite nicht benutzt.
func TestSMTPFallback(t *testing.T) {
	p2, got2 := fakeSMTP(t)
	s := SMTP{Host: "127.0.0.1", Port: deadPort(t), TLS: "none", From: "team@example.org",
		Alt: &SMTP{Host: "127.0.0.1", Port: p2, TLS: "none", From: "team@example.org"}}
	if err := s.Send([]string{"a@example.org", "a2@example.org"}, "x", "y", ""); err != nil {
		t.Fatal("Ausweichen auf Server 2:", err)
	}
	if n, r := got2(); n != 1 || len(r) != 2 {
		t.Fatal("Server 2 hat nicht genau eine Mail an 2 Empfänger angenommen:", n, r)
	}
	p1, got1 := fakeSMTP(t)
	s.Port = p1
	if err := s.Send([]string{"b@example.org"}, "x", "y", ""); err != nil {
		t.Fatal(err)
	}
	if n1, _ := got1(); n1 != 1 {
		t.Fatal("Server 1 nicht benutzt")
	}
	if n2, _ := got2(); n2 != 1 {
		t.Fatal("Server 2 benutzt, obwohl Server 1 arbeitet")
	}
	s.Port = deadPort(t)
	s.Alt.Port = deadPort(t)
	if err := s.Send([]string{"c@example.org"}, "x", "y", ""); err == nil || !strings.Contains(err.Error(), "server 2") {
		t.Fatal("beide aus: Fehler mit beiden Servern erwartet, war:", err)
	}
	only := SMTP{Alt: &SMTP{Host: "127.0.0.1", Port: p2, TLS: "none", From: "team@example.org"}}
	if !only.Enabled() || only.Send([]string{"d@example.org"}, "x", "y", "") != nil {
		t.Fatal("nur Server 2 eingerichtet: muss direkt benutzt werden")
	}
}

// 0.71: zweiter Mailserver in den Einstellungen: speichern, Passwort behalten/löschen, Absender vom ersten, entfernen.
func TestSettingsAltSMTP(t *testing.T) {
	st := store.NewMem()
	s := NewSettings(st, SMTP{}, false)
	pw := "geheim"
	in := settingsIn{Host: "mail1.example.org", From: "team@example.org", Alt: &altIn{Host: "mail2.example.org", Port: "465", TLS: "ssl", User: "u2", Pass: &pw}}
	if err := s.set(in); err != nil {
		t.Fatal(err)
	}
	c := NewSettings(st, SMTP{}, false).SMTP() // neu geladen
	if c.Alt == nil || c.Alt.Host != "mail2.example.org" || c.Alt.Pass != "geheim" || c.Alt.From != "team@example.org" || c.Alt.TLS != "ssl" {
		t.Fatalf("zweiter Server falsch: %+v", c.Alt)
	}
	in.Alt.Pass = nil // gleiches Ziel, kein Passwort gesendet: bleibt
	if err := s.set(in); err != nil || s.SMTP().Alt.Pass != "geheim" {
		t.Fatal("Passwort bei gleichem Server verloren", err)
	}
	in.Alt.Host = "mail3.example.org" // anderes Ziel: Passwort nie weitergeben
	if err := s.set(in); err != nil || s.SMTP().Alt.Pass != "" {
		t.Fatal("Passwort an neuen Server weitergegeben", err)
	}
	in.Alt = nil // fehlt: unverändert
	if err := s.set(in); err != nil || s.SMTP().Alt == nil {
		t.Fatal("zweiter Server ohne Angabe entfernt", err)
	}
	in.Alt = &altIn{Port: "99999", Host: "x"}
	if s.set(in) == nil {
		t.Fatal("ungültiger Port für Server 2 angenommen")
	}
	in.Alt = &altIn{} // Server leer: keiner
	if err := s.set(in); err != nil || s.SMTP().Alt != nil {
		t.Fatal("zweiter Server nicht entfernt", err)
	}
}
