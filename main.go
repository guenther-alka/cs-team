// cs-team: Multiuser CalDAV + Calc + Text auf RustFS/S3. Ein Binary, eine Abhängigkeitsliste.
//
//	cs-team                      Server starten
//	cs-team adduser <name> <pw> [admin]  Benutzer anlegen / Passwort setzen
package main

import (
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Zeitzonen eingebettet (Windows und Minimalsysteme haben oft keine tzdata)

	"cs-team/ai"
	"cs-team/auth"
	"cs-team/cal"
	"cs-team/chat"
	"cs-team/doc"
	"cs-team/files"
	"cs-team/office"
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

const version = "0.14.0"

var started = time.Now()

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "version" || os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println("cs-team", version)
		return
	}
	loadConf()
	var st store.Store
	switch {
	case os.Getenv("CS_MEM") == "1": // Demo/Test ohne Speicher (Daten nur im RAM)
		st = store.NewMem()
	case os.Getenv("CS_DIR") != "": // lokaler Ordner / ZFS-Dataset, ohne S3
		d, err := store.NewFS(filepath.Join(os.Getenv("CS_DIR"), ".csteam")) // Daten in <CS_DIR>/.csteam (Rest des Datasets bleibt unberührt)
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
	a := auth.New(st)
	ctx := context.Background()
	setupSnapshot(os.Getenv("CS_DIR"))

	a.TrustProxy = os.Getenv("CS_TRUST_PROXY") == "1"
	if err := a.Migrate(ctx); err != nil { // Standardgruppe "users" -> "alluser"
		log.Println("migrate groups:", err)
	}

	// cs-team adduser <name> <pw> [admin]   (legt an oder setzt Passwort; "admin" macht zum Admin)
	if len(os.Args) >= 4 && os.Args[1] == "adduser" {
		if err := a.SetUser(ctx, os.Args[2], os.Args[3], len(os.Args) > 4 && os.Args[4] == "admin"); err != nil {
			log.Fatal(err)
		}
		fmt.Println("ok:", os.Args[2])
		return
	}
	if u, p := os.Getenv("CS_ADMIN_USER"), os.Getenv("CS_ADMIN_PASS"); u != "" && p != "" {
		if err := a.Bootstrap(ctx, u, p); err != nil {
			log.Fatal("bootstrap: ", err)
		}
	}

	handler := routes(st, a)

	addr := env("CS_LISTEN", ":8080")
	log.Println("cs-team listening on", addr)
	// Timeouts gegen Slowloris; kein Read-/WriteTimeout, weil WebSocket, Datei-Up-/Downloads und WebDAV lange laufen dürfen.
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

// certReloader: lädt das Zertifikat neu, wenn sich die PEM-Datei ändert (Erneuerung ohne Neustart).
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

func routes(st store.Store, a *auth.Auth) http.Handler {
	mux := http.NewServeMux()
	caldav := a.Wrap(auth.Need("cal", cal.Handler(st)))
	mux.Handle(cal.Prefix+"/", caldav)
	mux.Handle("/.well-known/caldav", caldav)
	a.Routes(mux)
	cb := &cal.Backend{St: st}
	cb.Routes(mux, a.Wrap)
	auth.OnNewGroup = func(ctx context.Context, g, mode string) { cb.NewGroupCalendar(ctx, g, mode) }
	hub := doc.NewHub(st)
	hub.Routes(mux, a.Wrap)
	mb, _ := strconv.Atoi(env("CS_MAX_UPLOAD_MB", "100"))
	if mb < 1 {
		mb = 100
	}
	fsvc := &files.Svc{St: st, Max: int64(mb) << 20}
	fsvc.Routes(mux, a.Wrap)
	(&office.Svc{Hub: hub, Files: fsvc}).Routes(mux, a.Wrap)
	cs := chat.New(st)
	if n, _ := strconv.Atoi(env("CS_CHAT_MAX_MB", "10")); n > 0 {
		cs.MaxMB = int64(n)
	}
	cs.Routes(mux, a.Wrap)
	envSMTP := chat.SMTP{Host: os.Getenv("CS_SMTP_HOST"), Port: os.Getenv("CS_SMTP_PORT"), User: os.Getenv("CS_SMTP_USER"), Pass: os.Getenv("CS_SMTP_PASS"), From: os.Getenv("CS_SMTP_FROM"), TLS: os.Getenv("CS_SMTP_TLS")}
	cfg := chat.NewSettings(st, envSMTP, os.Getenv("CS_CHAT_ALLOW_PRIVATE") == "1") // Einstellungen der Oberfläche; Umgebung nur als Vorgabe
	if n, err := strconv.ParseInt(os.Getenv("CS_QUOTA_MB"), 10, 64); err == nil && n > 0 {
		cfg.EnvQuotaMB = n
	}
	fsvc.Quota = cfg.Quota // Dateikontingent aus den Einstellungen
	cs.Cfg = cfg           // Videochat-Server aus den Einstellungen
	mailer := &chat.Mailer{St: st, Chat: cs, Cfg: cfg}
	cfg.Routes(mux, a.Wrap, mailer)
	mailer.Routes(mux, a.Wrap)
	ts := tasks.New(st)
	ts.Notify = func(user, subj, text string) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		mailer.Notify(ctx, user, subj, text)
	}
	ts.Base = cfg.PublicURL
	ts.Routes(mux, a.Wrap)
	ts.Start(context.Background())
	auth.RenameHooks = []auth.RenameHook{ // Jahrgangswechsel im Modus "Gruppe wird umbenannt": jedes Modul benennt seine Daten um
		{Name: "files", Rename: fsvc.RenameGroup, Used: fsvc.FolderUsed},
		{Name: "file shares", Rename: fsvc.RenameShares},
		{Name: "document shares", Rename: hub.RenameShares},
		{Name: "calendar", Area: "cal", Rename: cb.RenameGroup, Used: cb.CalUsed, Drop: cb.DropGroup},
		{Name: "chat", Area: "chat", Rename: cs.RenameGroup, Used: cs.ChatUsed},
		{Name: "tasks", Area: "tasks", Rename: ts.RenameGroup, Used: ts.TasksUsed},
	}
	auth.GroupCalMode = cb.GroupCalMode
	auth.UserHooks = []auth.UserHook{ // Benutzer löschen (Assistent mit Snapshot): jedes Modul entfernt die Daten des Namens
		{Name: "files", Count: fsvc.UserCount, Purge: fsvc.PurgeUser},
		{Name: "documents", Count: hub.UserCount, Purge: hub.PurgeUser},
		{Name: "calendar", Count: cb.UserCount, Purge: cb.PurgeUser},
	}
	aiSvc := ai.New(st) // KI-Assistent: Provider zentral in den Einstellungen; Daten nur mit den Rechten des Fragenden
	aiSvc.H = mux
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
	mux.Handle("GET /lang/", a.Wrap(http.HandlerFunc(langHandler)))
	mux.Handle("/", a.Wrap(http.FileServerFS(web)))
	return secHeaders(mux)
}

// secHeaders: Sicherheits-Header für alle Antworten (Clickjacking, MIME-Sniffing, Referrer, Skript-Quellen).
// Die Oberfläche ist eine Datei mit eingebettetem Skript/Stil, daher 'unsafe-inline'; fremde Quellen sind gesperrt.
func secHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data: blob:; connect-src 'self' ws://"+r.Host+" wss://"+r.Host+"; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=15552000")
		}
		next.ServeHTTP(w, r)
	})
}

// setupSnapshot: vor jeder globalen Aktion (Jahrgangswechsel ...) wird nach Möglichkeit ein ZFS-Snapshot angelegt.
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

// zfsDataset: Dataset des Ordners. Erst "zfs list <pfad>", sonst das Dataset mit dem längsten passenden Mountpoint
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
		// Windows (OpenZFS on Windows): Laufwerk = Pool (Datenträgerbezeichnung), Unterordner/Junctions = Datasets
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

// volLabel: Datenträgerbezeichnung des Laufwerks (Windows; sonst leer). In Tests ersetzbar.
var volLabel = volumeLabel

// winDataset: ermittelt aus Windows-Pfad (D:\data\x), Laufwerksbezeichnung (= Pool) und der Dataset-Liste das
// zuständige Dataset: längster Pfad-Präfix unterhalb des Pools, der als Dataset existiert (D:\data -> winpool/data).
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
