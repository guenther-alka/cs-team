// Package osacct (0.72): cs-team als Quelle fuer Betriebssystem-Konten, damit der cs-team-Rechner zugleich SMB-Server sein kann
// (Schalter CS_OSACCOUNTS=1, siehe docs/HA_SPEC.md Abschnitt 7 und 12).
//
//   - Mitglieder exportierter Gruppen bekommen ein lokales Konto (illumos: uid/gid aus cs-team, Windows: gleicher Name),
//     exportierte Gruppen eine lokale Gruppe. Angefasst werden nur Konten, die cs-team selbst angelegt hat (Kommentar "cs-team");
//     faellt ein Konto aus dem Export, wird es gesperrt, nie geloescht (sonst gingen uid/SID in den ACL verloren).
//   - Das SMB-/Windows-Passwort setzt cs-team im Klartextmoment (Anmeldung, Passwortwechsel, Startpasswort; auth.OnPlain).
//   - Der Master schreibt den Zustand dieses Rechners nach failover/osaccounts.json (mit repliziert): illumos Rechner-SID/-UUID
//     und SMB-Passwortzeilen der verwalteten Konten, Windows die SID je Konto/Gruppe.
//   - Adopt (Failover, Start auf einem anderen Rechner als dem letzten Master): illumos uebernimmt Rechner-SID und SMB-Passwoerter,
//     Windows schreibt die ACL der Freigaben von den SIDs des alten auf die des neuen Rechners um (icacls /substitute).
package osacct

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"cs-team/store"
)

const stateKey = "failover/osaccounts.json"

// Comment: Kennzeichen der von cs-team angelegten Konten und Gruppen.
const Comment = "cs-team"

type User struct {
	Name   string
	Uid    int
	Gid    int      // Hauptgruppe (alluser)
	Groups []string // exportierte Gruppen
}

type Group struct {
	Name string
	Gid  int
}

// Host: Zustand eines Rechners, vom jeweiligen Master geschrieben.
type Host struct {
	OS     string            `json:"os"`
	Time   string            `json:"time"`
	SID    string            `json:"sid,omitempty"`    // illumos: Rechner-SID (idmap config/machine_sid)
	UUID   string            `json:"uuid,omitempty"`   // illumos: idmap config/machine_uuid
	SMB    map[string]string `json:"smb,omitempty"`    // illumos: Zeile aus /var/smb/smbpasswd je verwaltetem Konto (NT-Hash)
	WinSID map[string]string `json:"winsid,omitempty"` // Windows: Name -> SID (Gruppen als "g:<name>")
}

type State struct {
	Users  map[string]int  `json:"users"`            // verwaltete Konten -> uid
	Groups map[string]int  `json:"groups"`           // verwaltete Gruppen -> gid
	Locked map[string]bool `json:"locked,omitempty"` // gesperrte Konten (nicht mehr im Export)
	Hosts  map[string]Host `json:"hosts"`
	Last   string          `json:"last,omitempty"` // Rechner, der zuletzt als Master geschrieben hat
}

func (s *State) fill() {
	if s.Users == nil {
		s.Users = map[string]int{}
	}
	if s.Groups == nil {
		s.Groups = map[string]int{}
	}
	if s.Locked == nil {
		s.Locked = map[string]bool{}
	}
	if s.Hosts == nil {
		s.Hosts = map[string]Host{}
	}
}

// Backend: je Betriebssystem (illumos.go, windows.go); nil = nicht unterstuetzt.
type Backend interface {
	OS() string
	ValidName(n string) bool
	EnsureGroup(g Group) error
	EnsureUser(u User, prim string) error
	SetPassword(name, pass string) error
	Lock(name string) error
	Unlock(name string) error
	Export(users, groups []string) (Host, error)
	Adopt(from, mine Host, users map[string]int, shares []string, dry bool, logf func(string, ...any)) error
}

type Mgr struct {
	St       store.Store
	B        Backend
	Host     string // Name dieses Rechners (CS_HA_NAME bzw. Rechnername)
	ReadOnly bool   // Standby: nichts schreiben, nichts anlegen
	Shares   []string
	Logf     func(string, ...any)
	// Src liefert Konten (nur Mitglieder exportierter Gruppen), exportierte Gruppen und die Hauptgruppe.
	Src func(ctx context.Context) ([]User, []Group, Group)

	mu      sync.Mutex
	scope   map[string]bool
	done    map[string][32]byte // zuletzt gesetztes Passwort je Konto (Abdruck, nur im Speicher)
	applied map[string]string   // zuletzt angewendete Gruppenliste je Konto
	foreign map[string]bool     // schon gemeldete Probleme (nur einmal ins Log)
	kick    chan struct{}
	pwq     chan [2]string // Passwoerter in Reihenfolge (ein Arbeiter)
	rmu     sync.Mutex     // ein Abgleich gleichzeitig
	miss    map[string]time.Time // letzter sofortiger Abgleich je unbekanntem Namen (setPw)
}

func (m *Mgr) logf(f string, a ...any) {
	if m.Logf != nil {
		m.Logf("osaccounts: "+f, a...)
	}
}

func (m *Mgr) init() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done == nil {
		m.scope, m.done, m.applied, m.foreign = map[string]bool{}, map[string][32]byte{}, map[string]string{}, map[string]bool{}
		m.miss = map[string]time.Time{}
		m.kick = make(chan struct{}, 1)
		m.pwq = make(chan [2]string, 1024)
		go func() {
			for p := range m.pwq {
				m.setPw(p[0], p[1])
			}
		}()
	}
}

func (m *Mgr) LoadState(ctx context.Context) (State, error) {
	var s State
	b, _, err := m.St.Get(ctx, stateKey)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return s, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s); err != nil {
			return s, err
		}
	}
	s.fill()
	return s, nil
}

func (m *Mgr) saveState(ctx context.Context, fn func(*State)) error {
	return store.Update(ctx, m.St, stateKey, func(cur []byte) ([]byte, error) {
		var s State
		if len(cur) > 0 {
			if err := json.Unmarshal(cur, &s); err != nil {
				return nil, err
			}
		}
		s.fill()
		fn(&s)
		return json.MarshalIndent(s, "", " ")
	})
}

// Reconcile gleicht Konten und Gruppen ab und schreibt den Zustand (nur Master).
func (m *Mgr) Reconcile(ctx context.Context) error {
	m.init()
	if m.ReadOnly {
		return nil
	}
	m.rmu.Lock()
	defer m.rmu.Unlock()
	users, groups, prim := m.Src(ctx)
	st, err := m.LoadState(ctx)
	if err != nil {
		return err
	}
	okGroups := map[string]bool{}
	for _, g := range append([]Group{prim}, groups...) {
		if g.Name == "" || g.Gid <= 0 || !m.B.ValidName(g.Name) {
			continue
		}
		if err := m.B.EnsureGroup(g); err != nil {
			m.once("g:"+g.Name+err.Error(), "group %s: %v", g.Name, err)
			continue
		}
		okGroups[g.Name] = true
		st.Groups[g.Name] = g.Gid
	}
	scope := map[string]bool{}
	for _, u := range users {
		if !m.B.ValidName(u.Name) || u.Uid <= 0 {
			m.once("n:"+u.Name, "user %s: name not usable on %s, skipped", u.Name, m.B.OS())
			continue
		}
		var gs []string
		for _, g := range u.Groups {
			if okGroups[g] {
				gs = append(gs, g)
			}
		}
		sort.Strings(gs)
		u.Groups = gs
		key := strings.Join(gs, ",")
		m.mu.Lock()
		same := m.applied[u.Name] == key
		m.mu.Unlock()
		if !same || st.Locked[u.Name] || st.Users[u.Name] == 0 {
			if err := m.B.EnsureUser(u, prim.Name); err != nil {
				m.once("u:"+u.Name+err.Error(), "user %s: %v", u.Name, err)
				continue
			}
			if st.Locked[u.Name] {
				if err := m.B.Unlock(u.Name); err != nil {
					m.logf("unlock %s: %v", u.Name, err)
				}
				delete(st.Locked, u.Name)
				m.logf("user %s unlocked", u.Name)
			}
			m.mu.Lock()
			m.applied[u.Name] = key
			m.mu.Unlock()
		}
		scope[u.Name] = true
		st.Users[u.Name] = u.Uid
	}
	for n := range st.Users {
		if !scope[n] && !st.Locked[n] {
			if err := m.B.Lock(n); err != nil {
				m.logf("lock %s: %v", n, err)
				continue
			}
			st.Locked[n] = true
			m.logf("user %s locked (disabled or deleted in cs-team, or no longer in an exported group)", n)
		}
	}
	m.mu.Lock()
	m.scope = scope
	m.mu.Unlock()
	return m.export(ctx, st)
}

func names(st State) (us, gs []string) {
	for n := range st.Users {
		us = append(us, n)
	}
	for n := range st.Groups {
		gs = append(gs, n)
	}
	sort.Strings(us)
	sort.Strings(gs)
	return
}

// export schreibt den Zustand dieses Rechners, wenn er sich geaendert hat.
func (m *Mgr) export(ctx context.Context, st State) error {
	us, gs := names(st)
	h, err := m.B.Export(us, gs)
	if err != nil {
		return err
	}
	cur, _ := m.LoadState(ctx)
	old := cur.Hosts[m.Host]
	old.Time, h.Time = "", ""
	ob, _ := json.Marshal(old)
	nb, _ := json.Marshal(h)
	if string(ob) == string(nb) && cur.Last == m.Host && sameMap(cur.Users, st.Users) && sameMap(cur.Groups, st.Groups) && sameBool(cur.Locked, st.Locked) {
		return nil
	}
	h.Time = time.Now().UTC().Format(time.RFC3339)
	return m.saveState(ctx, func(s *State) {
		s.Users, s.Groups, s.Locked = st.Users, st.Groups, st.Locked
		s.Hosts[m.Host] = h
		s.Last = m.Host
	})
}

// Plain: Klartext-Passwort aus auth.OnPlain; nur einreihen (der Aufrufer ist eine laufende Anfrage), Reihenfolge bleibt erhalten.
func (m *Mgr) Plain(name, pass string) {
	m.init()
	if m.ReadOnly {
		return
	}
	select {
	case m.pwq <- [2]string{name, pass}:
	default:
		m.logf("password queue full, %s skipped (set again at the next sign-in)", name)
	}
}

// Flush wartet, bis die Warteschlange abgearbeitet ist (Tests, Unterbefehle).
func (m *Mgr) Flush() {
	m.init()
	for i := 0; i < 200 && len(m.pwq) > 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
}

func (m *Mgr) setPw(name, pass string) {
	m.mu.Lock()
	in := m.scope[name]
	fp := sha256.Sum256([]byte(name + "\x00" + pass))
	same := m.done[name] == fp
	retry := !in && time.Since(m.miss[name]) > 30*time.Second
	if retry {
		if len(m.miss) > 10000 {
			m.miss = map[string]time.Time{}
		}
		m.miss[name] = time.Now()
	}
	m.mu.Unlock()
	if retry { // z.B. gerade angelegt oder einer exportierten Gruppe zugeordnet: einmal sofort abgleichen
		if err := m.Reconcile(context.Background()); err != nil {
			m.logf("%v", err)
		}
		m.mu.Lock()
		in = m.scope[name]
		m.mu.Unlock()
	}
	if !in || same {
		return
	}
	if err := m.B.SetPassword(name, pass); err != nil {
		m.logf("password %s: %v", name, err)
		return
	}
	m.mu.Lock()
	m.done[name] = fp
	m.mu.Unlock()
	m.logf("password of %s set", name)
	m.Kick()
}

// Kick: baldiger Abgleich (z.B. nach Passwort oder Gruppenaenderung).
func (m *Mgr) Kick() {
	m.init()
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run: Abgleich sofort, danach alle every und nach Kick.
func (m *Mgr) Run(ctx context.Context, every time.Duration) {
	m.init()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			if err := m.Reconcile(ctx); err != nil {
				m.logf("%v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-m.kick:
				time.Sleep(2 * time.Second) // mehrere Ereignisse zusammenfassen
			}
		}
	}()
}

// NeedAdopt: der Zustand stammt von einem anderen Rechner (Failover oder Umzug).
func (m *Mgr) NeedAdopt(ctx context.Context) (string, bool) {
	st, err := m.LoadState(ctx)
	if err != nil || st.Last == "" || st.Last == m.Host {
		return st.Last, false
	}
	_, ok := st.Hosts[st.Last]
	return st.Last, ok
}

// Adopt: uebernimmt den Zustand des letzten Masters auf diesen Rechner: Konten anlegen, Backend.Adopt, dann den eigenen
// Zustand schreiben (Last = dieser Rechner). dry = nur anzeigen, nichts aendern.
func (m *Mgr) Adopt(ctx context.Context, dry bool) error {
	m.init()
	st, err := m.LoadState(ctx)
	if err != nil {
		return err
	}
	if st.Last == "" || st.Last == m.Host {
		m.logf("adopt: nothing to do (last master %q, this host %q)", st.Last, m.Host)
		return nil
	}
	from, ok := st.Hosts[st.Last]
	if !ok {
		return fmt.Errorf("adopt: no data of %s", st.Last)
	}
	if from.OS != m.B.OS() {
		return fmt.Errorf("adopt: last master %s ran %s, this host is %s (only the same operating system)", st.Last, from.OS, m.B.OS())
	}
	m.logf("adopt from %s (%s, written %s), dry=%v", st.Last, from.OS, from.Time, dry)
	if !dry {
		users, groups, prim := m.Src(ctx)
		for _, g := range append([]Group{prim}, groups...) {
			if g.Gid > 0 && m.B.ValidName(g.Name) {
				if err := m.B.EnsureGroup(g); err != nil {
					m.logf("adopt group %s: %v", g.Name, err)
				}
			}
		}
		for _, u := range users {
			if m.B.ValidName(u.Name) && u.Uid > 0 {
				if err := m.B.EnsureUser(u, prim.Name); err != nil {
					m.logf("adopt user %s: %v", u.Name, err)
				}
			}
		}
	}
	us, gs := names(st)
	mine, err := m.B.Export(us, gs)
	if err != nil {
		return err
	}
	if err := m.B.Adopt(from, mine, st.Users, m.Shares, dry, m.logf); err != nil {
		return err
	}
	if dry {
		return nil
	}
	for n := range st.Locked {
		_ = m.B.Lock(n)
	}
	m.logf("adopt from %s done", st.Last)
	return m.Reconcile(ctx)
}

func (m *Mgr) once(key, f string, a ...any) {
	m.mu.Lock()
	seen := m.foreign[key]
	m.foreign[key] = true
	m.mu.Unlock()
	if !seen {
		m.logf(f, a...)
	}
}

func sameMap(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func sameBool(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// SplitShares: CS_OSACCOUNTS_SHARES, Pfade getrennt durch ";".
func SplitShares(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
