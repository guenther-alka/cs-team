//go:build linux || freebsd || darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func platformTitle() string {
	switch runtime.GOOS {
	case "darwin":
		return tr("macOS-Anmeldung mit cs-team-Konten (LDAP)", "macOS sign-in with cs-team accounts (LDAP)")
	case "freebsd":
		return tr("FreeBSD-Anmeldung mit cs-team-Konten (LDAP)", "FreeBSD sign-in with cs-team accounts (LDAP)")
	}
	return tr("Linux-Anmeldung mit cs-team-Konten (SSSD)", "Linux sign-in with cs-team accounts (SSSD)")
}

func uiLang() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "de") {
				return "de"
			}
			return "en"
		}
	}
	return "en"
}

// askSecret reads a line without echo (stty).
func askSecret(label string) string {
	fmt.Printf("%s: ", label)
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	if off.Run() == nil {
		defer func() {
			on := exec.Command("stty", "echo")
			on.Stdin = os.Stdin
			on.Run()
		}()
	}
	s := readLine()
	fmt.Println()
	return s
}

func isAdmin() bool { return os.Geteuid() == 0 }

func pause(o options) {}
