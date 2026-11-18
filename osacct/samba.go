//go:build linux || freebsd

package osacct

import (
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Linux und FreeBSD mit Samba (0.72): Unix-Konten mit der uid/gid aus cs-team (Linux useradd/groupadd/usermod, FreeBSD pw),
// ohne Home und ohne Shell; das SMB-Passwort setzt smbpasswd (Samba passdb), Sperren mit smbpasswd -d/-e.
// Beim Failover werden keine Passwoerter uebertragen: sie gelten ab der naechsten cs-team-Anmeldung des Benutzers.
// Die ACL auf dem Dateisystem gehen ueber die uid/gid (POSIX- bzw. NFSv4-ACL); Samba bildet die SIDs aus seiner eigenen
// Domaenen-SID, deshalb sind NT-ACL (Samba-xattr) nach dem Hochstufen nur mit gleicher Samba-SID eindeutig.

var unixName = regexp.MustCompile(`^[a-z_][a-z0-9._-]*$`)

const unixMaxName = 32

type samba struct{ mu sync.Mutex }

func New() Backend { return &samba{} }

func (b *samba) OS() string { return runtime.GOOS }

func (b *samba) ValidName(n string) bool { return len(n) <= unixMaxName && unixName.MatchString(n) }

func urun(name string, stdin string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	out, err := c.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, s)
	}
	return s, nil
}

func ugetent(db, name string) []string {
	out, err := exec.Command("getent", db, name).Output()
	if err != nil {
		return nil
	}
	l := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if l == "" {
		return nil
	}
	return strings.Split(l, ":")
}

func umanaged(gecos string) bool { return gecos == Comment || strings.HasPrefix(gecos, Comment+" ") }

func nologin() string {
	for _, p := range []string{"/usr/sbin/nologin", "/sbin/nologin", "/bin/false"} {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return "/bin/false"
}

func (b *samba) EnsureGroup(g Group) error {
	if f := ugetent("group", g.Name); f != nil {
		if len(f) >= 3 && f[2] != strconv.Itoa(g.Gid) {
			return fmt.Errorf("exists with gid %s (cs-team: %d), not changed", f[2], g.Gid)
		}
		return nil
	}
	if f := ugetent("group", strconv.Itoa(g.Gid)); f != nil {
		return fmt.Errorf("gid %d is used by group %s, not created", g.Gid, f[0])
	}
	var err error
	if runtime.GOOS == "freebsd" {
		_, err = urun("pw", "", "groupadd", g.Name, "-g", strconv.Itoa(g.Gid))
	} else {
		_, err = urun("groupadd", "", "-g", strconv.Itoa(g.Gid), g.Name)
	}
	return err
}

func (b *samba) EnsureUser(u User, prim string) error {
	gs := strings.Join(u.Groups, ",")
	f := ugetent("passwd", u.Name)
	if f == nil {
		if o := ugetent("passwd", strconv.Itoa(u.Uid)); o != nil {
			return fmt.Errorf("uid %d is used by %s, not created", u.Uid, o[0])
		}
		var err error
		if runtime.GOOS == "freebsd" {
			args := []string{"useradd", u.Name, "-u", strconv.Itoa(u.Uid), "-g", prim, "-c", Comment + " " + u.Name, "-d", "/nonexistent", "-s", nologin()}
			if gs != "" {
				args = append(args, "-G", gs)
			}
			_, err = urun("pw", "", args...)
		} else {
			args := []string{"-u", strconv.Itoa(u.Uid), "-g", prim, "-c", Comment + " " + u.Name, "-d", "/nonexistent", "-M", "-s", nologin()}
			if gs != "" {
				args = append(args, "-G", gs)
			}
			_, err = urun("useradd", "", append(args, u.Name)...)
		}
		return err
	}
	if len(f) < 5 || !umanaged(f[4]) {
		return fmt.Errorf("account exists and was not created by cs-team (comment %q), not changed", strings.Join(f[4:5], ""))
	}
	if f[2] != strconv.Itoa(u.Uid) {
		return fmt.Errorf("account exists with uid %s (cs-team: %d), not changed", f[2], u.Uid)
	}
	if gs == "" {
		gs = prim
	}
	var err error
	if runtime.GOOS == "freebsd" {
		_, err = urun("pw", "", "usermod", u.Name, "-g", prim, "-G", gs)
	} else {
		_, err = urun("usermod", "", "-g", prim, "-G", gs, u.Name)
	}
	return err
}

// SetPassword: Samba-Passwort (smbpasswd -a legt den passdb-Eintrag an oder aendert ihn); ohne Samba eine Meldung.
func (b *samba) SetPassword(name, pass string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := ugetent("passwd", name)
	if f == nil || len(f) < 5 || !umanaged(f[4]) {
		return fmt.Errorf("no cs-team account %s on this host", name)
	}
	if _, err := exec.LookPath("smbpasswd"); err != nil {
		return fmt.Errorf("smbpasswd not found (Samba not installed): SMB password of %s not set", name)
	}
	if strings.ContainsAny(pass, "\r\n") {
		return fmt.Errorf("password of %s contains a line break, not set", name)
	}
	_, err := urun("smbpasswd", pass+"\n"+pass+"\n", "-s", "-a", name)
	return err
}

func (b *samba) hasPw(name string) bool {
	if _, err := exec.LookPath("pdbedit"); err != nil {
		return false
	}
	_, err := urun("pdbedit", "", "-u", name)
	return err == nil
}

func (b *samba) Lock(name string) error {
	if !b.hasPw(name) {
		return nil
	}
	_, err := urun("smbpasswd", "", "-d", name)
	return err
}

func (b *samba) Unlock(name string) error {
	if !b.hasPw(name) {
		return nil
	}
	_, err := urun("smbpasswd", "", "-e", name)
	return err
}

// Export: keine Passwoerter im replizierten Zustand (sie gelten nach dem Hochstufen ab der naechsten Anmeldung).
func (b *samba) Export(users, groups []string) (Host, error) { return Host{OS: b.OS()}, nil }

func (b *samba) Adopt(from, mine Host, users map[string]int, shares []string, dry bool, logf func(string, ...any)) error {
	logf("adopt: %s: accounts and groups with the same uid/gid exist; SMB passwords apply from each user's next cs-team sign-in", b.OS())
	return nil
}
