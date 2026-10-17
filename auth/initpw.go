package auth

// Initialpasswoerter und Gruppenlisten (0.63).
//
// Ablauf fuer Admin und Benutzer:
//  1. Der Admin laedt in einer Gruppe eine Liste hoch: je Zeile "name[, kontaktmail[, pw]]"; Kopfzeile "org: meine.einrichtung.de"
//     haengt die Domain an Namen ohne @ an (Anmeldename = Adresse, zugleich Kontakt-Mail). pw: leer / "init:" = unveraendert
//     (neues Konto: noch kein Passwort), "local:" / "oidc:" = unveraendert, sonst ein ausdruecklich gesetztes Initialpasswort.
//  2. "Initialpasswoerter erzeugen": je Konto ein eigenes Zufallspasswort, nur fuer Konten ohne Passwort bzw. ohne offenes
//     Initialpasswort (local/oidc nie). Es wird dem Admin einmal angezeigt (gespeichert wird nur der Abdruck), gilt InitDays Tage
//     und muss beim ersten Anmelden geaendert werden - oder das Konto wird mit einem Anbieter verknuepft (oidc.go).
//  3. Optional wird es per E-Mail zugestellt; Absender ist die eingestellte SMTP-Adresse (Zustellfehler gehen dorthin).
//  4. "Zuruecksetzen": neues Initialpasswort, Verknuepfung geloest, Zwei-Faktor und App-Passwoerter weg = Zustand wie beim Erstlogin.
// Admins, Sysadmin, Gaeste und Verzeichniskonten sind ausgenommen.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	InitDays  = 30
	initAlpha = "abcdefghjkmnpqrstuvwxyz23456789" // ohne i l o 0 1: leicht abzulesen
	maxList   = 2000
)

var ErrNotEligible = errors.New("account is an administrator, directory or guest account, or not yours to manage")

var (
	// SendMail / MailOK: Mailversand (von main.go gesetzt). Ohne sie wird nichts verschickt.
	SendMail func(to, subject, body string) error
	MailOK   func() bool
)

var validOrg = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}(\.[a-z0-9][a-z0-9-]{0,62})+$`)

// initExpired: das Initialpasswort ist abgelaufen (das Konto ist dann gesperrt, bis ein neues erzeugt wird).
func initExpired(u Account) bool {
	if u.InitExp == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, u.InitExp)
	return err != nil || time.Now().After(t)
}

// pwState: oidc | init (offenes Initialpasswort) | expired | local | none (noch kein Passwort) | dir.
func pwState(u Account) string {
	switch {
	case u.Link != nil:
		return "oidc"
	case u.Source == "dir":
		return "dir"
	case u.Hash == "":
		return "none"
	case u.Must && initExpired(u):
		return "expired"
	case u.Must:
		return "init"
	}
	return "local"
}

// eligible: Konten, fuer die Initialpasswoerter gelten.
func eligible(u Account) bool { return !u.Admin && !u.Sys && !u.Guest && u.Source != "dir" }

func genInitPw() string {
	out := make([]byte, 0, 10)
	buf := make([]byte, 32)
	for len(out) < 10 {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		for _, b := range buf {
			if b < 248 && len(out) < 10 { // 31*8: ohne Verzerrung
				out = append(out, initAlpha[int(b)%len(initAlpha)])
			}
		}
	}
	return string(out[:5]) + "-" + string(out[5:])
}

// setInit: Konto in den Erstlogin-Zustand mit Initialpasswort pw versetzen.
func setInit(u Account, pw string) (Account, error) {
	h, err := hash(pw)
	if err != nil {
		return u, err
	}
	u = clear2FA(u)
	u.Hash, u.Must, u.Link = h, true, nil
	u.InitExp = time.Now().Add(InitDays * 24 * time.Hour).UTC().Format(time.RFC3339)
	return u, nil
}

// ---------- Liste lesen ----------

type listRow struct {
	Name, Mail, PW string // Name = voller Kontoname
}

// parseList: Zeilen "name[, mail[, pw]]", "# Kommentar", "org: domain". Fehler je Zeile; gute Zeilen gelten trotzdem.
func parseList(text string) (rows []listRow, errs []string) {
	org := ""
	seen := map[string]bool{}
	for i, ln := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		if k, v, ok := strings.Cut(ln, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "org") {
			v = strings.ToLower(strings.TrimSpace(v))
			if v != "" && !validOrg.MatchString(v) {
				errs = append(errs, fmt.Sprintf("line %d: bad org", i+1))
				continue
			}
			org = v
			continue
		}
		if !strings.Contains(ln, ",") && strings.Contains(ln, "\t") {
			ln = strings.ReplaceAll(ln, "\t", ",")
		}
		f := strings.SplitN(ln, ",", 3)
		for j := range f {
			f[j] = strings.TrimSpace(f[j])
		}
		name := strings.ToLower(f[0])
		if !strings.Contains(name, "@") && org != "" {
			name += "@" + org
		}
		r := listRow{Name: name}
		if len(f) > 1 {
			r.Mail = f[1]
		}
		if len(f) > 2 {
			r.PW = f[2]
		}
		if len(rows) >= maxList {
			errs = append(errs, fmt.Sprintf("line %d: list too long (max %d)", i+1, maxList))
			break
		}
		switch {
		case !okAcct(name):
			errs = append(errs, fmt.Sprintf("line %d: bad name %q", i+1, safeName(f[0])))
			continue
		case seen[name]:
			errs = append(errs, fmt.Sprintf("line %d: %s twice", i+1, name))
			continue
		}
		if r.Mail != "" {
			m, _, err := cleanContact(r.Mail, "")
			if err != nil {
				errs = append(errs, fmt.Sprintf("line %d: bad mail for %s", i+1, name))
				continue
			}
			r.Mail = m
		}
		seen[name] = true
		rows = append(rows, r)
	}
	return rows, errs
}

// listPW: Spalte pw. keep = unveraendert; sonst das ausdruecklich gesetzte Initialpasswort.
func listPW(s string) (pw string, keep bool) {
	switch strings.ToLower(s) {
	case "", "init:", "local:", "oidc:":
		return "", true
	}
	return s, false
}

type listRes struct {
	Name   string `json:"name"`
	Mail   string `json:"mail,omitempty"`
	Action string `json:"action"` // new | added | updated | unchanged | skipped
	State  string `json:"state"`
	Note   string `json:"note,omitempty"`
}

// ---------- Verwaltung ----------

func (a *Auth) groupExists(ctx context.Context, g string) bool {
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.groups[g]
	return ok
}

// membersOf: Konten mit g in der Handliste.
func (a *Auth) membersOf(ctx context.Context, g string) []string {
	a.refresh(ctx)
	a.mu.Lock()
	var out []string
	for n, u := range a.users {
		for _, x := range u.Groups {
			if x == g {
				out = append(out, n)
				break
			}
		}
	}
	a.mu.Unlock()
	sort.Strings(out)
	return out
}

// ImportList legt Konten an bzw. nimmt vorhandene in die Gruppe g auf. Rechte: der Aufrufer verwaltet g; vorhandene Konten
// fasst er nur an, wenn er sie verwaltet (Gruppen-Admin) bzw. sie noch keiner Gruppe angehoeren.
func (a *Auth) ImportList(ctx context.Context, g, text string) ([]listRes, []string, error) {
	if !CanManage(ctx, g) || !a.groupExists(ctx, g) {
		return nil, nil, ErrNotEligible
	}
	rows, errs := parseList(text)
	allowed := map[string]bool{}
	for _, r := range rows {
		if a.NameRealmClash(r.Name) {
			allowed[r.Name] = false
			continue
		}
		if u, ok := a.get(ctx, r.Name); ok {
			// Gruppen-Admins sind eine Rolle: nur ein globaler Admin fasst sie an
			allowed[r.Name] = IsAdmin(ctx) || (len(a.adminOf(r.Name)) == 0 && (a.manages(ctx, r.Name) || a.unassigned(ctx, r.Name) || inGroup(u, g)))
		} else {
			allowed[r.Name] = true
		}
	}
	var res []listRes
	err := a.mutate(ctx, func(m map[string]Account) error {
		res = res[:0]
		for _, r := range rows {
			x := listRes{Name: r.Name, Mail: r.Mail}
			u, have := m[r.Name]
			pw, keep := listPW(r.PW)
			switch {
			case a.NameRealmClash(r.Name):
				x.Action, x.Note = "skipped", "name falls into the directory realm"
			case !allowed[r.Name]:
				x.Action, x.Note = "skipped", "account belongs to another group"
			case have && !eligible(u):
				x.Action, x.Note = "skipped", "administrator, guest or directory account"
			default:
				x.Action = "unchanged"
				if !have {
					u = Account{Created: time.Now().UTC().Format(time.RFC3339), Groups: []string{g}, Mail: r.Mail}
					if u.Mail == "" && strings.Contains(r.Name, "@") {
						u.Mail = r.Name // Anmeldename ist eine Adresse: zugleich Kontakt-Mail
					}
					x.Mail = u.Mail
					x.Action = "new"
				} else {
					if !inGroup(u, g) {
						u.Groups = append(append([]string{}, u.Groups...), g)
						x.Action = "added"
					}
					if u.Mail == "" && r.Mail == "" && strings.Contains(r.Name, "@") {
						r.Mail = r.Name
					}
					if r.Mail != "" && r.Mail != u.Mail {
						u.Mail = r.Mail
						if x.Action == "unchanged" {
							x.Action = "updated"
						}
					}
				}
				if !keep {
					if st := pwState(u); st == "local" || st == "oidc" {
						x.Note = "password column ignored: account is " + st
					} else if nu, err := setInit(u, pw); err != nil {
						x.Note = "initial password refused: " + err.Error()
					} else {
						u = nu
						if x.Action == "unchanged" {
							x.Action = "updated"
						}
					}
				}
				m[r.Name] = u
			}
			x.State = pwState(u)
			res = append(res, x)
		}
		return nil
	})
	return res, errs, err
}

func inGroup(u Account, g string) bool {
	for _, x := range u.Groups {
		if x == g {
			return true
		}
	}
	return false
}

type genRes struct {
	Name string `json:"name"`
	Mail string `json:"mail,omitempty"`
	PW   string `json:"pw,omitempty"`
	Sent string `json:"sent,omitempty"` // ok | no mail | error: ...
	Note string `json:"note,omitempty"`
}

// generate: Initialpasswoerter fuer die Konten names erzeugen (nur ohne offenes Initialpasswort, ausser renew).
// Rechte pruefen die Aufrufer; hier gilt zusaetzlich: nur eligible-Konten, nie local/oidc.
func (a *Auth) generate(ctx context.Context, names []string, renew, force, send bool) ([]genRes, error) {
	var res []genRes
	err := a.mutate(ctx, func(m map[string]Account) error {
		res = res[:0]
		for _, n := range names {
			u, ok := m[n]
			switch {
			case !ok || !eligible(u):
				res = append(res, genRes{Name: n, Note: "not eligible"})
				continue
			}
			st := pwState(u)
			switch {
			case !force && (st == "local" || st == "oidc"):
				res = append(res, genRes{Name: n, Note: "skipped: " + st})
				continue
			case !force && st == "init" && !renew:
				res = append(res, genRes{Name: n, Note: "skipped: open initial password"})
				continue
			}
			pw := genInitPw()
			nu, err := setInit(u, pw)
			if err != nil {
				return err
			}
			m[n] = nu
			res = append(res, genRes{Name: n, Mail: u.Mail, PW: pw})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range res {
		if res[i].PW == "" {
			continue
		}
		a.dropWeb(res[i].Name)
		if !send {
			continue
		}
		switch {
		case SendMail == nil || MailOK == nil || !MailOK():
			res[i].Sent = "error: mail not configured"
		case res[i].Mail == "":
			res[i].Sent = "no mail"
		default:
			if err := SendMail(res[i].Mail, "Zugang cs-team", initMail(res[i].Name, res[i].PW)); err != nil {
				res[i].Sent = "error: " + safeName(err.Error())
			} else {
				res[i].Sent = "ok"
			}
		}
	}
	return res, nil
}

func initMail(name, pw string) string {
	until := time.Now().Add(InitDays * 24 * time.Hour).Format("02.01.2006")
	site := oidcCfg.base
	if site != "" {
		site = "\nAdresse: " + site
	}
	return "Ihr Zugang zu cs-team:" + site + "\nAnmeldename: " + name + "\nInitialpasswort: " + pw +
		"\nGueltig bis: " + until + "\n\nBeim ersten Anmelden vergeben Sie ein eigenes Passwort oder verknuepfen Ihr Konto mit einem Anbieter.\n"
}

// ---------- Strecken ----------

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func (a *Auth) initRoutes(mux *http.ServeMux, usr func(http.HandlerFunc) http.Handler) {
	// Liste hochladen (Text), Ergebnis je Zeile
	mux.Handle("POST /api/groups/{g}/userlist", usr(func(w http.ResponseWriter, r *http.Request) {
		g := r.PathValue("g")
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		res, errs, err := a.ImportList(r.Context(), g, string(raw))
		if err != nil {
			if errors.Is(err, ErrNotEligible) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			fail_(w, err)
			return
		}
		log.Printf("auth: user list for group %q by %q: %d rows, %d errors", safeName(g), safeName(User(r.Context())), len(res), len(errs))
		jsonOut(w, map[string]any{"rows": res, "errors": errs})
	}))
	// Liste herunterladen: wieder hochladbar
	mux.Handle("GET /api/groups/{g}/userlist", usr(func(w http.ResponseWriter, r *http.Request) {
		g := r.PathValue("g")
		if !CanManage(r.Context(), g) || !a.groupExists(r.Context(), g) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var b strings.Builder
		for _, n := range a.membersOf(r.Context(), g) {
			u, _ := a.get(r.Context(), n)
			if !eligible(u) {
				continue
			}
			mark := "init:"
			if st := pwState(u); st == "oidc" || st == "local" {
				mark = st + ":"
			}
			fmt.Fprintf(&b, "%s, %s, %s\n", n, u.Mail, mark)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+g+`.txt"`)
		io.WriteString(w, b.String())
	}))
	// Initialpasswoerter fuer die Gruppe erzeugen
	mux.Handle("POST /api/groups/{g}/initpw", usr(func(w http.ResponseWriter, r *http.Request) {
		g := r.PathValue("g")
		var in struct{ All, Send bool }
		if !body(w, r, &in) {
			return
		}
		if !CanManage(r.Context(), g) || !a.groupExists(r.Context(), g) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var names []string
		for _, n := range a.membersOf(r.Context(), g) {
			if IsAdmin(r.Context()) || (a.manages(r.Context(), n) && len(a.adminOf(n)) == 0) {
				names = append(names, n)
			}
		}
		res, err := a.generate(r.Context(), names, in.All, false, in.Send)
		if err != nil {
			fail_(w, err)
			return
		}
		log.Printf("auth: initial passwords for group %q by %q: %d accounts", safeName(g), safeName(User(r.Context())), len(res))
		jsonOut(w, map[string]any{"rows": res})
	}))
	// Zuruecksetzen: wie Erstlogin
	mux.Handle("POST /api/users/{name}/reconfigure", usr(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var in struct{ Send bool }
		if !body(w, r, &in) {
			return
		}
		u, ok := a.get(r.Context(), name)
		if !ok {
			fail_(w, ErrNoUser)
			return
		}
		if !eligible(u) || !a.manages(r.Context(), name) || (!IsAdmin(r.Context()) && len(a.adminOf(name)) > 0) {
			fail_(w, ErrNotEligible)
			return
		}
		res, err := a.generate(r.Context(), []string{name}, true, true, in.Send)
		if err != nil || len(res) != 1 {
			fail_(w, err)
			return
		}
		log.Printf("auth: reconfigure %q by %q", safeName(name), safeName(User(r.Context())))
		jsonOut(w, res[0])
	}))
}
