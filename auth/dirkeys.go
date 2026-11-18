package auth

// Konten umbenennen und alte Spiegelkonten umstellen (0.72).
//
// Bis 0.71 hiessen Spiegelkonten von Verzeichnisbenutzern "anna@local.de". Ab 0.72 heisst das Konto "anna", der
// Namensraum steht nur noch als Merkmal in Account.Realm (identity.go). MigrateDirKeys stellt beim Start alle alten
// Schluessel um, wenn der Name ohne Namensraum frei ist; ist er von einem lokalen Konto belegt, bleibt das alte Konto
// unveraendert und anmeldefaehig (Protokollzeile), bis ein Admin das lokale Konto umbenennt oder entfernt.
//
// RenameUser zieht alle Daten des Namens mit: zuerst die Module (Dateien, Dokumente, Kalender; dann die Namen in Chat,
// Aufgaben, Terminen und Aushaengen ueber die AnonHooks), danach Gruppen-Admin- und Wiederholer-Listen, zuletzt das Konto
// selbst (mit uid, Gruppen, 2FA, App-Passwoertern). Jeder Schritt kopiert erst und loescht dann; bricht ein Lauf ab,
// setzt der naechste Start dort fort (bereits umgestellte Teile werden nicht noch einmal gefunden).
// Verlaufs-Protokolle (Loesch-, Routine-, Chat-Auswertungsprotokoll, Aenderungsverlauf der Dokumente) bleiben wie sie sind.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
)

// UserRenameHook: ein Modul zieht die Daten eines Kontos auf den neuen Namen um (persoenliche Dateien, Kalender,
// Besitz und Freigaben). Muss wiederholbar sein: ein zweiter Aufruf mit denselben Namen aendert nichts mehr.
type UserRenameHook struct {
	Name   string
	Rename func(ctx context.Context, from, to string) error
}

var UserRenameHooks []UserRenameHook

var ErrNameTaken = errors.New("the new name is already used")

// RenameUser: Konto from heisst danach to; alle Daten ziehen mit (siehe Kopfkommentar).
func (a *Auth) RenameUser(ctx context.Context, from, to string) error {
	if from == to {
		return nil
	}
	if !okName(to) {
		return ErrBadName
	}
	if _, ok := a.get(ctx, from); !ok {
		return ErrNoUser
	}
	if _, ok := a.get(ctx, to); ok {
		return ErrNameTaken
	}
	a.dropSessions(from) // offene Sitzungen des alten Namens enden (sie wuerden auf ein leeres Konto zeigen)
	for _, h := range UserRenameHooks {
		if err := h.Rename(ctx, from, to); err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
	}
	for _, h := range AnonHooks { // Namen in Inhalten, die der Gruppe gehoeren: Nachrichten, Aufgaben, Termine, Aushaenge
		if h.Anon == nil {
			continue
		}
		if _, err := h.Anon(ctx, from, to); err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
	}
	if err := a.mutateGroups(ctx, func(m map[string]Group) error {
		for n, g := range m {
			a1, c1 := swapName(g.Admins, from, to)
			s1, c2 := swapName(g.Stay, from, to)
			if c1 || c2 {
				g.Admins, g.Stay = a1, s1
				m[n] = g
			}
		}
		return nil
	}); err != nil {
		return err
	}
	err := a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[from]
		if !ok {
			return ErrNoUser
		}
		if _, ok := m[to]; ok {
			return ErrNameTaken
		}
		delete(m, from)
		m[to] = u // uid, Gruppen, 2FA, App-Passwoerter und Merkmal Realm bleiben
		return nil
	})
	if err != nil {
		return err
	}
	a.dropSessions(from)
	a.invalidate()
	return nil
}

// swapName: from -> to in einer Namensliste (doppelte Eintraege entfallen).
func swapName(l []string, from, to string) ([]string, bool) {
	var out []string
	ch := false
	seen := map[string]bool{}
	for _, x := range l {
		if x == from {
			x, ch = to, true
		}
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out, ch
}

// DirKeyResult: Ergebnis der Umstellung alter Spiegelkonten.
type DirKeyResult struct {
	Renamed []string // "anna@local.de -> anna"
	Kept    []string // Name ohne Namensraum belegt: bleibt unter dem alten Schluessel
	Failed  []string
}

// legacyDirKeys: alte Spiegelkonten "name@realm" (sortiert).
func (a *Auth) legacyDirKeys(ctx context.Context) []string {
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for n, u := range a.users {
		if u.Source == "dir" && strings.Contains(n, "@") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// MigrateDirKeys stellt alte Spiegelkonten "name@realm" auf "name" um (beim Start, bevor Anfragen bedient werden).
// Vorher wird ein Snapshot angelegt, wenn einer moeglich ist; scheitert er, bleibt alles unveraendert.
func (a *Auth) MigrateDirKeys(ctx context.Context) DirKeyResult {
	var res DirKeyResult
	var keys []string
	for _, k := range a.legacyDirKeys(ctx) {
		base, _, _ := strings.Cut(k, "@")
		if SysName(base) {
			log.Printf("identity: mirror account %s kept under its old name: %s is the name of a system account", k, base)
			res.Kept = append(res.Kept, k)
			continue
		}
		if o, taken := a.get(ctx, base); taken {
			why := "a local account"
			if o.Source == "dir" {
				why = "a mirror account of " + o.Realm
			}
			log.Printf("identity: mirror account %s kept under its old name: %s is %s", k, base, why)
			res.Kept = append(res.Kept, k)
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 { // nichts umzustellen (oder nur belegte Namen): kein Snapshot
		return res
	}
	if SnapshotMode() != "none" {
		id := newRunID()
		if err := runSnapshot("dirkeys-" + id); err != nil {
			log.Printf("identity: %d old mirror accounts (name@realm) not converted: snapshot failed: %v", len(keys), err)
			return res
		}
		log.Printf("identity: snapshot cs-team-dirkeys-%s before converting %d old mirror accounts", id, len(keys))
	} else {
		log.Printf("identity: converting %d old mirror accounts without snapshot (no snapshot possible)", len(keys))
	}
	for _, k := range keys {
		base, realm, _ := strings.Cut(k, "@")
		u, ok := a.get(ctx, k)
		if !ok {
			continue
		}
		if _, taken := a.get(ctx, base); taken { // inzwischen belegt
			res.Kept = append(res.Kept, k)
			continue
		}
		if u.Realm == "" { // sehr alte Spiegelkonten ohne Merkmal: der Namensraum steht im Schluessel
			a.mutate(ctx, func(m map[string]Account) error {
				if x, ok := m[k]; ok && x.Realm == "" {
					x.Realm = realm
					m[k] = x
				}
				return nil
			})
		}
		if err := a.RenameUser(ctx, k, base); err != nil {
			log.Printf("identity: mirror account %s could not be renamed to %s: %v (old name stays valid)", k, base, err)
			res.Failed = append(res.Failed, k)
			continue
		}
		log.Printf("audit: mirror account %s renamed to %s (realm %s stays as marker)", k, base, realm)
		res.Renamed = append(res.Renamed, k+" -> "+base)
	}
	return res
}
