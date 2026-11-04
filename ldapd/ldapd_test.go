package ldapd

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

type fakeSrc struct{}

func (fakeSrc) Directory(ctx context.Context) ([]User, []Group) {
	return []User{{"anna", 10001, 10000, "anna@x.org"}, {"ben", 10002, 10000, ""}},
		[]Group{{"klasse5a", 10003, []string{"anna", "ben"}}, {"lehrer", 10004, []string{"anna"}}}
}
func (fakeSrc) Bind(ctx context.Context, name, pass, ip string) bool {
	return (name == "reader" || name == "anna" || name == "ben") && pass == "pw-"+name
}

type cl struct {
	c  net.Conn
	r  *bufio.Reader
	id int
	t  *testing.T
}

func start(t *testing.T) *cl { return startOpt(t, false) }

func startOpt(t *testing.T, anon bool) *cl {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Src: fakeSrc{}, Base: "dc=csteam,dc=local", Reader: "reader", AnonRead: anon}
	go s.Serve(l)
	t.Cleanup(func() { l.Close() })
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { c.Close() })
	return &cl{c: c, r: bufio.NewReader(c), t: t}
}

func (c *cl) send(op []byte) {
	c.id++
	if _, err := c.c.Write(msg(c.id, op)); err != nil {
		c.t.Fatal(err)
	}
}

// next liest eine Antwort: Operationsnummer (Tag) und Inhalt.
func (c *cl) next() (byte, []tlv) {
	m, err := readMsg(c.r)
	if err != nil {
		c.t.Fatal("read:", err)
	}
	its, _ := items(m)
	f, _ := items(its[1].val)
	return its[1].tag, f
}

func (c *cl) bind(dn, pw string) int {
	c.send(enc(0x60, cat(encInt(0x02, 3), encStr(0x04, dn), encStr(0x80, pw))))
	tag, f := c.next()
	if tag != 0x61 {
		c.t.Fatalf("bind tag %x", tag)
	}
	return asInt(f[0].val)
}

func eq(a, v string) []byte { return enc(0xa3, cat(encStr(0x04, a), encStr(0x04, v))) }

// search liefert die DNs der Treffer und den Ergebniscode.
func (c *cl) search(base string, scope int, filter []byte, attrs ...string) (map[string]map[string][]string, int) {
	var al []byte
	for _, a := range attrs {
		al = append(al, encStr(0x04, a)...)
	}
	c.send(enc(0x63, cat(encStr(0x04, base), encInt(0x0a, scope), encInt(0x0a, 0), encInt(0x02, 0), encInt(0x02, 0),
		enc(0x01, []byte{0}), filter, enc(0x30, al))))
	out := map[string]map[string][]string{}
	for {
		tag, f := c.next()
		if tag == 0x65 {
			return out, asInt(f[0].val)
		}
		if tag != 0x64 {
			c.t.Fatalf("search tag %x", tag)
		}
		at := map[string][]string{}
		as, _ := items(f[1].val)
		for _, a := range as {
			kv, _ := items(a.val)
			vs, _ := items(kv[1].val)
			for _, v := range vs {
				at[strings.ToLower(string(kv[0].val))] = append(at[strings.ToLower(string(kv[0].val))], string(v.val))
			}
		}
		out[string(f[0].val)] = at
	}
}

const base = "dc=csteam,dc=local"

func TestBindAndReader(t *testing.T) {
	c := start(t)
	if code := c.bind("uid=reader,ou=people,"+base, "falsch"); code != rInvalidCred {
		t.Fatal("wrong password:", code)
	}
	if code := c.bind("uid=reader,ou=people,"+base, ""); code != rUnwilling {
		t.Fatal("empty password must be refused:", code)
	}
	if code := c.bind("uid=reader,ou=people,"+base, "pw-reader"); code != 0 {
		t.Fatal("reader bind:", code)
	}
	r, code := c.search(base, 2, eq("objectClass", "posixAccount"), "uid", "uidNumber")
	if code != 0 || len(r) != 2 {
		t.Fatal("users:", code, r)
	}
	if r["uid=anna,ou=people,"+base]["uidnumber"][0] != "10001" {
		t.Fatal("uidNumber:", r)
	}
	r, _ = c.search("ou=groups,"+base, 1, eq("cn", "klasse5a"))
	g := r["cn=klasse5a,ou=groups,"+base]
	if len(g["memberuid"]) != 2 || g["gidnumber"][0] != "10003" {
		t.Fatal("group:", g)
	}
	// Filter: and / or / not / present / substring
	f := enc(0xa0, cat(eq("objectClass", "posixAccount"), enc(0xa2, eq("uid", "ben")), enc(0x87, []byte("mail")[:0]))) // present mit leerem Namen trifft nie
	if r, _ = c.search(base, 2, f); len(r) != 0 {
		t.Fatal("and with empty present should match nothing:", r)
	}
	f = enc(0xa1, cat(eq("uid", "anna"), eq("cn", "lehrer")))
	if r, _ = c.search(base, 2, f); len(r) != 2 { // anna und die Gruppe lehrer
		t.Fatal("or:", r)
	}
	sub := enc(0xa4, cat(encStr(0x04, "uid"), enc(0x30, encStr(0x80, "be"))))
	if r, _ = c.search(base, 2, sub); len(r) != 1 {
		t.Fatal("substring:", r)
	}
	// Teilmengen: nur verlangte Attribute
	r, _ = c.search("uid=ben,ou=people,"+base, 0, enc(0x87, []byte("objectClass")), "cn")
	if at := r["uid=ben,ou=people,"+base]; len(at) != 1 || at["cn"][0] != "ben" {
		t.Fatal("attribute selection:", at)
	}
}

func TestAnonymousAndUserScope(t *testing.T) {
	c := start(t)
	if _, code := c.search(base, 2, enc(0x87, []byte("objectClass"))); code != rInsufficient {
		t.Fatal("anonymous search must be refused:", code)
	}
	r, code := c.search("", 0, enc(0x87, []byte("objectClass")))
	if code != 0 || r[""]["namingcontexts"][0] != base {
		t.Fatal("root DSE:", code, r)
	}
	if c.bind("anna", "pw-anna") != 0 { // auch nur der Name ist als Bind-DN erlaubt
		t.Fatal("bind by plain name")
	}
	r, _ = c.search(base, 2, enc(0x87, []byte("objectClass")))
	if len(r) != 1 || r["uid=anna,ou=people,"+base] == nil {
		t.Fatal("a normal user must only see the own entry:", r)
	}
}

// CS_LDAP_ANON=1 (macOS): anonymous clients may search users and groups, but never see a password.
func TestAnonRead(t *testing.T) {
	c := startOpt(t, true)
	r, code := c.search(base, 2, enc(0x87, []byte("objectClass")))
	if code != 0 || len(r) < 2 || r["uid=anna,ou=people,"+base] == nil {
		t.Fatal("anonymous read must list the entries:", code, r)
	}
	for dn, e := range r {
		if e["userpassword"] != nil {
			t.Fatal("password attribute served for", dn)
		}
	}
}

func TestWriteRefused(t *testing.T) {
	c := start(t)
	c.send(enc(0x68, cat(encStr(0x04, "uid=x,ou=people,"+base), enc(0x30, nil)))) // add
	tag, f := c.next()
	if tag != 0x69 || asInt(f[0].val) != rUnwilling {
		t.Fatalf("add: tag %x %v", tag, f)
	}
}

func TestSizeLimitAndNoSuchBase(t *testing.T) {
	c := start(t)
	c.bind("uid=reader,ou=people,"+base, "pw-reader")
	if _, code := c.search("ou=nix,"+base, 2, enc(0x87, []byte("objectClass"))); code != rNoSuchObject {
		t.Fatal("no such object:", code)
	}
	if _, code := c.search("dc=other", 2, enc(0x87, []byte("objectClass"))); code != rNoSuchObject {
		t.Fatal("foreign base:", code)
	}
}

func TestBERInt(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 255, 256, 65535, 100000} {
		got := asInt(encInt(0x02, n)[2:])
		if got != n {
			t.Fatalf("%d -> %d", n, got)
		}
	}
}
