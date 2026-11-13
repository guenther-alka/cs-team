package main

// Ad-hoc-Einrichtung (0.70.0):
//
//	cs-team              ohne Parameter: Konfiguration verwenden; fehlt sie oder ist sie ungueltig und laeuft cs-team in
//	                     einem Terminal, fragt ein Textmenue die Einstellungen ab (wie napp-it System > Services > cs-team),
//	                     prueft sie, speichert sie und startet im Vordergrund
//	cs-team setup        Menue mit den gespeicherten Werten (zum Aendern), danach Start
//	cs-team -c <datei>   wie bisher (napp-it); ebenso CS_CONF oder CS_*-Variablen eines Startskripts: dann kein Menue
//
// Wo die Konfiguration liegt:
//   - napp-it installiert (/opt/csweb-gui/_cfg bzw. C:\opt\csweb-gui\_cfg vorhanden): dieselben Dateien wie das napp-it-Menue,
//     _cfg/team/team.par (Vorgaben des Menues) und _cfg/team/team.cfg (daraus erzeugt, wie cstlib.pl _cst_write_cfg);
//     beide werden geschrieben, damit napp-it-Menue und cs-team immer dasselbe zeigen. Webcraft: _my/wwwroot/webcraft.
//   - sonst: team.cfg neben dem Programm (KEY=VALUE). Das Admin-Startpasswort wird dort nie gespeichert (nur fuer diesen
//     Start: erster Admin eines leeren Speichers, muss beim ersten Login geaendert werden). Webcraft-Ordner: CS_WEBCRAFT.
//     Vorgabe fuer den Speicher-Ordner ist der Programmordner (Daten in <programmordner>/.csteam).
//   - standalone.set neben dem Programm (Inhalt egal): wie ohne napp-it, auch wenn napp-it installiert ist.

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"cs-team/auth"
	"cs-team/store"
)

// adhocEnv: ist eine davon gesetzt, kommt die Konfiguration aus der Umgebung (Startskript): keine Datei, kein Menue.
var adhocEnv = []string{"CS_CONF", "CS_DIR", "CS_MEM", "S3_ENDPOINT", "S3_BUCKET", "CS_LISTEN"}

// errBusy: Port belegt - meist laeuft cs-team schon (z. B. von napp-it gestartet); dann kein Menue, nur die Meldung.
var errBusy = errors.New("port is not free (cs-team already running?)")

// nappDir: Installationsordner von napp-it CS.
func nappDir() string {
	if runtime.GOOS == "windows" {
		return `C:\opt\csweb-gui`
	}
	return "/opt/csweb-gui"
}

// napp: napp-it ist auf diesem Rechner installiert und standalone.set fehlt neben dem Programm.
func napp() bool {
	if standalone() {
		return false
	}
	st, err := os.Stat(filepath.Join(nappDir(), "_cfg"))
	return err == nil && st.IsDir()
}

// standalone: Datei standalone.set neben dem Programm (Inhalt egal) - napp-it-Einstellungen nicht uebernehmen,
// team.cfg und webcraft neben dem Programm, wie auf einem Rechner ohne napp-it.
func standalone() bool {
	_, err := os.Stat(filepath.Join(exeDir(), "standalone.set"))
	return err == nil
}

// exeDir: Ordner des Programms (Symlinks aufgeloest).
func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Dir(p)
}

// exeCfg: team.cfg im Ordner des Programms.
func exeCfg() string { return filepath.Join(exeDir(), "team.cfg") }

// cfgPath: napp-it: <napp-it>/_cfg/team/team.cfg, sonst team.cfg neben dem Programm.
func cfgPath() string {
	if napp() {
		return filepath.Join(nappDir(), "_cfg", "team", "team.cfg")
	}
	return exeCfg()
}

func isTTY() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// parseConf: KEY=VALUE-Datei (# Kommentare, Werte ohne umschliessende Anfuehrungszeichen).
func parseConf(p string) (map[string]string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		k, v, ok := strings.Cut(ln, "=")
		if ln == "" || ln[0] == '#' || !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return m, nil
}

// adhoc: vor loadConf. Setzt CS_CONF auf die Konfigurationsdatei oder fragt die Einstellungen im Menue ab.
func adhoc() {
	setup := len(os.Args) == 2 && os.Args[1] == "setup"
	if !setup && len(os.Args) > 1 && os.Args[1] == "-c" {
		return
	}
	for _, k := range adhocEnv {
		if os.Getenv(k) != "" {
			if setup {
				log.Fatalf("setup: the configuration comes from the environment (%s), not from a file", k)
			}
			return
		}
	}
	cfg, nap := cfgPath(), napp()
	c, err := parseConf(cfg)
	have := err == nil
	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("%s: %v", cfg, err)
	}
	if !setup && len(os.Args) > 1 { // Unterbefehle (adduser, sysadmin, vipdown) nutzen die Konfiguration, wenn vorhanden
		if have {
			os.Setenv("CS_CONF", cfg)
		}
		return
	}
	if setup {
		os.Args = os.Args[:1]
	}
	var why error
	if have && !setup {
		why = checkConf(c)
		if why == nil {
			os.Setenv("CS_CONF", cfg)
			return
		}
		if errors.Is(why, errBusy) {
			log.Fatalf("%s: %v", cfg, why)
		}
	}
	if !isTTY() {
		if setup {
			log.Fatal("setup: needs a terminal (interactive menu)")
		}
		if have {
			log.Fatalf("%s: %v - run \"cs-team setup\" in a terminal to correct it", cfg, why)
		}
		log.Fatalf("no configuration: start cs-team once in a terminal (the setup menu writes %s), or use -c <file> or CS_* variables", cfg)
	}
	var par map[string]string
	if nap { // Vorgaben aus dem napp-it-Menue (team.par enthaelt alles, auch die S3-Werte bei Speicher "Ordner")
		par, _ = parseConf(filepath.Join(filepath.Dir(cfg), "team.par"))
		if par == nil {
			par = map[string]string{}
		}
		c = parToCS(par)
	} else if !have {
		c = map[string]string{}
	}
	var n map[string]string
	var pw string
	if nap {
		n, pw = menu(cfg, c, why, !have && par["admin_pass"] == "", nap)
	} else {
		n, pw = menuSA(cfg, c, why) // setup_sa.go
	}
	if nap {
		err = saveNapp(cfg, par, n, pw)
	} else {
		err = writeConf(cfg, n)
	}
	if err != nil {
		log.Fatalf("%s: %v", cfg, err)
	}
	if pw != "" {
		os.Setenv("CS_ADMIN_PASS", pw) // fuer diesen Start (erster Admin eines leeren Speichers)
	}
	fmt.Printf("saved %s - cs-team runs in the foreground now (Ctrl+C stops it; next time just start cs-team)\n\n", cfg)
	os.Setenv("CS_CONF", cfg)
}

// parToCS: Vorgaben des napp-it-Menues (team.par) als CS_*-Schluessel fuer das Menue.
func parToCS(p map[string]string) map[string]string {
	c := map[string]string{
		"CS_LISTEN":     ":" + dflt(p["listen"], "9004"),
		"S3_ENDPOINT":   p["s3_endpoint"],
		"S3_KEY":        p["s3_key"],
		"S3_SECRET":     p["s3_secret"],
		"S3_BUCKET":     p["s3_bucket"],
		"CS_ADMIN_USER": p["admin_user"],
		"CS_TLS_CERT":   p["tls_cert"],
		"CS_TLS_KEY":    p["tls_key"],
	}
	if p["s3_tls"] == "yes" {
		c["S3_TLS"] = "1"
	}
	if p["storage"] != "s3" {
		c["CS_DIR"] = p["dir"]
	}
	if p["snap_mode"] == "off" {
		c["CS_SNAPSHOT"] = "off"
	}
	return c
}

// saveNapp: Menuewerte in team.par uebernehmen (uebrige Felder wie autostart, HA, LDAP bleiben) und team.cfg daraus erzeugen.
func saveNapp(cfg string, par, n map[string]string, pw string) error {
	par["listen"] = n["CS_LISTEN"][strings.LastIndex(n["CS_LISTEN"], ":")+1:]
	if n["CS_DIR"] != "" {
		par["storage"], par["dir"] = "dir", n["CS_DIR"]
	} else {
		par["storage"] = "s3"
		par["s3_endpoint"], par["s3_key"], par["s3_secret"], par["s3_bucket"] = n["S3_ENDPOINT"], n["S3_KEY"], n["S3_SECRET"], n["S3_BUCKET"]
		par["s3_tls"] = "no"
		if n["S3_TLS"] == "1" {
			par["s3_tls"] = "yes"
		}
	}
	par["admin_user"], par["tls_cert"], par["tls_key"] = n["CS_ADMIN_USER"], n["CS_TLS_CERT"], n["CS_TLS_KEY"]
	if pw != "" {
		par["admin_pass"] = pw
	}
	par["snap_mode"] = "auto"
	if n["CS_SNAPSHOT"] == "off" {
		par["snap_mode"] = "off"
	}
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		return err
	}
	var keys []string
	for k := range par {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + par[k] + "\n")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), "team.par"), []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.WriteFile(cfg, []byte(napCfg(par)), 0o600)
}

// napCfg: team.cfg aus team.par, genau wie das napp-it-Menue sie schreibt (cstlib.pl _cst_write_cfg).
func napCfg(c map[string]string) string {
	port := c["listen"]
	if port == "" || strings.Trim(port, "0123456789") != "" {
		port = "9004"
	}
	l := []string{"CS_LISTEN=:" + port}
	if c["storage"] == "s3" {
		tl := "0"
		if c["s3_tls"] == "yes" {
			tl = "1"
		}
		l = append(l, "S3_ENDPOINT="+c["s3_endpoint"], "S3_KEY="+c["s3_key"], "S3_SECRET="+c["s3_secret"], "S3_BUCKET="+c["s3_bucket"], "S3_TLS="+tl)
	} else {
		l = append(l, "CS_DIR="+c["dir"])
	}
	if c["admin_user"] != "" && c["admin_pass"] != "" {
		l = append(l, "CS_ADMIN_USER="+c["admin_user"], "CS_ADMIN_PASS="+c["admin_pass"])
	}
	if c["proxy"] == "yes" {
		l = append(l, "CS_TRUST_PROXY=1")
	}
	if c["tls_cert"] != "" {
		l = append(l, "CS_TLS_CERT="+c["tls_cert"])
		if c["tls_key"] != "" {
			l = append(l, "CS_TLS_KEY="+c["tls_key"])
		}
	}
	if c["snap_mode"] == "off" {
		l = append(l, "CS_SNAPSHOT=off")
	}
	if c["snap_dataset"] != "" {
		l = append(l, "CS_SNAPSHOT_DATASET="+c["snap_dataset"])
	}
	if c["snap_cmd"] != "" {
		l = append(l, "CS_SNAPSHOT_CMD="+c["snap_cmd"])
	}
	ha := dflt(c["ha_mode"], "off")
	if ha == "master" || ha == "standby" || ha == "takeover" {
		l = append(l, "CS_FAILOVER="+ha)
	}
	if ha != "off" && c["ha_name"] != "" {
		l = append(l, "CS_HA_NAME="+c["ha_name"])
	}
	if ha != "off" && c["ha_assume_off"] == "yes" {
		l = append(l, "CS_HA_ASSUME_OFF=1")
	}
	if c["ldap_listen"] != "" && c["tls_cert"] != "" {
		l = append(l, "CS_LDAP="+c["ldap_listen"])
		if c["ldap_base"] != "" {
			l = append(l, "CS_LDAP_BASE="+c["ldap_base"])
		}
		if c["ldap_reader"] != "" {
			l = append(l, "CS_LDAP_READER="+c["ldap_reader"])
		}
	}
	return strings.Join(l, "\n") + "\n"
}

// checkConf: Port frei, Speicher erreichbar und beschreibbar, Zertifikat lesbar, Webcraft-Ordner vorhanden.
func checkConf(cf map[string]string) error {
	c := map[string]string{}
	for k, v := range cf {
		c[k] = v
	}
	relPaths(c, exeDir()) // Standalone: ./ = Programmordner (wie loadConf mit team.cfg neben dem Programm)
	switch {
	case c["CS_MEM"] == "1":
	case c["CS_DIR"] != "":
		if err := dirOK(c["CS_DIR"]); err != nil {
			return err
		}
	case c["S3_ENDPOINT"] != "":
		if err := s3OK(c); err != nil {
			return err
		}
	default:
		return errors.New("no storage set (folder or S3)")
	}
	if cert := c["CS_TLS_CERT"]; cert != "" {
		if _, err := tls.LoadX509KeyPair(cert, dflt(c["CS_TLS_KEY"], cert)); err != nil {
			return fmt.Errorf("tls: %v", err)
		}
	}
	if w := c["CS_WEBCRAFT"]; w != "" && w != "off" {
		if st, err := os.Stat(w); err != nil || !st.IsDir() {
			return fmt.Errorf("webcraft folder %s does not exist", w)
		}
	}
	addr := dflt(c["CS_LISTEN"], ":8080")
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%s: %w", addr, errBusy)
	}
	l.Close()
	return nil
}

// dirOK: Ordner vorhanden und in <ordner>/.csteam beschreibbar.
func dirOK(d string) error {
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return fmt.Errorf("folder %s does not exist", d)
	}
	p := filepath.Join(d, ".csteam")
	if err := os.MkdirAll(p, 0o755); err != nil {
		return fmt.Errorf("folder %s: %v", d, err)
	}
	f, err := os.CreateTemp(p, ".setup-*")
	if err != nil {
		return fmt.Errorf("folder %s is not writable: %v", d, err)
	}
	n := f.Name()
	f.Close()
	os.Remove(n)
	return nil
}

// s3OK: Bucket erreichbar (wird bei Bedarf angelegt, wie beim Start), hoechstens 15 s.
func s3OK(c map[string]string) error {
	ch := make(chan error, 1)
	go func() {
		_, err := store.NewS3(c["S3_ENDPOINT"], c["S3_KEY"], c["S3_SECRET"], dflt(c["S3_BUCKET"], "cs-team"), c["S3_TLS"] == "1")
		ch <- err
	}()
	select {
	case err := <-ch:
		if err != nil {
			return fmt.Errorf("s3 %s: %v", c["S3_ENDPOINT"], err)
		}
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("s3 %s: no answer within 15 s", c["S3_ENDPOINT"])
	}
}

var stdin = bufio.NewReader(os.Stdin)

// ask: eine Zeile; Enter = Vorgabe, "-" = leer.
func ask(q, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", q, def)
	} else {
		fmt.Printf("%s: ", q)
	}
	s, err := stdin.ReadString('\n')
	if err != nil && strings.TrimSpace(s) == "" { // Eingabe beendet (Strg+D / Strg+Z)
		fmt.Println()
		os.Exit(1)
	}
	switch s = strings.TrimSpace(s); s {
	case "":
		return def
	case "-":
		return ""
	}
	return s
}

// askSecret: Eingabe ohne Anzeige (Passwort, S3-Secret).
func askSecret(q string) string {
	fmt.Printf("%s: ", q)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		os.Exit(1)
	}
	return strings.TrimSpace(string(b))
}

func dflt(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func yes(s string) bool {
	s = strings.ToLower(s)
	return s == "y" || s == "yes" || s == "j" || s == "ja" || s == "1"
}

func yn(b bool) string {
	if b {
		return "y"
	}
	return "n"
}

// menu: Einstellungen wie in napp-it System > Services > cs-team abfragen und pruefen (bei Fehler erneut fragen).
// first = noch keine Konfiguration und kein Startpasswort: dann ist es Pflicht (sonst kann sich auf einem leeren Speicher niemand anmelden).
// nap = napp-it-Modus: nur Port als Zahl, kein Webcraft-Feld (napp-it: _my/wwwroot/webcraft).
func menu(cfg string, c map[string]string, why error, first, nap bool) (map[string]string, string) {
	fmt.Printf("\ncs-team %s - setup, writes %s\n", version, cfg)
	fmt.Println("napp-it found: same settings as napp-it System > Services > cs-team (team.par + team.cfg).")
	fmt.Println("Enter keeps the value in [brackets], - clears it, Ctrl+C cancels.")
	for {
		if why != nil {
			fmt.Printf("\n  ! %v\n", why)
		}
		fmt.Println()
		n := map[string]string{}
		for k, v := range c { // Schluessel, die das Menue nicht zeigt (z. B. CS_TRUST_PROXY, CS_OIDC_*, CS_LDAP), bleiben erhalten
			n[k] = v
		}
		delete(n, "CS_ADMIN_PASS") // Startpasswort nie in die eigene team.cfg (napp-it: team.par/team.cfg wie im Menue)
		st := "folder"
		if c["CS_DIR"] == "" && c["S3_ENDPOINT"] != "" {
			st = "s3"
		}
		if strings.HasPrefix(strings.ToLower(ask("Storage: folder or s3", st)), "s") {
			delete(n, "CS_DIR")
			delete(n, "CS_SNAPSHOT")
			n["S3_ENDPOINT"] = ask("  S3 endpoint host:port", dflt(c["S3_ENDPOINT"], "127.0.0.1:9000"))
			n["S3_KEY"] = ask("  S3 access key", c["S3_KEY"])
			q := "  S3 secret key"
			if c["S3_SECRET"] != "" {
				q += " (Enter = unchanged)"
			}
			if s := askSecret(q); s != "" {
				n["S3_SECRET"] = s
			}
			n["S3_BUCKET"] = ask("  S3 bucket", dflt(c["S3_BUCKET"], "cs-team"))
			n["S3_TLS"] = "0"
			if yes(ask("  S3 with TLS (y/n)", yn(c["S3_TLS"] == "1"))) {
				n["S3_TLS"] = "1"
			}
		} else {
			if !nap { // napp-it behaelt die S3-Werte in team.par
				for _, k := range []string{"S3_ENDPOINT", "S3_KEY", "S3_SECRET", "S3_BUCKET", "S3_TLS"} {
					delete(n, k)
				}
			}
			n["CS_DIR"] = ask("  Folder, e.g. a ZFS filesystem (data goes to <folder>"+string(filepath.Separator)+".csteam)", c["CS_DIR"])
			if yes(ask("  ZFS snapshot before global actions (y/n)", yn(c["CS_SNAPSHOT"] != "off"))) {
				delete(n, "CS_SNAPSHOT")
			} else {
				n["CS_SNAPSHOT"] = "off"
			}
		}
		port := ask("Port", dflt(strings.TrimPrefix(c["CS_LISTEN"], ":"), "9004"))
		if nap { // napp-it kennt nur die Portnummer
			port = port[strings.LastIndex(port, ":")+1:]
		}
		if !strings.Contains(port, ":") {
			port = ":" + port
		}
		n["CS_LISTEN"] = port
		if cert := ask("TLS certificate, PEM file (none = plain http)", c["CS_TLS_CERT"]); cert == "" {
			delete(n, "CS_TLS_CERT")
			delete(n, "CS_TLS_KEY")
		} else {
			n["CS_TLS_CERT"] = cert
			if k := ask("TLS key, PEM file (none = inside the certificate file)", c["CS_TLS_KEY"]); k == "" {
				delete(n, "CS_TLS_KEY")
			} else {
				n["CS_TLS_KEY"] = k
			}
		}
		n["CS_ADMIN_USER"] = ask("Admin account (sysadmin)", dflt(c["CS_ADMIN_USER"], "admin"))
		pw := ""
		for {
			q := "Admin start password (only for a new, empty storage; must be changed at first login)"
			if !first {
				q = "Admin start password for a new, empty storage (Enter = unchanged)"
			}
			if pw = askSecret(q); pw == "" && !first {
				break
			}
			if err := auth.CheckPass(pw); err != nil {
				fmt.Println("  !", err)
				continue
			}
			if askSecret("Repeat the password") != pw {
				fmt.Println("  ! the passwords differ")
				continue
			}
			break
		}
		fmt.Println("\nchecking ...")
		if why = checkConf(n); why != nil {
			c = n
			continue
		}
		return n, pw
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// writeConf: team.cfg neben dem Programm (KEY=VALUE, sortiert, nur fuer den Besitzer lesbar: enthaelt ggf. das S3-Secret).
func writeConf(p string, m map[string]string) error {
	var keys []string
	for k, v := range m {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# cs-team configuration (KEY=VALUE), written by the setup menu " + time.Now().Format("2006-01-02 15:04") + "\n")
	b.WriteString("# change it with \"cs-team setup\" or an editor; all CS_* options: see README\n")
	for _, k := range keys {
		b.WriteString(k + "=" + m[k] + "\n")
	}
	return os.WriteFile(p, []byte(b.String()), 0o600)
}
