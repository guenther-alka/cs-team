package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ---- certificate ----

// ensureCert makes sure that the server certificate is next to the program and accepted. It returns the PEM.
func ensureCert(c *Config, o options, certPath string) ([]byte, bool) {
	if pemb, err := os.ReadFile(certPath); err == nil {
		fp, err := fingerprintOfPEM(pemb)
		if err == nil && (c.CertSHA256 == "" || c.CertSHA256 == fp) {
			c.CertSHA256 = fp
			return pemb, true
		}
		fmt.Println(tr("Das Zertifikat in ", "The certificate in ") + certPath + tr(" passt nicht zum gespeicherten Fingerabdruck.", " does not match the stored fingerprint."))
	}
	pemb, fp, err := fetchCert(c.Server, c.Port)
	if err != nil {
		fmt.Println(tr("Zertifikat vom Server nicht lesbar: ", "Cannot read the certificate from the server: ") + err.Error())
		return nil, false
	}
	if c.CertSHA256 != "" && c.CertSHA256 == fp {
		// accepted before (e.g. on another computer): trust the stored fingerprint
	} else {
		fmt.Printf("\n%s %s:%d\n  SHA-256  %s\n", tr("Zertifikat des Servers", "Server certificate of"), c.Server, c.Port, prettyFP(fp))
		if c.CertSHA256 != "" {
			fmt.Println(tr("  ACHTUNG: weicht vom gespeicherten Fingerabdruck ab: ", "  WARNING: differs from the stored fingerprint: ") + prettyFP(c.CertSHA256))
		}
		if o.yes {
			fmt.Println(tr("Mit -y muss cert_sha256 in ldap.cfg zum Serverzertifikat passen (oder ldap.pem liegt daneben).", "With -y, cert_sha256 in ldap.cfg must match the server certificate (or ldap.pem must be next to it)."))
			return nil, false
		}
		if !askYN(tr("Diesem Zertifikat vertrauen (Fingerabdruck am Server vergleichen)", "Trust this certificate (compare the fingerprint on the server)"), false) {
			fmt.Println(tr("Nicht bestätigt, Abbruch.", "Not confirmed, aborted."))
			return nil, false
		}
	}
	if err := os.WriteFile(certPath, pemb, 0o644); err != nil {
		fmt.Println(err)
		return nil, false
	}
	c.CertSHA256 = fp
	return pemb, true
}

// checkServer binds as the reader with the accepted certificate.
func checkServer(c Config, pemb []byte) bool {
	pool, err := poolFromPEM(pemb)
	if err != nil {
		fmt.Println(err)
		return false
	}
	code, err := ldapBind(c.Server, c.Port, pool, c.ReaderDN(), c.ReaderPassword)
	fmt.Printf("  %s %s:%d  %s: %s\n", tr("Server", "Server"), c.Server, c.Port, tr("Lesekonto", "reader account"), bindMessage(code, err))
	return err == nil && code == 0
}

func askTestUser(c Config, pemb []byte) {
	u := ask(tr("Testbenutzer (cs-team-Name, leer = überspringen)", "Test user (cs-team name, empty = skip)"), "")
	if u == "" {
		return
	}
	pw := askSecret(tr("Passwort", "Password"))
	pool, _ := poolFromPEM(pemb)
	code, err := ldapBind(c.Server, c.Port, pool, "uid="+u+",ou=people,"+c.Base, pw)
	fmt.Printf("  %s: %s\n", u, bindMessage(code, err))
}

// ---- test / show ----

func doTest(c *Config, cfgPath string) bool {
	if p := c.Problems(); len(p) > 0 {
		fmt.Println(tr("In der Konfiguration fehlt: ", "Missing in the settings: ") + strings.Join(p, ", "))
		return false
	}
	certPath := filepath.Join(filepath.Dir(cfgPath), c.CertFile)
	pemb, ok := ensureCert(c, options{}, certPath)
	if !ok {
		return false
	}
	saveConfig(cfgPath, *c)
	good := checkServer(*c, pemb)
	if good {
		askTestUser(*c, pemb)
	}
	return good
}
