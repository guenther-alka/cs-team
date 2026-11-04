package main

import (
	"context"
	"crypto/tls"
	"log"
	"os"

	"cs-team/auth"
	"cs-team/ldapd"
)

// ldapSrc verbindet den LDAP-Dienst mit den cs-team-Konten und -Gruppen.
type ldapSrc struct{ a *auth.Auth }

func (s ldapSrc) Directory(ctx context.Context) ([]ldapd.User, []ldapd.Group) {
	us, gs := s.a.ExportData(ctx)
	var ou []ldapd.User
	var og []ldapd.Group
	for _, u := range us {
		ou = append(ou, ldapd.User{Name: u.Name, Uid: u.Uid, Gid: u.Gid, Mail: u.Mail})
	}
	for _, g := range gs {
		og = append(og, ldapd.Group{Name: g.Name, Gid: g.Gid, Members: g.Members})
	}
	return ou, og
}

func (s ldapSrc) Bind(ctx context.Context, name, pass, ip string) bool {
	return s.a.LDAPBind(ctx, name, pass, ip)
}

// startLDAP: LDAPS-Dienst (nur lesen) auf CS_LDAP (z.B. :636), mit dem Zertifikat von CS_TLS_CERT.
// CS_LDAP_BASE = Basis-DN (Standard dc=csteam,dc=local), CS_LDAP_READER = Konto mit Leserecht auf das ganze Verzeichnis.
func startLDAP(a *auth.Auth) {
	addr := os.Getenv("CS_LDAP")
	if addr == "" {
		return
	}
	cert := os.Getenv("CS_TLS_CERT")
	if cert == "" {
		log.Fatal("ldap: CS_LDAP needs CS_TLS_CERT (LDAPS only, no unencrypted LDAP)")
	}
	key := env("CS_TLS_KEY", cert)
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		log.Fatal("ldap tls: ", err)
	}
	l, err := tls.Listen("tcp", addr, &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certReloader(cert, key)})
	if err != nil {
		log.Fatal("ldap: ", err)
	}
	s := &ldapd.Server{Src: ldapSrc{a}, Base: env("CS_LDAP_BASE", "dc=csteam,dc=local"), Reader: os.Getenv("CS_LDAP_READER"), AnonRead: os.Getenv("CS_LDAP_ANON") == "1", Logf: log.Printf}
	log.Printf("ldap: LDAPS on %s, base %s, reader %q", addr, s.Base, s.Reader)
	go func() { log.Println("ldap:", s.Serve(l)) }()
}
