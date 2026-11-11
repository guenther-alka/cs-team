// cs-team: Multiuser CalDAV + Calc + Text auf RustFS/S3. Ein Binary, eine AbhÃ¤ngigkeitsliste.
//
//	cs-team                      Server starten
//	cs-team adduser <name> <pw> [admin]  Benutzer anlegen / Passwort setzen
package main

import (
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // Zeitzonen eingebettet (Windows und Minimalsysteme haben oft keine tzdata)

	"cs-team/ai"
	"cs-team/auth"
	"cs-team/ha"
	"cs-team/cal"
	"cs-team/chat"
	"cs-team/doc"
	"cs-team/files"
	"cs-team/news"
	"cs-team/office"
	"cs-team/share"
	"cs-team/store"
	"cs-team/tasks"
)

//go:embed web
var webFS embed.FS

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// envInt: Zahl aus der Umgebung (fehlend/ungÃ¼ltig = Standardwert).
func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return n
	}
	return def
}

// envList: durch Komma oder Semikolon getrennte Liste (leere EintrÃ¤ge entfallen).
func envList(s string) []string {
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// envIdentity: Vorgabe der Anmeldung (Namensraum name@realm + Verzeichnis) aus den Startparametern CS_IDENTITY_*.
// Die Einstellungen der OberflÃ¤che haben Vorrang (chat.Settings.Identity).
func envIdentity() auth.Identity {
	return auth.Identity{
		Mode:         env("CS_IDENTITY_MODE", ""),                    // "", "local", "dir" oder "mixed"
		Realm:        env("CS_IDENTITY_REALM", ""),                   // eigener Namensraum, z.B. local.de
		DefaultRealm: env("CS_IDENTITY_REALM_DEFAULT", ""),           // Namensraum fÃ¼r Namen ohne @ (Anzeige)
		AdmitGroups:  envList(os.Getenv("CS_IDENTITY_ADMIT_GROUPS")), // Gruppen im Verzeichnis (leer = alle)
		LocalGroup:   env("CS_IDENTITY_LOCAL_GROUP", ""),             // cs-team-Gruppe der Verzeichnisbenutzer
		AllowLocal:   os.Getenv("CS_IDENTITY_ALLOW_LOCAL") == "1", // ohne Wirkung (0.55): lokale Konten sind immer erlaubt
		CacheDays:    envInt("CS_IDENTITY_CACHE_DAYS", 0),
		URL:          env("CS_IDENTITY_URL", ""),       // ldap://host:389 oder ldaps://host:636
		Base:         env("CS_IDENTITY_BASE", ""),      // Suchbasis, z.B. DC=local,DC=de
		BindDN:       env("CS_IDENTITY_BIND_DN", ""),   // Dienstkonto fÃ¼r die Gruppensuche
		BindPW:       os.Getenv("CS_IDENTITY_BIND_PW"), // Passwort des Dienstkontos
		StartTLS:     os.Getenv("CS_IDENTITY_STARTTLS") == "1",
	}
}

// loadConf: KEY=VALUE-Datei (# Kommentare) in die Umgebung laden; bereits gesetzte Variablen gewinnen.
// Datei: erstes Argument "-c <datei>" oder CS_CONF.
func loadConf() {
	p := os.Getenv("CS_CONF")
	if len(os.Args) > 2 && os.Args[1] == "-c" {
		p = os.Args[2]
		os.Args = append(os.Args[:1], os.Args[3:]...)
	}
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		log.Fatal("conf: ", err)
	}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		k, v, ok := strings.Cut(ln, "=")
		if ln == "" || ln[0] == '#' || !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}

// notifySend: eine Meldung an einen Benutzer ueber seine externen Adressen (E-Mail und/oder Chat-Adresse, z. B. ntfy fuers Handy); die Tests ersetzen die Funktion.
var notifySend = func(m *chat.Mailer, user, subj, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	m.Notify(ctx, user, subj, text)
}

// notifyUser: Meldung der Art kind (task | mention | news), nur wenn der Benutzer sie nicht abgewaehlt hat (Konto: "Benachrichtigen bei").
func notifyUser(m *chat.Mailer, user, kind, subj, text string) {
	if auth.NotifyWanted(user, kind) {
		notifySend(m, user, subj, text)
	}
}

const version = "0.69.3" // Zaehlung neu ab 0.50.0 (0.1x waren die ersten Tests, 1.0 folgt, wenn es ausgereifter ist)

var started = time.Now()

// haSvc: Ausfallschutz (Startoption CS_FAILOVER); haStandby = nur lesen, keine Hintergrundauftraege (docs/HA_SPEC.md)
var (
	haSvc     *ha.Svc
	haStandby bool
)

func init() { auth.Version = version }

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println("cs-team", version)
		return
	}
	loadConf()
	if len(os.Args) == 2 && os.Args[1] == "vipdown" { // Dienst-IP dieses Rechners wegnehmen (Windows beendet cs-team hart; auch zum Aufraeumen von Hand)
		removed, err := ha.DownFromDir(os.Getenv("CS_DIR"))
		if err != nil {
			log.Fatal("vipdown: ", err)
		}
		fmt.Println("vipdown: removed =", removed)
		return
	}
	log.Printf("cs-team %s (%s/%s, %s) starting", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
	var st store.Store
	switch {
	case os.Getenv("CS_MEM") == "1": // Demo/Test ohne Speicher (Daten nur im RAM)
		st = store.NewMem()
	case os.Getenv("CS_DIR") != "": // lokaler Ordner / ZFS-Dataset, ohne S3
		d, err := store.NewFS(filepath.Join(os.Getenv("CS_DIR"), ".csteam")) // Daten in <CS_DIR>/.csteam (Rest des Datasets bleibt unberÃ¼hrt)
		if err != nil {
			log.Fatal("dir: ", err)
		}
		log.Println("storage: folder", filepath.Join(os.Getenv("CS_DIR"), ".csteam"))
		st = d
	default:
		s3, err := store.NewS3(env("S3_ENDPOINT", "127.0.0.1:9000"), env("S3_KEY", ""), env("S3_SECRET", ""),
			env("S3_BUCKET", "cs-team"), env("S3_TLS", "0") == "1")
		if err != nil {
			log.Fatal("s3: ", err)
		}
		log.Println("storage: s3 bucket", env("S3_BUCKET", "cs-team"))
		st = s3
	}
	haMode, haTake, err := ha.ParseMode(os.Getenv("CS_FAILOVER"))
	if err != nil {
		log.Fatal(err)
	}
	if haMode == "standby" {
		st = store.ReadOnly(st) // Standby: die replizierte Kopie wird nie veraendert
		haStandby = true
	}
	haName := os.Getenv("CS_HA_NAME")
	if haName == "" {
		haName, _ = os.Hostname()
	}
	haSvc = ha.New(st, haMode, haName, os.Getenv("CS_HA_FORCE") == "1", os.Getenv("CS_HA_ASSUME_OFF") == "1")
	if err := haSvc.Start(context.Background(), haTake); err != nil {
		log.Fatal("failover: ", err)
	}
	if haMode != "" {
		log.Printf("failover: %s, name %s", haMode, haName)
		haSvc.Watch(context.Background(), 10*time.Second)
	}
	a := auth.New(st)
	ctx := context.Background()
	if !haStandby {
		setupSnapshot(os.Getenv("CS_DIR"))
	}

	a.TrustProxy = os.Getenv("CS_TRUST_PROXY") == "1"
	auth.LoginForm = os.Getenv("CS_LOGIN_FORM") != "0" // Anmeldeformular mit Sitzungs-Cookie (Standard; CS_LOGIN_FORM=0 = Browser-Dialog wie bisher). WebDAV/CalDAV bleiben bei Basic Auth
	auth.OIDCFromEnv(os.Getenv, log.Printf)           // OIDC-Anbieter (CS_OIDC, CS_OIDC_BASE, CS_OIDC_<ID>_...), nur mit Anmeldeformular
	if n := envInt("CS_MAX_FAILS_IP", 0); n > 0 { // Fehlversuche je Adresse bis zur Sperre (Schul-NAT/Proxy: hÃ¶her setzen)
		auth.MaxFailsIP = n
	}
	if !haStandby {
		if err := a.Migrate(ctx); err != nil { // Standardgruppe "users" -> "alluser"
			log.Println("migrate groups:", err)
		}
		if err := a.EnsureIDs(ctx); err != nil { // uid/gid fuer vorhandene Konten und Gruppen (HA_SPEC.md, Abschnitt 2)
			log.Println("ids:", err)
		}
	}

	// cs-team sysadmin <name>   (bestimmt das Sysadmin-Konto: lokales cs-team-Konto, immer Admin, nicht lÃ¶schbar)
	if len(os.Args) == 3 && os.Args[1] == "sysadmin" {
		if err := a.SetSys(ctx, os.Args[2]); err != nil {
			log.Fatal(err)
		}
		fmt.Println("ok: sysadmin", os.Args[2])
		return
	}
	// cs-team adduser <name> <pw> [admin]   (legt an oder setzt Passwort; "admin" macht zum Admin)
	if len(os.Args) >= 4 && os.Args[1] == "adduser" {
		if err := a.SetUser(ctx, os.Args[2], os.Args[3], len(os.Args) > 4 && os.Args[4] == "admin"); err != nil {
			log.Fatal(err)
		}
		fmt.Println("ok:", os.Args[2])
		return
	}
	if u, p := os.Getenv("CS_ADMIN_USER"), os.Getenv("CS_ADMIN_PASS"); u != "" && p != "" && !haStandby {
		if err := a.Bootstrap(ctx, u, p); err != nil {
			log.Fatal("bootstrap: ", err)
		}
	}

	if haStandby {
	} else if err := a.EnsureSys(ctx, os.Getenv("CS_ADMIN_USER")); err != nil { // Sysadmin-Konto: lokal, immer Admin, nicht lÃ¶schbar (0.57.1)
		log.Println("sysadmin:", err)
	}

	handler := routes(st, a)
	startLDAP(a) // LDAPS-Dienst (nur lesen), wenn CS_LDAP gesetzt ist

	addr := env("CS_LISTEN", ":8080")
	if haMode == "master" { // Dienst-IP (virtuelle Adresse) setzen, beim Beenden wieder wegnehmen
		_, port, _ := net.SplitHostPort(addr)
		if err := haSvc.VIPUp(context.Background(), port); errors.Is(err, ha.ErrVIPInUse) {
			log.Fatal("failover: ", err)
		} else if err != nil {
			log.Println("failover: service IP not set:", err)
		} else if haSvc.VIPActive() {
			log.Println("failover:", haSvc.Status().VIP)
		}
		if haSvc.VIPActive() {
			go func() {
				c := make(chan os.Signal, 1)
				signal.Notify(c, os.Interrupt, syscall.SIGTERM)
				<-c
				haSvc.VIPDown()
				os.Exit(0)
			}()
		}
	}
	log.Println("cs-team listening on", addr)
	// Timeouts gegen Slowloris; kein Read-/WriteTimeout, weil WebSocket, Datei-Up-/Downloads und WebDAV lange laufen dÃ¼rfen.
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}
	if cert := os.Getenv("CS_TLS_CERT"); cert != "" { // HTTPS mit PEM-Datei(en); Key-Datei optional, wenn im selben PEM
		key := env("CS_TLS_KEY", cert)
		if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
			log.Fatal("tls: ", err)
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certReloader(cert, key)}
		srv.Handler = hsts(handler)
		log.Println("https, cert", cert)
		log.Fatal(srv.ListenAndServeTLS("", ""))
	}
	log.Println("WARNING: plain HTTP - passwords travel unencrypted; set CS_TLS_CERT or run behind a TLS proxy")
	log.Fatal(srv.ListenAndServe())
}

// hsts: Strict-Transport-Security nur bei eigenem TLS.
func hsts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}

// certReloader: lÃ¤dt das Zertifikat neu, wenn sich die PEM-Datei Ã¤ndert (Erneuerung ohne Neustart).
func certReloader(certFile, keyFile string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	var mu sync.Mutex
	var cur *tls.Certificate
	var mod time.Time
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		mu.Lock()
		defer mu.Unlock()
		st, err := os.Stat(certFile)
		if err == nil && (cur == nil || st.ModTime().After(mod)) {
			if c, e := tls.LoadX509KeyPair(certFile, keyFile); e == nil {
				cur, mod = &c, st.ModTime()
			} else if cur == nil {
				return nil, e
			}
		}
		if cur == nil {
			return nil, err
		}
		return cur, nil
	}
}

// shareRoot: Ordner der schreibgeschÃ¼tzten Dokumente: CS_SHARE, sonst <CS_DIR>/share (neben .csteam); leer = aus.
// Er liegt lokal auf dem cs-team-Server und ist vom Speicher (.csteam bzw. S3) getrennt; cs-team schreibt nie hinein.
func shareRoot() string {
	if p := os.Getenv("CS_SHARE"); p != "" {
		return p
	}
	if d := os.Getenv("CS_DIR"); d != "" {
		return filepath.Join(d, "share")
	}
	return ""
}

func routes(st store.Store, a *auth.Auth) http.Handler {
	mux := http.NewServeMux()
	if haSvc == nil { // Tests rufen routes ohne main auf: Ausfallschutz aus
		haSvc = ha.New(st, "", "", false, false)
	}
	caldav := davLog(a.Wrap(auth.Need("cal", cal.Handler(st))))
	mux.Handle(cal.Prefix+"/", caldav)
	// RFC 6764: /.well-known/caldav leitet auf die CalDAV-Basis um (DAVx5, Apple, Thunderbird folgen der Umleitung, 0.69.2)
	mux.Handle("/.well-known/caldav", http.RedirectHandler(cal.Prefix+"/", http.StatusMovedPermanently))
	a.Routes(mux)
	cb := &cal.Backend{St: st}
	cb.Routes(mux, a.Wrap)
	auth.OnNewGroup = func(ctx context.Context, g, mode string) { cb.NewGroupCalendar(ctx, g, mode) }
	auth.OnGroupCal = cb.SetGroupCalendar // Gruppen-Einstellungen: Gruppenkalender anlegen, freigeben oder entfernen
	hub := doc.NewHub(st)
	hub.Routes(mux, a.Wrap)
	mb, _ := strconv.Atoi(env("CS_MAX_UPLOAD_MB", "100"))
	if mb < 1 {
		mb = 100
	}
	fsvc := &files.Svc{St: st, Max: int64(mb) << 20}
	fsvc.Routes(mux, a.Wrap)
	if root := shareRoot(); root != "" { // schreibgeschÃ¼tzter Ordner "share" (TODO G); gibt es ihn nicht, bleibt alles aus
		(&share.Svc{Root: root, Files: fsvc}).Routes(mux, a.Wrap)
	}
	osvc := &office.Svc{Hub: hub, Files: fsvc}
	osvc.Routes(mux, a.Wrap)
	cs := chat.New(st)
	if n, _ := strconv.Atoi(env("CS_CHAT_MAX_MB", "10")); n > 0 {
		cs.MaxMB = int64(n)
	}
	cs.Routes(mux, a.Wrap)
	if !haStandby { cs.RunRetention(context.Background()) } // Aufbewahrung Chat je Gruppe (stÃ¼ndlich)
	envSMTP := chat.SMTP{Host: os.Getenv("CS_SMTP_HOST"), Port: os.Getenv("CS_SMTP_PORT"), User: os.Getenv("CS_SMTP_USER"), Pass: os.Getenv("CS_SMTP_PASS"), From: os.Getenv("CS_SMTP_FROM"), TLS: os.Getenv("CS_SMTP_TLS")}
	cfg := chat.NewSettings(st, envSMTP, os.Getenv("CS_CHAT_ALLOW_PRIVATE") == "1") // Einstellungen der OberflÃ¤che; Umgebung nur als Vorgabe
	if n, err := strconv.ParseInt(os.Getenv("CS_QUOTA_MB"), 10, 64); err == nil && n > 0 {
		cfg.EnvQuotaMB = n
	}
	cfg.EnvTrashDays = 30 // Papierkorb: Standard 30 Tage
	if n, err := strconv.Atoi(os.Getenv("CS_TRASH_DAYS")); err == nil && n >= 0 {
		cfg.EnvTrashDays = n
	}
	cfg.EnvIdentity = envIdentity() // Anmeldung: Namensraum (name@realm) + Verzeichnis aus den Startparametern
	a.SetIdentitySource(cfg)        // OberflÃ¤che hat Vorrang; Ã„nderungen gelten sofort (auth.Auth liest die Einstellung)
	a.WarmDir(context.Background()) // Verzeichnisverbindung im Hintergrund offen halten (erster Aufbau ist nach Leerlauf trÃ¤ge)
	fsvc.Quota = cfg.Quota          // Dateikontingent aus den Einstellungen
	fsvc.TrashDays = cfg.TrashDays
	fsvc.PubMax = cfg.PubMaxDays // Ã¶ffentliche Links: lÃ¤ngste GÃ¼ltigkeit (globale Einstellung, 0 = unbegrenzt erlaubt)
	auth.Enforce2FA = cfg.Enforce2FA // Zwei-Faktor-Pflicht fÃ¼r globale Admins (globale Einstellung)
	if !haStandby { go fsvc.RunTrash(context.Background()) } // abgelaufene EintrÃ¤ge im Papierkorb entfernen
	cs.Cfg = cfg                           // Videochat-Server aus den Einstellungen
	mailer := &chat.Mailer{St: st, Chat: cs, Cfg: cfg}
	cb.Mail, cb.MailOK = mailer.Invite, mailer.MailEnabled
	auth.SendMail, auth.MailOK = mailer.SendOne, mailer.MailEnabled
	auth.PublicURL = cfg.PublicURL
	cfg.Routes(mux, a.Wrap, mailer)
	mailer.Routes(mux, a.Wrap)
	ts := tasks.New(st)
	ts.Notify = func(user, subj, text string) { notifyUser(mailer, user, "task", subj, text) }
	cs.Notify = func(user, subj, text string) { notifyUser(mailer, user, "mention", subj, text) } // Erwaehnung und Antwort im Chat (chat/notify.go)
	cs.NotifyDM = func(user, subj, text string) { notifyUser(mailer, user, "dm", subj, text) }       // 1:1-Nachricht
	cs.Base = cfg.PublicURL
	ts.Base = cfg.PublicURL
	ts.ClosedDays = cfg.ClosedDays // Aufbewahrung abgeschlossener Aufgaben (globale Einstellung, 0 = unbegrenzt)
	ts.Routes(mux, a.Wrap)
	searchRoutes(mux, a.Wrap, ts, cs, fsvc, hub, cb)
	ns := news.New(st) // Aushaenge auf der Startseite (news/news.go)
	ns.Notify = func(user, subj, text string) { notifyUser(mailer, user, "news", subj, text) }
	ns.Base = cfg.PublicURL
	ns.Routes(mux, a.Wrap)
	if !haStandby {
		ts.Start(context.Background())
	}
	haSvc.Routes(mux, a.Wrap, func(r *http.Request) bool { return auth.IsAdmin(r.Context()) })
	auth.RenameHooks = []auth.RenameHook{ // Jahrgangswechsel im Modus "Gruppe wird umbenannt": jedes Modul benennt seine Daten um
		{Name: "files", Rename: fsvc.RenameGroup, Used: fsvc.FolderUsed},
		{Name: "file shares", Rename: fsvc.RenameShares},
		{Name: "document shares", Rename: hub.RenameShares},
		{Name: "calendar", Area: "cal", Rename: cb.RenameGroup, Used: cb.CalUsed, Drop: cb.DropGroup},
		{Name: "chat", Area: "chat", Rename: cs.RenameGroup, Used: cs.ChatUsed},
		{Name: "tasks", Area: "tasks", Rename: ts.RenameGroup, Used: ts.TasksUsed},
		{Name: "news", Rename: ns.RenameGroup},
	}
	auth.MessageCount = cs.MsgCount
	auth.GroupCalMode = cb.GroupCalMode
	auth.GroupCalState = cb.GroupCalState // Gruppen-Einstellungen zeigen/Ã¤ndern die Freigabe des Gruppenkalenders
	auth.UserHooks = []auth.UserHook{     // Benutzer lÃ¶schen (Assistent mit Snapshot): jedes Modul entfernt die Daten des Namens
		{Name: "files", Count: fsvc.UserCount, Purge: fsvc.PurgeUser},
		{Name: "documents", Count: hub.UserCount, Purge: hub.PurgeUser},
		{Name: "calendar", Count: cb.UserCount, Purge: cb.PurgeUser},
		{Name: "news", Early: true, Count: ns.Count, Purge: ns.Purge}, // vor dem Anonymisieren, sonst findet Purge die Aushaenge nicht mehr
		{Name: "chat", Early: true, Count: cs.UserCount, Purge: cs.PurgeUser}, // 1:1-Kanaele, eigene Anhaenge, auf Wunsch die Nachrichten
	}
	auth.AnonHooks = []auth.AnonHook{ // Benutzer lÃ¶schen mit Option "anonymize": der Name bleibt in Inhalten der Gruppen nicht stehen
		{Name: "chat", Count: cs.AnonCount, Anon: cs.Anon},
		{Name: "tasks", Count: ts.AnonCount, Anon: ts.Anon},
		{Name: "events", Count: cb.AnonCount, Anon: cb.Anon},
		{Name: "news", Count: ns.AnonCount, Anon: ns.Anon},
	}
	auth.ExportHooks = []auth.ExportHook{ // Datenauskunft: ZIP mit den Daten einer Person
		{Name: "files", Write: fsvc.ExportUser},
		{Name: "calendar", Write: cb.ExportUser},
		{Name: "tasks", Write: ts.ExportUser},
		{Name: "chat", Write: cs.ExportUser},
		{Name: "documents", Write: osvc.ExportUser},
		{Name: "notices", Write: ns.ExportUser},
	}
	aiSvc := ai.New(st) // KI-Assistent: Provider zentral in den Einstellungen; Daten nur mit den Rechten des Fragenden
	aiSvc.H = mux
	auth.AckAddr = func(key string) string { // Hinweis vor der ersten Nutzung: Ziel der Daten (KI-Anbieter, Videoserver)
		switch key {
		case "ai":
			return aiSvc.AckAddr()
		case "video":
			return cfg.VideoAddr()
		}
		return ""
	}
	auth.PrivacyNote = cfg.Privacy
	aiSvc.Chat = cs
	aiSvc.Docs = hub
	cs.AIReview = aiSvc.ReviewEnabled
	aiSvc.LangName = func(code string) string { return langList()[code] }
	aiSvc.Info = func() map[string]string {
		store := "s3 bucket " + env("S3_BUCKET", "cs-team")
		if os.Getenv("CS_MEM") == "1" {
			store = "memory (demo)"
		} else if os.Getenv("CS_DIR") != "" {
			store = "folder"
		}
		return map[string]string{"version": version, "uptime": time.Since(started).Round(time.Minute).String(), "platform": runtime.GOOS + "/" + runtime.GOARCH,
			"storage": store, "tls": fmt.Sprint(os.Getenv("CS_TLS_CERT") != ""), "listen": env("CS_LISTEN", ":8080")}
	}
	aiSvc.Routes(mux, a.Wrap)
	web, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /lang/", a.Public(http.HandlerFunc(langHandler))) // Public: ohne Anmeldung nur bei CS_LOGIN_FORM=1 (auth/session.go)
	mux.Handle("/", a.Public(http.FileServerFS(web)))
	return logMW(secHeaders(haSvc.Guard(mux)))
}

// cspHost: Hostname ohne Port aus dem Host-Header, nur wenn er ausschliesslich aus Buchstaben, Ziffern, Punkt und Bindestrich besteht (sonst leer).
func cspHost(host string) string {
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.HasSuffix(host, "]") {
		host = host[:i]
	}
	if host == "" {
		return ""
	}
	for _, c := range host {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return ""
		}
	}
	return host
}

// secHeaders: Sicherheits-Header fÃ¼r alle Antworten (Clickjacking, MIME-Sniffing, Referrer, Skript-Quellen).
// Das Skript liegt in web/app.js (eingebettet per go:embed), deshalb script-src 'self' ohne 'unsafe-inline': eingeschleustes
// Markup kann kein Skript ausfÃ¼hren. Stil bleibt 'unsafe-inline' (style=-Attribute der OberflÃ¤che); fremde Quellen sind gesperrt.
func secHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		fsrc := "'self'" // Webcraft-Editoren (WordCraft/GridCraft/PDFCraft) liegen auf demselben Host unter /webcraft/ (napp-it CS, https, beliebiger Port)
		if fh := cspHost(r.Host); fh != "" {
			fsrc += " https://" + fh + " https://" + fh + ":*"
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data: blob:; frame-src "+fsrc+"; connect-src 'self' ws://"+r.Host+" wss://"+r.Host+strings.TrimPrefix(fsrc, "'self'")+"; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=15552000")
		}
		// API-Antworten enthalten personenbezogene Daten: nicht in Browser-/Proxy-Caches ablegen (Handler dÃ¼rfen Ã¼berschreiben).
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/dav/") || strings.HasPrefix(r.URL.Path, "/webdav/") {
			h.Set("Cache-Control", "no-store")
		}
		if r.Method == "PROPFIND" && r.URL.Path == "/" { // CalDAV-Client mit Basis-URL ohne Pfad: zur CalDAV-Basis (0.69.2)
			http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setupSnapshot: vor jeder globalen Aktion (Jahrgangswechsel ...) wird nach MÃ¶glichkeit ein ZFS-Snapshot angelegt.
// CS_SNAPSHOT_CMD = eigener Befehl ({id} = Lauf-ID), CS_SNAPSHOT=off = ausschalten; sonst wird bei Ordner-Speicher
// (CS_DIR) das ZFS-Dataset erkannt und "zfs snapshot <dataset>@cs-team-<id>" verwendet. Bei S3/RustFS: CS_SNAPSHOT_CMD setzen.
// CS_SNAPSHOT_DATASET = Dataset fest vorgeben (statt Erkennung).
func setupSnapshot(dir string) {
	if c := os.Getenv("CS_SNAPSHOT_CMD"); c != "" {
		auth.SnapshotCmd = c
		log.Println("snapshot before global actions: custom command")
		return
	}
	if os.Getenv("CS_SNAPSHOT") == "off" || dir == "" {
		return
	}
	zfs, err := exec.LookPath("zfs")
	if err != nil {
		return
	}
	ds := strings.TrimSpace(os.Getenv("CS_SNAPSHOT_DATASET"))
	if ds != "" && strings.ContainsAny(ds, " '\"\\$`;&|<>@") {
		log.Println("snapshot: CS_SNAPSHOT_DATASET invalid, ignored:", ds)
		ds = ""
	}
	if ds == "" {
		ds = zfsDataset(zfs, dir)
	}
	if ds == "" {
		log.Println("snapshot: no ZFS dataset found for", dir, "- global actions run without snapshot unless confirmed")
		return
	}
	auth.SnapshotArgv = []string{zfs, "snapshot", ds + "@cs-team-{id}"}
	auth.SnapshotInfo = ds
	log.Println("snapshot before global actions: zfs snapshot", ds+"@cs-team-<id>")
}

// zfsDataset: Dataset des Ordners. Erst "zfs list <pfad>", sonst das Dataset mit dem lÃ¤ngsten passenden Mountpoint
// (funktioniert auch dort, wo zfs keine Pfade annimmt, z.B. OpenZFS on Windows).
func zfsDataset(zfs, dir string) string {
	okName := func(n string) bool { return n != "" && !strings.ContainsAny(n, " '\"\\$`;&|<>@") }
	run := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, zfs, args...).Output()
		if err != nil {
			return ""
		}
		return string(out)
	}
	if out := run("list", "-H", "-o", "name", dir); out != "" {
		if n := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]); okName(n) {
			return n
		}
	}
	if len(dir) >= 2 && dir[1] == ':' {
		// Windows (OpenZFS on Windows): Laufwerk = Pool (DatentrÃ¤gerbezeichnung), Unterordner/Junctions = Datasets
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if n := winDataset(dir, volLabel(dir), strings.Split(run("list", "-H", "-o", "name", "-t", "filesystem"), "\n")); n != "" && okName(n) {
			return n
		}
	}
	norm := func(p string) string {
		p = strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
		return strings.TrimRight(p, "/") + "/"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	want, best, name := norm(abs), 0, ""
	for _, l := range strings.Split(run("list", "-H", "-o", "name,mountpoint", "-t", "filesystem"), "\n") {
		f := strings.Split(strings.TrimRight(l, "\r"), "\t")
		if len(f) != 2 || !okName(f[0]) || f[1] == "none" || f[1] == "legacy" || f[1] == "-" {
			continue
		}
		if m := norm(f[1]); strings.HasPrefix(want, m) && len(m) > best {
			best, name = len(m), f[0]
		}
	}
	return name
}

// volLabel: DatentrÃ¤gerbezeichnung des Laufwerks (Windows; sonst leer). In Tests ersetzbar.
var volLabel = volumeLabel

// winDataset: ermittelt aus Windows-Pfad (D:\data\x), Laufwerksbezeichnung (= Pool) und der Dataset-Liste das
// zustÃ¤ndige Dataset: lÃ¤ngster Pfad-PrÃ¤fix unterhalb des Pools, der als Dataset existiert (D:\data -> winpool/data).
// Passt die Bezeichnung zu keinem Pool, wird bei genau einem Pool dieser genommen.
func winDataset(path, label string, names []string) string {
	p := strings.ReplaceAll(path, "\\", "/")
	if len(p) < 2 || p[1] != ':' {
		return ""
	}
	have := map[string]string{}
	var pools []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		have[strings.ToLower(n)] = n
		if !strings.Contains(n, "/") {
			pools = append(pools, n)
		}
	}
	pool := ""
	for _, n := range pools {
		if label != "" && strings.EqualFold(n, label) {
			pool = n
		}
	}
	if pool == "" && len(pools) == 1 {
		pool = pools[0]
	}
	if pool == "" {
		return ""
	}
	var comps []string
	for _, c := range strings.Split(p[2:], "/") {
		if c != "" && c != "." {
			comps = append(comps, c)
		}
	}
	for i := len(comps); i >= 0; i-- {
		cand := strings.ToLower(strings.Join(append([]string{pool}, comps[:i]...), "/"))
		if n, ok := have[cand]; ok {
			return n
		}
	}
	return ""
}
