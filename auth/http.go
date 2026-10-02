package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
)

func body(w http.ResponseWriter, r *http.Request, v any) bool {
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(v) != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return false
	}
	return true
}

func fail_(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrExists):
		code = http.StatusConflict
	case errors.Is(err, ErrNoUser), errors.Is(err, ErrNoGroup), errors.Is(err, ErrNoUnit):
		code = http.StatusNotFound
	case errors.Is(err, ErrBadName), errors.Is(err, ErrBadPass), errors.Is(err, ErrLastAdm), errors.Is(err, ErrBadArea), errors.Is(err, ErrBadFolder), errors.Is(err, ErrGroupUsed), errors.Is(err, ErrLastGroup), errors.Is(err, ErrNoGroups), errors.Is(err, ErrDefaultGroup), errors.Is(err, ErrDefaultUnit), errors.Is(err, ErrBadMail), errors.Is(err, ErrBadChat), errors.Is(err, ErrBadMode):
		code = http.StatusBadRequest
	}
	http.Error(w, err.Error(), code)
}

// Routes: Konto- und Benutzerverwaltung (alles hinter Basic Auth).
func (a *Auth) Routes(mux *http.ServeMux) {
	adm := func(fn http.HandlerFunc) http.Handler {
		return a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsAdmin(r.Context()) {
				http.Error(w, "admin only", http.StatusForbidden)
				return
			}
			fn(w, r)
		}))
	}
	// Abmelden (Basic Auth): 401 mit ungültigen/ohne Zugangsdaten lässt den Browser die gemerkten Daten verwerfen
	mux.HandleFunc("GET /logout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="cs-team"`)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>cs-team</title><body style="font:16px system-ui;margin:3em"><p>Abgemeldet / Signed out.</p><p><a href="/">Anmelden / Sign in</a></p>`))
	})
	mux.Handle("GET /api/me", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ar := []string{}
		for _, x := range Areas {
			if Can(r.Context(), x) {
				ar = append(ar, x)
			}
		}
		u, _ := a.get(r.Context(), User(r.Context()))
		json.NewEncoder(w).Encode(map[string]any{"lang": u.Lang, "mail": u.Mail, "chat": u.Chat, "must": u.Must, "name": User(r.Context()), "admin": IsAdmin(r.Context()), "areas": ar, "groups": GroupsOf(User(r.Context())), "adminOf": AdminOf(r.Context()), "version": Version})
	})))

	// eigene Oberflächensprache speichern ("" = automatisch)
	mux.Handle("POST /api/me/lang", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Lang string }
		if !body(w, r, &in) {
			return
		}
		if len(in.Lang) > 8 {
			http.Error(w, "bad lang", http.StatusBadRequest)
			return
		}
		me := User(r.Context())
		if err := a.mutate(r.Context(), func(m map[string]Account) error {
			u, ok := m[me]
			if !ok {
				return ErrNoUser
			}
			u.Lang = in.Lang
			m[me] = u
			return nil
		}); err != nil {
			fail_(w, err)
		}
	})))

	// eigenes Passwort ändern (altes Passwort nötig; Fehlversuche zählen für die Sperre)
	mux.Handle("POST /api/me/password", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Old, New string }
		if !body(w, r, &in) {
			return
		}
		me := User(r.Context())
		if u, _ := a.get(r.Context(), me); u.Must && in.Old == "" {
			// Startpasswort: diese Anfrage ist bereits damit angemeldet, das alte Passwort muss nicht noch einmal eingegeben werden
			if _, same := a.verify(r.Context(), me, in.New); same {
				http.Error(w, "new password must differ", http.StatusBadRequest)
				return
			}
			if err := a.SetPassword(r.Context(), me, in.New); err != nil {
				fail_(w, err)
			}
			return
		}
		if in.New == in.Old {
			http.Error(w, "new password must differ", http.StatusBadRequest)
			return
		}
		key := me + "|" + a.ip(r)
		if _, ok := a.verify(r.Context(), me, in.Old); !ok {
			a.failed(key)
			http.Error(w, "old password wrong", http.StatusForbidden)
			return
		}
		if err := a.SetPassword(r.Context(), me, in.New); err != nil {
			fail_(w, err)
		}
	})))

	// Benutzerliste: globaler Admin alle, Gruppen-Admin die Mitglieder seiner Gruppen, sonst nur man selbst.
	mux.Handle("GET /api/users", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.refresh(r.Context())
		type row struct {
			Name     string   `json:"name"`
			Admin    bool     `json:"admin,omitempty"`
			Disabled bool     `json:"disabled,omitempty"`
			Created  string   `json:"created,omitempty"`
			Groups   []string `json:"groups,omitempty"`
			Mail     string   `json:"mail,omitempty"`
			Chat     string   `json:"chat,omitempty"`
			ChatSet  bool     `json:"chatSet,omitempty"` // Webhook-Adresse vorhanden (die Adresse selbst nur für den Besitzer und globale Admins, S-09)
		}
		me, isAdm, ao := User(r.Context()), IsAdmin(r.Context()), AdminOf(r.Context())
		a.mu.Lock()
		out := []row{}
		for n, u := range a.users {
			switch {
			case isAdm:
				out = append(out, row{n, u.Admin, u.Disabled, u.Created, effGroups(u), u.Mail, u.Chat, u.Chat != ""})
			case n == me:
				out = append(out, row{Name: n, Disabled: u.Disabled, Groups: effGroups(u), Mail: u.Mail, Chat: u.Chat, ChatSet: u.Chat != ""})
			case len(ao) > 0 && !u.Admin && shares(effGroups(u), ao):
				out = append(out, row{Name: n, Disabled: u.Disabled, Groups: effGroups(u), Mail: u.Mail, ChatSet: u.Chat != ""})
			}
		}
		a.mu.Unlock()
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		json.NewEncoder(w).Encode(out)
	})))

	usr := func(fn http.HandlerFunc) http.Handler { return a.Wrap(http.HandlerFunc(fn)) } // Admin oder Gruppen-Admin (Prüfung im Handler)

	mux.Handle("POST /api/users", usr(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name, Password string
			Admin          bool
			Groups         []string
			Mail, Chat     string
		}
		if !body(w, r, &in) {
			return
		}
		if !IsAdmin(r.Context()) {
			if len(in.Groups) == 0 || in.Admin {
				http.Error(w, "group admin: give groups, no admin flag", http.StatusForbidden)
				return
			}
			for _, g := range in.Groups {
				if !CanManage(r.Context(), g) {
					http.Error(w, "not admin of group "+g, http.StatusForbidden)
					return
				}
			}
		}
		if _, _, err := cleanContact(in.Mail, in.Chat); err != nil {
			fail_(w, err)
			return
		}
		if in.Chat != "" && !IsAdmin(r.Context()) {
			http.Error(w, "chat address: only the owner or a global admin", http.StatusForbidden)
			return
		}
		if err := a.AddUser(r.Context(), in.Name, in.Password, in.Admin, in.Groups); err != nil {
			fail_(w, err)
			return
		}
		if in.Mail != "" || in.Chat != "" {
			if err := a.SetContact(r.Context(), in.Name, in.Mail, in.Chat); err != nil {
				fail_(w, err)
			}
		}
	}))

	// Passwort setzen: globaler Admin (jeder) oder Gruppen-Admin (Mitglieder seiner Gruppen, keine Admins)
	mux.Handle("POST /api/users/{name}/password", usr(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Password string }
		if !body(w, r, &in) {
			return
		}
		if !a.manages(r.Context(), r.PathValue("name")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := a.ResetPassword(r.Context(), r.PathValue("name"), in.Password); err != nil {
			fail_(w, err)
		}
	}))

	mux.Handle("POST /api/users/import", usr(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) && len(AdminOf(r.Context())) == 0 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		res := a.ImportCSV(r.Context(), string(raw), ImportOpts{Create: r.URL.Query().Get("create") == "1", Update: r.URL.Query().Get("update") == "1", Template: r.URL.Query().Get("template")})
		json.NewEncoder(w).Encode(res)
	}))

	a.groupRoutes(mux, adm)
	a.routineRoutes(mux, adm)
	a.purgeRoutes(mux, adm)
	a.contactRoutes(mux, usr)
	a.exportRoutes(mux)
	mux.Handle("POST /api/users/{name}/flags", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Admin, Disabled *bool }
		if !body(w, r, &in) {
			return
		}
		if err := a.SetFlags(r.Context(), r.PathValue("name"), in.Admin, in.Disabled); err != nil {
			fail_(w, err)
		}
	}))

	// ?purge=1 löscht zusätzlich die Kalender des Benutzers. Dokumente bleiben (Admin kann sie übernehmen/löschen).
	mux.Handle("DELETE /api/users/{name}", adm(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if err := a.DeleteUser(r.Context(), name); err != nil {
			fail_(w, err)
			return
		}
		if r.URL.Query().Get("purge") == "1" && validName.MatchString(name) {
			infos, _ := a.st.List(r.Context(), "cal/"+name+"/")
			for _, i := range infos {
				if strings.HasPrefix(i.Key, "cal/"+name+"/") {
					a.st.Delete(r.Context(), i.Key)
				}
			}
		}
	}))
}
