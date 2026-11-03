package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Console user interface shared by all systems. System specific parts (uiLang, askSecret, isAdmin, pause,
// platformTitle) live in ui_windows.go and ui_unix.go.

var stdin = bufio.NewReader(os.Stdin)

func readLine() string {
	s, _ := stdin.ReadString('\n')
	return strings.TrimSpace(s)
}

func ask(label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	s := readLine()
	if s == "" {
		return def
	}
	return s
}

func askYN(label string, def bool) bool {
	d := tr("j/N", "y/N")
	if def {
		d = tr("J/n", "Y/n")
	}
	fmt.Printf("%s (%s): ", label, d)
	s := strings.ToLower(readLine())
	if s == "" {
		return def
	}
	return yes(s)
}

func exeDir() string {
	p, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(p)
}

// runCmd runs a program and returns its combined output.
func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func menu(o options, c *Config, cfgPath string) int {
	for {
		fmt.Printf("\ncs-ldapclient %s  -  %s\n", version, platformTitle())
		fmt.Printf("%s: %s\n\n", tr("Einstellungen", "Settings"), cfgPath)
		fmt.Println(tr("  1  Installieren", "  1  Install"))
		fmt.Println(tr("  2  Deinstallieren", "  2  Uninstall"))
		fmt.Println(tr("  3  Einstellungen anzeigen / ändern", "  3  Show / change settings"))
		fmt.Println(tr("  4  Server testen", "  4  Test the server"))
		fmt.Println(tr("  0  Ende", "  0  Exit"))
		fmt.Print("\n> ")
		switch readLine() {
		case "1":
			doInstall(o, c, cfgPath)
		case "2":
			doUninstall(o)
		case "3":
			editSettings(c, cfgPath)
		case "4":
			doTest(c, cfgPath)
		case "0", "q", "":
			return 0
		}
	}
}

func showSettings(c Config) {
	pw := ""
	if c.ReaderPassword != "" {
		pw = "********"
	}
	rows := [][2]string{
		{"server", c.Server}, {"port", fmt.Sprint(c.Port)}, {"base", c.Base},
		{"reader", c.Reader}, {"reader_password", pw}, {"groups", c.Groups},
		{"remove_profiles", yn(c.RemoveProfiles)}, {"short_setup", yn(c.ShortSetup)}, {"hide_users", yn(c.HideUsers)},
		{"cert_file", c.CertFile}, {"cert_sha256", prettyFP(c.CertSHA256)},
		{"installer", c.Installer}, {"lang", c.Lang},
	}
	for i, r := range rows {
		fmt.Printf("  %2d  %-16s %s\n", i+1, r[0], r[1])
	}
}

var keyOrder = []string{"server", "port", "base", "reader", "reader_password", "groups", "remove_profiles", "short_setup", "hide_users", "cert_file", "cert_sha256", "installer", "lang"}

func editSettings(c *Config, cfgPath string) {
	for {
		fmt.Println()
		showSettings(*c)
		fmt.Print(tr("\nNummer ändern, Eingabetaste = speichern und zurück: ", "\nNumber to change, Enter = save and back: "))
		s := readLine()
		if s == "" {
			if err := saveConfig(cfgPath, *c); err != nil {
				fmt.Println(tr("Speichern fehlgeschlagen: ", "Saving failed: ") + err.Error())
			} else {
				fmt.Println(tr("Gespeichert: ", "Saved: ") + cfgPath)
			}
			return
		}
		var n int
		if _, err := fmt.Sscan(s, &n); err != nil || n < 1 || n > len(keyOrder) {
			continue
		}
		k := keyOrder[n-1]
		var v string
		if k == "reader_password" {
			v = askSecret(k)
		} else if k == "cert_sha256" {
			fmt.Println(tr("Wird beim Installieren oder Testen gesetzt (Zertifikat bestätigen).", "Set at install or test (confirm the certificate)."))
			continue
		} else {
			v = ask(k, "")
		}
		if k == "groups" {
			t := *c
			t.Groups = v
			if _, err := t.GroupRules(); err != nil {
				fmt.Println(err)
				continue
			}
		}
		if !c.set(k, v) {
			fmt.Println(tr("Ungültiger Wert.", "Invalid value."))
		}
		if k == "server" || k == "port" {
			c.CertSHA256 = "" // another server: the certificate must be confirmed again
		}
	}
}

// wizard asks for what is missing.
func wizard(c *Config) {
	fmt.Println(tr("\nEinstellungen für den cs-team-LDAP-Server:", "\nSettings of the cs-team LDAP server:"))
	c.Server = ask(tr("IP oder Name des cs-team-Servers", "IP or name of the cs-team server"), c.Server)
	c.Port = atoiDef(ask(tr("LDAPS-Port", "LDAPS port"), fmt.Sprint(c.Port)), 636)
	c.Base = ask(tr("Basis-DN", "Base DN"), c.Base)
	c.Reader = ask(tr("Lesekonto (CS_LDAP_READER)", "Reader account (CS_LDAP_READER)"), c.Reader)
	if c.ReaderPassword == "" {
		c.ReaderPassword = askSecret(tr("Passwort des Lesekontos", "Password of the reader account"))
	}
	for {
		g := ask(tr("Gruppen: cs-team-Gruppe=lokale Gruppe, mit ; getrennt (z.B. klasse5a=Users;lehrer=Administrators)", "Groups: cs-team group=local group, separated by ; (e.g. klasse5a=Users;lehrer=Administrators)"), c.Groups)
		t := *c
		t.Groups = g
		if _, err := t.GroupRules(); err != nil {
			fmt.Println(err)
			continue
		}
		c.Groups = g
		break
	}
	c.RemoveProfiles = askYN(tr("Lokales Konto und Profil beim Abmelden löschen", "Delete the local account and profile at logout"), c.RemoveProfiles)
	c.ShortSetup = askYN(tr("Kurze Kontoerstellung (keine Datenschutzfragen)", "Short account creation (no privacy questions)"), c.ShortSetup)
	c.HideUsers = askYN(tr("Anmeldebildschirm: nur \"Anderer Benutzer\" und pGina", "Sign-in screen: only \"other user\" and pGina"), c.HideUsers)
}

func atoiDef(s string, d int) int {
	var n int
	if _, err := fmt.Sscan(s, &n); err != nil || n < 1 || n > 65535 {
		return d
	}
	return n
}
