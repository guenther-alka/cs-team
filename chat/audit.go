package chat

// Vorlage "Chat-Auswertung": Suche (Mobbing-Vorfälle), Beweissicherung (ZIP mit Prüfsummen) und Löschen nach Löschanforderung.
// Nur globale Admins. Jede Aktion braucht einen Anlass (Aktenzeichen/Begründung) und wird ohne Nachrichteninhalt protokolliert.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cs-team/auth"
)

const (
	auditKey    = "chat/_audit.json"
	maxHits     = 500  // Anzeige in der Suche
	maxSelect   = 5000 // Auswahl für Export / Löschen
	maxAudit    = 2000 // Einträge im Protokoll
	maxExportMB = 150  // Anhänge im Beweispaket
	maxReasonLn = 500
)

var auditMu sync.Mutex

type Ref struct {
	G  string `json:"g"`
	C  string `json:"c"`
	ID int64  `json:"id"`
}

type Filter struct {
	Groups  []string `json:"groups"`  // leer = alle
	Channel string   `json:"channel"` // leer = alle
	Users   []string `json:"users"`   // Autoren, leer = alle
	From    string   `json:"from"`    // JJJJ-MM-TT
	To      string   `json:"to"`
	Words   []string `json:"words"` // Suchbegriffe (ohne Groß-/Kleinschreibung)
	All     bool     `json:"all"`   // alle Begriffe nötig (sonst einer)
	Ctx     int      `json:"ctx"`   // Nachrichten davor/danach (0..10)
}

type Hit struct {
	Ref
	By   string `json:"by"`
	T    string `json:"t"`
	Time int64  `json:"time"` // ms seit 1970
	Ed   bool   `json:"ed,omitempty"`
	Att  *Att   `json:"att,omitempty"`
	Ctx  bool   `json:"ctx,omitempty"` // nur Zusammenhang, kein Treffer
}

type Result struct {
	Hits      []Hit          `json:"hits"`
	Total     int            `json:"total"` // Treffer insgesamt
	Shown     int            `json:"shown"`
	Truncated bool           `json:"truncated"`
	Users     map[string]int `json:"users"` // Treffer je Autor
}

type Audit struct {
	ID       string          `json:"id"`
	T        int64           `json:"t"`
	Admin    string          `json:"admin"`
	Action   string          `json:"action"` // search | export | delete | ai
	Reason   string          `json:"reason"`
	Filter   *Filter         `json:"filter,omitempty"`
	Count    int             `json:"count"`
	Users    map[string]int  `json:"users,omitempty"`
	Hash     string          `json:"hash,omitempty"`     // export: SHA-256 des ZIP; delete: Hash der Vorschau
	Mode     string          `json:"mode,omitempty"`     // delete: clear | remove
	Snapshot string          `json:"snapshot,omitempty"` // Name des Snapshots oder "none"
	Refs     []Ref           `json:"refs,omitempty"`     // delete: gelöschte Nachrichten (ohne Inhalt)
	Extra    map[string]bool `json:"extra,omitempty"`
}

func uniq(l []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range l {
		x = strings.TrimSpace(x)
		if x != "" && !seen[strings.ToLower(x)] {
			seen[strings.ToLower(x)] = true
			out = append(out, x)
		}
	}
	return out
}

func validG(g string) bool {
	return g != "" && g != "f" && !strings.HasPrefix(g, "_") && !strings.ContainsAny(g, "/\\\x00") && g != "." && g != ".."
}

func validC(c string) bool { return c == DefaultCh || reChan.MatchString(c) }

// allGroups: Gruppen mit Chat-Daten im Speicher (auch Gruppen, die es nicht mehr gibt).
func (s *Svc) allGroups(ctx context.Context) []string {
	infos, _ := s.St.List(ctx, "chat/")
	seen := map[string]bool{}
	for _, i := range infos {
		k := strings.TrimPrefix(i.Key, "chat/")
		if j := strings.Index(k, "/"); j > 0 {
			if g := k[:j]; validG(g) {
				seen[g] = true
			}
		}
	}
	var out []string
	for g := range seen {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

func (s *Svc) channelsOf(ctx context.Context, g string) []string {
	out := []string{DefaultCh}
	var more []string
	for c := range s.list(ctx, g) {
		more = append(more, c)
	}
	sort.Strings(more)
	return append(out, more...)
}

func dayStart(s string, end bool) (int64, error) {
	if strings.TrimSpace(s) == "" {
		return 0, nil
	}
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
	if err != nil {
		return 0, errors.New("bad date (YYYY-MM-DD)")
	}
	if end {
		t = t.AddDate(0, 0, 1)
	}
	return t.UnixMicro(), nil
}

func hitOf(g, c string, m Msg, ctx bool) Hit {
	return Hit{Ref: Ref{G: g, C: c, ID: m.ID}, By: m.By, T: m.T, Time: m.ID / 1000, Ed: m.Ed, Att: m.Att, Ctx: ctx}
}

// search: Treffer samt Zusammenhang. limit begrenzt die Treffer (nicht den Zusammenhang); Total zählt alle Treffer.
func (s *Svc) search(ctx context.Context, f Filter, limit int) (*Result, error) {
	from, err := dayStart(f.From, false)
	if err != nil {
		return nil, err
	}
	to, err := dayStart(f.To, true)
	if err != nil {
		return nil, err
	}
	if f.Ctx < 0 {
		f.Ctx = 0
	}
	if f.Ctx > 10 {
		f.Ctx = 10
	}
	users := map[string]bool{}
	for _, u := range uniq(f.Users) {
		users[strings.ToLower(u)] = true
	}
	var words []string
	for _, w := range uniq(f.Words) {
		words = append(words, strings.ToLower(w))
	}
	groups := uniq(f.Groups)
	if len(groups) == 0 {
		groups = s.allGroups(ctx)
	}
	res := &Result{Hits: []Hit{}, Users: map[string]int{}}
	for _, g := range groups {
		if !validG(g) {
			continue
		}
		for _, cn := range s.channelsOf(ctx, g) {
			if f.Channel != "" && f.Channel != cn {
				continue
			}
			c := s.ch(ctx, g, cn)
			c.mu.Lock()
			msgs := append([]Msg(nil), c.msgs...)
			c.mu.Unlock()
			match := make([]bool, len(msgs))
			for i, m := range msgs {
				if m.Del || (from > 0 && m.ID < from) || (to > 0 && m.ID >= to) {
					continue
				}
				if len(users) > 0 && !users[strings.ToLower(m.By)] {
					continue
				}
				if len(words) > 0 {
					t := strings.ToLower(m.T)
					if m.Att != nil {
						t += "\n" + strings.ToLower(m.Att.Name)
					}
					n := 0
					for _, w := range words {
						if strings.Contains(t, w) {
							n++
						}
					}
					if (f.All && n < len(words)) || (!f.All && n == 0) {
						continue
					}
				}
				match[i] = true
			}
			taken := make([]bool, len(msgs))
			for i, m := range msgs {
				if !match[i] {
					continue
				}
				res.Total++
				res.Users[m.By]++
				if limit > 0 && res.Shown >= limit {
					res.Truncated = true
					continue
				}
				res.Shown++
				for j := i - f.Ctx; j <= i+f.Ctx; j++ {
					if j < 0 || j >= len(msgs) || taken[j] || msgs[j].Del {
						continue
					}
					taken[j] = true
				}
			}
			for i, m := range msgs {
				if taken[i] {
					res.Hits = append(res.Hits, hitOf(g, cn, m, !match[i]))
				}
			}
		}
	}
	return res, nil
}

// get: eine vorhandene (nicht gelöschte) Nachricht.
func (s *Svc) get(ctx context.Context, r Ref) (Msg, bool) {
	if !validG(r.G) || !validC(r.C) {
		return Msg{}, false
	}
	c := s.ch(ctx, r.G, r.C)
	c.mu.Lock()
	defer c.mu.Unlock()
	if i := find(c, r.ID); i >= 0 && !c.msgs[i].Del {
		return c.msgs[i], true
	}
	return Msg{}, false
}

// resolve: ausgewählte Nachrichten (Refs) oder - ohne Refs - alle Treffer des Filters (mit Zusammenhang, wenn withCtx).
func (s *Svc) resolve(ctx context.Context, f Filter, refs []Ref, withCtx bool) ([]Hit, error) {
	var out []Hit
	if len(refs) > 0 {
		if len(refs) > maxSelect {
			return nil, errors.New("too many messages selected")
		}
		seen := map[Ref]bool{}
		for _, r := range refs {
			if seen[r] {
				continue
			}
			seen[r] = true
			if m, ok := s.get(ctx, r); ok {
				out = append(out, hitOf(r.G, r.C, m, false))
			}
		}
	} else {
		if !withCtx {
			f.Ctx = 0
		}
		res, err := s.search(ctx, f, maxSelect)
		if err != nil {
			return nil, err
		}
		if res.Truncated {
			return nil, errors.New("too many matches - narrow the filter")
		}
		out = res.Hits
	}
	if len(out) == 0 {
		return nil, errors.New("no messages")
	}
	return out, nil
}

// ---------- Protokoll ----------

func (s *Svc) audit(ctx context.Context) []Audit {
	var l []Audit
	if b, _, err := s.St.Get(ctx, auditKey); err == nil {
		json.Unmarshal(b, &l)
	}
	return l
}

func (s *Svc) addAudit(ctx context.Context, e Audit) {
	auditMu.Lock()
	defer auditMu.Unlock()
	l := append(s.audit(ctx), e)
	if len(l) > maxAudit {
		l = l[len(l)-maxAudit:]
	}
	b, _ := json.Marshal(l)
	if _, err := s.St.Put(ctx, auditKey, b, ""); err != nil {
		log.Printf("chat audit: %v", err)
	}
}

// Review: Zeilen für die KI-Auswertung (Admin) und Protokolleintrag; liefert "Zeit | Gruppe/Kanal | Autor | Text".
func (s *Svc) Lines(ctx context.Context, admin, reason string, refs []Ref, max int) ([]string, error) {
	hits, err := s.resolve(ctx, Filter{}, refs, false)
	if err != nil {
		return nil, err
	}
	if len(hits) > max {
		return nil, errors.New("too many messages for the analysis")
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.G != b.G {
			return a.G < b.G
		}
		if a.C != b.C {
			return a.C < b.C
		}
		return a.ID < b.ID
	})
	var out []string
	users := map[string]int{}
	for _, h := range hits {
		out = append(out, fmt.Sprintf("%s | %s/%s | %s | %s", time.UnixMilli(h.Time).Format("2006-01-02 15:04"), h.G, h.C, h.By, strings.ReplaceAll(strings.ReplaceAll(h.T, "\r", ""), "\n", " ⏎ ")))
		users[h.By]++
	}
	s.addAudit(ctx, Audit{ID: newID(), T: time.Now().Unix(), Admin: admin, Action: "ai", Reason: reason, Count: len(hits), Users: users})
	return out, nil
}

func newID() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

func cleanReason(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) < 5 {
		return "", errors.New("reason (case number / justification) required")
	}
	if utf8.RuneCountInString(s) > maxReasonLn {
		s = string([]rune(s)[:maxReasonLn])
	}
	return s, nil
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// ---------- Beweissicherung ----------

func safeName(n string) string {
	var b strings.Builder
	for _, r := range n {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			r = '_'
		}
		b.WriteRune(r)
	}
	return b.String()
}

type attInfo struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type exported struct {
	Hit
	Time8601 string   `json:"time_iso"`
	File     *attInfo `json:"file,omitempty"`
}

// evidence: ZIP mit messages.csv, messages.json, Anhängen und manifest.txt; liefert die Bytes und deren SHA-256.
func (s *Svc) evidence(ctx context.Context, admin, reason string, f Filter, hits []Hit) ([]byte, string, error) {
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.G != b.G {
			return a.G < b.G
		}
		if a.C != b.C {
			return a.C < b.C
		}
		return a.ID < b.ID
	})
	now := time.Now()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hashes := map[string]string{}
	add := func(name string, data []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		hashes[name] = sha(data)
		return err
	}
	var rows []exported
	var skipped []string
	var attBytes int64
	for _, h := range hits {
		e := exported{Hit: h, Time8601: time.UnixMilli(h.Time).UTC().Format(time.RFC3339)}
		if h.Att != nil {
			if attBytes+h.Att.Size > maxExportMB<<20 {
				skipped = append(skipped, fmt.Sprintf("%s/%s/%d: %s", h.G, h.C, h.ID, h.Att.Name))
			} else if rc, err := s.St.GetStream(ctx, fileKey(h.G, h.C, h.Att.ID)); err == nil {
				data, err := io.ReadAll(io.LimitReader(rc, maxExportMB<<20+1))
				rc.Close()
				if err == nil {
					attBytes += int64(len(data))
					p := "attachments/" + safeName(h.G) + "/" + safeName(h.C) + "/" + h.Att.ID + "-" + safeName(h.Att.Name)
					if err := add(p, data); err != nil {
						return nil, "", err
					}
					e.File = &attInfo{Path: p, SHA256: sha(data), Size: int64(len(data))}
				}
			} else {
				skipped = append(skipped, fmt.Sprintf("%s/%s/%d: %s (not readable)", h.G, h.C, h.ID, h.Att.Name))
			}
		}
		rows = append(rows, e)
	}
	// CSV (Semikolon, UTF-8 mit BOM: öffnet in Excel/Calc direkt)
	var cb bytes.Buffer
	cb.WriteString("\xef\xbb\xbf")
	cw := csv.NewWriter(&cb)
	cw.Comma = ';'
	cw.Write([]string{"group", "channel", "id", "time_utc", "author", "edited", "context_only", "attachment", "attachment_sha256", "text"})
	for _, e := range rows {
		an, ah := "", ""
		if e.Att != nil {
			an = e.Att.Name
		}
		if e.File != nil {
			ah = e.File.SHA256
		}
		cw.Write([]string{e.G, e.C, strconv.FormatInt(e.ID, 10), e.Time8601, e.By, strconv.FormatBool(e.Ed), strconv.FormatBool(e.Ctx), an, ah, csvSafe(e.T)})
	}
	cw.Flush()
	if err := add("messages.csv", cb.Bytes()); err != nil {
		return nil, "", err
	}
	js, _ := json.MarshalIndent(rows, "", " ")
	if err := add("messages.json", js); err != nil {
		return nil, "", err
	}
	fj, _ := json.Marshal(f)
	var m strings.Builder
	fmt.Fprintf(&m, "cs-team evidence package\r\nexported (UTC): %s\r\nexported by: %s\r\nreason / case: %s\r\nmessages: %d (context-only lines are marked)\r\nfilter: %s\r\n\r\n", now.UTC().Format(time.RFC3339), admin, reason, len(rows), fj)
	if len(skipped) > 0 {
		fmt.Fprintf(&m, "attachments NOT included (size limit or unreadable):\r\n%s\r\n\r\n", strings.Join(skipped, "\r\n"))
	}
	m.WriteString("SHA-256 of the files in this package:\r\n")
	var names []string
	for n := range hashes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&m, "%s  %s\r\n", hashes[n], n)
	}
	m.WriteString("\r\nThe SHA-256 of the ZIP file itself is stored in the cs-team log and shown at download.\r\n")
	if err := add("manifest.txt", []byte(m.String())); err != nil {
		return nil, "", err
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), sha(buf.Bytes()), nil
}

// csvSafe: keine Formeln beim Öffnen in Tabellenprogrammen (Text bleibt lesbar, nur ein Hochkomma davor).
func csvSafe(t string) string {
	if t != "" && strings.ContainsAny(t[:1], "=+-@\t\r") {
		return "'" + t
	}
	return t
}

// ---------- Löschen ----------

type DelParams struct {
	Filter Filter `json:"filter"`
	Refs   []Ref  `json:"refs"`
	Mode   string `json:"mode"` // clear (Tombstone ohne Inhalt) | remove (Eintrag ganz entfernen)
	Reason string `json:"reason"`
}

type DelPlan struct {
	Items     []Hit          `json:"items"`
	Count     int            `json:"count"`
	Atts      int            `json:"atts"`
	Users     map[string]int `json:"users"`
	Mode      string         `json:"mode"`
	Hash      string         `json:"hash"`
	Truncated bool           `json:"truncated"` // Liste gekürzt (alle werden gelöscht)
}

func (s *Svc) planDelete(ctx context.Context, p DelParams) (*DelPlan, []Hit, error) {
	if p.Mode != "clear" && p.Mode != "remove" {
		return nil, nil, errors.New("mode: clear | remove")
	}
	hits, err := s.resolve(ctx, p.Filter, p.Refs, false)
	if err != nil {
		return nil, nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.G != b.G {
			return a.G < b.G
		}
		if a.C != b.C {
			return a.C < b.C
		}
		return a.ID < b.ID
	})
	h := sha256.New()
	pl := &DelPlan{Count: len(hits), Users: map[string]int{}, Mode: p.Mode}
	h.Write([]byte(p.Mode))
	for _, x := range hits {
		aid := ""
		if x.Att != nil {
			aid = x.Att.ID
			pl.Atts++
		}
		fmt.Fprintf(h, "|%s/%s/%d/%s/%s/%s", x.G, x.C, x.ID, x.By, sha([]byte(x.T)), aid)
		pl.Users[x.By]++
	}
	pl.Hash = hex.EncodeToString(h.Sum(nil))[:16]
	pl.Items = hits
	if len(hits) > maxHits {
		pl.Items, pl.Truncated = hits[:maxHits], true
	}
	return pl, hits, nil
}

// applyDelete: löscht die Nachrichten (je Kanal einmal gesichert) samt Anhängen und meldet es den verbundenen Clients.
func (s *Svc) applyDelete(ctx context.Context, mode string, hits []Hit) int {
	by := map[string][]Hit{}
	var order []string
	for _, h := range hits {
		k := h.G + "/" + h.C
		if _, ok := by[k]; !ok {
			order = append(order, k)
		}
		by[k] = append(by[k], h)
	}
	n := 0
	for _, k := range order {
		l := by[k]
		c := s.ch(ctx, l[0].G, l[0].C)
		var upd []Msg
		c.mu.Lock()
		for _, h := range l {
			i := find(c, h.ID)
			if i < 0 || c.msgs[i].Del {
				continue
			}
			if a := c.msgs[i].Att; a != nil {
				s.St.Delete(ctx, fileKey(c.g, c.c, a.ID))
			}
			c.msgs[i] = Msg{ID: h.ID, By: c.msgs[i].By, Del: true}
			upd = append(upd, c.msgs[i])
			n++
		}
		if mode == "remove" {
			keep := c.msgs[:0]
			gone := map[int64]bool{}
			for _, m := range upd {
				gone[m.ID] = true
			}
			for _, m := range c.msgs {
				if !gone[m.ID] {
					keep = append(keep, m)
				}
			}
			c.msgs = keep
		}
		if err := s.save(ctx, c); err != nil {
			log.Printf("chat: audit save: %v", err)
		}
		c.mu.Unlock()
		for _, m := range upd {
			if mode == "remove" {
				m.By = ""
			}
			s.broadcast(l[0].G, map[string]any{"t": "upd", "g": l[0].G, "c": l[0].C, "m": m})
		}
	}
	return n
}

// ---------- Routen ----------

func (s *Svc) auditRoutes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	adm := func(fn http.HandlerFunc) http.Handler {
		return wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !auth.IsAdmin(r.Context()) {
				http.Error(w, "admin only", http.StatusForbidden)
				return
			}
			fn(w, r)
		}))
	}
	dec := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(v) != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return false
		}
		return true
	}
	bad := func(w http.ResponseWriter, err error) { http.Error(w, err.Error(), http.StatusBadRequest) }
	userCount := func(hs []Hit) map[string]int {
		m := map[string]int{}
		for _, h := range hs {
			if !h.Ctx {
				m[h.By]++
			}
		}
		return m
	}

	mux.Handle("GET /api/routines/chat/info", adm(func(w http.ResponseWriter, r *http.Request) {
		type gi struct {
			G        string   `json:"g"`
			Channels []string `json:"channels"`
		}
		var gs []gi
		for _, g := range s.allGroups(r.Context()) {
			gs = append(gs, gi{g, s.channelsOf(r.Context(), g)})
		}
		if gs == nil {
			gs = []gi{}
		}
		json.NewEncoder(w).Encode(map[string]any{"snapshot": auth.SnapshotMode(), "dataset": auth.SnapshotInfo, "groups": gs, "ai": s.AIReview != nil && s.AIReview()})
	}))
	mux.Handle("POST /api/routines/chat/search", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Filter Filter `json:"filter"`
			Reason string `json:"reason"`
		}
		if !dec(w, r, &in) {
			return
		}
		reason, err := cleanReason(in.Reason)
		if err != nil {
			bad(w, err)
			return
		}
		res, err := s.search(r.Context(), in.Filter, maxHits)
		if err != nil {
			bad(w, err)
			return
		}
		f := in.Filter
		s.addAudit(r.Context(), Audit{ID: newID(), T: time.Now().Unix(), Admin: auth.User(r.Context()), Action: "search", Reason: reason, Filter: &f, Count: res.Total, Users: res.Users})
		json.NewEncoder(w).Encode(res)
	}))
	mux.Handle("POST /api/routines/chat/export", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Filter   Filter `json:"filter"`
			Refs     []Ref  `json:"refs"`
			Ctx      []Ref  `json:"ctx"` // Zusammenhang (nur zur Einordnung, im Paket markiert)
			Reason   string `json:"reason"`
			Password string `json:"password"`
		}
		if !dec(w, r, &in) {
			return
		}
		reason, err := cleanReason(in.Reason)
		if err != nil {
			bad(w, err)
			return
		}
		if code, msg := auth.Confirm(r, in.Password); code != 0 {
			http.Error(w, msg, code)
			return
		}
		hits, err := s.resolve(r.Context(), in.Filter, in.Refs, true)
		if err != nil {
			bad(w, err)
			return
		}
		if len(in.Refs) > 0 && len(in.Ctx) > 0 && len(in.Ctx)+len(hits) <= maxSelect {
			have := map[Ref]bool{}
			for _, h := range hits {
				have[h.Ref] = true
			}
			for _, x := range in.Ctx {
				if m, ok := s.get(r.Context(), x); ok && !have[x] {
					have[x] = true
					hits = append(hits, hitOf(x.G, x.C, m, true))
				}
			}
		}
		admin := auth.User(r.Context())
		data, sum, err := s.evidence(r.Context(), admin, reason, in.Filter, hits)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		f := in.Filter
		id := newID()
		s.addAudit(r.Context(), Audit{ID: id, T: time.Now().Unix(), Admin: admin, Action: "export", Reason: reason, Filter: &f, Count: len(hits), Users: userCount(hits), Hash: sum})
		log.Printf("chat evidence by %s: %d messages, sha256 %s", admin, len(hits), sum)
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="chat-evidence-`+id+`.zip"`)
		w.Header().Set("X-Content-SHA256", sum)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Write(data)
	}))
	mux.Handle("POST /api/routines/chat/delete/preview", adm(func(w http.ResponseWriter, r *http.Request) {
		var p DelParams
		if !dec(w, r, &p) {
			return
		}
		pl, _, err := s.planDelete(r.Context(), p)
		if err != nil {
			bad(w, err)
			return
		}
		json.NewEncoder(w).Encode(pl)
	}))
	mux.Handle("POST /api/routines/chat/delete/run", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			DelParams
			Hash     string `json:"hash"`
			Password string `json:"password"`
			NoSnap   bool   `json:"noSnapshot"`
		}
		if !dec(w, r, &in) {
			return
		}
		reason, err := cleanReason(in.Reason)
		if err != nil {
			bad(w, err)
			return
		}
		if auth.SnapshotMode() == "none" && !in.NoSnap {
			http.Error(w, "no snapshot possible - confirm to run without snapshot (noSnapshot) or set CS_SNAPSHOT_CMD", http.StatusBadRequest)
			return
		}
		if code, msg := auth.Confirm(r, in.Password); code != 0 {
			http.Error(w, msg, code)
			return
		}
		pl, hits, err := s.planDelete(r.Context(), in.DelParams)
		if err != nil {
			bad(w, err)
			return
		}
		if in.Hash == "" || pl.Hash != in.Hash {
			http.Error(w, "the data changed since the preview - please check the preview again", http.StatusConflict)
			return
		}
		id := newID()
		snap := "none"
		if !in.NoSnap && auth.SnapshotMode() != "none" {
			snap = "cs-team-chat-" + id
			if err := auth.RunSnapshot("chat-" + id); err != nil {
				http.Error(w, "snapshot failed, nothing deleted: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		n := s.applyDelete(r.Context(), in.Mode, hits)
		admin := auth.User(r.Context())
		refs := make([]Ref, 0, len(hits))
		for _, h := range hits {
			refs = append(refs, h.Ref)
		}
		f := in.Filter
		s.addAudit(r.Context(), Audit{ID: id, T: time.Now().Unix(), Admin: admin, Action: "delete", Reason: reason, Filter: &f, Count: n, Users: pl.Users, Hash: pl.Hash, Mode: in.Mode, Snapshot: snap, Refs: refs})
		log.Printf("chat delete by %s: %d messages (%s), snapshot %s", admin, n, in.Mode, snap)
		json.NewEncoder(w).Encode(map[string]any{"id": id, "deleted": n, "snapshot": snap, "mode": in.Mode})
	}))
	mux.Handle("GET /api/routines/chat/log", adm(func(w http.ResponseWriter, r *http.Request) {
		l := s.audit(r.Context())
		for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
			l[i], l[j] = l[j], l[i]
		}
		if l == nil {
			l = []Audit{}
		}
		if len(l) > 200 {
			l = l[:200]
		}
		json.NewEncoder(w).Encode(l)
	}))
}
