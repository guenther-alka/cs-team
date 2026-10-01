package auth

import (
	"context"
	"crypto/sha256"
	"strconv"
	"sync"
	"time"
)

type cacheEnt struct {
	h   [32]byte // Hash des gespeicherten Passwort-Hashs: Passwortänderung macht den Eintrag ungültig
	exp time.Time
}

// verifyCached wie verify, merkt sich aber erfolgreiche Anmeldungen authTTL lang (nur im Speicher, gesalzen).
// Ohne das kostet jede Anfrage (auch WebDAV/API) ein bcrypt (~50-80 ms). Gesperrte/deaktivierte Konten und geänderte
// Passwörter werden nicht aus dem Cache bedient.
func (a *Auth) verifyCached(ctx context.Context, name, pass string) (Account, bool) {
	u, ok := a.get(ctx, name)
	if !ok || !validName.MatchString(name) || u.Disabled {
		return a.verify(ctx, name, pass)
	}
	k := sha256.Sum256(append(append(append(a.salt[:], name...), 0), pass...))
	h := sha256.Sum256([]byte(u.Hash))
	now := time.Now()
	a.mu.Lock()
	e, found := a.cache[k]
	a.mu.Unlock()
	if found && e.h == h && now.Before(e.exp) {
		return u, true
	}
	if _, good := a.verify(ctx, name, pass); !good {
		return Account{}, false
	}
	a.mu.Lock()
	if len(a.cache) >= maxCache {
		a.cache = map[[32]byte]cacheEnt{}
	}
	a.cache[k] = cacheEnt{h: h, exp: now.Add(authTTL)}
	a.mu.Unlock()
	return u, true
}

var (
	runMu   sync.Mutex
	runLast string
	runN    int
)

// newRunID: Lauf-ID aus der Uhrzeit (Sekunden); in derselben Sekunde ein Zähler dahinter, damit Sicherungs-
// und Snapshot-Namen nie kollidieren (Anfragen sind seit dem Anmelde-Cache sehr schnell).
func newRunID() string {
	runMu.Lock()
	defer runMu.Unlock()
	id := time.Now().Format("20060102-150405")
	if id == runLast {
		runN++
		return id + "-" + strconv.Itoa(runN)
	}
	runLast, runN = id, 0
	return id
}
