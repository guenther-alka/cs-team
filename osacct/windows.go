//go:build windows

package osacct

import (
	"crypto/rand"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows: lokale Konten und Gruppen mit gleichem Namen wie in cs-team (netapi32), Passwort im Klartextmoment.
// Windows speichert in ACL nur SIDs; jeder Rechner hat eine eigene Rechner-SID. Beim Failover schreibt Adopt die ACL der
// Freigaben (CS_OSACCOUNTS_SHARES) von den SIDs des alten Masters auf die dieses Rechners um (SubstTree).

var (
	netapi                   = windows.NewLazySystemDLL("netapi32.dll")
	pNetUserAdd              = netapi.NewProc("NetUserAdd")
	pNetUserGetInfo          = netapi.NewProc("NetUserGetInfo")
	pNetUserSetInfo          = netapi.NewProc("NetUserSetInfo")
	pNetLocalGroupAdd        = netapi.NewProc("NetLocalGroupAdd")
	pNetLocalGroupGetInfo    = netapi.NewProc("NetLocalGroupGetInfo")
	pNetLocalGroupAddMembers = netapi.NewProc("NetLocalGroupAddMembers")
	pNetLocalGroupDelMembers = netapi.NewProc("NetLocalGroupDelMembers")
	pNetApiBufferFree        = netapi.NewProc("NetApiBufferFree")
)

const (
	userPrivUser        = 1
	ufScript            = 0x0001
	ufAccountDisable    = 0x0002
	ufPasswdCantChange  = 0x0040
	ufDontExpirePasswd  = 0x10000
	nerrUserNotFound    = 2221
	nerrGroupNotFound   = 2220
	errMemberInAlias    = 1378
	errNoSuchMember     = 1387
	errMemberNotInAlias = 1377
)

type userInfo1 struct {
	Name        *uint16
	Password    *uint16
	PasswordAge uint32
	Priv        uint32
	HomeDir     *uint16
	Comment     *uint16
	Flags       uint32
	ScriptPath  *uint16
}

type userInfo1003 struct{ Password *uint16 }
type userInfo1008 struct{ Flags uint32 }
type groupInfo1 struct{ Name, Comment *uint16 }
type memberInfo3 struct{ DomainAndName *uint16 }

type win struct {
	mu     sync.Mutex
	groups map[string]bool // in diesem Abgleich bestaetigte cs-team-Gruppen
}

func New() Backend { return &win{groups: map[string]bool{}} }

func (b *win) OS() string { return "windows" }

func (b *win) ValidName(n string) bool {
	if n == "" || len(n) > 20 || strings.Trim(n, ". ") == "" {
		return false
	}
	return !strings.ContainsAny(n, `"/\[]:;|=,+*?<>@`)
}

func u16(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

func neterr(op string, r uintptr) error {
	if r == 0 {
		return nil
	}
	return fmt.Errorf("%s: %v (%d)", op, windows.Errno(r), r)
}

func (b *win) getUser(name string) (comment string, flags uint32, ok bool, err error) {
	var buf *byte
	r, _, _ := pNetUserGetInfo.Call(0, uintptr(unsafe.Pointer(u16(name))), 1, uintptr(unsafe.Pointer(&buf)))
	if r == nerrUserNotFound {
		return "", 0, false, nil
	}
	if r != 0 {
		return "", 0, false, neterr("NetUserGetInfo "+name, r)
	}
	defer pNetApiBufferFree.Call(uintptr(unsafe.Pointer(buf)))
	ui := (*userInfo1)(unsafe.Pointer(buf))
	return windows.UTF16PtrToString(ui.Comment), ui.Flags, true, nil
}

func (b *win) groupComment(name string) (string, bool, error) {
	var buf *byte
	r, _, _ := pNetLocalGroupGetInfo.Call(0, uintptr(unsafe.Pointer(u16(name))), 1, uintptr(unsafe.Pointer(&buf)))
	if r == nerrGroupNotFound {
		return "", false, nil
	}
	if r != 0 {
		return "", false, neterr("NetLocalGroupGetInfo "+name, r)
	}
	defer pNetApiBufferFree.Call(uintptr(unsafe.Pointer(buf)))
	gi := (*groupInfo1)(unsafe.Pointer(buf))
	return windows.UTF16PtrToString(gi.Comment), true, nil
}

func (b *win) EnsureGroup(g Group) error {
	c, ok, err := b.groupComment(g.Name)
	if err != nil {
		return err
	}
	if !ok {
		gi := groupInfo1{Name: u16(g.Name), Comment: u16(Comment)}
		var perr uint32
		if r, _, _ := pNetLocalGroupAdd.Call(0, 1, uintptr(unsafe.Pointer(&gi)), uintptr(unsafe.Pointer(&perr))); r != 0 {
			return neterr("NetLocalGroupAdd "+g.Name, r)
		}
	} else if c != Comment {
		return fmt.Errorf("local group exists and was not created by cs-team (comment %q), not changed", c)
	}
	b.mu.Lock()
	b.groups[g.Name] = true
	b.mu.Unlock()
	return nil
}

// randPw: Startpasswort fuer neue Konten (Windows verlangt eines); das echte setzt cs-team im Klartextmoment.
func randPw() string {
	const set = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 24)
	rand.Read(b)
	for i := range b {
		b[i] = set[int(b[i])%len(set)]
	}
	return "Cs-" + string(b) + "#9"
}

func member(group, user string, add bool) error {
	mi := memberInfo3{DomainAndName: u16(user)}
	p := pNetLocalGroupDelMembers
	if add {
		p = pNetLocalGroupAddMembers
	}
	r, _, _ := p.Call(0, uintptr(unsafe.Pointer(u16(group))), 3, uintptr(unsafe.Pointer(&mi)), 1)
	switch {
	case r == 0, add && r == errMemberInAlias, !add && (r == errMemberNotInAlias || r == errNoSuchMember):
		return nil
	}
	return neterr("local group member "+group+" "+user, r)
}

func (b *win) EnsureUser(u User, prim string) error {
	c, _, ok, err := b.getUser(u.Name)
	if err != nil {
		return err
	}
	if !ok {
		ui := userInfo1{Name: u16(u.Name), Password: u16(randPw()), Priv: userPrivUser, Comment: u16(Comment),
			Flags: ufScript | ufDontExpirePasswd | ufPasswdCantChange}
		var perr uint32
		if r, _, _ := pNetUserAdd.Call(0, 1, uintptr(unsafe.Pointer(&ui)), uintptr(unsafe.Pointer(&perr))); r != 0 {
			return neterr("NetUserAdd "+u.Name, r)
		}
	} else if c != Comment {
		return fmt.Errorf("local account exists and was not created by cs-team (comment %q), not changed", c)
	}
	want := map[string]bool{prim: true}
	for _, g := range u.Groups {
		want[g] = true
	}
	b.mu.Lock()
	var gs []string
	for g := range b.groups {
		gs = append(gs, g)
	}
	b.mu.Unlock()
	sort.Strings(gs)
	for _, g := range gs { // nur cs-team-Gruppen; fremde Gruppen (Administratoren, Benutzer ...) bleiben unberuehrt
		if err := member(g, u.Name, want[g]); err != nil {
			return err
		}
	}
	return nil
}

func (b *win) SetPassword(name, pass string) error {
	c, _, ok, err := b.getUser(name)
	if err != nil {
		return err
	}
	if !ok || c != Comment {
		return fmt.Errorf("no cs-team account %s on this host", name)
	}
	pi := userInfo1003{Password: u16(pass)}
	var perr uint32
	r, _, _ := pNetUserSetInfo.Call(0, uintptr(unsafe.Pointer(u16(name))), 1003, uintptr(unsafe.Pointer(&pi)), uintptr(unsafe.Pointer(&perr)))
	return neterr("NetUserSetInfo(password) "+name, r)
}

func (b *win) setDisabled(name string, off bool) error {
	c, fl, ok, err := b.getUser(name)
	if err != nil || !ok || c != Comment {
		return err
	}
	if off {
		fl |= ufAccountDisable
	} else {
		fl &^= ufAccountDisable
	}
	fi := userInfo1008{Flags: fl}
	var perr uint32
	r, _, _ := pNetUserSetInfo.Call(0, uintptr(unsafe.Pointer(u16(name))), 1008, uintptr(unsafe.Pointer(&fi)), uintptr(unsafe.Pointer(&perr)))
	return neterr("NetUserSetInfo(flags) "+name, r)
}

func (b *win) Lock(name string) error   { return b.setDisabled(name, true) }
func (b *win) Unlock(name string) error { return b.setDisabled(name, false) }

func localSID(name string) string {
	cn, _ := windows.ComputerName()
	sid, _, _, err := windows.LookupSID("", cn+`\`+name)
	if err != nil {
		return ""
	}
	return sid.String()
}

func (b *win) Export(users, groups []string) (Host, error) {
	h := Host{OS: b.OS(), WinSID: map[string]string{}}
	for _, n := range users {
		if c, _, ok, _ := b.getUser(n); ok && c == Comment {
			if s := localSID(n); s != "" {
				h.WinSID[n] = s
			}
		}
	}
	for _, n := range groups {
		if c, ok, _ := b.groupComment(n); ok && c == Comment {
			if s := localSID(n); s != "" {
				h.WinSID["g:"+n] = s
			}
		}
	}
	return h, nil
}

// SubstPairs: alte -> neue SID je Name (nur wo beide bekannt und verschieden), sortiert.
func SubstPairs(from, mine map[string]string) [][2]string {
	var keys []string
	for k := range from {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out [][2]string
	for _, k := range keys {
		if o, n := from[k], mine[k]; o != "" && n != "" && o != n {
			out = append(out, [2]string{o, n})
		}
	}
	return out
}

func (b *win) Adopt(from, mine Host, users map[string]int, shares []string, dry bool, logf func(string, ...any)) error {
	pairs := SubstPairs(from.WinSID, mine.WinSID)
	logf("adopt: %d SID pair(s) old -> new, shares: %s", len(pairs), strings.Join(shares, "; "))
	if len(pairs) == 0 || len(shares) == 0 {
		if len(shares) == 0 {
			logf("adopt: no shares set (CS_OSACCOUNTS_SHARES), ACLs not rewritten")
		}
		return nil
	}
	for _, p := range pairs {
		logf("adopt: %s -> %s", p[0], p[1])
	}
	if dry {
		return nil
	}
	sub := map[string]*windows.SID{}
	for _, p := range pairs {
		if s, err := windows.StringToSid(p[1]); err == nil {
			sub[p[0]] = s
		}
	}
	for _, sh := range shares {
		start := time.Now()
		n, ch, fails := SubstTree(sh, sub, logf)
		logf("adopt: %s: %d objects checked, %d changed, %d errors, %s", sh, n, ch, fails, time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// SubstTree ersetzt in Besitzer und expliziten ACL-Eintraegen (erlaubt/verweigert) die alten SIDs durch die neuen, von oben
// nach unten. Geerbte Eintraege bleiben unberuehrt: Windows berechnet sie beim Setzen der DACL des Elternobjekts neu
// (icacls /substitute geht nur mit /save + /restore und macht dabei aus geerbten Eintraegen explizite).
// Alte und neue SID sind lokale Konten-SIDs gleicher Laenge (Rechner-SID + RID), sie werden an Ort und Stelle ersetzt.
func SubstTree(root string, sub map[string]*windows.SID, logf func(string, ...any)) (n, changed, fails int) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			fails++
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		n++
		ok, err := substOne(p, sub)
		if err != nil {
			fails++
			if fails <= 20 {
				logf("adopt: %s: %v", p, err)
			}
		} else if ok {
			changed++
		}
		return nil
	})
	return
}

func substOne(p string, sub map[string]*windows.SID) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	var info windows.SECURITY_INFORMATION
	var newOwner *windows.SID
	if o, _, err := sd.Owner(); err == nil && o != nil {
		if s, ok := sub[o.String()]; ok {
			newOwner, info = s, info|windows.OWNER_SECURITY_INFORMATION
		}
	}
	dacl, _, err := sd.DACL()
	if err == nil && dacl != nil {
		for i := 0; i < int(dacl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if windows.GetAce(dacl, uint32(i), &ace) != nil || ace == nil {
				continue
			}
			if ace.Header.AceFlags&windows.INHERITED_ACE != 0 || ace.Header.AceType > 1 { // nur explizite Allow/Deny-Eintraege
				continue
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			ns, ok := sub[sid.String()]
			if !ok || windows.GetLengthSid(ns) != windows.GetLengthSid(sid) {
				continue
			}
			if err := windows.CopySid(windows.GetLengthSid(sid), sid, ns); err != nil {
				return false, err
			}
			info |= windows.DACL_SECURITY_INFORMATION
		}
	}
	if info == 0 {
		return false, nil
	}
	if info&windows.DACL_SECURITY_INFORMATION != 0 {
		if c, _, err := sd.Control(); err == nil && c&windows.SE_DACL_PROTECTED != 0 {
			info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
		} else {
			info |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
		}
	} else {
		dacl = nil
	}
	return true, windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, info, newOwner, nil, dacl, nil)
}
