package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"cs-team/store"
)

func TestOIDCGuiSettings(t *testing.T) {
	ctx := context.Background()
	oldF, oldC, oldP, oldB, oldV, oldG := ForceChange, LoginForm, PublicURL, oidcCfg.base, oidcCfg.provs, oidcCfg.gui
	ForceChange, LoginForm, PublicURL, oidcCfg.base, oidcCfg.provs, oidcCfg.gui = false, true, nil, "", nil, nil
	defer func() {
		ForceChange, LoginForm, PublicURL, oidcCfg.base, oidcCfg.provs, oidcCfg.gui = oldF, oldC, oldP, oldB, oldV, oldG
	}()
	st := store.NewMem()
	a := New(st)
	if err := a.Bootstrap(ctx, "root", "Root-Passwort-1x"); err != nil {
		t.Fatal(err)
	}
	if err := a.AddUser(ctx, "anna", "Anna-Passwort-77x", false, nil); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	as := func(user, pw string) map[string]string {
		return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pw)), "Sec-Fetch-Mode": "", "Sec-Fetch-Site": ""}
	}
	root, anna := as("root", "Root-Passwort-1x"), as("anna", "Anna-Passwort-77x")
	info := func() string { return webReq(mux, "GET", "/api/login/info", "", "", nil).Body.String() }
	post := func(h map[string]string, js string) int { return webReq(mux, "POST", "/api/settings/oidc", js, "", h).Code }

	// nur Admins
	if c := post(anna, `{}`); c != 403 {
		t.Fatalf("Benutzer darf nicht speichern: %d", c)
	}
	if w := webReq(mux, "GET", "/api/settings/oidc", "", "", anna); w.Code != 403 {
		t.Fatalf("Benutzer darf nicht lesen: %d", w.Code)
	}
	// Google ausgefuellt, aber noch keine oeffentliche https-Adresse: nicht angeboten
	if c := post(root, `{"googleId":"gid.apps.example","googleSecret":"gsec-geheim"}`); c != 200 {
		t.Fatalf("speichern: %d", c)
	}
	w := webReq(mux, "GET", "/api/settings/oidc", "", "", root)
	if !strings.Contains(w.Body.String(), `"googleOn":true`) || !strings.Contains(w.Body.String(), `"googleSet":true`) || strings.Contains(w.Body.String(), "gsec-geheim") || !strings.Contains(w.Body.String(), `"redirect":""`) {
		t.Fatalf("GET: %s", w.Body.String())
	}
	if !strings.Contains(info(), `"oidc":[]`) {
		t.Fatalf("ohne Adresse darf nichts angeboten werden: %s", info())
	}
	// http-Adresse (nicht localhost): weiterhin nichts
	PublicURL = func() string { return "http://nas.example:9004" }
	if !strings.Contains(info(), `"oidc":[]`) {
		t.Fatalf("http: %s", info())
	}
	// https: Google wird angeboten, Rueckkehradresse steht in den Einstellungen
	PublicURL = func() string { return "https://team.example" }
	if i := info(); !strings.Contains(i, `"id":"google"`) || strings.Contains(i, `"id":"ms"`) || !strings.Contains(i, `"forgot":false`) {
		t.Fatalf("Google: %s", i)
	}
	if w = webReq(mux, "GET", "/api/settings/oidc", "", "", root); !strings.Contains(w.Body.String(), `"redirect":"https://team.example/oidc/callback"`) {
		t.Fatalf("Rueckkehradresse: %s", w.Body.String())
	}
	// Microsoft: ohne Mandant unvollstaendig, common/organizations/consumers abgelehnt, mit Mandant aktiv
	if c := post(root, `{"googleId":"gid.apps.example","msId":"mid","msSecret":"msec"}`); c != 200 {
		t.Fatalf("ms ohne Mandant: %d", c)
	}
	if strings.Contains(info(), `"id":"ms"`) {
		t.Fatal("Microsoft ohne Mandanten-ID darf nicht angeboten werden")
	}
	for _, bad := range []string{"common", "organizations", "consumers", "a b", "x/y"} {
		if c := post(root, `{"msTenant":"`+bad+`","msId":"mid"}`); c != 400 {
			t.Fatalf("Mandant %q: %d", bad, c)
		}
	}
	if c := post(root, `{"googleId":"gid.apps.example","msTenant":"11111111-2222-3333-4444-555555555555","msId":"mid"}`); c != 200 { // Secret leer = unveraendert
		t.Fatalf("ms mit Mandant: %d", c)
	}
	if i := info(); !strings.Contains(i, `"id":"google"`) || !strings.Contains(i, `"id":"ms"`) {
		t.Fatalf("beide: %s", i)
	}
	if p := oidcProv("ms"); p == nil || p.Issuer != "https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v2.0" || p.Secret != "msec" {
		t.Fatalf("ms-Anbieter: %+v", p)
	}
	// nach Neustart (neue Auth auf demselben Speicher) sind die Werte da
	oidcCfg.gui = nil
	a2 := New(st)
	mux2 := http.NewServeMux()
	a2.Routes(mux2)
	if i := webReq(mux2, "GET", "/api/login/info", "", "", nil).Body.String(); !strings.Contains(i, `"id":"google"`) || !strings.Contains(i, `"id":"ms"`) {
		t.Fatalf("nach Neustart: %s", i)
	}
	// entfernen
	if c := post(root, `{"clearGoogle":true,"clearMS":true}`); c != 200 {
		t.Fatalf("entfernen: %d", c)
	}
	if !strings.Contains(info(), `"oidc":[]`) {
		t.Fatalf("nach Entfernen: %s", info())
	}
	// Startparameter bleiben wirksam, wenn nichts eingestellt ist
	oidcCfg.provs = []*OIDCProvider{{ID: "keycloak", Name: "Keycloak", Issuer: "https://kc.example/realms/x", ClientID: "c", Secret: "s", Scope: "openid"}}
	if !strings.Contains(info(), `"id":"keycloak"`) {
		t.Fatalf("Startparameter: %s", info())
	}
}
