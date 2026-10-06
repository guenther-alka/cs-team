package auth

import (
	"bytes"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockBuf struct {
	sync.Mutex
	b bytes.Buffer
}

func (l *lockBuf) Write(p []byte) (int, error) { l.Lock(); defer l.Unlock(); return l.b.Write(p) }
func (l *lockBuf) String() string              { l.Lock(); defer l.Unlock(); return l.b.String() }

// Zwei getrennte Kontingente; die Zusammenfassung kommt auch ohne weitere Meldung nach Ablauf des Fensters.
func TestLogQuota(t *testing.T) {
	lb := &lockBuf{}
	log.SetOutput(lb)
	defer log.SetOutput(os.Stderr)
	f := &logQuota{label: "failed-login", burst: 3, span: 150 * time.Millisecond}
	l := &logQuota{label: "locked", burst: 3, span: 150 * time.Millisecond}
	okF, okL := 0, 0
	for i := 0; i < 10; i++ {
		if f.allow() {
			okF++
		}
	}
	for i := 0; i < 2; i++ { // das Kontingent der Sperr-Zeilen ist von den Fehlversuchen unabhängig
		if l.allow() {
			okL++
		}
	}
	if okF != 3 || okL != 2 {
		t.Fatal("Kontingente:", okF, okL)
	}
	time.Sleep(400 * time.Millisecond) // ohne weitere Meldung
	if s := lb.String(); !strings.Contains(s, "7 further failed-login messages suppressed") || strings.Contains(s, "locked messages") {
		t.Fatal("Zusammenfassung fehlt oder falsch:", s)
	}
	if !f.allow() { // neues Fenster
		t.Fatal("neues Fenster muss wieder erlauben")
	}
	if strings.Count(lb.String(), "suppressed") != 1 {
		t.Fatal("Zusammenfassung doppelt:", lb.String())
	}
}
