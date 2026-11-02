// Package ha: Ausfallschutz (HA-Gruppe) mit Master und Standby, siehe docs/HA_SPEC.md.
// Der Modus kommt nur aus der Startoption CS_FAILOVER (master | standby | takeover), nicht aus den Einstellungen.
// Die ZFS-Replikation Master -> Standby ist ein eigener, von Hand angelegter Auftrag; cs-team legt keinen an.
package ha

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cs-team/store"
)

const (
	groupKey = "failover/group.json"
	stateKey = "failover/state.json"
)

type Member struct {
	Name  string `json:"name"`
	Addr  string `json:"addr"`            // host:port, mit oder ohne http(s)://
	Iface string `json:"iface,omitempty"` // Netzwerkschnittstelle fuer die Dienst-IP
}

// Group: gleich auf allen Hosts (liegt in den replizierten Daten).
type Group struct {
	Members []Member `json:"members"`
	Service string   `json:"service,omitempty"` // Dienstname fuer die Clients
	VIP     string   `json:"vip,omitempty"`     // Dienst-IP (virtuelle Adresse)
}

type State struct {
	Epoch  int       `json:"epoch"`
	Master string    `json:"master"`
	Since  time.Time `json:"since"`
}

// Status: das, was /ha/status oeffentlich meldet.
type Status struct {
	Name     string `json:"name"`
	Mode     string `json:"mode"` // off | master | standby
	Epoch    int    `json:"epoch"`
	Conflict string `json:"conflict,omitempty"`
}

type Svc struct {
	Mode   string // "", master, standby
	Name   string
	Force  bool // CS_HA_FORCE=1: Pruefungen uebergehen
	Assume bool // CS_HA_ASSUME_OFF=1: nicht erreichbare Mitglieder gelten als aus

	St store.Store // Speicher (im Standby der schreibgeschuetzte)

	mu       sync.Mutex
	epoch    int
	conflict string
	client   *http.Client
}

func New(st store.Store, mode, name string, force, assume bool) *Svc {
	return &Svc{St: st, Mode: mode, Name: name, Force: force, Assume: assume,
		client: &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
}

// ParseMode: aus CS_FAILOVER; takeover wird beim Start zu master (mit neuer Epoche).
func ParseMode(s string) (mode string, takeover bool, err error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return "", false, nil
	case "master":
		return "master", false, nil
	case "standby":
		return "standby", false, nil
	case "takeover":
		return "master", true, nil
	}
	return "", false, fmt.Errorf("CS_FAILOVER=%q: erlaubt sind master, standby, takeover", s)
}

func (s *Svc) Enabled() bool { return s.Mode != "" }

func (s *Svc) LoadGroup(ctx context.Context) Group {
	var g Group
	if b, _, err := s.St.Get(ctx, groupKey); err == nil {
		_ = json.Unmarshal(b, &g)
	}
	return g
}

func (s *Svc) loadState(ctx context.Context) State {
	var t State
	if b, _, err := s.St.Get(ctx, stateKey); err == nil {
		_ = json.Unmarshal(b, &t)
	}
	return t
}

var ErrNotMaster = errors.New("not master")

func (s *Svc) SaveGroup(ctx context.Context, g Group) error {
	if s.Mode != "master" {
		return ErrNotMaster
	}
	seen := map[string]bool{}
	var out []Member
	for _, m := range g.Members {
		m.Name, m.Addr, m.Iface = strings.TrimSpace(m.Name), strings.TrimSpace(m.Addr), strings.TrimSpace(m.Iface)
		if m.Name == "" || m.Addr == "" {
			continue
		}
		if seen[m.Name] {
			return fmt.Errorf("duplicate member %q", m.Name)
		}
		seen[m.Name] = true
		out = append(out, m)
	}
	g.Members = out
	g.Service, g.VIP = strings.TrimSpace(g.Service), strings.TrimSpace(g.VIP)
	b, _ := json.MarshalIndent(g, "", " ")
	_, err := s.St.Put(ctx, groupKey, b, "")
	return err
}

func (s *Svc) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.Mode
	if m == "" {
		m = "off"
	}
	return Status{Name: s.Name, Mode: m, Epoch: s.epoch, Conflict: s.conflict}
}

// probe fragt ein Mitglied; ok=false heisst nicht erreichbar.
func (s *Svc) probe(addr string) (st Status, ok bool) {
	urls := []string{addr}
	if !strings.Contains(addr, "://") {
		urls = []string{"https://" + addr, "http://" + addr}
	}
	for _, u := range urls {
		resp, err := s.client.Get(strings.TrimRight(u, "/") + "/ha/status")
		if err != nil {
			continue
		}
		err = json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if err == nil && resp.StatusCode == 200 {
			return st, true
		}
	}
	return st, false
}

func (s *Svc) others(g Group) []Member {
	var o []Member
	for _, m := range g.Members {
		if m.Name != s.Name {
			o = append(o, m)
		}
	}
	return o
}

// Start: Startpruefungen (HA_SPEC.md, Abschnitt 4). Fehler = cs-team startet nicht.
func (s *Svc) Start(ctx context.Context, takeover bool) error {
	if s.Mode == "" {
		return nil
	}
	if s.Name == "" {
		return errors.New("CS_HA_NAME fehlt (Name dieses Hosts in der HA-Gruppe)")
	}
	if s.Mode == "standby" {
		s.epoch = s.loadState(ctx).Epoch
		return nil
	}
	g := s.LoadGroup(ctx)
	old := s.loadState(ctx)
	if !s.Force {
		for _, m := range s.others(g) {
			st, ok := s.probe(m.Addr)
			switch {
			case ok && st.Mode == "master":
				return fmt.Errorf("%s ist bereits Master (Epoche %d)", m.Name, st.Epoch)
			case ok && st.Epoch > old.Epoch:
				return fmt.Errorf("%s hat die neuere Epoche %d (hier %d): zuerst Daten von dort replizieren", m.Name, st.Epoch, old.Epoch)
			case !ok && takeover && !s.Assume:
				return fmt.Errorf("%s nicht erreichbar: ist es sicher aus? Dann mit CS_HA_ASSUME_OFF=1 starten", m.Name)
			}
		}
		if old.Master != "" && old.Master != s.Name && !takeover {
			return fmt.Errorf("letzter Master war %s: mit CS_FAILOVER=takeover uebernehmen", old.Master)
		}
	}
	ep := old.Epoch
	if ep == 0 || (takeover && old.Master != s.Name) {
		ep++
	}
	b, _ := json.Marshal(State{Epoch: ep, Master: s.Name, Since: time.Now().UTC()})
	if _, err := s.St.Put(ctx, stateKey, b, ""); err != nil {
		return err
	}
	s.epoch = ep
	return nil
}

// Watch: Master prueft regelmaessig, ob ein anderer Master mit gleicher oder neuerer Epoche aufgetaucht ist (dann sperrt er das Schreiben).
func (s *Svc) Watch(ctx context.Context, every time.Duration) {
	if s.Mode != "master" {
		return
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			c := ""
			for _, m := range s.others(s.LoadGroup(ctx)) {
				if st, ok := s.probe(m.Addr); ok && st.Mode == "master" && st.Epoch >= s.epoch {
					c = fmt.Sprintf("%s meldet sich als Master (Epoche %d)", m.Name, st.Epoch)
				}
			}
			s.mu.Lock()
			if c != s.conflict {
				log.Println("ha: conflict:", c)
			}
			s.conflict = c
			s.mu.Unlock()
		}
	}()
}

func (s *Svc) readOnly() bool {
	if s.Mode == "standby" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conflict != ""
}

// Guard: im Standby (oder bei Konflikt) nur lesende Anfragen.
func (s *Svc) Guard(next http.Handler) http.Handler {
	if !s.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET", "HEAD", "OPTIONS", "PROPFIND", "REPORT":
		default:
			if s.readOnly() && !strings.HasPrefix(r.URL.Path, "/api/login") && !strings.HasPrefix(r.URL.Path, "/api/logout") {
				w.Header().Set("Retry-After", "30")
				http.Error(w, "standby: read only", http.StatusServiceUnavailable)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Routes: /ha/status (oeffentlich, fuer die anderen Mitglieder) und /api/ha (Admin).
func (s *Svc) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler, isAdmin func(*http.Request) bool) {
	mux.HandleFunc("GET /ha/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.Status())
	})
	mux.Handle("GET /api/ha", wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		g := s.LoadGroup(r.Context())
		type row struct {
			Member
			Mode  string `json:"mode"` // master | standby | off | unreachable
			Epoch int    `json:"epoch"`
			Self  bool   `json:"self,omitempty"`
		}
		var rows []row
		for _, m := range g.Members {
			rw := row{Member: m, Self: m.Name == s.Name}
			if rw.Self {
				st := s.Status()
				rw.Mode, rw.Epoch = st.Mode, st.Epoch
			} else if st, ok := s.probe(m.Addr); ok {
				rw.Mode, rw.Epoch = st.Mode, st.Epoch
			} else {
				rw.Mode = "unreachable"
			}
			rows = append(rows, rw)
		}
		st := s.loadState(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": s.Status(), "group": g, "members": rows, "master": st.Master, "since": st.Since})
	})))
	mux.Handle("POST /api/ha/group", wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		var g Group
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&g); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if err := s.SaveGroup(r.Context(), g); err != nil {
			code := 400
			if errors.Is(err, ErrNotMaster) {
				code = 409
			}
			http.Error(w, err.Error(), code)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})))
}
