//go:build darwin

package main

import (
	_ "embed"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// macOS: sign-in with cs-team accounts through Open Directory (LDAPv3 node, LDAPS, RFC2307 mappings).
// The node is created with the OpenDirectory API (macos/odhelper, universal binary, embedded here): the
// configuration files are not writable by scripts. The cs-team server must run with CS_LDAP_ANON=1,
// because the node cannot keep a search account (see docs/TODO_cs-ldapclient.md).

//go:embed macos/odhelper
var odhelper []byte

const (
	cleanupScript = "/usr/local/sbin/cs-ldapclient-cleanup"
	cleanupPlist  = "/Library/LaunchDaemons/org.csteam.ldapclient.cleanup.plist"
	loginPlist    = "/Library/Preferences/com.apple.loginwindow"
)

// cleanupSh deletes the home folder of cs-team users (not local accounts) that are not logged in.
const cleanupSh = `#!/bin/sh
# written by cs-ldapclient: delete the home folders of cs-team users after logout
NODE="$1"
CON=$(stat -f%Su /dev/console 2>/dev/null)
for d in /Users/*; do
  [ -d "$d" ] || continue
  n=$(basename "$d")
  case "$n" in Shared|Guest|.*) continue ;; esac
  dscl . -read "/Users/$n" >/dev/null 2>&1 && continue          # local account: keep
  dscl "$NODE" -read "/Users/$n" UniqueID >/dev/null 2>&1 || continue   # not a cs-team user (or server unreachable): keep
  [ "$n" = "$CON" ] && continue
  # still signed in (screen session or shell)? background daemons of a finished session do not count
  pgrep -u "$n" -x 'Dock|Finder|loginwindow|SystemUIServer|sshd|login|zsh|bash|sh|tcsh' >/dev/null 2>&1 && continue
  pkill -u "$n" >/dev/null 2>&1
  sleep 1
  rm -rf -- "$d"
done
exit 0
`

const sudoersFile = "/etc/sudoers.d/cs-ldapclient"

func nodeOf(c Config) string { return "/LDAPv3/" + c.Server }

func runHelper(args ...string) (string, error) {
	dir, err := os.MkdirTemp("", "cs-odhelper")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, "odhelper")
	if err := os.WriteFile(p, odhelper, 0o755); err != nil {
		return "", err
	}
	return runCmd(p, args...)
}

// trustCert puts the server certificate into the System keychain as trusted root (needs an admin session).
func trustCert(pemb []byte) {
	b, _ := pem.Decode(pemb)
	if b == nil {
		fmt.Println(tr("Zertifikat nicht lesbar.", "Certificate not readable."))
		return
	}
	cer := filepath.Join(os.TempDir(), "cs-team-ca.cer")
	os.WriteFile(cer, b.Bytes, 0o644)
	defer os.Remove(cer)
	out, err := runCmd("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", cer)
	if err != nil {
		fmt.Println(tr("Das Zertifikat konnte nicht eingetragen werden (dafür braucht macOS eine Anmeldung am Bildschirm):", "The certificate could not be added (macOS needs a sign-in at the screen for that):"), out)
		fmt.Println("  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain <cs-team-ca.cer>")
	}
}

func searchPathHas(node string) bool {
	out, _ := runCmd("dscl", "/Search", "-read", "/", "CSPSearchPath")
	return strings.Contains(out, node)
}

func doInstall(o options, c *Config, cfgPath string) bool {
	if !isAdmin() {
		fmt.Println(tr("Bitte als root starten (sudo).", "Please run as root (sudo)."))
		return false
	}
	if p := c.Problems(); len(p) > 0 {
		if o.yes {
			fmt.Println(tr("In der Konfiguration fehlt: ", "Missing in the settings: ") + strings.Join(p, ", "))
			return false
		}
		wizard(c)
		if err := saveConfig(cfgPath, *c); err != nil {
			fmt.Println(err)
		}
	}
	rules, err := c.GroupRules()
	if err != nil {
		fmt.Println(err)
		return false
	}
	pemb, ok := ensureCert(c, o, filepath.Join(filepath.Dir(cfgPath), c.CertFile))
	if !ok {
		return false
	}
	saveConfig(cfgPath, *c)
	if !checkServer(*c, pemb) {
		if o.yes || !askYN(tr("Der Server antwortet nicht richtig. Trotzdem installieren", "The server does not answer correctly. Install anyway"), false) {
			return false
		}
	}
	if !o.yes {
		fmt.Println(tr("\nVorher sicherstellen: Es gibt ein lokales Administratorkonto mit Passwort (Rückweg). Der cs-team-Server muss mit CS_LDAP_ANON=1 laufen (der Mac kann kein Suchkonto speichern).",
			"\nMake sure first: there is a local administrator account with a password (the way back). The cs-team server must run with CS_LDAP_ANON=1 (the Mac cannot keep a search account)."))
		if !askYN(tr("Fortfahren", "Continue"), false) {
			return false
		}
	}
	trustCert(pemb)
	if out, err := runHelper("add", c.Server, c.Base, fmt.Sprint(c.Port)); err != nil {
		fmt.Println("odhelper:", out)
		return false
	}
	node := nodeOf(*c)
	if !searchPathHas(node) {
		runCmd("dscl", "/Search", "-create", "/", "SearchPolicy", "dsAttrTypeStandard:CSPSearchPath")
		runCmd("dscl", "/Search", "-append", "/", "CSPSearchPath", node)
	}
	// Nesting an LDAP group into the local admin group is not honoured by macOS,
	// so administrator rights are given with a sudoers rule (sudo and admin tools).
	var sudoLines string
	for _, r := range rules {
		if strings.EqualFold(r.Local, "Administrators") || strings.EqualFold(r.Local, "admin") {
			if !strings.ContainsAny(r.Group, " \t\"'\\#\n") {
				sudoLines += "%" + r.Group + " ALL=(ALL) ALL\n"
			}
		}
	}
	os.Remove(sudoersFile)
	if sudoLines != "" {
		tmp := sudoersFile + ".new"
		os.WriteFile(tmp, []byte("# cs-ldapclient: cs-team groups with administrator rights\n"+sudoLines), 0o440)
		if _, err := runCmd("visudo", "-cf", tmp); err == nil {
			os.Rename(tmp, sudoersFile)
		} else {
			os.Remove(tmp)
			fmt.Println(tr("sudoers-Regel ungültig, nicht gesetzt.", "sudoers rule invalid, not set."))
		}
	}
	if c.HideUsers {
		runCmd("defaults", "write", loginPlist, "SHOWFULLNAME", "-bool", "true")
	}
	os.Remove(cleanupPlist)
	if c.RemoveProfiles {
		os.MkdirAll(filepath.Dir(cleanupScript), 0o755)
		os.WriteFile(cleanupScript, []byte(cleanupSh), 0o755)
		pl := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>org.csteam.ldapclient.cleanup</string>
<key>ProgramArguments</key><array><string>` + cleanupScript + `</string><string>` + node + `</string></array>
<key>StartInterval</key><integer>120</integer>
<key>RunAtLoad</key><true/>
</dict></plist>
`
		os.WriteFile(cleanupPlist, []byte(pl), 0o644)
		runCmd("launchctl", "bootout", "system/org.csteam.ldapclient.cleanup")
		runCmd("launchctl", "bootstrap", "system", cleanupPlist)
	}
	runCmd("killall", "opendirectoryd")
	runCmd("dscacheutil", "-flushcache")
	out, _ := runCmd("dscl", "localhost", "-list", node+"/Users")
	if strings.TrimSpace(out) == "" || strings.Contains(out, "Error") {
		fmt.Println(tr("Hinweis: Es werden noch keine Benutzer gelesen. Läuft der cs-team-Server mit CS_LDAP_ANON=1 und ist das Zertifikat vertraut? Nach einer Minute: dscl localhost -list "+node+"/Users",
			"Note: no users are read yet. Does the cs-team server run with CS_LDAP_ANON=1 and is the certificate trusted? After a minute: dscl localhost -list "+node+"/Users"))
	} else {
		fmt.Println(tr("\nFertig. Benutzer vom Server:\n", "\nDone. Users from the server:\n") + out)
	}
	fmt.Println(tr("Anmeldung am Bildschirm mit Name und Passwort. Das Home wird beim ersten Login angelegt.", "Sign in at the screen with name and password. The home folder is created at the first login."))
	return true
}

func doUninstall(o options) bool {
	if !isAdmin() {
		fmt.Println(tr("Bitte als root starten (sudo).", "Please run as root (sudo)."))
		return false
	}
	if !o.yes && !askYN(tr("Die cs-team-Anmeldung entfernen (lokale Konten bleiben)", "Remove the cs-team sign-in (local accounts stay)"), false) {
		return false
	}
	runCmd("launchctl", "bootout", "system/org.csteam.ldapclient.cleanup")
	os.Remove(cleanupPlist)
	os.Remove(cleanupScript)
	cp := o.cfgPath
	if cp == "" {
		cp = filepath.Join(exeDir(), "ldap.cfg")
	}
	cfg, found := loadConfig(cp)
	if !found || cfg.Server == "" {
		fmt.Println(tr("ldap.cfg nicht gefunden: der Server ist unbekannt, der Knoten bleibt (Verzeichnisdienstprogramm > LDAPv3).", "ldap.cfg not found: the server is unknown, the node stays (Directory Utility > LDAPv3)."))
	} else {
		node := nodeOf(cfg)
		if searchPathHas(node) {
			runCmd("dscl", "/Search", "-delete", "/", "CSPSearchPath", node)
		}
		if _, err := runHelper("remove", cfg.Server); err != nil {
			fmt.Println(tr("Knoten nicht entfernt: ", "Node not removed: ") + node)
		}
	}
	os.Remove(sudoersFile)
	runCmd("defaults", "delete", loginPlist, "SHOWFULLNAME")
	fmt.Println(tr("Entfernt. Das Zertifikat bleibt im Schlüsselbund (Schlüsselbundverwaltung, System).", "Removed. The certificate stays in the keychain (Keychain Access, System)."))
	return true
}

func stateText() string {
	out, _ := runCmd("dscl", "localhost", "-list", "/LDAPv3")
	s := tr("nicht eingerichtet", "not set up")
	if strings.TrimSpace(out) != "" {
		s = tr("eingerichtet", "set up") + ": " + strings.Join(strings.Fields(out), ", ")
	}
	return "Open Directory: " + s
}
