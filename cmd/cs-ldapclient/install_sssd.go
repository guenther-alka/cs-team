//go:build linux || freebsd

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Shared by Linux and FreeBSD: sign-in with cs-team accounts through SSSD (LDAPS, rfc2307).

func have(cmd string) bool {
	_, err := runCmd("sh", "-c", "command -v "+cmd)
	return err == nil
}

func sssdConfig(c Config) string {
	var b strings.Builder
	b.WriteString("# written by cs-ldapclient\n[sssd]\nservices = nss, pam\ndomains = csteam\n\n[domain/csteam]\n")
	b.WriteString("id_provider = ldap\nauth_provider = ldap\nchpass_provider = none\n")
	fmt.Fprintf(&b, "ldap_uri = ldaps://%s:%d\n", c.Server, c.Port)
	fmt.Fprintf(&b, "ldap_search_base = %s\n", c.Base)
	fmt.Fprintf(&b, "ldap_tls_cacert = %s\nldap_tls_reqcert = demand\n", sssdCA)
	fmt.Fprintf(&b, "ldap_default_bind_dn = %s\nldap_default_authtok_type = password\nldap_default_authtok = %s\n", c.ReaderDN(), c.ReaderPassword)
	b.WriteString("ldap_schema = rfc2307\nldap_user_object_class = posixAccount\nldap_group_object_class = posixGroup\nldap_group_member = memberUid\n")
	fmt.Fprintf(&b, "ldap_user_search_base = ou=people,%s\nldap_group_search_base = ou=groups,%s\n", c.Base, c.Base)
	b.WriteString("fallback_homedir = /home/%u\ndefault_shell = " + defaultShell + "\ncache_credentials = true\nenumerate = false\n")
	return b.String()
}

// The script runs as root when a session closes (pam_exec). It removes the home directory of a
// directory user (one that is not in /etc/passwd) as soon as the user has no session or process left.
const logoutSh = `#!/bin/sh
# written by cs-ldapclient: delete the home directory of cs-team users after the last logout
busy() {
  if command -v loginctl >/dev/null 2>&1; then loginctl show-user "$1" >/dev/null 2>&1; else pgrep -u "$1" >/dev/null 2>&1; fi
}
if [ "$1" = "wait" ]; then
  U="$2"; H="$3"; i=0
  while busy "$U" && [ $i -lt 40 ]; do sleep 2; i=$((i+1)); done
  busy "$U" || rm -rf -- "$H"
  exit 0
fi
[ "$PAM_TYPE" = "close_session" ] || [ "$PAM_SM_FUNC" = "pam_sm_close_session" ] || exit 0
U="$PAM_USER"
[ -n "$U" ] || exit 0
grep -q "^$U:" /etc/passwd && exit 0
H=$(getent passwd "$U" | cut -d: -f6)
ID=$(id -u "$U" 2>/dev/null)
case "$H" in /home/?*|/usr/home/?*) ;; *) exit 0 ;; esac
[ -n "$ID" ] && [ "$ID" -ge 1000 ] || exit 0
if command -v setsid >/dev/null 2>&1; then
  setsid "$0" wait "$U" "$H" </dev/null >/dev/null 2>&1 &
elif command -v daemon >/dev/null 2>&1; then
  daemon -f "$0" wait "$U" "$H"
else
  nohup "$0" wait "$U" "$H" </dev/null >/dev/null 2>&1 &
fi
exit 0
`

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
func dirExists(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }

func writeSudoers(rules []GroupRule) {
	var b strings.Builder
	for _, r := range rules {
		if strings.EqualFold(r.Local, "Administrators") || strings.EqualFold(r.Local, "sudo") {
			fmt.Fprintf(&b, "%%%s ALL=(ALL:ALL) ALL\n", r.Group)
		}
	}
	if b.Len() == 0 {
		os.Remove(sudoersCfg)
		return
	}
	os.WriteFile(sudoersCfg, []byte("# written by cs-ldapclient: cs-team groups with administrator rights\n"+b.String()), 0o440)
	if have("visudo") {
		if out, err := runCmd("visudo", "-cf", sudoersCfg); err != nil {
			fmt.Println("sudoers:", out)
			os.Remove(sudoersCfg)
		}
	}
}

// hideUsers switches the user list off in the graphical login (GDM, LightDM), if present.
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
	dir := filepath.Dir(cfgPath)
	pemb, ok := ensureCert(c, o, filepath.Join(dir, c.CertFile))
	if !ok {
		return false
	}
	saveConfig(cfgPath, *c)
	if !checkServer(*c, pemb) {
		if o.yes || !askYN(tr("Der Server antwortet nicht richtig. Trotzdem installieren", "The server does not answer correctly. Install anyway"), false) {
			return false
		}
	} else if !o.yes {
		askTestUser(*c, pemb)
	}
	if !o.yes {
		fmt.Println(tr("\nVorher sicherstellen: Es gibt ein lokales Konto mit sudo-Rechten und Passwort (Rückweg, falls der Server nicht erreichbar ist).", "\nMake sure first: there is a local account with sudo rights and a password (the way back if the server is not reachable)."))
		if !askYN(tr("Fortfahren", "Continue"), false) {
			return false
		}
	}
	if !installPackages(dir) {
		return false
	}
	os.MkdirAll("/etc/sssd", 0o711)
	if err := os.WriteFile(sssdCA, pemb, 0o644); err != nil {
		fmt.Println(err)
		return false
	}
	if err := os.WriteFile(sssdConf, []byte(sssdConfig(*c)), 0o600); err != nil {
		fmt.Println(err)
		return false
	}
	os.Chown(sssdConf, 0, 0)
	writeSudoers(rules)
	osEnable(*c)
	osRestart()
	fmt.Println(tr("\nFertig. Ein Test: getent passwd <benutzer>", "\nDone. A test: getent passwd <user>"))
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
	osDisable()
	for _, f := range []string{sssdConf, sssdCA, sudoersCfg} {
		os.Remove(f)
	}
	fmt.Println(tr("Entfernt. Die Pakete (sssd) bleiben installiert.", "Removed. The packages (sssd) stay installed."))
	return true
}

func stateText() string {
	s := tr("nicht eingerichtet", "not set up")
	if fileExists(sssdConf) {
		s = tr("eingerichtet", "set up")
		if osActive() {
			s += ", " + tr("sssd läuft", "sssd running")
		}
	}
	return "SSSD: " + s
}
