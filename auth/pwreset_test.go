package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cs-team/store"
)

func TestPasswordForgot(t *testing.T) {
	ctx := context.Background()
	oldF, oldC, oldS, oldM, oldP := ForceChange, LoginForm, SendMail, MailOK, PublicURL
	ForceChange, LoginForm = true, true
	mails := make(chan string, 8)
	SendMail = func(to, subject, body string) error { mails <- to + "\n" + body; return nil }
	MailOK = func() bool { return true }
	PublicURL = func() string { return "https://team.example" }
	defer func() { ForceChange, LoginForm, SendMail, MailOK, PublicURL = oldF, oldC, oldS, oldM, oldP }()

	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "root", "Root-Passwort-1x"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "anna", "Anna-Passwort-77x", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "boss", "Boss-Passwort-77x", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "otto", "Otto-Passwort-77x", false, nil); err != nil {
		t.Fatal(err)
	}
	for n, mail := range map[string]string{"anna": "anna@x.example", "boss": "boss@x.example", "root": "root@x.example", "otto": "otto@x.example"} {
		n, mail := n, mail
		if err := a.mutate(ctx, func(m map[string]Account) error {
			u := m[n]
			u.Mail = mail
			if n == "otto" {
				u.TOTP = "JBSWY3DPEHPK3PXP" // Zwei-Faktor: kein Zugang per Mail
			}
			m[n] = u
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	forgot := func(q string) int {
		return webReq(mux, "POST", "/api/pwforgot", `{"name":"`+q+`"}`, "", nil).Code
	}
	next := func() string {
		select {
		case m := <-mails:
			return m
		case <-time.After(300 * time.Millisecond):
			return ""
		}
	}

	// info: Funktion angeboten
	if w := webReq(mux, "GET", "/api/login/info", "", "", nil); !strings.Contains(w.Body.String(), `"forgot":true`) {
		t.Fatalf("info: %s", w.Body.String())
	}
	// cross-site: abgelehnt
	if w := webReq(mux, "POST", "/api/pwforgot", `{"name":"anna"}`, "", map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != 403 {
		t.Fatalf("cross-site: %d", w.Code)
	}
	// unbekannt, Admin, Sysadmin, Zwei-Faktor: gleiche Antwort, keine Mail
	for _, q := range []string{"gibtesnicht", "boss", "root", "otto", "boss@x.example"} {
		if c := forgot(q); c != 200 {
			t.Fatalf("%s: %d", q, c)
		}
		if m := next(); m != "" {
			t.Fatalf("%s: unerwartete Mail %s", q, m)
		}
	}
	// anna per Mailadresse
	if c := forgot("Anna@X.example"); c != 200 {
		t.Fatalf("anna: %d", c)
	}
	m := next()
	i := strings.Index(m, "https://team.example/#reset=anna~")
	if !strings.HasPrefix(m, "anna@x.example\n") || i < 0 {
		t.Fatalf("Mail: %q", m)
	}
	if !strings.Contains(m, "Password reset for cs-team") || !strings.Contains(m, "Account: anna") { // ohne Sprache im Konto: Englisch
		t.Fatalf("Mail nicht englisch: %q", m)
	}
	tok := strings.Fields(m[i:])[0]
	tok = tok[strings.Index(tok, "~")+1:]
	// zweite Anfrage gleich danach: keine zweite Mail
	forgot("anna")
	if m2 := next(); m2 != "" {
		t.Fatalf("zweite Mail: %s", m2)
	}
	use := func(name, token string) (int, string) {
		w := webReq(mux, "POST", "/api/pwreset", `{"name":"`+name+`","token":"`+token+`"}`, "", nil)
		return w.Code, cookieOf(w)
	}
	if c, ck := use("anna", "falsch"); c != 403 || ck != "" {
		t.Fatalf("falscher Token: %d", c)
	}
	if c, _ := use("boss", tok); c != 403 {
		t.Fatalf("Token fuer anderes Konto: %d", c)
	}
	c, ck := use("anna", tok)
	if c != 200 || ck == "" {
		t.Fatalf("Token einloesen: %d", c)
	}
	// alte Passwort gilt nicht mehr, Sitzung steht in der Aenderungspflicht, Passwort setzen geht ohne altes
	if w := webReq(mux, "POST", "/api/login", `{"name":"anna","password":"Anna-Passwort-77x"}`, "", nil); w.Code == 200 {
		t.Fatal("altes Passwort gilt noch")
	}
	if w := webReq(mux, "GET", "/api/me", "", ck, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"must":true`) {
		t.Fatalf("/api/me: %d %s", w.Code, w.Body.String())
	}
	if w := webReq(mux, "GET", "/api/users", "", ck, nil); w.Code == 200 {
		t.Fatal("Aenderungspflicht umgangen")
	}
	if w := webReq(mux, "POST", "/api/me/password", `{"old":"","new":"Neues-Passwort-9x"}`, ck, nil); w.Code != 200 {
		t.Fatalf("Passwort setzen: %d %s", w.Code, w.Body.String())
	}
	if w := webReq(mux, "POST", "/api/login", `{"name":"anna","password":"Neues-Passwort-9x"}`, "", nil); w.Code != 200 {
		t.Fatalf("Anmeldung mit neuem Passwort: %d", w.Code)
	}
	// Token ist verbraucht
	if c, _ := use("anna", tok); c != 403 {
		t.Fatalf("Token zweimal: %d", c)
	}
	// Passwortaenderung loescht offene Links
	if err := a.mutate(ctx, func(m map[string]Account) error { u := m["anna"]; u.Lang = "de"; m["anna"] = u; return nil }); err != nil {
		t.Fatal(err)
	}
	forgot("anna")
	m3 := next()
	if !strings.Contains(m3, "Konto: anna") { // eingestellte Sprache des Kontos
		t.Fatalf("Mail nicht deutsch: %q", m3)
	}
	j := strings.Index(m3, "~")
	if j < 0 {
		t.Fatalf("dritte Mail fehlt: %q", m3)
	}
	tok2 := strings.Fields(m3[j+1:])[0]
	if err := a.SetPassword(ctx, "anna", "Wieder-Neu-12345"); err != nil {
		t.Fatal(err)
	}
	if c, _ := use("anna", tok2); c != 403 {
		t.Fatalf("Link nach Passwortaenderung: %d", c)
	}
}
