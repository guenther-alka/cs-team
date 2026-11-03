//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Linux: sign-in with cs-team accounts through SSSD (LDAPS, rfc2307). cs-team delivers posixAccount/posixGroup,
// so users, groups, uid/gid, home and shell come straight from the server.

const defaultShell = "/bin/bash"

const (
	sssdConf   = "/etc/sssd/sssd.conf"
	sssdCA     = "/etc/sssd/cs-team-ca.pem"
	sudoersCfg = "/etc/sudoers.d/cs-ldapclient"
	gdmDconf   = "/etc/dconf/db/gdm.d/00-cs-ldapclient"
	gdmProfile = "/etc/dconf/profile/gdm"
	lightdmCfg = "/etc/lightdm/lightdm.conf.d/50-cs-ldapclient.conf"
)

// installPackages installs sssd. Packages (*.deb / *.rpm) in the settings folder are used first, then the package manager.
func installPackages(dir string) bool {
	if have("sssd") && (fileExists("/usr/lib/x86_64-linux-gnu/sssd/libsss_ldap.so") || fileExists("/usr/lib64/sssd/libsss_ldap.so") || have("sssctl")) {
		return true
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.deb")); len(m) > 0 && have("dpkg") {
		fmt.Println(tr("Installiere Pakete aus dem Ordner ...", "Installing packages from the folder ..."))
		if out, err := runCmd("dpkg", append([]string{"-i"}, m...)...); err != nil {
			fmt.Println(out)
		}
	} else if m, _ := filepath.Glob(filepath.Join(dir, "*.rpm")); len(m) > 0 && have("rpm") {
		fmt.Println(tr("Installiere Pakete aus dem Ordner ...", "Installing packages from the folder ..."))
		if out, err := runCmd("rpm", append([]string{"-U", "--replacepkgs"}, m...)...); err != nil {
			fmt.Println(out)
		}
	}
	if have("sssd") {
		return true
	}
	fmt.Println(tr("Installiere sssd mit dem Paketmanager ...", "Installing sssd with the package manager ..."))
	var out string
	var err error
	switch {
	case have("apt-get"):
		out, err = runCmd("sh", "-c", "DEBIAN_FRONTEND=noninteractive apt-get update; DEBIAN_FRONTEND=noninteractive apt-get install -y sssd sssd-ldap libnss-sss libpam-sss ca-certificates")
	case have("dnf"):
		out, err = runCmd("dnf", "install", "-y", "sssd", "sssd-ldap", "oddjob-mkhomedir", "authselect")
	case have("zypper"):
		out, err = runCmd("zypper", "--non-interactive", "install", "sssd", "sssd-ldap")
	case have("pacman"):
		out, err = runCmd("pacman", "-S", "--noconfirm", "sssd")
	default:
		fmt.Println(tr("Kein bekannter Paketmanager. sssd bitte von Hand installieren oder .deb/.rpm neben ldap.cfg legen.", "No known package manager. Install sssd by hand or put .deb/.rpm next to ldap.cfg."))
		return false
	}
	if err != nil {
		fmt.Println(out)
		return false
	}
	return have("sssd")
}

// ensureNSS adds sss to passwd and group in nsswitch.conf if it is missing.
func ensureNSS() {
	b, err := os.ReadFile("/etc/nsswitch.conf")
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		for _, db := range []string{"passwd:", "group:", "shadow:"} {
			if strings.HasPrefix(l, db) && !strings.Contains(l, " sss") {
				lines[i] = strings.TrimRight(l, " \t") + " sss"
			}
		}
	}
	os.WriteFile("/etc/nsswitch.conf", []byte(strings.Join(lines, "\n")), 0o644)
}

func enablePAM() {
	switch {
	case have("pam-auth-update"):
		runCmd("sh", "-c", "DEBIAN_FRONTEND=noninteractive pam-auth-update --enable sss mkhomedir")
	case have("authselect"):
		runCmd("authselect", "select", "sssd", "with-mkhomedir", "--force")
		runCmd("systemctl", "enable", "--now", "oddjobd")
	}
}

func hideUsers(on bool) {
	if dirExists("/etc/gdm3") || dirExists("/etc/gdm") {
		if on {
			os.MkdirAll("/etc/dconf/profile", 0o755)
			os.MkdirAll("/etc/dconf/db/gdm.d", 0o755)
			if !fileExists(gdmProfile) {
				os.WriteFile(gdmProfile, []byte("user-db:user\nsystem-db:gdm\nfile-db:/usr/share/gdm/greeter-dconf-defaults\n"), 0o644)
			}
			os.WriteFile(gdmDconf, []byte("[org/gnome/login-screen]\ndisable-user-list=true\n"), 0o644)
		} else {
			os.Remove(gdmDconf)
		}
		if have("dconf") {
			runCmd("dconf", "update")
		}
	}
	if dirExists("/etc/lightdm") {
		if on {
			os.MkdirAll("/etc/lightdm/lightdm.conf.d", 0o755)
			os.WriteFile(lightdmCfg, []byte("[Seat:*]\ngreeter-hide-users=true\ngreeter-show-manual-login=true\n"), 0o644)
		} else {
			os.Remove(lightdmCfg)
		}
	}
}

const (
	logoutScript  = "/usr/local/sbin/cs-ldapclient-logout"
	logoutProfile = "/usr/share/pam-configs/cs-ldapclient-cleanup"
)

const logoutPam = `Name: cs-ldapclient: delete home directory of cs-team users after logout
Default: no
Priority: 0
Session-Type: Additional
Session:
	optional	pam_exec.so quiet /usr/local/sbin/cs-ldapclient-logout
`

// profileRemoval switches the deletion of the home directory at logout on or off.
// Works with pam-auth-update (Debian, Ubuntu) and systemd; returns false if that is not available.
func profileRemoval(on bool) bool {
	if !have("pam-auth-update") || !have("loginctl") {
		return false
	}
	if on {
		if err := os.WriteFile(logoutScript, []byte(logoutSh), 0o755); err != nil {
			return false
		}
		if err := os.WriteFile(logoutProfile, []byte(logoutPam), 0o644); err != nil {
			return false
		}
		runCmd("sh", "-c", "DEBIAN_FRONTEND=noninteractive pam-auth-update --enable cs-ldapclient-cleanup")
		return true
	}
	if fileExists(logoutProfile) {
		runCmd("sh", "-c", "DEBIAN_FRONTEND=noninteractive pam-auth-update --remove cs-ldapclient-cleanup")
	}
	os.Remove(logoutProfile)
	os.Remove(logoutScript)
	return true
}

// osEnable switches the system over: name service, PAM, login screen, profile removal.
func osEnable(c Config) {
	ensureNSS()
	enablePAM()
	hideUsers(c.HideUsers)
	if c.RemoveProfiles {
		if !profileRemoval(true) {
			fmt.Println(tr("Hinweis: remove_profiles (Profil beim Abmelden löschen) geht hier nur mit Debian/Ubuntu (pam-auth-update) und systemd.", "Note: remove_profiles (delete the profile at logout) works here only with Debian/Ubuntu (pam-auth-update) and systemd."))
		}
	} else {
		profileRemoval(false)
	}
}

func osRestart() {
	runCmd("sh", "-c", "sss_cache -E 2>/dev/null; systemctl enable sssd >/dev/null 2>&1; systemctl restart sssd")
}

func osDisable() {
	runCmd("sh", "-c", "systemctl disable --now sssd >/dev/null 2>&1")
	if have("pam-auth-update") {
		runCmd("sh", "-c", "DEBIAN_FRONTEND=noninteractive pam-auth-update --remove sss mkhomedir")
	} else if have("authselect") {
		runCmd("authselect", "select", "minimal", "--force")
	}
	profileRemoval(false)
	hideUsers(false)
}

func osActive() bool {
	out, _ := runCmd("systemctl", "is-active", "sssd")
	return strings.TrimSpace(out) == "active"
}
