// Package ldapd: kleiner LDAPv3-Server (nur lesen) fuer das cs-team-Verzeichnis (docs/HA_SPEC.md Abschnitt 3).
// Er bietet Benutzer (posixAccount) und exportierte Gruppen (posixGroup, flach) nach RFC 2307 und prueft Anmeldungen (Bind)
// gegen die cs-team-Passwoerter. Es gibt keinen Schreibzugriff; Verschluesselung ueber LDAPS (der Aufrufer liefert den Listener).
package ldapd

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

type User struct {
	Name     string
	Uid, Gid int
	Mail     string
}

type Group struct {
	Name    string
	Gid     int
	Members []string
}

// Source: die Daten und die Passwortpruefung (in main.go mit auth.Auth verbunden).
type Source interface {
	Directory(ctx context.Context) ([]User, []Group)
	Bind(ctx context.Context, name, pass, ip string) bool
}

type Server struct {
	Src    Source
	Base   string // z.B. dc=csteam,dc=local
	Reader string // cs-team-Konto, das alles lesen darf; alle anderen sehen nur den eigenen Eintrag
	Logf   func(format string, args ...any)

	sem chan struct{}
}

const (
	opBind, opUnbind, opSearch, opExt = 0x60, 0x42, 0x63, 0x77
	rSuccess, rProto, rSizeLimit      = 0, 2, 4
	rInsufficient, rInvalidCred       = 50, 49
	rUnwilling, rNoSuchObject         = 53, 32
	maxConns                          = 256
)

func (s *Server) logf(f string, a ...any) {
	if s.Logf != nil {
		s.Logf(f, a...)
	}
}

// Serve nimmt Verbindungen an, bis der Listener schliesst.
func (s *Server) Serve(l net.Listener) error {
	s.Base = strings.ToLower(strings.ReplaceAll(s.Base, " ", ""))
	s.sem = make(chan struct{}, maxConns)
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		select {
		case s.sem <- struct{}{}:
			go func() { defer func() { <-s.sem }(); s.conn(c) }()
		default:
			c.Close()
		}
	}
}

func (s *Server) conn(c net.Conn) {
	defer c.Close()
	ip, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	r := bufio.NewReader(c)
	bound := "" // angemeldeter Kontoname, "" = anonym
	for {
		c.SetReadDeadline(time.Now().Add(10 * time.Minute))
		m, err := readMsg(r)
		if err != nil {
			return
		}
		its, err := items(m)
		if err != nil || len(its) < 2 || its[0].tag != 0x02 {
			return
		}
		id := asInt(its[0].val)
		op := its[1]
		c.SetWriteDeadline(time.Now().Add(30 * time.Second))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		switch op.tag {
		case opUnbind:
			cancel()
			return
		case opBind:
			bound = s.bind(ctx, c, id, op.val, ip)
		case opSearch:
			if !s.search(ctx, c, id, op.val, bound) {
				cancel()
				return
			}
		default: // Extended (StartTLS u.a.) und alles Schreibende: nicht unterstuetzt
			code := byte(0x61) // BindResponse als allgemeine Antwort waere falsch; passende Antwort je Operation
			switch op.tag {
			case opExt:
				code = 0x78
				if sub, e := items(op.val); e == nil && len(sub) > 0 && string(sub[0].val) == "1.3.6.1.4.1.4203.1.11.3" {
					// Who am I? (RFC 4532)
					authz := ""
					if bound != "" {
						authz = "dn:uid=" + bound + ",ou=people," + s.Base
					}
					c.Write(msg(id, enc(code, cat(result(0, "", ""), encStr(0x8b, authz)))))
					cancel()
					continue
				}
			case 0x66: // modify
				code = 0x67
			case 0x68: // add
				code = 0x69
			case 0x4a: // delete
				code = 0x6b
			case 0x6c: // modifyDN
				code = 0x6d
			case 0x6e: // compare
				code = 0x6f
			default:
				cancel()
				return
			}
			c.Write(msg(id, enc(code, result(rUnwilling, "", "read-only directory"))))
		}
		cancel()
	}
}

func result(code int, matched, diag string) []byte {
	return cat(encInt(0x0a, code), encStr(0x04, matched), encStr(0x04, diag))
}

func msg(id int, op []byte) []byte { return enc(0x30, cat(encInt(0x02, id), op)) }

// userOf: Kontoname aus der Bind-DN: uid=name,ou=people,<base> oder nur der Name.
func (s *Server) userOf(dn string) string {
	d := normDN(dn)
	if p := "uid="; strings.HasPrefix(d, p) {
		rest := d[len(p):]
		if i := strings.Index(rest, ","); i >= 0 && rest[i+1:] == "ou=people,"+s.Base {
			return rest[:i]
		}
		return ""
	}
	if strings.ContainsAny(d, "=,") {
		return ""
	}
	return d
}

func (s *Server) bind(ctx context.Context, c net.Conn, id int, v []byte, ip string) string {
	reply := func(code int, diag string) { c.Write(msg(id, enc(0x61, result(code, "", diag)))) }
	f, err := items(v)
	if err != nil || len(f) < 3 {
		reply(rProto, "bad bind")
		return ""
	}
	if asInt(f[0].val) != 3 {
		reply(rProto, "only LDAPv3")
		return ""
	}
	if f[2].tag != 0x80 { // nur einfache Bindung
		reply(7, "only simple bind (use LDAPS)")
		return ""
	}
	dn, pass := string(f[1].val), string(f[2].val)
	if dn == "" && pass == "" { // anonym
		reply(rSuccess, "")
		return ""
	}
	if pass == "" {
		reply(rUnwilling, "empty password")
		return ""
	}
	name := s.userOf(dn)
	if name == "" || !s.Src.Bind(ctx, name, pass, ip) {
		s.logf("ldap: bind failed for %q from %s", dn, ip)
		reply(rInvalidCred, "")
		return ""
	}
	reply(rSuccess, "")
	return name
}

type entry struct {
	dn    string
	attrs map[string][]string // Schluessel kleingeschrieben
	order []string            // Originalschreibweise der Attributnamen, in Reihenfolge
	names map[string]string   // klein -> Originalschreibweise
}

func newEntry(dn string) *entry {
	return &entry{dn: dn, attrs: map[string][]string{}, names: map[string]string{}}
}

func (e *entry) add(name string, vals ...string) {
	if len(vals) == 0 {
		return
	}
	k := strings.ToLower(name)
	if _, ok := e.attrs[k]; !ok {
		e.order = append(e.order, name)
		e.names[k] = name
	}
	e.attrs[k] = append(e.attrs[k], vals...)
}

func normDN(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ", ", ",")
	s = strings.ReplaceAll(s, " ,", ",")
	s = strings.ReplaceAll(s, " =", "=")
	s = strings.ReplaceAll(s, "= ", "=")
	return s
}

// entries baut das ganze Verzeichnis (klein: ein Eintrag je Konto und Gruppe).
func (s *Server) entries(ctx context.Context) []*entry {
	users, groups := s.Src.Directory(ctx)
	base := s.Base
	first := strings.TrimPrefix(strings.SplitN(base, ",", 2)[0], "dc=")
	var out []*entry
	b := newEntry(base)
	b.add("objectClass", "top", "domain")
	b.add("dc", first)
	pe, ge := newEntry("ou=people,"+base), newEntry("ou=groups,"+base)
	pe.add("objectClass", "top", "organizationalUnit")
	pe.add("ou", "people")
	ge.add("objectClass", "top", "organizationalUnit")
	ge.add("ou", "groups")
	out = append(out, b, pe, ge)
	for _, u := range users {
		e := newEntry("uid=" + u.Name + ",ou=people," + base)
		e.add("objectClass", "top", "person", "organizationalPerson", "inetOrgPerson", "posixAccount", "shadowAccount")
		e.add("uid", u.Name)
		e.add("cn", u.Name)
		e.add("sn", u.Name)
		e.add("gecos", u.Name)
		e.add("uidNumber", fmt.Sprint(u.Uid))
		e.add("gidNumber", fmt.Sprint(u.Gid))
		e.add("homeDirectory", "/home/"+u.Name)
		e.add("loginShell", "/bin/sh")
		if u.Mail != "" {
			e.add("mail", u.Mail)
		}
		out = append(out, e)
	}
	for _, g := range groups {
		e := newEntry("cn=" + g.Name + ",ou=groups," + base)
		e.add("objectClass", "top", "posixGroup")
		e.add("cn", g.Name)
		e.add("gidNumber", fmt.Sprint(g.Gid))
		e.add("memberUid", g.Members...)
		out = append(out, e)
	}
	return out
}

func (s *Server) rootDSE() *entry {
	e := newEntry("")
	e.add("objectClass", "top")
	e.add("namingContexts", s.Base)
	e.add("supportedLDAPVersion", "3")
	e.add("vendorName", "cs-team")
	e.add("subschemaSubentry", "cn=Subschema")
	return e
}

// schemaEntry: leerer Schema-Eintrag; manche Clients (macOS Open Directory) lesen
// subschemaSubentry und brechen ab, wenn er fehlt.
func schemaEntry() *entry {
	e := newEntry("cn=Subschema")
	e.add("objectClass", "top", "subschema", "subentry")
	e.add("cn", "Subschema")
	e.add("objectClasses",
		"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
		"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY ( userPassword $ description ) )",
		"( 2.5.6.7 NAME 'organizationalPerson' SUP person STRUCTURAL MAY ( ou $ mail ) )",
		"( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP organizationalPerson STRUCTURAL MAY ( uid $ mail ) )",
		"( 1.3.6.1.1.1.2.0 NAME 'posixAccount' SUP top AUXILIARY MUST ( cn $ uid $ uidNumber $ gidNumber $ homeDirectory ) MAY ( userPassword $ loginShell $ gecos $ description ) )",
		"( 1.3.6.1.1.1.2.1 NAME 'shadowAccount' SUP top AUXILIARY MUST uid MAY ( userPassword $ description ) )",
		"( 1.3.6.1.1.1.2.2 NAME 'posixGroup' SUP top STRUCTURAL MUST ( cn $ gidNumber ) MAY ( userPassword $ memberUid $ description ) )",
		"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou MAY description )",
		"( 0.9.2342.19200300.100.4.13 NAME 'domain' SUP top STRUCTURAL MUST dc MAY description )")
	e.add("attributeTypes",
		"( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
		"( 2.5.4.3 NAME 'cn' SUP name )",
		"( 2.5.4.4 NAME 'sn' SUP name )",
		"( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.11 NAME 'ou' SUP name )",
		"( 2.5.4.13 NAME 'description' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.35 NAME 'userPassword' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
		"( 0.9.2342.19200300.100.1.1 NAME 'uid' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 0.9.2342.19200300.100.1.3 NAME 'mail' EQUALITY caseIgnoreIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
		"( 0.9.2342.19200300.100.1.25 NAME 'dc' EQUALITY caseIgnoreIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 SINGLE-VALUE )",
		"( 1.3.6.1.1.1.1.0 NAME 'uidNumber' EQUALITY integerMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.27 SINGLE-VALUE )",
		"( 1.3.6.1.1.1.1.1 NAME 'gidNumber' EQUALITY integerMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.27 SINGLE-VALUE )",
		"( 1.3.6.1.1.1.1.2 NAME 'gecos' EQUALITY caseIgnoreIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 SINGLE-VALUE )",
		"( 1.3.6.1.1.1.1.3 NAME 'homeDirectory' EQUALITY caseExactIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 SINGLE-VALUE )",
		"( 1.3.6.1.1.1.1.4 NAME 'loginShell' EQUALITY caseExactIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 SINGLE-VALUE )",
		"( 1.3.6.1.1.1.1.12 NAME 'memberUid' EQUALITY caseExactIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )")
	return e
}

func inScope(dn, base string, scope int) bool {
	switch scope {
	case 0:
		return dn == base
	case 1:
		i := strings.Index(dn, ",")
		return i >= 0 && dn[i+1:] == base
	default:
		return dn == base || strings.HasSuffix(dn, ","+base)
	}
}

// search beantwortet eine Suche; false = Verbindung schliessen.
func (s *Server) search(ctx context.Context, c net.Conn, id int, v []byte, bound string) bool {
	f, err := items(v)
	if err != nil || len(f) < 8 {
		return false
	}
	base := normDN(string(f[0].val))
	scope, size := asInt(f[1].val), asInt(f[3].val)
	typesOnly := len(f[5].val) > 0 && f[5].val[0] != 0
	flt, err := parseFilter(f[6])
	if err != nil {
		c.Write(msg(id, enc(0x65, result(rProto, "", "bad filter"))))
		return true
	}
	var want []string
	if as, err := items(f[7].val); err == nil {
		for _, a := range as {
			want = append(want, strings.ToLower(string(a.val)))
		}
	}
	done := func(code int, diag string) { c.Write(msg(id, enc(0x65, result(code, "", diag)))) }
	var list []*entry
	if base == "" && scope == 0 { // Root DSE: fuer alle
		list = []*entry{s.rootDSE()}
	} else if base == "cn=subschema" && scope == 0 { // Schema-Eintrag: fuer alle
		list = []*entry{schemaEntry()}
	} else {
		if bound == "" {
			done(rInsufficient, "bind required")
			return true
		}
		if !strings.HasSuffix(","+base, ","+s.Base) {
			done(rNoSuchObject, "")
			return true
		}
		all := s.entries(ctx)
		found := false
		for _, e := range all {
			if e.dn == base {
				found = true
			}
			if !inScope(e.dn, base, scope) {
				continue
			}
			if bound != s.Reader && e.dn != "uid="+bound+",ou=people,"+s.Base { // nur der Lesekonto-Inhaber sieht alles
				continue
			}
			list = append(list, e)
		}
		if !found {
			done(rNoSuchObject, "")
			return true
		}
	}
	n := 0
	for _, e := range list {
		if !flt(e) {
			continue
		}
		if size > 0 && n >= size {
			done(rSizeLimit, "")
			return true
		}
		n++
		c.Write(msg(id, enc(0x64, cat(encStr(0x04, e.dn), enc(0x30, s.attrsOut(e, want, typesOnly))))))
	}
	done(rSuccess, "")
	return true
}

func (s *Server) attrsOut(e *entry, want []string, typesOnly bool) []byte {
	all := len(want) == 0
	pick := map[string]bool{}
	for _, w := range want {
		switch w {
		case "*":
			all = true
		case "1.1", "+":
		default:
			pick[w] = true
		}
	}
	var out []byte
	for _, name := range e.order {
		k := strings.ToLower(name)
		if !all && !pick[k] {
			continue
		}
		var vals []byte
		if !typesOnly {
			for _, v := range e.attrs[k] {
				vals = append(vals, encStr(0x04, v)...)
			}
		}
		out = append(out, enc(0x30, cat(encStr(0x04, name), enc(0x31, vals)))...)
	}
	return out
}
