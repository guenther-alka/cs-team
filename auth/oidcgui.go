package auth

// OIDC-Anbieter in den Einstellungen (0.62): Google und Microsoft ohne Startparameter und ohne Neustart.
//
// Der Admin traegt Client-ID und Secret ein (Microsoft zusaetzlich die feste Mandanten-ID); die Rueckkehradresse
// (BASE/oidc/callback) zeigt die Oberflaeche an. BASE ist CS_OIDC_BASE, sonst die oeffentliche Adresse aus den Einstellungen
// (Netzwerk); sie muss https sein (oder localhost). Ein Anbieter ist aktiv, sobald seine Felder vollstaendig sind: dann steht er
// auf der Anmeldeseite und bei "Passwort waehlen oder mit Anbieter verknuepfen" zur Wahl. Eingestellte Werte gehen den
// Startparametern (CS_OIDC_*) vor; das Secret wird nie wieder ausgeliefert.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"cs-team/store"
)

const oidcGuiKey = "oidc-settings.json"

type oidcGuiCfg struct {
	GoogleID     string `json:"googleId,omitempty"`
	GoogleSecret string `json:"googleSecret,omitempty"`
	MSTenant     string `json:"msTenant,omitempty"`
	MSID         string `json:"msId,omitempty"`
	MSSecret     string `json:"msSecret,omitempty"`
}

var oidcGuiMu sync.RWMutex

func oidcBaseOK(b string) bool {
	return strings.HasPrefix(b, "https://") || strings.HasPrefix(b, "http://localhost") || strings.HasPrefix(b, "http://127.0.0.1")
}

// oidcBase: Basisadresse fuer die Rueckkehradresse - CS_OIDC_BASE, sonst die oeffentliche Adresse; leer, wenn keine brauchbare da ist.
func oidcBase() string {
	b := oidcCfg.base
	if b == "" && PublicURL != nil {
		b = strings.TrimRight(strings.TrimSpace(PublicURL()), "/")
	}
	if !oidcBaseOK(b) {
		return ""
	}
	return b
}

// oidcProvList: wirksame Anbieter - aus den Einstellungen, dazu die aus den Startparametern (andere Kennungen).
func oidcProvList() []*OIDCProvider {
	if !LoginForm || oidcBase() == "" {
		return nil
	}
	oidcGuiMu.RLock()
	gui := oidcCfg.gui
	oidcGuiMu.RUnlock()
	out := append([]*OIDCProvider{}, gui...)
	for _, p := range oidcCfg.provs {
		dup := false
		for _, g := range gui {
			if g.ID == p.ID {
				dup = true
			}
		}
		if !dup {
			out = append(out, p)
		}
	}
	return out
}

var (
	reOIDCID     = regexp.MustCompile(`^[A-Za-z0-9._~@:/+=-]{0,300}$`)
	reOIDCTenant = regexp.MustCompile(`^[A-Za-z0-9.-]{1,100}$`)
)

// build: aus den Einstellungen die aktiven (vollstaendigen) Anbieter bilden.
func (c oidcGuiCfg) build() []*OIDCProvider {
	var out []*OIDCProvider
	if c.GoogleID != "" && c.GoogleSecret != "" {
		out = append(out, &OIDCProvider{ID: "google", Name: "Google", Issuer: "https://accounts.google.com", ClientID: c.GoogleID, Secret: c.GoogleSecret, Scope: "openid"})
	}
	if c.MSTenant != "" && c.MSID != "" && c.MSSecret != "" {
		out = append(out, &OIDCProvider{ID: "ms", Name: "Microsoft", Issuer: "https://login.microsoftonline.com/" + c.MSTenant + "/v2.0", ClientID: c.MSID, Secret: c.MSSecret, Scope: "openid"})
	}
	return out
}

func (a *Auth) oidcGuiLoad(ctx context.Context) oidcGuiCfg {
	var c oidcGuiCfg
	b, _, err := a.st.Get(ctx, oidcGuiKey)
	if err == nil {
		json.Unmarshal(b, &c)
	} else if !errors.Is(err, store.ErrNotFound) {
		log.Printf("auth: oidc settings not readable: %v", err)
	}
	oidcGuiMu.Lock()
	oidcCfg.gui = c.build()
	oidcGuiMu.Unlock()
	return c
}

func (a *Auth) oidcGuiRoutes(mux *http.ServeMux) {
	a.oidcGuiLoad(context.Background())
	adm := func(fn http.HandlerFunc) http.Handler {
		return a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsAdmin(r.Context()) {
				http.Error(w, "admin only", http.StatusForbidden)
				return
			}
			fn(w, r)
		}))
	}
	mux.Handle("GET /api/settings/oidc", adm(func(w http.ResponseWriter, r *http.Request) {
		c := a.oidcGuiLoad(r.Context())
		base := oidcBase()
		redirect := ""
		if base != "" {
			redirect = base + "/oidc/callback"
		}
		var env []string
		for _, p := range oidcCfg.provs {
			env = append(env, p.ID)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"redirect": redirect, "form": LoginForm, "env": env,
			"googleId": c.GoogleID, "googleSet": c.GoogleSecret != "",
			"msTenant": c.MSTenant, "msId": c.MSID, "msSet": c.MSSecret != "",
			"googleOn": c.GoogleID != "" && c.GoogleSecret != "",
			"msOn":     c.MSTenant != "" && c.MSID != "" && c.MSSecret != "",
		})
	}))
	mux.Handle("POST /api/settings/oidc", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			GoogleID, GoogleSecret, MSTenant, MSID, MSSecret string
			ClearGoogle, ClearMS                             bool
		}
		if !body(w, r, &in) {
			return
		}
		bad := func(m string) { http.Error(w, m, http.StatusBadRequest) }
		in.GoogleID, in.MSTenant, in.MSID = strings.TrimSpace(in.GoogleID), strings.TrimSpace(in.MSTenant), strings.TrimSpace(in.MSID)
		for _, s := range []string{in.GoogleSecret, in.MSSecret} {
			if len(s) > 600 || strings.ContainsAny(s, "\r\n\t ") {
				bad("secret: no spaces, max 600 characters")
				return
			}
		}
		if !reOIDCID.MatchString(in.GoogleID) || !reOIDCID.MatchString(in.MSID) {
			bad("client id: invalid characters")
			return
		}
		if t := strings.ToLower(in.MSTenant); in.MSTenant != "" && (!reOIDCTenant.MatchString(in.MSTenant) || t == "common" || t == "organizations" || t == "consumers") {
			bad("tenant: the fixed tenant id (or domain) of your organization, not common/organizations/consumers")
			return
		}
		err := store.Update(r.Context(), a.st, oidcGuiKey, func(cur []byte) ([]byte, error) {
			var c oidcGuiCfg
			if cur != nil {
				json.Unmarshal(cur, &c)
			}
			if in.ClearGoogle {
				c.GoogleID, c.GoogleSecret = "", ""
			} else {
				c.GoogleID = in.GoogleID
				if in.GoogleSecret != "" {
					c.GoogleSecret = in.GoogleSecret
				}
			}
			if in.ClearMS {
				c.MSTenant, c.MSID, c.MSSecret = "", "", ""
			} else {
				c.MSTenant, c.MSID = in.MSTenant, in.MSID
				if in.MSSecret != "" {
					c.MSSecret = in.MSSecret
				}
			}
			return json.Marshal(c)
		})
		if err != nil {
			fail_(w, err)
			return
		}
		c := a.oidcGuiLoad(r.Context())
		log.Printf("auth: oidc settings changed by %q: google=%v microsoft=%v", safeName(User(r.Context())), c.GoogleID != "" && c.GoogleSecret != "", c.MSTenant != "" && c.MSID != "" && c.MSSecret != "")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
}
