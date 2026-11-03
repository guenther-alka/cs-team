//go:build darwin

package main

import "fmt"

// macOS: not supported yet. Open Directory (LDAPv3) did not accept the cs-team LDAP server
// in the live test (opendirectoryd: "Failed to get rootDSE with error -9", with and without TLS),
// see docs/TODO_cs-ldapclient.md.

func notYet() bool {
	fmt.Println(tr("macOS wird von cs-ldapclient noch nicht unterstützt (Open Directory akzeptiert den cs-team-LDAP-Server noch nicht). Windows, Linux und FreeBSD funktionieren.",
		"macOS is not supported by cs-ldapclient yet (Open Directory does not accept the cs-team LDAP server yet). Windows, Linux and FreeBSD work."))
	return false
}

func doInstall(o options, c *Config, cfgPath string) bool { return notYet() }

func doUninstall(o options) bool { return notYet() }

func stateText() string { return "-" }
