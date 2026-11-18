package osacct

import (
	"context"
	"strings"
	"sync"
	"testing"

	"cs-team/store"
)

func TestNTHash(t *testing.T) {
	if h := NTHash("password"); h != "8846F7EAEE8FB117AD06BDD830B7586C" {
		t.Fatalf("NTHash(password) = %s", h)
	}
	if h := NTHash(""); h != "31D6CFE0D16AE931B73C59D7E0C089C0" {
		t.Fatalf("NTHash('') = %s", h)
	}
	// echter Eintrag aus /var/smb/smbpasswd (OmniOS r151058, gesetzt mit passwd/pam_smb_passwd)
	if h := SMBHash("1234567."); h != "407413138FE9E8603A4FE72F58163AB4" {
		t.Fatalf("SMBHash = %s", h)
	}
}

func TestSMBFile(t *testing.T) {
	cur := "root:0::5E88472DB266620524F74DF43A41994B\nanna:10002::AAAA:x:y\n"
	got := SMBSet(cur, "anna", 10002, "BBBB")
	if !strings.Contains(got, "anna:10002::BBBB:x:y\n") || !strings.HasPrefix(got, "root:0::5E88") {
		t.Fatalf("SMBSet replace: %q", got)
	}
	got = SMBSet(got, "ben", 10003, "CCCC")
	if !strings.HasSuffix(got, "ben:10003::CCCC\n") {
		t.Fatalf("SMBSet append: %q", got)
	}
	m := SMBGet(got, []string{"anna", "ben", "nobody"})
	if len(m) != 2 || m["ben"] != "ben:10003::CCCC" {
		t.Fatalf("SMBGet: %v", m)
	}
	// Merge: ben mit falscher uid wird nicht uebernommen, anna ersetzt, carl angehaengt
	nc, done := SMBMerge("root:0::R\nanna:10002::OLD\n", map[string]string{
		"anna": "anna:10002::NEW", "ben": "ben:99::X", "carl": "carl:10004::C"},
		map[string]int{"anna": 10002, "ben": 10003, "carl": 10004})
	if !strings.Contains(nc, "anna:10002::NEW\n") || strings.Contains(nc, "ben:") || !strings.Contains(nc, "carl:10004::C\n") || len(done) != 2 {
		t.Fatalf("SMBMerge: %q %v", nc, done)
	}
}

func TestSplitShares(t *testing.T) {
	if s := SplitShares(" D:\\a ; ;E:\\b"); len(s) != 2 || s[1] != "E:\\b" {
		t.Fatalf("%v", s)
	}
}

// fake: Test-Backend im Speicher
type fake struct {
	mu     sync.Mutex
	os     string
	users  map[string]User
	groups map[string]int
	pw     map[string]string
	locked map[string]bool
	sid    string
	adopt  []string
}

func newFake(sid string) *fake {
	return &fake{os: "test", users: map[string]User{}, groups: map[string]int{}, pw: map[string]string{}, locked: map[string]bool{}, sid: sid}
}

func (f *fake) OS() string              { return f.os }
func (f *fake) ValidName(n string) bool { return n != "" && !strings.Contains(n, "@") }
func (f *fake) EnsureGroup(g Group) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups[g.Name] = g.Gid
	return nil
}
func (f *fake) EnsureUser(u User, prim string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.Name] = u
	return nil
}
func (f *fake) SetPassword(n, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pw[n] = p
	return nil
}
func (f *fake) Lock(n string) error   { f.mu.Lock(); f.locked[n] = true; f.mu.Unlock(); return nil }
func (f *fake) Unlock(n string) error { f.mu.Lock(); delete(f.locked, n); f.mu.Unlock(); return nil }
func (f *fake) Export(us, gs []string) (Host, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h := Host{OS: f.os, SID: f.sid, WinSID: map[string]string{}}
	for _, n := range us {
		if _, ok := f.users[n]; ok {
			h.WinSID[n] = f.sid + "-" + n
		}
	}
	return h, nil
}
func (f *fake) Adopt(from, mine Host, users map[string]int, shares []string, dry bool, logf func(string, ...any)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range [][2]string{{from.SID, mine.SID}} {
		f.adopt = append(f.adopt, p[0]+">"+p[1])
	}
	if !dry {
		f.sid = from.SID
	}
	return nil
}

func TestReconcileAdopt(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	users := []User{{Name: "anna", Uid: 10002, Gid: 10000, Groups: []string{"klasse5a"}}, {Name: "ben", Uid: 10003, Gid: 10000, Groups: []string{"lehrer"}}}
	src := func(context.Context) ([]User, []Group, Group) {
		return users, []Group{{"klasse5a", 10004}, {"lehrer", 10003}}, Group{"alluser", 10000}
	}
	a := newFake("S-A")
	ma := &Mgr{St: st, B: a, Host: "hostA", Src: src}
	if err := ma.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.users) != 2 || a.groups["alluser"] != 10000 || a.groups["lehrer"] != 10003 {
		t.Fatalf("accounts: %v %v", a.users, a.groups)
	}
	ma.Plain("anna", "altes-pw-1") // Wechsel: erst altes (Pruefung), dann neues Passwort - das neue muss gelten
	ma.Plain("anna", "geheim123")
	ma.Plain("nobody", "x") // nicht im Export: nichts
	ma.Flush()
	if a.pw["anna"] != "geheim123" || a.pw["nobody"] != "" {
		t.Fatalf("passwords: %v", a.pw)
	}
	// neu angelegtes Konto: sofortiger Abgleich je Name (nicht durch einen anderen unbekannten Namen kurz davor blockiert)
	users = append(users, User{Name: "carl", Uid: 10005, Gid: 10000, Groups: []string{"lehrer"}})
	ma.Plain("other", "x")
	ma.Plain("carl", "carl-pw-12")
	ma.Flush()
	if a.pw["carl"] != "carl-pw-12" {
		t.Fatalf("new account: %v", a.pw)
	}
	users = users[:2]
	s, _ := ma.LoadState(ctx)
	if s.Last != "hostA" || s.Users["ben"] != 10003 || s.Hosts["hostA"].SID != "S-A" {
		t.Fatalf("state: %+v", s)
	}
	// ben faellt aus dem Export -> gesperrt, nicht geloescht
	users = users[:1]
	if err := ma.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if !a.locked["ben"] {
		t.Fatal("ben not locked")
	}
	s, _ = ma.LoadState(ctx)
	if !s.Locked["ben"] || s.Users["ben"] != 10003 {
		t.Fatalf("locked state: %+v", s)
	}
	// Standby schreibt nichts
	sb := &Mgr{St: st, B: newFake("S-B"), Host: "hostB", ReadOnly: true, Src: src}
	if err := sb.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if _, need := sb.NeedAdopt(ctx); !need {
		t.Fatal("hostB should need adopt")
	}
	// Uebernahme auf hostB
	b := newFake("S-B")
	mb := &Mgr{St: st, B: b, Host: "hostB", Src: src}
	if err := mb.Adopt(ctx, true); err != nil { // Probelauf: nichts geaendert
		t.Fatal(err)
	}
	if b.sid != "S-B" || len(b.users) != 0 {
		t.Fatalf("dry run changed something: %s %v", b.sid, b.users)
	}
	if err := mb.Adopt(ctx, false); err != nil {
		t.Fatal(err)
	}
	if b.sid != "S-A" || len(b.users) != 1 || !b.locked["ben"] {
		t.Fatalf("adopt: sid %s users %v locked %v", b.sid, b.users, b.locked)
	}
	s, _ = mb.LoadState(ctx)
	if s.Last != "hostB" {
		t.Fatalf("last = %s", s.Last)
	}
	if _, need := mb.NeedAdopt(ctx); need {
		t.Fatal("no second adopt expected")
	}
	// anderes Betriebssystem: abgelehnt
	c := newFake("S-C")
	c.os = "other"
	if err := (&Mgr{St: st, B: c, Host: "hostC", Src: src}).Adopt(ctx, false); err == nil {
		t.Fatal("adopt across operating systems must fail")
	}
}
