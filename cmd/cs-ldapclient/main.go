// cs-ldapclient installs and configures pGina on a Windows computer so that users sign in with their cs-team account
// (cs-team LDAPS server). It is started by a local administrator, e.g. from a USB stick with ldap.cfg next to it.
//
//	cs-ldapclient.exe                 menu: install / uninstall / show or change settings
//	cs-ldapclient.exe install [-y]    install pGina and apply ldap.cfg
//	cs-ldapclient.exe uninstall [-y]  remove pGina and the policies again
//	cs-ldapclient.exe show            show the settings and the state
//	cs-ldapclient.exe set key=value   change settings in ldap.cfg (no installation)
//	cs-ldapclient.exe test            test server, certificate and reader account
//	-cfg <file>  use another settings file (default: ldap.cfg next to the program)
package main

import (
	"os"
	"strings"
)

const version = "0.1.0"

var lang = "en"

// tr returns the German or the English text.
func tr(de, en string) string {
	if lang == "de" {
		return de
	}
	return en
}

type options struct {
	yes     bool
	cfgPath string
	cmd     string
	args    []string
}

func parseArgs(a []string) options {
	var o options
	for i := 0; i < len(a); i++ {
		switch s := a[i]; {
		case s == "-y" || s == "--yes" || s == "/y":
			o.yes = true
		case s == "-cfg" || s == "--cfg":
			if i+1 < len(a) {
				i++
				o.cfgPath = a[i]
			}
		case o.cmd == "":
			o.cmd = strings.ToLower(strings.TrimLeft(s, "-/"))
		default:
			o.args = append(o.args, s)
		}
	}
	return o
}

func main() {
	os.Exit(run(parseArgs(os.Args[1:])))
}
