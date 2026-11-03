//go:build windows || linux || freebsd || darwin

package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Command line of all systems. The system specific parts are doInstall, doUninstall, doTest, stateText.

func doShow(c Config, cfgPath string, found bool) {
	fmt.Printf("%s: %s", tr("Einstellungen", "Settings"), cfgPath)
	if !found {
		fmt.Print(tr(" (noch nicht vorhanden)", " (does not exist yet)"))
	}
	fmt.Println()
	showSettings(c)
	fmt.Println("\n" + stateText())
}

func run(o options) int {
	lang = uiLang()
	cfgPath := o.cfgPath
	if cfgPath == "" {
		cfgPath = filepath.Join(exeDir(), "ldap.cfg")
	}
	c, found := loadConfig(cfgPath)
	if c.Lang == "de" || c.Lang == "en" {
		lang = c.Lang
	}
	switch o.cmd {
	case "":
		return menu(o, &c, cfgPath)
	case "install":
		defer pause(o)
		if !doInstall(o, &c, cfgPath) {
			return 1
		}
	case "uninstall", "remove":
		defer pause(o)
		if !doUninstall(o) {
			return 1
		}
	case "show", "status":
		doShow(c, cfgPath, found)
	case "test":
		if !doTest(&c, cfgPath) {
			return 1
		}
	case "set":
		for _, a := range o.args {
			i := strings.IndexByte(a, '=')
			if i < 1 || !c.set(strings.ToLower(strings.TrimSpace(a[:i])), strings.TrimSpace(a[i+1:])) {
				fmt.Println(tr("Ungültig: ", "Invalid: ") + a)
				return 1
			}
		}
		if _, err := c.GroupRules(); err != nil {
			fmt.Println(err)
			return 1
		}
		if err := saveConfig(cfgPath, c); err != nil {
			fmt.Println(err)
			return 1
		}
		fmt.Println(tr("Gespeichert: ", "Saved: ") + cfgPath)
	default:
		fmt.Println(strings.TrimSpace(usage))
		return 2
	}
	return 0
}

const usage = `
cs-ldapclient [-y] [-cfg file] [install | uninstall | show | test | set key=value ...]
without a command: menu (install, uninstall, show/change settings, test)
settings: ldap.cfg next to the program (server, port, base, reader, reader_password, groups,
remove_profiles, short_setup, hide_users, cert_file, cert_sha256, installer, download_url, lang)
`
