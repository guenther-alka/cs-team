package ai

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// topic: ein Status-Knopf. collect liefert kompakten Text aus den normalen API-Routen mit den Rechten des Fragenden.
type topic struct {
	title   string
	admin   bool // nur globale Admins
	notAdm  bool // nur Nicht-Admins (ersetzt "system" durch "account")
	collect func(s *Svc, r *http.Request, w who) (string, error)
}

var topics = map[string]topic{
	"tasks":    {title: "tasks", collect: collectTasks},
	"calendar": {title: "calendar (next 7 days)", collect: collectCalendar},
	"files":    {title: "files", collect: collectFiles},
	"chat":     {title: "chat groups and channels (no message contents)", collect: collectChat},
	"groups":   {title: "groups and users in the user's scope", collect: collectGroups},
	"system":   {title: "system status", admin: true, collect: collectSystem},
	"account":  {title: "own account and permissions", notAdm: true, collect: collectAccount},
}

// topicIDs: die sechs Knöpfe in fester Reihenfolge; der letzte hängt von der Rolle ab.
func topicIDs(admin bool) []string {
	last := "account"
	if admin {
		last = "system"
	}
	return []string{"tasks", "calendar", "files", "chat", "groups", last}
}

// clean: Steuerzeichen und Zeilenumbrüche raus, Länge begrenzen (Daten sind nicht vertrauenswürdig).
func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
	return clip(strings.TrimSpace(s), n)
}

func list(l []string, n int) string {
	var o []string
	for i, x := range l {
		if i >= n {
			o = append(o, fmt.Sprintf("... +%d", len(l)-n))
			break
		}
		o = append(o, clean(x, 40))
	}
	return strings.Join(o, ", ")
}

func collectTasks(s *Svc, r *http.Request, w who) (string, error) {
	var in struct {
		Tasks []struct {
			Title, By, Group, Assignee, Status, Due string
			Prio                                    int
			Own, Take                               bool
		} `json:"tasks"`
	}
	if err := s.get(r, "/api/tasks", &in); err != nil {
		return "", err
	}
	today := time.Now().Format("2006-01-02")
	cnt := map[string]int{}
	over := 0
	type row struct{ due, line string }
	var rows []row
	for _, t := range in.Tasks {
		cnt[t.Status]++
		if t.Status == "closed" {
			continue
		}
		od := t.Due != "" && t.Due < today && t.Status != "done"
		if od {
			over++
		}
		prio := []string{"low", "normal", "high"}[min(max(t.Prio, 0), 2)]
		l := fmt.Sprintf("- [%s] %s | due %s%s | prio %s | assignee %s | group %s | created by %s", clean(t.Status, 10), clean(t.Title, 100),
			orDash(t.Due), map[bool]string{true: " (OVERDUE)"}[od], prio, orDash(clean(t.Assignee, 40)), orDash(clean(t.Group, 40)), clean(t.By, 40))
		d := t.Due
		if d == "" {
			d = "9999"
		}
		rows = append(rows, row{d, l})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].due < rows[j].due })
	var sb strings.Builder
	fmt.Fprintf(&sb, "Today %s. Visible tasks: %d (open %d, doing %d, done-not-accepted %d, closed %d). Overdue: %d.\n", today, len(in.Tasks), cnt["open"], cnt["doing"], cnt["done"], cnt["closed"], over)
	for i, r := range rows {
		if i >= 40 {
			fmt.Fprintf(&sb, "... and %d more\n", len(rows)-40)
			break
		}
		sb.WriteString(r.line + "\n")
	}
	return sb.String(), nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func collectCalendar(s *Svc, r *http.Request, w who) (string, error) {
	var cals []struct{ ID, Name, Scope string }
	if err := s.get(r, "/api/cal", &cals); err != nil {
		return "", err
	}
	now := time.Now()
	from, to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), now.AddDate(0, 0, 7)
	type ev struct {
		t    time.Time
		line string
	}
	var evs []ev
	for i, c := range cals {
		if i >= 12 {
			break
		}
		var rows []struct {
			Summary, Location, Start, End string
			AllDay                        bool
		}
		if s.get(r, "/api/cal/"+url.PathEscape(c.ID)+"/events", &rows) != nil {
			continue
		}
		for _, e := range rows {
			st, err := time.Parse(time.RFC3339, e.Start)
			if err != nil {
				st, err = time.Parse("2006-01-02", e.Start)
			}
			if err != nil || st.Before(from) || st.After(to) {
				continue
			}
			when := st.Local().Format("Mon 2006-01-02 15:04")
			if e.AllDay {
				when = st.Format("Mon 2006-01-02") + " (all day)"
			}
			evs = append(evs, ev{st, fmt.Sprintf("- %s | %s | calendar %s%s", when, clean(e.Summary, 100), clean(c.Name, 40), locSuffix(e.Location))})
		}
	}
	sort.Slice(evs, func(i, j int) bool { return evs[i].t.Before(evs[j].t) })
	var sb strings.Builder
	fmt.Fprintf(&sb, "Calendars visible: %d. Events from %s to %s: %d (recurring series are shown only at their first date).\n", len(cals), from.Format("2006-01-02"), to.Format("2006-01-02"), len(evs))
	for i, e := range evs {
		if i >= 40 {
			fmt.Fprintf(&sb, "... and %d more\n", len(evs)-40)
			break
		}
		sb.WriteString(e.line + "\n")
	}
	return sb.String(), nil
}

func locSuffix(l string) string {
	if l == "" {
		return ""
	}
	return " | at " + clean(l, 60)
}

func collectFiles(s *Svc, r *http.Request, w who) (string, error) {
	var in struct {
		Own, Shared []struct {
			Name, Owner string
			Size, Mod   int64
		}
		Folders []struct{ Name string }
		MaxMB   int64
	}
	if err := s.get(r, "/api/files", &in); err != nil {
		return "", err
	}
	var sb strings.Builder
	sum := func(name string, l []struct {
		Name, Owner string
		Size, Mod   int64
	}) {
		var tot int64
		for _, f := range l {
			tot += f.Size
		}
		sort.Slice(l, func(i, j int) bool { return l[i].Mod > l[j].Mod })
		fmt.Fprintf(&sb, "%s: %d files, %.1f MB.\n", name, len(l), float64(tot)/1048576)
		for i, f := range l {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&sb, "- %s | %.1f KB | owner %s | changed %s\n", clean(f.Name, 100), float64(f.Size)/1024, clean(f.Owner, 40), time.Unix(0, f.Mod).Format("2006-01-02 15:04"))
		}
	}
	sum("Own files", in.Own)
	sum("Shared with the user", in.Shared)
	var fo []string
	for _, f := range in.Folders {
		fo = append(fo, f.Name)
	}
	fmt.Fprintf(&sb, "Group folders accessible: %d (%s). Upload limit %d MB per file.\n", len(fo), list(fo, 20), in.MaxMB)
	return sb.String(), nil
}

func collectChat(s *Svc, r *http.Request, w who) (string, error) {
	var in struct {
		Groups []struct {
			Name  string
			W     bool
			Adm   bool
			Make  bool
			Chans []struct{ Name string }
		}
	}
	if err := s.get(r, "/api/chat/groups", &in); err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Chat groups visible: %d. Message contents are NOT included in this data.\n", len(in.Groups))
	for _, g := range in.Groups {
		var ch []string
		for _, c := range g.Chans {
			ch = append(ch, c.Name)
		}
		fmt.Fprintf(&sb, "- group %s | channels: %s | may write: %v | chat admin: %v\n", clean(g.Name, 40), list(ch, 20), g.W, g.Adm)
	}
	return sb.String(), nil
}

type grp struct {
	Name    string
	Areas   []string
	Admins  []string
	Members []string
	Manage  bool
	Chat    string
	Tasks   string
}

func collectGroups(s *Svc, r *http.Request, w who) (string, error) {
	var gs []grp
	if err := s.get(r, "/api/groups", &gs); err != nil {
		return "", err
	}
	if !w.Admin { // /api/groups listet alle Gruppennamen; die KI bekommt nur die eigenen (Datensparsamkeit)
		mine := map[string]bool{}
		for _, g := range append(append([]string{}, w.Groups...), w.AdminOf...) {
			mine[g] = true
		}
		var own []grp
		for _, g := range gs {
			if mine[g.Name] {
				own = append(own, g)
			}
		}
		gs = own
	}
	var us []struct {
		Name     string
		Admin    bool
		Disabled bool
		Groups   []string
	}
	s.get(r, "/api/users", &us)
	dis, adm := 0, 0
	for _, u := range us {
		if u.Disabled {
			dis++
		}
		if u.Admin {
			adm++
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Role of the user: %s. Groups visible: %d. Users visible: %d (global admins %d, disabled %d).\n", w.Role(), len(gs), len(us), adm, dis)
	for i, g := range gs {
		if i >= 40 {
			fmt.Fprintf(&sb, "... and %d more groups\n", len(gs)-40)
			break
		}
		mem := "members hidden"
		if g.Members != nil {
			mem = fmt.Sprintf("%d members: %s", len(g.Members), list(g.Members, 15))
		}
		fmt.Fprintf(&sb, "- %s | areas: %s | group admins: %s | %s | manageable by the user: %v\n", clean(g.Name, 40), strings.Join(g.Areas, ","), list(g.Admins, 8), mem, g.Manage)
	}
	return sb.String(), nil
}

func collectAccount(s *Svc, r *http.Request, w who) (string, error) {
	return fmt.Sprintf("User: %s. Role: %s.\nGroups: %s.\nAreas (menus) allowed: %s.\n", clean(w.Name, 40), w.Role(), list(w.Groups, 20), strings.Join(w.Areas, ", ")), nil
}

func collectSystem(s *Svc, r *http.Request, w who) (string, error) {
	var sb strings.Builder
	if s.Info != nil {
		info := s.Info()
		keys := make([]string, 0, len(info))
		for k := range info {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, "%s: %s\n", k, clean(info[k], 100))
		}
	}
	var us []struct {
		Admin, Disabled bool
		Must            bool
	}
	s.get(r, "/api/users", &us)
	adm, dis, must := 0, 0, 0
	for _, u := range us {
		if u.Admin {
			adm++
		}
		if u.Disabled {
			dis++
		}
		if u.Must {
			must++
		}
	}
	var gs []grp
	s.get(r, "/api/groups", &gs)
	var ts struct{ Tasks []struct{ Status string } }
	s.get(r, "/api/tasks", &ts)
	open := 0
	for _, t := range ts.Tasks {
		if t.Status != "closed" {
			open++
		}
	}
	var st struct {
		Enabled bool
		Public  string
	}
	s.get(r, "/api/settings", &st)
	fmt.Fprintf(&sb, "users: %d (global admins %d, disabled %d)\ngroups: %d\ntasks not closed: %d\nmail (SMTP) configured: %v\npublic address set: %v\n", len(us), adm, dis, len(gs), open, st.Enabled, st.Public != "")
	return sb.String(), nil
}
