//go:build freebsd

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FreeBSD: sign-in with cs-team accounts through SSSD (package sssd2), same as on Linux.
// NSS: passwd/group "files sss". PAM: lines marked "# cs-ldapclient" in /etc/pam.d/system and /etc/pam.d/sshd.

const defaultShell = "/bin/sh"

const (
	sssdConf      = "/usr/local/etc/sssd/sssd.conf"
	sssdCA        = "/usr/local/etc/sssd/cs-team-ca.pem"
	sudoersCfg    = "/usr/local/etc/sudoers.d/cs-ldapclient"
	logoutScript  = "/usr/local/sbin/cs-ldapclient-logout"
	pamMark       = "# cs-ldapclient"
	nsswitchOrig  = "/etc/nsswitch.conf.cs-ldapclient.orig"
	nsswitchFile  = "/etc/nsswitch.conf"
	sssdBinary    = "/usr/local/sbin/sssd"
	pamLibMkHome  = "/usr/local/lib/pam_mkhomedir.so"
	pamSystemFile = "/etc/pam.d/system"
	pamSshdFile   = "/etc/pam.d/sshd"
)

// installPackages installs sssd2, pam_mkhomedir and sudo. Packages (*.pkg) in the settings folder are used first.
func installPackages(dir string) bool {
	if fileExists(sssdBinary) && fileExists(pamLibMkHome) {
		return true
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.pkg")); len(m) > 0 {
		fmt.Println(tr("Installiere Pakete aus dem Ordner ...", "Installing packages from the folder ..."))
		if out, err := runCmd("pkg", append([]string{"add"}, m...)...); err != nil {
			fmt.Println(out)
		}
	}
	if fileExists(sssdBinary) && fileExists(pamLibMkHome) {
		return true
	}
	fmt.Println(tr("Installiere sssd2 mit pkg ...", "Installing sssd2 with pkg ..."))
	out, err := runCmd("sh", "-c", "env ASSUME_ALWAYS_YES=yes pkg install -y sssd2 pam_mkhomedir sudo")
	if err != nil {
		fmt.Println(out)
		return false
	}
	return fileExists(sssdBinary)
}

// ensureNSS makes passwd and group read local files first and then sss (original is saved once).
func ensureNSS() {
	b, err := os.ReadFile(nsswitchFile)
	if err != nil {
		return
	}
	if !fileExists(nsswitchOrig) {
		os.WriteFile(nsswitchOrig, b, 0o644)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		for _, db := range []string{"passwd:", "group:"} {
			if strings.HasPrefix(l, db) && !strings.Contains(l, " sss") {
				lines[i] = db + " files sss"
			}
		}
	}
	os.WriteFile(nsswitchFile, []byte(strings.Join(lines, "\n")), 0o644)
}

func restoreNSS() {
	if b, err := os.ReadFile(nsswitchOrig); err == nil {
		os.WriteFile(nsswitchFile, b, 0o644)
		os.Remove(nsswitchOrig)
	}
}

// patchPAM removes all lines of cs-ldapclient from a PAM file and, if add is true, puts them in again:
// auth and account before the first pam_unix line, session at the end.
func patchPAM(path string, add bool, profiles bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.Contains(l, pamMark) {
			lines = append(lines, l)
		}
	}
	if add {
		var out []string
		doneAuth, doneAcc := false, false
		for _, l := range lines {
			f := strings.Fields(l)
			if len(f) >= 3 && strings.HasSuffix(f[2], "pam_unix.so") {
				if f[0] == "auth" && !doneAuth {
					out = append(out, "auth\t\tsufficient\tpam_sss.so\t\tno_warn "+pamMark)
					doneAuth = true
				}
				if f[0] == "account" && !doneAcc {
					out = append(out, "account\t\tsufficient\tpam_sss.so "+pamMark)
					doneAcc = true
				}
			}
			out = append(out, l)
		}
		out = append(out, "session\t\toptional\tpam_mkhomedir.so "+pamMark)
		if profiles {
			out = append(out, "session\t\toptional\tpam_exec.so\t\t"+logoutScript+" "+pamMark)
		}
		lines = out
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func osEnable(c Config) {
	ensureNSS()
	patchPAM(pamSystemFile, true, c.RemoveProfiles)
	patchPAM(pamSshdFile, true, c.RemoveProfiles)
	if c.RemoveProfiles {
		os.WriteFile(logoutScript, []byte(logoutSh), 0o755)
	} else {
		os.Remove(logoutScript)
	}
	os.MkdirAll("/usr/local/etc/sssd", 0o711)
}

func osRestart() {
	runCmd("sh", "-c", "sysrc sssd_enable=YES >/dev/null 2>&1; sss_cache -E 2>/dev/null; service sssd restart 2>&1")
}

func osDisable() {
	runCmd("sh", "-c", "service sssd stop >/dev/null 2>&1; sysrc sssd_enable=NO >/dev/null 2>&1")
	patchPAM(pamSystemFile, false, false)
	patchPAM(pamSshdFile, false, false)
	restoreNSS()
	os.Remove(logoutScript)
}

func osActive() bool {
	_, err := runCmd("service", "sssd", "status")
	return err == nil
}
