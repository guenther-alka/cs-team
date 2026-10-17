package auth

import (
	"context"
	"time"
)

// "Zuletzt gesehen" (last_seen): jede angemeldete Anfrage merkt sich die Zeit im Speicher (a.seen); in users.json wird sie hoechstens
// einmal pro Stunde und Konto nachgetragen (Account.Seen), damit die Angabe einen Neustart ueberlebt, ohne bei jeder Anfrage zu schreiben.
const seenEvery = time.Hour

// touch merkt sich die Anmeldung des Kontos key und schreibt sie bei Bedarf (im Hintergrund) nach users.json.
func (a *Auth) touch(key string) {
	now := time.Now()
	a.mu.Lock()
	if a.seen == nil {
		a.seen = map[string]time.Time{}
	}
	a.seen[key] = now
	u, ok := a.users[key]
	st, _ := time.Parse(time.RFC3339, u.Seen)
	a.mu.Unlock()
	if !ok || u.Source == "dir" || now.Sub(st) < seenEvery { // Verzeichniskonten: DirSeen (letzte Pruefung) gilt, kein zusaetzlicher Schreibzugriff
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.mutate(ctx, func(m map[string]Account) error {
			if c, ok := m[key]; ok {
				if t, _ := time.Parse(time.RFC3339, c.Seen); now.Sub(t) >= seenEvery {
					c.Seen = now.UTC().Format(time.RFC3339)
					m[key] = c
				}
			}
			return nil
		})
	}()
}

// seenOf: spaeterer Wert aus Speicher und users.json (RFC 3339), leer = noch nie. Unter a.mu aufrufen.
func (a *Auth) seenOf(key string, u Account) string {
	s := u.Seen
	if u.Source == "dir" && u.DirSeen > s {
		s = u.DirSeen
	}
	if t, ok := a.seen[key]; ok {
		if p, err := time.Parse(time.RFC3339, s); err != nil || t.After(p) {
			s = t.UTC().Format(time.RFC3339)
		}
	}
	return s
}
