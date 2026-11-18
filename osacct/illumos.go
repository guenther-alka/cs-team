//go:build illumos || solaris

package osacct

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// illumos (OmniOS, OpenIndiana): Unix-Konten mit der uid/gid aus cs-team, SMB-Passwort als NT-Hash (SMBHash) in /var/smb/smbpasswd
// (wie pam_smb_passwd), Sperren mit smbadm disable-user. Die SID eines Kontos bildet illumos aus Rechner-SID und uid,
// deshalb reicht beim Failover die Uebernahme der Rechner-SID (idmap) und der smbpasswd-Zeilen.

const (
	smbPwFile  = "/var/smb/smbpasswd"
	smbPwOld   = "/var/smb/osmbpasswd"
	smbPwLock  = "/var/smb/.pwd.lock"
	idmapCfg   = "svc:/system/idmap" // config/machine_sid und machine_uuid liegen auf Dienst-Ebene, nicht auf der Instanz
	idmapSvc   = "svc:/system/idmap:default"
	smbSvc     = "svc:/network/smb/server:default"
	userShell  = "/bin/false"
	userHome   = "/tmp"
	maxNameLen = 32
)

var illumosName = regexp.MustCompile(`^[a-z_][a-z0-9._-]*$`)

type illumos struct{ mu sync.Mutex }

func New() Backend { return &illumos{} }

func (b *illumos) OS() string { return runtime.GOOS } // illumos oder solaris (Solaris 11: gleicher Kernel-SMB-Dienst)

func (b *illumos) ValidName(n string) bool { return len(n) <= maxNameLen && illumosName.MatchString(n) }

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, s)
	}
	return s, nil
}

// getent liefert die Felder eines passwd-/group-Eintrags oder nil.
func getent(db, name string) []string {
	out, err := exec.Command("getent", db, name).Output()
	if err != nil {
		return nil
	}
	l := strings.TrimSpace(string(out))
	if l == "" {
		return nil
	}
	return strings.Split(l, ":")
}

func (b *illumos) EnsureGroup(g Group) error {
	if f := getent("group", g.Name); f != nil {
		if len(f) >= 3 && f[2] != strconv.Itoa(g.Gid) {
			return fmt.Errorf("exists with gid %s (cs-team: %d), not changed", f[2], g.Gid)
		}
		return nil
	}
	if f := getent("group", strconv.Itoa(g.Gid)); f != nil {
		return fmt.Errorf("gid %d is used by group %s, not created", g.Gid, f[0])
	}
	_, err := run("groupadd", "-g", strconv.Itoa(g.Gid), g.Name)
	return err
}

func managed(gecos string) bool { return gecos == Comment || strings.HasPrefix(gecos, Comment+" ") }

func (b *illumos) EnsureUser(u User, prim string) error {
	gs := strings.Join(u.Groups, ",")
	f := getent("passwd", u.Name)
	if f == nil {
		if o := getent("passwd", strconv.Itoa(u.Uid)); o != nil {
			return fmt.Errorf("uid %d is used by %s, not created", u.Uid, o[0])
		}
		args := []string{"-u", strconv.Itoa(u.Uid), "-g", prim, "-c", Comment + " " + u.Name, "-d", userHome, "-s", userShell}
		if gs != "" {
			args = append(args, "-G", gs)
		}
		_, err := run("useradd", append(args, u.Name)...)
		return err
	}
	if len(f) < 5 || !managed(f[4]) {
		return fmt.Errorf("account exists and was not created by cs-team (comment %q), not changed", strings.Join(f[4:5], ""))
	}
	if f[2] != strconv.Itoa(u.Uid) {
		return fmt.Errorf("account exists with uid %s (cs-team: %d), not changed", f[2], u.Uid)
	}
	if gs == "" {
		gs = prim // keine Zusatzgruppen: Liste = Hauptgruppe (usermod -G "" ist nicht ueberall erlaubt)
	}
	args := []string{"-g", prim, "-G", gs}
	_, err := run("usermod", append(args, u.Name)...)
	return err
}

// withPwLock: wie libsmb (smb_pwd) ueber die Sperrdatei, damit smbadm/passwd nicht gleichzeitig schreiben.
func withPwLock(fn func() error) error {
	f, err := os.OpenFile(smbPwLock, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	lk := unix.Flock_t{Type: unix.F_WRLCK, Whence: 0}
	if err := unix.FcntlFlock(f.Fd(), unix.F_SETLKW, &lk); err != nil {
		return err
	}
	defer func() {
		lk.Type = unix.F_UNLCK
		unix.FcntlFlock(f.Fd(), unix.F_SETLK, &lk)
	}()
	return fn()
}

// writePw schreibt smbpasswd neu (alte Fassung als osmbpasswd, wie libsmb), Rechte 0400 root:sys.
func writePw(content string) error {
	tmp := smbPwFile + ".cs-team"
	if err := os.WriteFile(tmp, []byte(content), 0400); err != nil {
		return err
	}
	os.Chown(tmp, 0, 3) // sys
	if old, err := os.ReadFile(smbPwFile); err == nil {
		os.Remove(smbPwOld)
		os.WriteFile(smbPwOld, old, 0400)
		os.Chown(smbPwOld, 0, 3)
	}
	return os.Rename(tmp, smbPwFile)
}

func (b *illumos) SetPassword(name, pass string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := getent("passwd", name)
	if f == nil || len(f) < 5 || !managed(f[4]) {
		return fmt.Errorf("no cs-team account %s on this host", name)
	}
	uid, _ := strconv.Atoi(f[2])
	return withPwLock(func() error {
		cur, err := os.ReadFile(smbPwFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		return writePw(SMBSet(string(cur), name, uid, SMBHash(pass)))
	})
}

func (b *illumos) hasPw(name string) bool {
	cur, _ := os.ReadFile(smbPwFile)
	return len(SMBGet(string(cur), []string{name})) > 0
}

func (b *illumos) Lock(name string) error {
	if !b.hasPw(name) {
		return nil
	}
	_, err := run("smbadm", "disable-user", name)
	return err
}

func (b *illumos) Unlock(name string) error {
	if !b.hasPw(name) {
		return nil
	}
	_, err := run("smbadm", "enable-user", name)
	return err
}

// idmapProp liest eine Eigenschaft von system/idmap (svcprop darf machine_sid nicht lesen, svccfg schon).
func idmapProp(p string) string {
	out, err := run("svccfg", "-s", idmapCfg, "listprop", "config/"+p)
	if err != nil {
		return ""
	}
	f := strings.Fields(out)
	if len(f) >= 3 {
		return f[2]
	}
	return ""
}

func (b *illumos) Export(users, groups []string) (Host, error) {
	h := Host{OS: b.OS(), SID: idmapProp("machine_sid"), UUID: idmapProp("machine_uuid")}
	cur, err := os.ReadFile(smbPwFile)
	if err != nil && !os.IsNotExist(err) {
		return h, err
	}
	h.SMB = SMBGet(string(cur), users)
	if len(h.SMB) == 0 {
		h.SMB = nil
	}
	return h, nil
}

func (b *illumos) Adopt(from, mine Host, users map[string]int, shares []string, dry bool, logf func(string, ...any)) error {
	restart := false
	for _, p := range []struct{ name, old, new string }{{"machine_sid", mine.SID, from.SID}, {"machine_uuid", mine.UUID, from.UUID}} {
		if p.new == "" || p.new == p.old {
			continue
		}
		logf("adopt: idmap config/%s %s -> %s", p.name, p.old, p.new)
		if !dry {
			if _, err := run("svccfg", "-s", idmapCfg, "setprop", "config/"+p.name, "=", "astring:", p.new); err != nil {
				return err
			}
			restart = true
		}
	}
	// SMB-Passwoerter: nur Zeilen verwalteter Konten, die hier mit derselben uid existieren
	local := map[string]int{}
	for n, uid := range users {
		if f := getent("passwd", n); f != nil && len(f) >= 5 && managed(f[4]) && f[2] == strconv.Itoa(uid) {
			local[n] = uid
		}
	}
	var done []string
	err := withPwLock(func() error {
		cur, err := os.ReadFile(smbPwFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		var nc string
		nc, done = SMBMerge(string(cur), from.SMB, local)
		if dry || len(done) == 0 {
			return nil
		}
		return writePw(nc)
	})
	if err != nil {
		return err
	}
	logf("adopt: SMB passwords of %d account(s) taken over: %s", len(done), strings.Join(done, ","))
	if restart {
		for _, cmd := range [][]string{{"svcadm", "refresh", idmapSvc}, {"svcadm", "restart", idmapSvc}, {"svcadm", "restart", smbSvc}} {
			if _, err := run(cmd[0], cmd[1:]...); err != nil {
				logf("adopt: %v", err)
			}
		}
		logf("adopt: idmap and smb/server restarted (new machine SID applies to all local accounts of this host)")
	}
	return nil
}
