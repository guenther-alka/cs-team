package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Jahrgangswechsel im Modus "rename": die Gruppe (mit Mitgliedern, Gruppenordner, Freigaben und - wählbar - Kalender, Chat, Aufgaben)
// bekommt den Namen der Folgestufe. Die Namen der Stufen werden von oben nach unten umgesetzt (7a -> Abgang, 6a -> 7a, 5a -> 6a),
// darunter entsteht eine neue, leere Gruppe 5a. Wiederholer wechseln aus der mitwandernden Gruppe in die Gruppe der Stufe.

// RenameHook: Umbenennen der Daten eines Moduls. Area "" = immer, sonst "cal" | "chat" | "tasks" (nur wenn mitgenommen).
type RenameHook struct {
	Name   string
	Area   string
	Rename func(ctx context.Context, from, to string) error
	Used   func(ctx context.Context, g string) bool // optional: enthält die Gruppe Daten? (Rückgängig nur bei leerer neuer Gruppe)
	Drop   func(ctx context.Context, g string)      // optional: leere Reste der neuen Gruppe entfernen
}

var (
	RenameHooks  []RenameHook
	GroupCalMode func(ctx context.Context, g string) string // "" = kein Gruppenkalender
	SnapshotCmd  string                                     // Befehl vor jedem Lauf (nur über Startparameter), {id} = Lauf-ID
	SnapshotArgv []string                                   // automatisch erkannt: Programm + Argumente ({id} wird ersetzt), ohne Shell
	SnapshotInfo string                                     // Dataset (Anzeige)
)

// SnapshotMode: "cmd" (eigener Befehl), "auto" (ZFS-Dataset erkannt) oder "none".
func SnapshotMode() string {
	switch {
	case len(SnapshotArgv) > 0:
		return "auto"
	case SnapshotCmd != "":
		return "cmd"
	}
	return "none"
}

func runSnapshot(id string) error {
	var cmd *exec.Cmd
	switch {
	case len(SnapshotArgv) > 0:
		args := make([]string, len(SnapshotArgv))
		for i, x := range SnapshotArgv {
			args[i] = strings.ReplaceAll(x, "{id}", id)
		}
		cmd = exec.Command(args[0], args[1:]...)
	case SnapshotCmd != "":
		c := strings.ReplaceAll(SnapshotCmd, "{id}", id)
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/C", c)
		} else {
			cmd = exec.Command("sh", "-c", c)
		}
	default:
		return nil
	}
	done := make(chan error, 1)
	var out []byte
	go func() { var err error; out, err = cmd.CombinedOutput(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	case <-time.After(120 * time.Second):
		cmd.Process.Kill()
		return errors.New("timeout")
	}
}

func (p YearParams) archiveName(g string) string {
	pat := p.Pattern
	if pat == "" {
		pat = "ehem-{name}-{year}"
	}
	return strings.NewReplacer("{name}", g, "{year}", strconv.Itoa(time.Now().Year())).Replace(pat)
}

func (a *Auth) planRename(ctx context.Context, p YearParams) (*Plan, error) {
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch p.Leave {
	case "", "keep", "archive":
	default:
		return nil, errors.New("rename mode: leave = keep | archive")
	}
	if p.Leave == "" {
		p.Leave = "keep"
	}
	pl := &Plan{Routine: "yearchange", Mode: "rename", Leave: p.Leave, Carry: p.Carry, effects: map[string]effect{}, newRec: map[string]Group{}}
	sel := map[string]bool{}
	for _, g := range p.Groups {
		sel[g] = true
	}
	var names []string
	for n := range a.groups {
		if n == DefaultGroup || reNum.FindStringSubmatch(n) == nil || (len(sel) > 0 && !sel[n]) {
			continue
		}
		if n == "f" {
			continue
		}
		names = append(names, n)
	}
	num := func(n string) int { _, v, _ := sortKey(n); return v }
	sort.Slice(names, func(i, j int) bool {
		if num(names[i]) != num(names[j]) {
			return num(names[i]) > num(names[j])
		}
		return names[i] < names[j]
	})
	renamed := map[string]string{}
	targets := map[string]bool{}
	var groups []PlanGroup
	for _, n := range names {
		g := a.groups[n]
		to, _ := NextName(n)
		_, toExists := a.groups[to]
		pg := PlanGroup{Group: n, To: to, Exists: toExists, Kind: "skip"}
		pair := Pair{From: n}
		switch {
		case toExists:
			if _, moved := renamed[to]; moved {
				pg.Kind, pair.To = "rename", to
			} else {
				pg.Kind, pg.To = "blocked", ""
				pg.Note = "target group " + to + " stays, so " + n + " cannot be renamed"
			}
		case p.Leave == "archive":
			an := p.archiveName(n)
			_, ex := a.groups[an]
			if !validName.MatchString(an) || ex || targets[an] {
				pg.Kind, pg.To = "blocked", ""
				pg.Note = "archive name " + an + " is invalid or taken"
			} else {
				pg.Kind, pair.To, pair.Archive, pg.To = "rename", an, true, an
			}
		default:
			pg.To = ""
		}
		var members []string
		for un, u := range a.users {
			for _, x := range effGroups(u) {
				if x == n {
					members = append(members, un)
				}
			}
		}
		sort.Strings(members)
		pg.Total = len(members)
		if pg.Kind != "rename" {
			groups = append(groups, pg)
			continue
		}
		renamed[n] = pair.To
		targets[pair.To] = true
		pl.Pairs = append(pl.Pairs, pair)
		src := g.Stay
		_, own := p.Stay[n]
		if own {
			src = p.Stay[n]
		}
		isMember := map[string]bool{}
		for _, m := range members {
			isMember[m] = true
		}
		stay := map[string]bool{}
		for _, s := range src {
			if isMember[s] {
				stay[s] = true
			} else if own {
				pl.Warnings = append(pl.Warnings, n+": "+s+" is not a member (ignored)")
			}
		}
		for _, m := range members {
			u := a.users[m]
			it := PlanItem{User: m, Stay: stay[m]}
			if u.Disabled {
				it.Note = "disabled"
			}
			pg.Items = append(pg.Items, it)
			if it.Stay {
				pg.Stay++
				pl.Repeat++
				e := pl.effects[m]
				if e.remove == nil {
					e = effect{remove: map[string]bool{}, add: map[string]bool{}}
				}
				e.remove[pair.To], e.add[n] = true, true
				pl.effects[m] = e
			} else {
				pg.Move++
			}
		}
		groups = append(groups, pg)
	}
	for _, pr := range pl.Pairs { // neue Gruppen: Namen, die keine umbenannte Gruppe übernimmt
		if !targets[pr.From] {
			g := a.groups[pr.From]
			n := Group{Areas: append([]string{}, g.Areas...), Read: append([]string{}, g.Read...), Chat: g.Chat, Msg: g.Msg, Chans: g.Chans,
				Tasks: g.Tasks, Units: append([]string{}, g.Units...), Folder: g.Folder}
			if p.NewAdmins {
				n.Admins = append([]string{}, g.Admins...)
			}
			pl.newRec[pr.From] = n
			pl.Create = append(pl.Create, pr.From)
		}
	}
	sort.Strings(pl.Create)
	// Anzeige: aufsteigend nach Stufe wie im Modus "members"
	for i, j := 0, len(groups)-1; i < j; i, j = i+1, j-1 {
		groups[i], groups[j] = groups[j], groups[i]
	}
	pl.Groups = groups
	pl.Moves = len(pl.Pairs)
	if len(pl.Groups) == 0 {
		pl.Warnings = append(pl.Warnings, "no groups with a number in the name found")
	}
	b, _ := json.Marshal(pl)
	h := sha256.Sum256(b)
	pl.Hash = hex.EncodeToString(h[:8])
	return pl, nil
}

// renameRecord: Gruppeneintrag from -> to (Admins, Modi, Merkliste wandern mit); optional auch die Mitgliedschaften der Konten.
func (a *Auth) renameRecord(ctx context.Context, from, to string, accounts bool) error {
	if err := a.mutateGroups(ctx, func(m map[string]Group) error {
		g, ok := m[from]
		if !ok {
			return ErrNoGroup
		}
		if _, ex := m[to]; ex {
			return fmt.Errorf("group %s exists", to)
		}
		delete(m, from)
		m[to] = g
		return nil
	}); err != nil {
		return err
	}
	if !accounts {
		return nil
	}
	return a.mutate(ctx, func(m map[string]Account) error {
		for n, u := range m {
			ch := false
			var out []string
			for _, g := range u.Groups {
				if g == from {
					g, ch = to, true
				}
				out = append(out, g)
			}
			if ch {
				sort.Strings(out)
				u.Groups = out
				m[n] = u
			}
		}
		return nil
	})
}

func carried(h RenameHook, c Carry) bool {
	switch h.Area {
	case "":
		return true
	case "cal":
		return c.Cal
	case "chat":
		return c.Chat
	case "tasks":
		return c.Tasks
	}
	return false
}

// runHooks: alle Module umbenennen; bei einem Fehler werden die bereits erledigten zurückgesetzt.
func runHooks(ctx context.Context, from, to string, c Carry) error {
	var done []RenameHook
	for _, h := range RenameHooks {
		if !carried(h, c) {
			continue
		}
		if err := h.Rename(ctx, from, to); err != nil {
			for i := len(done) - 1; i >= 0; i-- {
				done[i].Rename(ctx, to, from)
			}
			return fmt.Errorf("%s: %w", h.Name, err)
		}
		done = append(done, h)
	}
	return nil
}

func (a *Auth) applyRename(ctx context.Context, admin string, pl *Plan, id string) (*LogEntry, error) {
	a.refresh(ctx)
	bk := backup{Mode: "rename", Users: map[string]userState{}, Stay: map[string][]string{}, Created: pl.Create}
	a.mu.Lock()
	for _, pr := range pl.Pairs {
		bk.Stay[pr.From] = append([]string{}, a.groups[pr.From].Stay...)
		for un, u := range a.users {
			for _, x := range effGroups(u) {
				if x == pr.From {
					bk.Users[un] = userState{Groups: append([]string{}, u.Groups...), Disabled: u.Disabled}
				}
			}
		}
	}
	a.mu.Unlock()
	for k, name := range map[string]string{usersKey: "users", groupsKey: "groups"} { // Rohkopien für den Notfall
		if raw, _, err := a.st.Get(ctx, k); err == nil {
			a.st.Put(ctx, "users/routine-"+id+"-"+name+".json", raw, "*")
		}
	}
	calMode := map[string]string{}
	if pl.Carry.Cal && GroupCalMode != nil {
		for _, n := range pl.Create {
			calMode[n] = GroupCalMode(ctx, n)
		}
	}
	b, _ := json.Marshal(bk)
	if _, err := a.st.Put(ctx, "users/routine-"+id+".json", b, "*"); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	en := LogEntry{ID: id, Time: time.Now(), Admin: admin, Routine: pl.Routine, Mode: "rename", Carry: pl.Carry, Created: pl.Create}
	var runErr error
	for _, pr := range pl.Pairs {
		if err := runHooks(ctx, pr.From, pr.To, pl.Carry); err != nil {
			runErr = fmt.Errorf("%s -> %s: %w", pr.From, pr.To, err)
			break
		}
		if err := a.renameRecord(ctx, pr.From, pr.To, true); err != nil {
			for i := len(RenameHooks) - 1; i >= 0; i-- {
				if carried(RenameHooks[i], pl.Carry) {
					RenameHooks[i].Rename(ctx, pr.To, pr.From)
				}
			}
			runErr = fmt.Errorf("%s -> %s: %w", pr.From, pr.To, err)
			break
		}
		en.Pairs = append(en.Pairs, pr)
	}
	if runErr == nil {
		runErr = a.finishRename(ctx, pl, calMode)
	} else {
		en.Created = nil
		en.Partial = true
	}
	en.Moved = len(en.Pairs)
	en.Summary = fmt.Sprintf("rename: %d groups renamed, %d repeaters, %d new groups", len(en.Pairs), pl.Repeat, len(en.Created))
	if err := a.appendLog(ctx, en); err != nil && runErr == nil {
		runErr = err
	}
	if runErr != nil {
		return &en, fmt.Errorf("stopped after %d of %d groups (undo is possible in the history): %w", len(en.Pairs), len(pl.Pairs), runErr)
	}
	return &en, nil
}

// finishRename: neue Gruppen anlegen, Wiederholer umhängen, Merklisten leeren.
func (a *Auth) finishRename(ctx context.Context, pl *Plan, calMode map[string]string) error {
	if err := a.mutateGroups(ctx, func(m map[string]Group) error {
		for n, g := range pl.newRec {
			if _, ok := m[n]; !ok {
				m[n] = g
			}
		}
		for _, pr := range pl.Pairs {
			if g, ok := m[pr.To]; ok {
				g.Stay = nil
				m[pr.To] = g
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if OnNewGroup != nil {
		for n, mode := range calMode {
			if mode != "" {
				OnNewGroup(ctx, n, mode)
			}
		}
	}
	return a.mutate(ctx, func(m map[string]Account) error {
		for un, e := range pl.effects {
			if u, ok := m[un]; ok {
				u.Groups = finalGroups(u, e)
				m[un] = u
			}
		}
		return nil
	})
}

var errUndoUsed = errors.New("a new group already contains data - restore from the snapshot instead")

func (a *Auth) undoRename(ctx context.Context, en LogEntry, bk backup) error {
	for _, g := range en.Created {
		for _, h := range RenameHooks {
			if carried(h, en.Carry) && h.Used != nil && h.Used(ctx, g) {
				return fmt.Errorf("%w (%s: %s)", errUndoUsed, g, h.Name)
			}
		}
	}
	created := map[string]bool{}
	for _, g := range en.Created {
		created[g] = true
	}
	a.refresh(ctx)
	a.mu.Lock()
	for un, u := range a.users { // neue Mitglieder in den neuen Gruppen: ohne Datenverlust nicht rückgängig zu machen
		for _, x := range effGroups(u) {
			if _, known := bk.Users[un]; created[x] && !known {
				a.mu.Unlock()
				return fmt.Errorf("%w (new member %s in %s)", errUndoUsed, un, x)
			}
		}
	}
	a.mu.Unlock()
	for _, g := range en.Created {
		for _, h := range RenameHooks {
			if carried(h, en.Carry) && h.Drop != nil {
				h.Drop(ctx, g)
			}
		}
	}
	if err := a.mutateGroups(ctx, func(m map[string]Group) error {
		for g := range created {
			delete(m, g)
		}
		return nil
	}); err != nil {
		return err
	}
	for i := len(en.Pairs) - 1; i >= 0; i-- {
		pr := en.Pairs[i]
		for j := len(RenameHooks) - 1; j >= 0; j-- {
			h := RenameHooks[j]
			if carried(h, en.Carry) {
				if err := h.Rename(ctx, pr.To, pr.From); err != nil {
					return fmt.Errorf("%s: %w (stopped at %s)", h.Name, err, pr.To)
				}
			}
		}
		if err := a.renameRecord(ctx, pr.To, pr.From, false); err != nil {
			return err
		}
	}
	a.refresh(ctx)
	a.mu.Lock()
	have := map[string]bool{}
	for g := range a.groups {
		have[g] = true
	}
	a.mu.Unlock()
	if err := a.mutate(ctx, func(m map[string]Account) error {
		for un, st := range bk.Users {
			if u, ok := m[un]; ok {
				var gs []string
				for _, g := range st.Groups {
					if have[g] {
						gs = append(gs, g)
					}
				}
				u.Groups, u.Disabled = gs, st.Disabled
				m[un] = u
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return a.mutateGroups(ctx, func(m map[string]Group) error {
		for g, s := range bk.Stay {
			if x, ok := m[g]; ok {
				x.Stay = s
				m[g] = x
			}
		}
		return nil
	})
}

// Confirm: Passwort des angemeldeten Admins erneut prüfen (für Vorlagen anderer Module, mit Sperre gegen Raten).
func Confirm(r *http.Request, pass string) (int, string) {
	if std == nil {
		return http.StatusInternalServerError, "not ready"
	}
	return std.confirm(r, pass)
}

// RunSnapshot: Snapshot vor einer globalen Aktion (id wird im Namen verwendet); nil, wenn kein Snapshot konfiguriert ist.
func RunSnapshot(id string) error { return runSnapshot(id) }
