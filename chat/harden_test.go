package chat

import (
	"strconv"
	"testing"
	"time"
)

func TestTrimLogPerGroup(t *testing.T) {
	var l []logEntry
	for i := 0; i < 5; i++ { // 5 Einträge der ruhigen Gruppe
		l = trimLog(append(l, logEntry{Group: "ruhig", Subj: strconv.Itoa(i)}), "ruhig")
	}
	for i := 0; i < 500; i++ { // 500 der lauten Gruppe
		l = trimLog(append(l, logEntry{Group: "laut", Subj: strconv.Itoa(i)}), "laut")
	}
	n := map[string]int{}
	for _, e := range l {
		n[e.Group]++
	}
	if n["ruhig"] != 5 || n["laut"] != logPerGroup {
		t.Fatalf("je Gruppe: %v", n)
	}
	// die ältesten der lauten Gruppe fielen weg, die neuesten blieben
	last := l[len(l)-1]
	if last.Subj != "499" {
		t.Fatal("neuester Eintrag fehlt:", last)
	}
}

func TestTrimLogTotal(t *testing.T) {
	var l []logEntry
	for i := 0; i < logTotal+50; i++ {
		l = trimLog(append(l, logEntry{Group: "g" + strconv.Itoa(i)}), "g"+strconv.Itoa(i))
	}
	if len(l) != logTotal {
		t.Fatal(len(l))
	}
}

func TestDropOldest(t *testing.T) {
	s := &Svc{conns: map[*client]bool{}}
	now := time.Now()
	var cs []*client
	for i := 0; i < maxConnsUser; i++ {
		c := &client{user: "bob", born: now.Add(time.Duration(i) * time.Second), done: make(chan struct{})}
		cs = append(cs, c)
		s.conns[c] = true
	}
	other := &client{user: "anna", born: now.Add(-time.Hour), done: make(chan struct{})}
	s.conns[other] = true
	s.dropOldest("bob")
	if s.conns[cs[0]] || len(s.conns) != maxConnsUser {
		t.Fatal("älteste Verbindung muss weichen, andere Benutzer bleiben:", len(s.conns))
	}
	select {
	case <-cs[0].done:
	default:
		t.Fatal("done der verdrängten Verbindung nicht geschlossen")
	}
	if !s.conns[other] {
		t.Fatal("fremde Verbindung entfernt")
	}
}
