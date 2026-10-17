package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"cs-team/store"
)

func TestParseList(t *testing.T) {
	rows, errs := parseList(`# Klasse 7
org: Meine.Einrichtung.de
peter.pan
Petra.Ost, petra@privat.example, init:
karl@other.org,, local:
anna, , Start-Passwort-1
anna
bad name
org: kaputt
tab.user	tab@x.example
`)
	got := map[string]listRow{}
	for _, r := range rows {
		got[r.Name] = r
	}
	if len(rows) != 5 {
		t.Fatalf("Zeilen: %+v", rows)
	}
	if r := got["peter.pan@meine.einrichtung.de"]; r.Mail != "" || r.PW != "" {
		t.Fatalf("peter: %+v", r)
	}
	if r := got["petra.ost@meine.einrichtung.de"]; r.Mail != "petra@privat.example" || r.PW != "init:" {
		t.Fatalf("petra: %+v", r)
	}
	if r := got["karl@other.org"]; r.PW != "local:" {
		t.Fatalf("karl: %+v", r)
	}
	if r := got["anna@meine.einrichtung.de"]; r.PW != "Start-Passwort-1" {
		t.Fatalf("anna: %+v", r)
	}
	if r := got["tab.user@meine.einrichtung.de"]; r.Mail != "tab@x.example" {
		t.Fatalf("tab: %+v", r)
	}
	// doppelte Zeile, ungueltiger Name, ungueltiges org
	if len(errs) != 3 {
		t.Fatalf("Fehler: %v", errs)
	}
	for _, c := range []struct {
		in   string
		keep bool
	}{{"", true}, {"init:", true}, {"LOCAL:", true}, {"oidc:", true}, {"Start-1234", false}} {
		if _, keep := listPW(c.in); keep != c.keep {
			t.Fatalf("listPW(%q)", c.in)
		}
	}
}

func TestLocalAddressNames(t *testing.T) {
	ctx := context.Background()
	oldF, oldC := ForceChange, LoginForm
	ForceChange, LoginForm = false, true
	defer func() { ForceChange, LoginForm = oldF, oldC }()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "root", "Root-Passwort-1x"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "name@other.org", "Passwort-name-77x", false, nil); err != nil {
		t.Fatal(err)
	}
	u, ok := a.get(ctx, "name@other.org")
	if !ok || u.Mail != "name@other.org" || u.Source == "dir" || !isLocalKey("name@other.org", u) {
		t.Fatalf("Konto: %+v", u)
	}
	// eindeutig
	if err := a.AddUser(ctx, "name@other.org", "Passwort-name-77x", false, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("doppelt: %v", err)
	}
	// ungueltige Namen und der reservierte Namensraum
	for _, bad := range []string{"a@b", "x y@other.org", "@other.org", "x@@other.org", "x@other..org", "x@-other.org"} {
		if err := a.AddUser(ctx, bad, "Passwort-bad-77x", false, nil); !errors.Is(err, ErrBadName) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if err := a.AddUser(ctx, "x@local", "Passwort-bad-77x", false, nil); !errors.Is(err, ErrBadRealm) {
		t.Fatalf("Namensraum local: %v", err)
	}
	// Anmeldung als lokales Konto (Basic, Gross-/Kleinschreibung egal), kein Namensraum-Fehler
	mux := http.NewServeMux()
	a.Routes(mux)
	for _, n := range []string{"name@other.org", "NAME@Other.Org"} {
		w := webReq(mux, "POST", "/api/login", `{"name":"`+n+`","password":"Passwort-name-77x"}`, "", nil)
		if w.Code != 200 || cookieOf(w) == "" {
			t.Fatalf("Anmeldung %q: %d %s", n, w.Code, w.Body.String())
		}
	}
	if w := webReq(mux, "POST", "/api/login", `{"name":"name@other.org","password":"falsch-falsch-1"}`, "", nil); w.Code != 401 {
		t.Fatalf("falsches Passwort: %d", w.Code)
	}
	// ein lokaler Admin mit Adresse zaehlt als lokaler Admin (Notfallzugang)
	if err := a.AddUser(ctx, "boss@other.org", "Passwort-boss-77x", true, nil); err != nil {
		t.Fatal(err)
	}
	if n := a.LocalAdmins(ctx); n != 2 {
		t.Fatalf("lokale Admins: %d", n)
	}
	// ein Verzeichnis darf ein vorhandenes lokales Konto nie uebernehmen
	_, err := a.ensureDir(ctx, loginID{Key: "name@other.org", Name: "name", Realm: "other.org", Source: "dir"}, DirUser{Mail: "x@y.z"}, "Egal-egal-1")
	if err == nil {
		t.Fatal("Verzeichnis uebernimmt lokales Konto")
	}
	if u2, _ := a.get(ctx, "name@other.org"); u2.Hash != u.Hash || u2.Source == "dir" {
		t.Fatalf("lokales Konto veraendert: %+v", u2)
	}
}

type listEnv struct {
	a    *Auth
	mux  *http.ServeMux
	root string
}

func newListEnv(t *testing.T) *listEnv {
	ctx := context.Background()
	oldF, oldC, oldS, oldM := ForceChange, LoginForm, SendMail, MailOK
	ForceChange, LoginForm = false, true
	t.Cleanup(func() { ForceChange, LoginForm, SendMail, MailOK = oldF, oldC, oldS, oldM })
	a := New(store.NewMem())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(a.Bootstrap(ctx, "root", "Root-Passwort-1x"))
	for _, n := range []string{"ga", "ga2"} {
		must(a.AddUser(ctx, n, "Passwort-"+n+"-77x", false, nil))
	}
	must(a.SetGroup(ctx, "klasse7", []string{"files"}, nil))
	must(a.SetGroup(ctx, "klasse8", []string{"files"}, nil))
	must(a.SetGroupAdmins(ctx, "klasse7", []string{"ga"}))
	must(a.SetGroupAdmins(ctx, "klasse8", []string{"ga2"}))
	e := &listEnv{a: a, mux: http.NewServeMux()}
	a.Routes(e.mux)
	return e
}

func (e *listEnv) tok(t *testing.T, name, pw string) string {
	t.Helper()
	tok := cookieOf(webReq(e.mux, "POST", "/api/login", `{"name":"`+name+`","password":"`+pw+`"}`, "", nil))
	if tok == "" {
		t.Fatalf("Anmeldung %s", name)
	}
	return tok
}

func TestListImportGenerateReconfigure(t *testing.T) {
	e := newListEnv(t)
	ctx := context.Background()
	rt := e.tok(t, "root", "Root-Passwort-1x")
	gt := e.tok(t, "ga", "Passwort-ga-77x")
	g2 := e.tok(t, "ga2", "Passwort-ga2-77x")

	list := "org: school.example\npeter.pan\npetra.ost, petra@privat.example\nlocal.kid, , local:\nfest, , Start-Passwort-9z\n"
	// fremder Gruppen-Admin / Nicht-Admin: verboten
	if w := webReq(e.mux, "POST", "/api/groups/klasse7/userlist", list, g2, nil); w.Code != 403 {
		t.Fatalf("fremder Gruppen-Admin: %d", w.Code)
	}
	if w := webReq(e.mux, "POST", "/api/groups/klasse7/userlist", list, "", nil); w.Code != 401 {
		t.Fatalf("ohne Anmeldung: %d", w.Code)
	}
	if w := webReq(e.mux, "POST", "/api/groups/gibtesnicht/userlist", list, rt, nil); w.Code != 403 {
		t.Fatalf("unbekannte Gruppe: %d", w.Code)
	}
	w := webReq(e.mux, "POST", "/api/groups/klasse7/userlist", list, gt, nil)
	if w.Code != 200 {
		t.Fatalf("Import: %d %s", w.Code, w.Body.String())
	}
	var imp struct {
		Rows   []listRes
		Errors []string
	}
	json.Unmarshal(w.Body.Bytes(), &imp)
	if len(imp.Rows) != 4 || len(imp.Errors) != 0 {
		t.Fatalf("Ergebnis: %s", w.Body.String())
	}
	pp, _ := e.a.get(ctx, "peter.pan@school.example")
	if pp.Mail != "peter.pan@school.example" || pp.Hash != "" || pwState(pp) != "none" || !inGroup(pp, "klasse7") {
		t.Fatalf("peter: %+v", pp)
	}
	if po, _ := e.a.get(ctx, "petra.ost@school.example"); po.Mail != "petra@privat.example" {
		t.Fatalf("petra: %+v", po)
	}
	if f, _ := e.a.get(ctx, "fest@school.example"); pwState(f) != "init" || f.InitExp == "" || strings.Contains(f.Hash, "Start-Passwort") {
		t.Fatalf("fest: %+v", f)
	}
	// ausdrueckliches Passwort ohne Klartext in der Antwort
	if strings.Contains(w.Body.String(), "Start-Passwort-9z") {
		t.Fatal("Klartext in der Antwort")
	}
	// das ausdrueckliche Initialpasswort funktioniert, das Konto muss es aendern (Must)
	if tok := e.tok(t, "fest@school.example", "Start-Passwort-9z"); tok == "" {
		t.Fatal("Anmeldung mit Initialpasswort")
	}

	// Erzeugen: nur Konten ohne Passwort; der offene Fall (fest) und local bleiben, keine Admins
	if err := e.a.mutate(ctx, func(m map[string]Account) error {
		x := m["local.kid@school.example"]
		h, _ := hash("Eigenes-Passwort-7")
		x.Hash, x.Must = h, false
		m["local.kid@school.example"] = x
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w = webReq(e.mux, "POST", "/api/groups/klasse7/initpw", `{}`, gt, nil)
	var gen struct{ Rows []genRes }
	json.Unmarshal(w.Body.Bytes(), &gen)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Erzeugen: %d %v", w.Code, w.Header())
	}
	pws := map[string]string{}
	for _, r := range gen.Rows {
		if r.PW != "" {
			pws[r.Name] = r.PW
		}
	}
	if len(pws) != 2 || pws["peter.pan@school.example"] == "" || pws["petra.ost@school.example"] == "" {
		t.Fatalf("erzeugt: %s", w.Body.String())
	}
	if pws["peter.pan@school.example"] == pws["petra.ost@school.example"] {
		t.Fatal("Passwoerter muessen individuell sein")
	}
	for n, pw := range pws {
		if len(pw) != 11 || pw[5] != '-' {
			t.Fatalf("Format: %q", pw)
		}
		if tok := e.tok(t, n, pw); tok == "" {
			t.Fatalf("Anmeldung mit erzeugtem Passwort: %s", n)
		}
	}
	// zweiter Lauf: nichts Neues (offene Initialpasswoerter bleiben), mit "all" werden sie neu erzeugt
	w = webReq(e.mux, "POST", "/api/groups/klasse7/initpw", `{}`, gt, nil)
	if strings.Contains(w.Body.String(), `"pw"`) {
		t.Fatalf("zweiter Lauf erzeugt: %s", w.Body.String())
	}
	w = webReq(e.mux, "POST", "/api/groups/klasse7/initpw", `{"all":true}`, gt, nil)
	gen.Rows = nil
	json.Unmarshal(w.Body.Bytes(), &gen)
	n := 0
	for _, r := range gen.Rows {
		if r.PW != "" {
			n++
			if r.PW == pws[r.Name] {
				t.Fatal("neu erzeugt muss anders sein")
			}
		}
	}
	if n != 3 { // peter, petra, fest (offen); local.kid bleibt
		t.Fatalf("all: %d %s", n, w.Body.String())
	}
	if u, _ := e.a.get(ctx, "local.kid@school.example"); pwState(u) != "local" {
		t.Fatalf("local.kid: %s", pwState(u))
	}
	// ein Gruppen-Admin fasst nie Admins an; ein fremder Gruppen-Admin nichts
	if w := webReq(e.mux, "POST", "/api/groups/klasse7/initpw", `{}`, g2, nil); w.Code != 403 {
		t.Fatalf("fremder Gruppen-Admin: %d", w.Code)
	}

	// Mailversand: Hook, Absender kommt aus der SMTP-Einstellung; ohne Adresse "no mail"
	var sent []string
	SendMail = func(to, subj, body string) error {
		sent = append(sent, to+"|"+body)
		if to == "petra@privat.example" {
			return errors.New("550 rejected")
		}
		return nil
	}
	MailOK = func() bool { return true }
	e.a.mutate(ctx, func(m map[string]Account) error {
		x := m["peter.pan@school.example"]
		x.Mail = ""
		m["peter.pan@school.example"] = x
		return nil
	})
	w = webReq(e.mux, "POST", "/api/groups/klasse7/initpw", `{"all":true,"send":true}`, gt, nil)
	gen.Rows = nil
	json.Unmarshal(w.Body.Bytes(), &gen)
	st := map[string]string{}
	for _, r := range gen.Rows {
		if r.PW != "" {
			st[r.Name] = r.Sent
		}
	}
	if st["peter.pan@school.example"] != "no mail" || !strings.HasPrefix(st["petra.ost@school.example"], "error:") || st["fest@school.example"] != "ok" {
		t.Fatalf("Versand: %v", st)
	}
	if len(sent) != 2 || !strings.Contains(sent[0]+sent[1], "Initialpasswort: ") {
		t.Fatalf("gesendet: %v", sent)
	}

	// Zuruecksetzen: Gruppen-Admin darf sein Mitglied, nicht ein fremdes; Admins nie
	if w := webReq(e.mux, "POST", "/api/users/fest@school.example/reconfigure", `{}`, gt, nil); w.Code != 200 {
		t.Fatalf("Gruppen-Admin setzt Mitglied zurueck: %d %s", w.Code, w.Body.String())
	}
	if w := webReq(e.mux, "POST", "/api/users/fest@school.example/reconfigure", `{}`, g2, nil); w.Code == 200 {
		t.Fatalf("fremder Gruppen-Admin setzt zurueck: %d", w.Code)
	}
	if w := webReq(e.mux, "POST", "/api/users/ga/reconfigure", `{}`, gt, nil); w.Code == 200 {
		t.Fatalf("Gruppen-Admin setzt sich/anderen Gruppen-Admin zurueck: %d", w.Code)
	}
	if w := webReq(e.mux, "POST", "/api/users/root/reconfigure", `{}`, rt, nil); w.Code != 400 {
		t.Fatalf("Admin: %d", w.Code)
	}

	// Export ist wieder hochladbar und enthaelt keine Passwoerter
	w = webReq(e.mux, "GET", "/api/groups/klasse7/userlist", "", gt, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "local.kid@school.example, local.kid@school.example, local:") || strings.Contains(w.Body.String(), pws["peter.pan@school.example"]) {
		t.Fatalf("Export: %d %s", w.Code, w.Body.String())
	}

	// zweiter Import (Jahreswechsel-Korrektur): vorhandene Konten werden aufgenommen, Zustand bleibt; fremde Gruppe verboten
	w = webReq(e.mux, "POST", "/api/groups/klasse8/userlist", "fest@school.example\nneu.kid@school.example\nroot\nga\n", g2, nil)
	var imp2 struct{ Rows []listRes }
	json.Unmarshal(w.Body.Bytes(), &imp2)
	act := map[string]string{}
	for _, r := range imp2.Rows {
		act[r.Name] = r.Action
	}
	if act["fest@school.example"] != "skipped" || act["neu.kid@school.example"] != "new" || act["root"] != "skipped" || act["ga"] != "skipped" {
		t.Fatalf("zweiter Import: %s", w.Body.String())
	}
	// ein Namensraum des Verzeichnisses / local ist reserviert
	w = webReq(e.mux, "POST", "/api/groups/klasse8/userlist", "x@local\n", g2, nil)
	if !strings.Contains(w.Body.String(), "bad name") || strings.Contains(w.Body.String(), "new") {
		t.Fatalf("Namensraum: %s", w.Body.String())
	}
	if _, ok := e.a.get(ctx, "x@local"); ok {
		t.Fatal("x@local darf nicht entstehen")
	}
}
