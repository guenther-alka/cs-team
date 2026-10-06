package auth

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Datenauskunft (DSGVO Art. 15/20): ZIP mit den Daten einer Person. Jedes Modul trägt seine Einträge bei (ExportHook);
// das ZIP wird gestreamt (nie komplett im Speicher). Passwort-Hash und Webhook-Adresse (enthält einen Schlüssel) sind nie enthalten.

// ExportHook: schreibt die Daten des Benutzers als Einträge ins ZIP (Namen mit ZipSafe bereinigen).
type ExportHook struct {
	Name  string
	Write func(ctx context.Context, user string, zw *zip.Writer) error
}

var ExportHooks []ExportHook

// exportSlots: höchstens 3 Exporte gleichzeitig (ein Export liest Dateien und Kalender; Schutz vor Überlastung).
var exportSlots = make(chan struct{}, 3)

// ZipSafe macht aus einem Namen einen sicheren Pfad im ZIP: keine "..", keine absoluten Pfade, keine Laufwerksbuchstaben,
// keine Steuer- und Windows-Sonderzeichen. "/" und "\" trennen Ordner. Leere Teile werden "_".
func ZipSafe(name string) string {
	var out []string
	for _, p := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		p = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 || strings.ContainsRune(`:*?"<>|`, r) {
				return '_'
			}
			return r
		}, p)
		p = strings.Trim(p, " .")
		if p == "" {
			p = "_"
		}
		if utf8.RuneCountInString(p) > 120 {
			p = string([]rune(p)[:120])
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return "_"
	}
	if len(out) > 12 {
		out = out[len(out)-12:]
	}
	return strings.Join(out, "/")
}

const exportReadme = `Datenauskunft cs-team (DSGVO Art. 15 und 20)
=============================================
Konto: %s
Erstellt: %s

Enthalten:
  account.json    Konto: Name, Gruppen, E-Mail-Adresse, Sprache, Anlegedatum (nie das Passwort oder dessen Hash).
  files/          Ihre eigenen Dateien (persoenlicher Bereich, ohne Papierkorb).
  calendars/      Ihre persoenlichen Kalender als .ics (ein Kalender je Datei; Abos sind nicht enthalten).
  tasks.json      Aufgaben, die Sie angelegt haben oder bearbeiten, mit Verlauf.
  chat.json       Ihre eigenen Chat-Nachrichten mit Gruppe, Kanal und Zeit.
  README.txt      diese Datei.

Nicht enthalten:
  - Inhalte anderer Personen (deren Nachrichten, Dateien, Termine), auch wenn sie in denselben Aufgaben, Kanaelen
    oder Kalendern stehen; Aufgaben-Verlaeufe koennen Kommentare anderer Beteiligter enthalten.
  - Gruppenordner, Gruppen- und globale Kalender, mit Ihnen geteilte Dateien und Dokumente.
  - Calc- und Text-Dokumente: bitte beim Administrator anfragen.
  - Protokolle des Servers (Zugriffs-/Audit-Protokolle) und Sicherungen/Snapshots.
  - Anhaenge zu Chat-Nachrichten (nur der Dateiname steht in chat.json).
Bei Fragen wenden Sie sich an die Administration Ihrer cs-team-Installation.
`

func (a *Auth) writeExport(w http.ResponseWriter, r *http.Request, name string) {
	u, ok := a.get(r.Context(), name)
	if !ok {
		http.Error(w, ErrNoUser.Error(), http.StatusNotFound)
		return
	}
	select {
	case exportSlots <- struct{}{}:
		defer func() { <-exportSlots }()
	default:
		http.Error(w, "export busy, try again in a minute", http.StatusTooManyRequests)
		return
	}
	log.Printf("audit: export user=%q by=%q ip=%s", safeName(name), safeName(User(r.Context())), a.ip(r))
	now := time.Now()
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="cs-team-daten-`+name+`-`+now.Format("2006-01-02")+`.zip"`)
	h.Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	put := func(fn string, b []byte) {
		if f, err := zw.Create(fn); err == nil {
			f.Write(b)
		}
	}
	put("README.txt", []byte(strings.ReplaceAll(fmt.Sprintf(exportReadme, name, now.Format("2006-01-02 15:04")), "\n", "\r\n")))
	var notices []string // bestätigte Hinweise (nur die Namen, nicht die gespeicherten Adressen)
	for k := range u.Ack {
		notices = append(notices, k)
	}
	sort.Strings(notices)
	acc := map[string]any{"name": name, "groups": effGroups(u), "mail": u.Mail, "language": u.Lang, "created": u.Created,
		"admin": u.Admin, "disabled": u.Disabled, "chatAddressSet": u.Chat != "", "realm": u.Realm, "source": u.Source, "notices": notices}
	b, _ := json.MarshalIndent(acc, "", " ")
	put("account.json", b)
	var failed []string
	for _, hk := range ExportHooks {
		if err := hk.Write(r.Context(), name, zw); err != nil {
			log.Printf("export %s: %s: %v", name, hk.Name, err)
			failed = append(failed, hk.Name)
		}
	}
	if len(failed) > 0 {
		put("_fehler.txt", []byte("Beim Export sind Teile fehlgeschlagen: "+strings.Join(failed, ", ")+" (Details im Serverprotokoll).\r\n"))
	}
	zw.Close()
}

// dataExportRoutes: eigene Daten (jeder Angemeldete) und Daten eines Benutzers (nur globaler Admin).
func (a *Auth) dataExportRoutes(mux *http.ServeMux, adm func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/me/export", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.writeExport(w, r, User(r.Context()))
	})))
	mux.Handle("GET /api/users/{name}/export", adm(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !validName.MatchString(name) {
			http.Error(w, ErrBadName.Error(), http.StatusBadRequest)
			return
		}
		a.writeExport(w, r, name)
	}))
}
