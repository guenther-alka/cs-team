package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cs-team/store"
)

// RFC 6238, Anhang B (SHA1, Schlüssel "12345678901234567890", 8 Stellen).
func TestTOTPVectorsRFC6238(t *testing.T) {
	key := []byte("12345678901234567890")
	for _, v := range []struct {
		t    int64
		want string
	}{{59, "94287082"}, {1111111109, "07081804"}, {1111111111, "14050471"}, {1234567890, "89005924"}, {2000000000, "69279037"}, {20000000000, "65353130"}} {
		if got := hotp(key, uint64(v.t/30), 8); got != v.want {
			t.Fatalf("T=%d: %s, erwartet %s", v.t, got, v.want)
		}
	}
}

func TestTOTPWindowAndReplay(t *testing.T) {
	sec := newSecret()
	key, _ := b32.DecodeString(sec)
	now := time.Unix(1700000000, 0)
	code := func(at time.Time) string { return hotp(key, uint64(at.Unix()/30), 6) }
	for _, d := range []time.Duration{0, -30 * time.Second, 30 * time.Second} {
		if _, ok := totpCheck(sec, code(now.Add(d)), now, 0); !ok {
			t.Fatalf("Schritt %v muss gelten", d)
		}
	}
	for _, d := range []time.Duration{-60 * time.Second, 60 * time.Second, 24 * time.Hour} {
		if _, ok := totpCheck(sec, code(now.Add(d)), now, 0); ok {
			t.Fatalf("Schritt %v darf nicht gelten", d)
		}
	}
	step, ok := totpCheck(sec, code(now), now, 0)
	if !ok || step != now.Unix()/30 {
		t.Fatal("Schritt:", step)
	}
	if _, ok := totpCheck(sec, code(now), now, step); ok {
		t.Fatal("Wiederholung desselben Codes muss abgelehnt werden")
	}
	if _, ok := totpCheck(sec, code(now.Add(-30*time.Second)), now, step); ok {
		t.Fatal("älterer Schritt nach neuerem darf nicht gelten")
	}
	if _, ok := totpCheck(sec, "12345", now, 0); ok {
		t.Fatal("falsche Länge")
	}
	if _, _, k := splitCode("geheim123456"); k != 't' {
		t.Fatal("split t")
	}
	if p, c, k := splitCode("geheimABCD-EFGH-2345"); k != 'r' || p != "geheim" || c != "ABCD-EFGH-2345" {
		t.Fatal("split r:", p, c, k)
	}
	if _, _, k := splitCode("geheimgeheim"); k != 0 {
		t.Fatal("split 0")
	}
}

func TestTwoFactor(t *testing.T) {
	ForceChange = false
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	totpNow = func() time.Time { return now }
	defer func() { totpNow = time.Now; Enforce2FA = nil }()
	a := New(store.NewMem())
	if err := a.Bootstrap(ctx, "root", "rootrootroot"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "anna", "annaannaanna", false, nil); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	ok := a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	for _, p := range []string{"/webdav/", "/dav/", "/.well-known/caldav", "/api/files", "/api/filesshare/anna/x", "/other", "/api/settings", "/api/groups_x"} {
		mux.Handle(p, ok)
	}
	call := func(method, path, user, pass, ip, body string) (int, string, http.Header) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(user, pass)
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code, w.Body.String(), w.Header()
	}
	code := func(sec string, at time.Time) string { k, _ := b32.DecodeString(sec); return hotp(k, uint64(at.Unix()/30), 6) }
	const pw = "annaannaanna"
	must := func(want int, got int, what string, body string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s: %d (erwartet %d) %s", what, got, want, body)
		}
	}

	// Einrichtung: setup -> activate (Passwort + Code) -> Wiederherstellungscodes
	c, b, _ := call("POST", "/api/me/2fa/setup", "anna", pw, "192.0.2.1", "")
	must(200, c, "setup", b)
	var su struct{ Secret, URI string }
	json.Unmarshal([]byte(b), &su)
	if len(su.Secret) != 32 || !strings.HasPrefix(su.URI, "otpauth://totp/cs-team:anna?secret="+su.Secret) {
		t.Fatal("setup-Antwort:", b)
	}
	c, b, _ = call("POST", "/api/me/2fa/activate", "anna", pw, "192.0.2.1", `{"password":"falsch","code":"`+code(su.Secret, now)+`"}`)
	must(403, c, "activate mit falschem Passwort", b)
	c, b, _ = call("POST", "/api/me/2fa/activate", "anna", pw, "192.0.2.1", `{"password":"`+pw+`","code":"000000"}`)
	must(403, c, "activate mit falschem Code", b)
	c, b, _ = call("POST", "/api/me/2fa/activate", "anna", pw, "192.0.2.1", `{"password":"`+pw+`","code":"`+code(su.Secret, now)+`"}`)
	must(200, c, "activate", b)
	var act struct{ Codes []string }
	json.Unmarshal([]byte(b), &act)
	if len(act.Codes) != 8 || strings.Contains(b, su.Secret) {
		t.Fatal("Wiederherstellungscodes:", b)
	}
	if u, _ := a.get(ctx, "anna"); u.TOTP != su.Secret || len(u.Recovery) != 8 || strings.Contains(strings.Join(u.Recovery, ""), act.Codes[0]) {
		t.Fatal("Konto nach Aktivierung (Codes nur als Hash):", u.Recovery)
	}
	// Audit M1: die aktivierende Sitzung wird NICHT übernommen - nur Passwort gilt danach nirgends mehr (auch nicht von derselben Adresse)
	c, b, _ = call("GET", "/other", "anna", pw, "192.0.2.1", "")
	must(401, c, "nach dem Einschalten genügt das Passwort allein nicht mehr", b)
	c, b, _ = call("GET", "/other", "anna", pw, "192.0.2.2", "")
	must(401, c, "nur Passwort von anderer Adresse", b)
	now = now.Add(30 * time.Second)
	sess := pw + code(su.Secret, now) // neu anmelden mit Passwort + Code
	c, b, _ = call("GET", "/other", "anna", sess, "192.0.2.1", "")
	must(200, c, "neu anmelden mit Passwort + Code", b)

	// Anmeldung mit Passwort + Code (neuer Schritt); gleicher Kopf danach ohne neuen Code; Wiederholung des Codes von anderer Adresse abgelehnt
	now = now.Add(30 * time.Second)
	c1 := pw + code(su.Secret, now)
	c, b, _ = call("GET", "/other", "anna", c1, "192.0.2.3", "")
	must(200, c, "Passwort + Code", b)
	c, b, _ = call("GET", "/other", "anna", c1, "192.0.2.3", "")
	must(200, c, "Sitzung ohne neuen Code", b)
	c, b, _ = call("GET", "/other", "anna", c1, "192.0.2.4", "")
	must(401, c, "Code-Wiederholung von anderer Adresse", b)
	// parallele Anfragen desselben Browsers: der Code wird nicht als "schon benutzt" abgelehnt
	now = now.Add(30 * time.Second)
	c2 := pw + code(su.Secret, now)
	done := make(chan int, 6)
	for i := 0; i < 6; i++ {
		go func() { c, _, _ := call("GET", "/other", "anna", c2, "192.0.2.6", ""); done <- c }()
	}
	for i := 0; i < 6; i++ {
		if c := <-done; c != 200 {
			t.Fatal("parallele Anfrage:", c)
		}
	}

	// falscher Code sieht aus wie falsches Passwort (Status, Text) und zählt für dieselbe Sperre
	c, b, h1 := call("GET", "/other", "anna", pw+"123456", "192.0.2.7", "")
	c0, b0, h0 := call("GET", "/other", "anna", "falschfalsch", "192.0.2.7", "")
	cn, bn, hn := call("GET", "/other", "nobody", "falschfalsch", "192.0.2.7", "")
	if c != 401 || c0 != 401 || cn != 401 || b != b0 || b0 != bn || h1.Get("WWW-Authenticate") != h0.Get("WWW-Authenticate") || h0.Get("WWW-Authenticate") != hn.Get("WWW-Authenticate") {
		t.Fatal("falscher Code unterscheidbar:", c, c0, cn, b, b0, bn)
	}
	now = now.Add(30 * time.Second)
	for i := 0; i < 5; i++ {
		call("GET", "/other", "anna", pw+code(su.Secret, now.Add(240*time.Hour)), "192.0.2.8", "")
	}
	c, b, _ = call("GET", "/other", "anna", pw+code(su.Secret, now), "192.0.2.8", "")
	must(429, c, "Sperre nach Fehlversuchen (auch mit richtigem Code)", b)

	// Wiederherstellungscode: einmal gültig
	rc := pw + act.Codes[0]
	c, b, _ = call("GET", "/other", "anna", rc, "192.0.2.9", "")
	must(200, c, "Wiederherstellungscode", b)
	c, b, _ = call("GET", "/other", "anna", rc, "192.0.2.10", "")
	must(401, c, "Wiederherstellungscode zweimal", b)
	if u, _ := a.get(ctx, "anna"); len(u.Recovery) != 7 {
		t.Fatal("Codes übrig:", len(u.Recovery))
	}

	// App-Passwort: nur für Dateien/Kalender, nie für Konto-/Verwaltungsfunktionen
	c, b, _ = call("POST", "/api/me/apppass", "anna", sess, "192.0.2.1", `{"password":"falsch","name":"Thunderbird"}`)
	must(403, c, "App-Passwort mit falschem Passwort", b)
	c, b, _ = call("POST", "/api/me/apppass", "anna", sess, "192.0.2.1", `{"password":"`+pw+`","name":"Thunderbird"}`)
	must(200, c, "App-Passwort anlegen", b)
	var ap struct{ ID, Name, Password string }
	json.Unmarshal([]byte(b), &ap)
	if len(ap.Password) != 24 || ap.ID != ap.Password[:6] {
		t.Fatal("App-Passwort:", b)
	}
	if u, _ := a.get(ctx, "anna"); strings.Contains(u.AppPw[0].Hash, ap.Password) || !strings.HasPrefix(u.AppPw[0].Hash, "$2") {
		t.Fatal("App-Passwort nicht als bcrypt gespeichert")
	}
	for _, p := range []string{"/webdav/x", "/dav/x", "/.well-known/caldav", "/api/files", "/api/filesshare/anna/x"} {
		c, b, _ = call("GET", p, "anna", ap.Password, "192.0.2.20", "")
		must(200, c, "App-Passwort "+p, b)
	}
	for i, p := range []string{"/api/users", "/api/groups", "/api/settings", "/api/me", "/api/me/2fa", "/other"} {
		ip := "192.0.2." + string(rune('0'+i/10)) + string(rune('0'+i%10)) // eigene Adresse je Pfad (Fehlversuche sperren sonst)
		ip = "198.51.100." + ip[len("192.0.2."):]
		c, b, _ = call("GET", "/webdav/x", "anna", ap.Password, ip, "") // Eintrag kommt in den Speicher
		must(200, c, "App-Passwort "+ip, b)
		c, b, _ = call("GET", p, "anna", ap.Password, ip, "") // dann darf er für Konto-/Verwaltungspfade trotzdem nicht gelten
		must(403, c, "App-Passwort (Speicher) darf nicht für "+p, b)
	}
	for i, p := range []string{"/api/users", "/api/groups", "/api/settings", "/api/me"} {
		c, b, _ = call("GET", p, "anna", ap.Password, "203.0.113."+string(rune('1'+i)), "") // ohne Speicher-Eintrag
		must(403, c, "App-Passwort darf nicht für "+p, b)
	}
	// Audit M3: ein gültiges App-Passwort am falschen Pfad zählt nicht als Fehlversuch (keine Sperre), ein ungültiges schon
	call("GET", "/webdav/x", "anna", ap.Password, "203.0.113.50", "")
	for i := 0; i < 12; i++ {
		c, b, _ = call("GET", "/api/users", "anna", ap.Password, "203.0.113.50", "")
		must(403, c, "App-Passwort falscher Pfad, kein Fehlzähler", b)
	}
	c, b, _ = call("GET", "/webdav/x", "anna", ap.Password, "203.0.113.50", "")
	must(200, c, "App-Passwort danach weiter gültig (nicht gesperrt)", b)
	for i := 0; i < 6; i++ {
		call("GET", "/api/users", "anna", "x"+ap.Password[1:], "203.0.113.51", "")
	}
	c, b, _ = call("GET", "/webdav/x", "anna", ap.Password, "203.0.113.51", "")
	must(429, c, "ungültige App-Passwörter zählen weiter", b)
	c, b, _ = call("GET", "/api/me/2fa", "anna", sess, "192.0.2.1", "")
	must(200, c, "Liste", b)
	if !strings.Contains(b, `"name":"Thunderbird"`) || strings.Contains(b, ap.Password) {
		t.Fatal("Liste der App-Passwörter:", b)
	}
	c, b, _ = call("DELETE", "/api/me/apppass/"+ap.ID, "anna", sess, "192.0.2.1", "")
	must(200, c, "App-Passwort widerrufen", b)
	c, b, _ = call("GET", "/webdav/x", "anna", ap.Password, "192.0.2.20", "")
	must(401, c, "widerrufenes App-Passwort", b)
	c, b, _ = call("GET", "/other", "anna", sess, "192.0.2.1", "")
	must(200, c, "Audit M2: Widerrufen verwirft keine Sitzung", b)

	// Passwortänderung beendet die Sitzung
	if err := a.SetPassword(ctx, "anna", "neuesneuespw"); err != nil {
		t.Fatal(err)
	}
	c, b, _ = call("GET", "/other", "anna", c2, "192.0.2.6", "")
	must(401, c, "Sitzung nach Passwortänderung", b)
	now = now.Add(30 * time.Second)
	c, b, _ = call("GET", "/other", "anna", "neuesneuespw"+code(su.Secret, now), "192.0.2.6", "")
	must(200, c, "neues Passwort + Code", b)

	// Ausschalten: Passwort + Code nötig
	c, b, _ = call("POST", "/api/me/2fa/disable", "anna", "neuesneuespw"+code(su.Secret, now), "192.0.2.6", `{"password":"neuesneuespw","code":"000000"}`)
	must(403, c, "disable mit falschem Code", b)
	now = now.Add(30 * time.Second)
	c, b, _ = call("POST", "/api/me/2fa/disable", "anna", "neuesneuespw"+code(su.Secret, now), "192.0.2.6", `{"password":"neuesneuespw","code":"`+code(su.Secret, now)+`"}`)
	must(403, c, "disable mit schon benutztem Code (Wiederholungsschutz)", b)
	now = now.Add(30 * time.Second)
	c, b, _ = call("POST", "/api/me/2fa/disable", "anna", "neuesneuespw"+code(su.Secret, now.Add(-30*time.Second)), "192.0.2.6", `{"password":"neuesneuespw","code":"`+code(su.Secret, now)+`"}`)
	must(200, c, "disable", b)
	if u, _ := a.get(ctx, "anna"); u.TOTP != "" || len(u.Recovery) != 0 || len(u.AppPw) != 0 {
		t.Fatal("Konto nach disable:", u)
	}
	c, b, _ = call("GET", "/other", "anna", "neuesneuespw", "192.0.2.30", "")
	must(200, c, "ohne 2FA nur Passwort", b)

	// Pflicht für Admins: ohne 2FA nur Konto-Seite, danach frei
	Enforce2FA = func() bool { return true }
	c, b, hh := call("GET", "/api/users", "root", "rootrootroot", "192.0.2.40", "")
	if c != 403 || hh.Get("X-2FA-Required") != "1" {
		t.Fatal("Pflicht: Admin ohne 2FA erreicht", c, b)
	}
	c, b, _ = call("GET", "/api/me", "root", "rootrootroot", "192.0.2.40", "")
	must(200, c, "Konto-Schnittstelle trotz Pflicht", b)
	if !strings.Contains(b, `"need2fa":true`) {
		t.Fatal("/api/me need2fa:", b)
	}
	c, _, _ = call("GET", "/other", "anna", "neuesneuespw", "192.0.2.41", "")
	must(200, c, "Nicht-Admin von der Pflicht nicht betroffen", "")
	c, b, _ = call("POST", "/api/me/2fa/setup", "root", "rootrootroot", "192.0.2.40", "")
	must(200, c, "setup root", b)
	var sr struct{ Secret string }
	json.Unmarshal([]byte(b), &sr)
	now = now.Add(30 * time.Second)
	c, b, _ = call("POST", "/api/me/2fa/activate", "root", "rootrootroot", "192.0.2.40", `{"password":"rootrootroot","code":"`+code(sr.Secret, now)+`"}`)
	must(200, c, "activate root", b)
	c, b, _ = call("GET", "/api/users", "root", "rootrootroot", "192.0.2.40", "")
	must(401, c, "nach dem Einschalten genügt das Passwort allein nicht mehr", b)
	now = now.Add(30 * time.Second)
	rootSess := "rootrootroot" + code(sr.Secret, now)
	c, b, _ = call("GET", "/api/users", "root", rootSess, "192.0.2.40", "")
	must(200, c, "Admin nach Einrichtung (Passwort + Code)", b)
	c, b, _ = call("POST", "/api/me/2fa/disable", "root", rootSess, "192.0.2.40", `{"password":"rootrootroot","code":"`+code(sr.Secret, now)+`"}`)
	must(400, c, "Admin darf bei Pflicht nicht ausschalten", b)
	Enforce2FA = nil

	// Admin setzt die 2FA eines anderen Kontos zurück (verlorenes Gerät); das eigene nicht; Benutzer nicht
	c, b, _ = call("POST", "/api/me/2fa/setup", "anna", "neuesneuespw", "192.0.2.50", "")
	must(200, c, "setup anna", b)
	json.Unmarshal([]byte(b), &su)
	now = now.Add(30 * time.Second)
	c, b, _ = call("POST", "/api/me/2fa/activate", "anna", "neuesneuespw", "192.0.2.50", `{"password":"neuesneuespw","code":"`+code(su.Secret, now)+`"}`)
	must(200, c, "activate anna", b)
	c, b, _ = call("GET", "/api/users", "root", rootSess, "192.0.2.40", "")
	must(200, c, "Audit M2: fremdes Einschalten wirft root nicht hinaus", b)
	now = now.Add(30 * time.Second)
	c, b, _ = call("POST", "/api/users/anna/2fa/reset", "anna", "neuesneuespw"+code(su.Secret, now), "192.0.2.50", "")
	must(403, c, "Benutzer darf nicht zurücksetzen", b)
	now = now.Add(30 * time.Second)
	rootPw := "rootrootroot" + code(sr.Secret, now) // root hat 2FA: Passwort + Code (die Sitzungen wurden beim Einschalten bei anna verworfen)
	c, b, _ = call("POST", "/api/users/root/2fa/reset", "root", rootPw, "192.0.2.60", "")
	must(400, c, "eigenes Konto darf nicht per reset zurückgesetzt werden", b)
	c, b, _ = call("GET", "/api/users", "root", rootPw, "192.0.2.60", "")
	if !strings.Contains(b, `"name":"anna"`) || !strings.Contains(b, `"totp":true`) {
		t.Fatal("Benutzerliste zeigt 2FA nicht:", b)
	}
	c, b, _ = call("POST", "/api/users/anna/2fa/reset", "root", rootPw, "192.0.2.60", "")
	must(200, c, "reset durch Admin", b)
	c, b, _ = call("GET", "/other", "anna", "neuesneuespw", "192.0.2.52", "")
	must(200, c, "anna nach reset nur mit Passwort", b)

	// Sperren beendet die Sitzung sofort (Zwischenspeicher)
	c, b, _ = call("POST", "/api/me/2fa/setup", "anna", "neuesneuespw", "192.0.2.52", "")
	json.Unmarshal([]byte(b), &su)
	now = now.Add(30 * time.Second)
	call("POST", "/api/me/2fa/activate", "anna", "neuesneuespw", "192.0.2.52", `{"password":"neuesneuespw","code":"`+code(su.Secret, now)+`"}`)
	now = now.Add(30 * time.Second)
	sessA := "neuesneuespw" + code(su.Secret, now)
	c, b, _ = call("GET", "/other", "anna", sessA, "192.0.2.52", "")
	must(200, c, "Sitzung vor dem Sperren", b)
	d := true
	if err := a.SetFlags(ctx, "anna", nil, &d); err != nil {
		t.Fatal(err)
	}
	c, b, _ = call("GET", "/other", "anna", sessA, "192.0.2.52", "")
	must(401, c, "Sitzung nach dem Sperren", b)

	// Kommandozeile (SetUser) entfernt die 2FA: Notfallzugang
	if err := a.SetUser(ctx, "anna", "dritteszweites", false); err != nil {
		t.Fatal(err)
	}
	if u, _ := a.get(ctx, "anna"); u.TOTP != "" || u.Disabled {
		t.Fatal("SetUser muss 2FA entfernen und freischalten")
	}
}
