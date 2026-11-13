package main

// Standalone-Setup (ohne napp-it oder mit standalone.set), KISS:
//
//	1. Daten-Ordner   Vorgabe ./        Daten in <ordner>/.csteam
//	2. PEM-Ordner     Vorgabe ./pem     fehlt das Zertifikat, wird ein eigenes (selbst signiert) angelegt; off = http
//	3. Port           Vorgabe 9004
//	4. Webcraft       Vorgabe ./webcraft (je App ein Unterordner, z. B. ./webcraft/wordcraft); off = keine
//	5. Admin + Startpasswort, nur solange der Speicher leer ist
//
// Relative Pfade gelten ab dem Programmordner (team.cfg liegt dort), absolute Pfade sind erlaubt.
// Kein ZFS noetig: CS_SNAPSHOT=off, Snapshots macht der Benutzer selbst. S3 nur durch Bearbeiten von team.cfg.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cs-team/auth"
)

// pathKeys: Schluessel mit Pfaden, die relativ angegeben werden duerfen.
var pathKeys = []string{"CS_DIR", "CS_TLS_CERT", "CS_TLS_KEY", "CS_WEBCRAFT", "CS_SHARE"}

// relPaths: relative Pfade ab base aufloesen; absolute Pfade, leere Werte und "off" bleiben unveraendert.
func relPaths(m map[string]string, base string) {
	for _, k := range pathKeys {
		if v := m[k]; v != "" && v != "off" && !filepath.IsAbs(v) {
			m[k] = filepath.Join(base, v)
		}
	}
}

// usersExist: der Speicher-Ordner hat schon Benutzer (dann kein Startpasswort noetig).
func usersExist(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".csteam", "users", "users.json"))
	return err == nil
}

// menuSA: Standalone-Menue; Werte bleiben so, wie eingegeben (auch ./), team.cfg wird damit portabel.
func menuSA(cfg string, c map[string]string, why error) (map[string]string, string) {
	fmt.Printf("\ncs-team %s - setup, writes %s\n", version, cfg)
	if standalone() {
		fmt.Println("standalone.set found: napp-it settings are not used.")
	}
	fmt.Println("Relative paths (./) start at the program folder, absolute paths are allowed.")
	fmt.Println("Enter keeps the value in [brackets], Ctrl+C cancels. No ZFS needed: make snapshots yourself if you want them.")
	base := exeDir()
	abs := func(p string) string {
		if p == "" || p == "off" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	for {
		if why != nil {
			fmt.Printf("\n  ! %v\n", why)
		}
		fmt.Println()
		n := map[string]string{}
		for k, v := range c { // Schluessel, die das Menue nicht zeigt (OIDC, LDAP, HA ...), bleiben erhalten
			n[k] = v
		}
		delete(n, "CS_ADMIN_PASS")
		n["CS_SNAPSHOT"] = "off"
		if c["CS_DIR"] == "" && c["S3_ENDPOINT"] != "" {
			fmt.Println("Storage: S3 from team.cfg (edit team.cfg to change it)")
		} else {
			n["CS_DIR"] = ask("1. Data folder (data goes to <folder>"+string(filepath.Separator)+".csteam)", dflt(c["CS_DIR"], "./"))
		}
		pemDir := "./pem"
		if c["CS_TLS_CERT"] != "" {
			pemDir = filepath.Dir(c["CS_TLS_CERT"])
			if !filepath.IsAbs(c["CS_TLS_CERT"]) && !strings.HasPrefix(pemDir, ".") {
				pemDir = "./" + pemDir
			}
		} else if c["CS_DIR"] != "" || c["CS_LISTEN"] != "" { // vorhandene team.cfg ohne Zertifikat = http
			pemDir = "off"
		}
		pemDir = ask("2. PEM folder for the TLS certificate (created if missing; off = plain http)", pemDir)
		if pemDir == "off" {
			delete(n, "CS_TLS_CERT")
			delete(n, "CS_TLS_KEY")
		} else {
			cert, key := filepath.Join(pemDir, "cert.pem"), filepath.Join(pemDir, "key.pem")
			if !filepath.IsAbs(pemDir) { // relativ bleibt relativ (./pem/cert.pem)
				cert, key = "./"+filepath.ToSlash(cert), "./"+filepath.ToSlash(key)
			}
			if made, err := genCert(abs(cert), abs(key)); err != nil {
				fmt.Println("  !", err)
			} else if made {
				fmt.Println("  created a private (self-signed) certificate:", abs(cert))
			}
			n["CS_TLS_CERT"], n["CS_TLS_KEY"] = cert, key
		}
		port := ask("3. Port", dflt(strings.TrimPrefix(c["CS_LISTEN"], ":"), "9004"))
		if !strings.Contains(port, ":") {
			port = ":" + port
		}
		n["CS_LISTEN"] = port
		wc := ask("4. Webcraft folder, one sub folder per app, e.g. ./webcraft/wordcraft (off = none)", dflt(c["CS_WEBCRAFT"], "./webcraft"))
		if wc != "off" {
			os.MkdirAll(abs(wc), 0o755) // leer anlegen: Apps spaeter hineinkopieren
		}
		n["CS_WEBCRAFT"] = wc
		pw := ""
		if n["CS_DIR"] == "" || !usersExist(abs(n["CS_DIR"])) {
			n["CS_ADMIN_USER"] = ask("5. Admin account (sysadmin)", dflt(c["CS_ADMIN_USER"], "admin"))
			for {
				pw = askSecret("   Admin start password (must be changed at first login)")
				if err := auth.CheckPass(pw); err != nil {
					fmt.Println("  !", err)
					continue
				}
				if askSecret("   Repeat the password") != pw {
					fmt.Println("  ! the passwords differ")
					continue
				}
				break
			}
		}
		fmt.Println("\nchecking ...")
		if why = checkConf(n); why != nil {
			c = n
			continue
		}
		return n, pw
	}
}

// genCert: legt ein eigenes, selbst signiertes Zertifikat an (ECDSA P-256, 10 Jahre, Rechnername, localhost und alle
// eigenen IP-Adressen), wenn cert oder key fehlt. made = neu angelegt.
func genCert(cert, key string) (made bool, err error) {
	_, e1 := os.Stat(cert)
	_, e2 := os.Stat(key)
	if e1 == nil && e2 == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(cert), 0o755); err != nil {
		return false, err
	}
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, err
	}
	host, _ := os.Hostname()
	tpl := x509.Certificate{
		SerialNumber: new(big.Int).SetInt64(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: dflt(host, "cs-team"), Organization: []string{"cs-team (self-signed)"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	if host != "" {
		tpl.DNSNames = append(tpl.DNSNames, host)
	}
	if as, err := net.InterfaceAddrs(); err == nil {
		for _, a := range as {
			if ip, ok := a.(*net.IPNet); ok && !ip.IP.IsLoopback() && !ip.IP.IsLinkLocalUnicast() {
				tpl.IPAddresses = append(tpl.IPAddresses, ip.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &pk.PublicKey, pk)
	if err != nil {
		return false, err
	}
	kb, err := x509.MarshalPKCS8PrivateKey(pk)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return false, err
	}
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
