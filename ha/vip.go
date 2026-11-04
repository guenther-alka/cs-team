package ha

// Dienst-IP (virtuelle Adresse, docs/HA_SPEC.md Abschnitt 8): der Master setzt sie beim Start auf seine
// Netzwerkschnittstelle (nicht dauerhaft, ueberlebt keinen Neustart) und nimmt sie beim Beenden wieder weg.
// Ein Standby setzt sie nie. Die Schnittstelle ist optional: gesucht wird die mit einer Adresse im selben Subnetz.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrVIPInUse: unter der Dienst-IP antwortet schon ein anderer Rechner (cs-team startet dann nicht).
var ErrVIPInUse = errors.New("service IP is already used by another host")

// runCmd und dialInUse sind Variablen, damit die Tests ohne Administratorrechte laufen.
var runCmd = func(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

var dialInUse = func(ip, port string) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, port), 1500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

type vipPlan struct {
	IP     netip.Addr
	Prefix int
	Iface  string
	Local  bool // die Adresse liegt schon auf diesem Rechner
}

func validIface(s string) bool {
	if len(s) > 64 {
		return false
	}
	return !strings.ContainsAny(s, "'\"`;$&|<>\r\n\\") && !strings.ContainsFunc(s, func(r rune) bool { return r < 32 })
}

// planVIP: "ip" oder "ip/prefix"; iface leer = Schnittstelle im selben Subnetz suchen.
func planVIP(vip, iface string) (vipPlan, error) {
	var p vipPlan
	s := strings.TrimSpace(vip)
	pre := -1
	if i := strings.IndexByte(s, '/'); i >= 0 {
		n, err := strconv.Atoi(s[i+1:])
		if err != nil || n < 1 || n > 128 {
			return p, fmt.Errorf("service IP %q: bad prefix length", vip)
		}
		pre, s = n, s[:i]
	}
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return p, fmt.Errorf("service IP %q is not an IP address", vip)
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return p, fmt.Errorf("service IP %s is not a usable address", ip)
	}
	iface = strings.TrimSpace(iface)
	if !validIface(iface) {
		return p, fmt.Errorf("network interface %q: invalid name", iface)
	}
	p.IP = ip
	ifs, _ := net.Interfaces()
	for _, in := range ifs {
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			pf, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			pf = pf.Unmap()
			if pf == ip {
				p.Local = true
			}
			if p.Iface != "" || in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 || pf.Is4() != ip.Is4() {
				continue
			}
			if iface != "" && in.Name != iface {
				continue
			}
			ones, _ := ipn.Mask.Size()
			if px, err := pf.Prefix(ones); err == nil && px.Contains(ip) {
				p.Iface = in.Name
				if pre < 0 {
					pre = ones
				}
			}
		}
	}
	if p.Local && p.Iface == "" {
		p.Iface = iface
	}
	if p.Iface == "" && iface != "" && pre > 0 { // Schnittstelle vorgegeben, Adresse im anderen Subnetz: Prefix ist ausdruecklich angegeben
		p.Iface = iface
	}
	if p.Iface == "" && !p.Local {
		return p, fmt.Errorf("no network interface with an address in the subnet of the service IP %s (same subnet required; or enter interface and ip/prefix)", ip)
	}
	if pre < 0 {
		if ip.Is4() {
			pre = 24
		} else {
			pre = 64
		}
	}
	p.Prefix = pre
	return p, nil
}

type vipCmd struct {
	args     []string
	optional bool // Fehler ignorieren (Aufraeumen, ARP)
}

// vipCmds: die Befehle je Betriebssystem (goos wie runtime.GOOS).
func vipCmds(goos string, p vipPlan, up bool) []vipCmd {
	ip, pre, ifc := p.IP.String(), strconv.Itoa(p.Prefix), p.Iface
	cidr := ip + "/" + pre
	v6 := p.IP.Is6()
	switch goos {
	case "linux":
		if up {
			c := []vipCmd{{args: []string{"ip", "addr", "add", cidr, "dev", ifc}}}
			if !v6 {
				c = append(c, vipCmd{args: []string{"arping", "-U", "-c", "2", "-I", ifc, ip}, optional: true})
			}
			return c
		}
		return []vipCmd{{args: []string{"ip", "addr", "del", cidr, "dev", ifc}}}
	case "freebsd":
		fam := "inet"
		if v6 {
			fam = "inet6"
		}
		if up {
			return []vipCmd{{args: []string{"ifconfig", ifc, fam, cidr, "alias"}}}
		}
		return []vipCmd{{args: []string{"ifconfig", ifc, fam, ip, "-alias"}}}
	case "darwin":
		if v6 {
			if up {
				return []vipCmd{{args: []string{"ifconfig", ifc, "inet6", ip, "prefixlen", pre, "alias"}}}
			}
			return []vipCmd{{args: []string{"ifconfig", ifc, "inet6", ip, "-alias"}}}
		}
		if up {
			mask := "0x" + hex.EncodeToString(net.CIDRMask(p.Prefix, 32))
			return []vipCmd{{args: []string{"ifconfig", ifc, "inet", ip, "netmask", mask, "alias"}}}
		}
		return []vipCmd{{args: []string{"ifconfig", ifc, "inet", ip, "-alias"}}}
	case "illumos", "solaris":
		obj := ifc + "/csvip"
		a := cidr
		if goos == "solaris" {
			a = "local=" + cidr // Solaris 11
		}
		if up {
			return []vipCmd{{args: []string{"ipadm", "delete-addr", obj}, optional: true},
				{args: []string{"ipadm", "create-addr", "-t", "-T", "static", "-a", a, obj}}}
		}
		return []vipCmd{{args: []string{"ipadm", "delete-addr", obj}}}
	case "windows":
		ps := "$ErrorActionPreference='Stop'; "
		if up {
			// New-NetIPAddress switches DHCP off on the adapter (the computer would lose its address): refuse in that case
			fam := "IPv4"
			if v6 {
				fam = "IPv6"
			}
			ps += fmt.Sprintf("if ((Get-NetIPInterface -InterfaceAlias '%s' -AddressFamily %s).Dhcp -eq 'Enabled') { throw 'the network adapter uses DHCP: Windows cannot hold a second address together with DHCP, give this computer a static address first' }; ", ifc, fam)
			ps += fmt.Sprintf("New-NetIPAddress -IPAddress '%s' -PrefixLength %s -InterfaceAlias '%s' -SkipAsSource $true -PolicyStore ActiveStore | Out-Null", ip, pre, ifc)
		} else {
			ps += fmt.Sprintf("Remove-NetIPAddress -IPAddress '%s' -Confirm:$false -PolicyStore ActiveStore", ip)
		}
		return []vipCmd{{args: []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", ps}}}
	}
	return nil
}

func runVIP(goos string, p vipPlan, up bool) error {
	cmds := vipCmds(goos, p, up)
	if cmds == nil {
		return fmt.Errorf("service IP is not supported on %s (switch over with DNS)", goos)
	}
	for _, c := range cmds {
		if out, err := runCmd(c.args[0], c.args[1:]...); err != nil && !c.optional {
			return fmt.Errorf("%s: %v %s", strings.Join(c.args[:2], " "), err, out)
		}
	}
	return nil
}

// VIPUp: nur der Master, nur wenn die Gruppe eine Dienst-IP hat. port = Dienstport (Pruefung "frei").
// Fehler ErrVIPInUse = anderer Rechner antwortet; andere Fehler (keine Rechte, kein Subnetz) sind nur Warnungen.
func (s *Svc) VIPUp(ctx context.Context, port string) error {
	if s.Mode != "master" {
		return nil
	}
	g := s.LoadGroup(ctx)
	if g.VIP == "" {
		return nil
	}
	var iface string
	for _, m := range g.Members {
		if m.Name == s.Name {
			iface = m.Iface
		}
	}
	p, err := planVIP(g.VIP, iface)
	if err != nil {
		s.setVIP(nil, false, "error: "+err.Error())
		return err
	}
	if p.Local {
		s.setVIP(&p, false, "") // liegt schon hier (von Hand oder Neustart): lassen, beim Beenden nicht entfernen
		return nil
	}
	if !s.Force && port != "" && dialInUse(p.IP.String(), port) {
		err := fmt.Errorf("%w: %s answers on port %s", ErrVIPInUse, p.IP, port)
		s.setVIP(nil, false, "error: "+err.Error())
		return err
	}
	if err := runVIP(runtime.GOOS, p, true); err != nil {
		s.setVIP(nil, false, "error: "+err.Error())
		return err
	}
	s.setVIP(&p, true, "")
	return nil
}

// VIPDown: nimmt die Dienst-IP weg, wenn cs-team sie gesetzt hat (beim Beenden).
func (s *Svc) VIPDown() {
	s.mu.Lock()
	p, own := s.vip, s.vipOwned
	s.vip, s.vipOwned = nil, false
	s.mu.Unlock()
	if p != nil && own {
		if err := runVIP(runtime.GOOS, *p, false); err != nil {
			fmt.Println("ha: service IP not removed:", err)
		}
	}
}

// DownFromDir: `cs-team vipdown` (CS_DIR gesetzt): nimmt die Dienst-IP dieses Rechners weg, ohne dass cs-team laeuft.
// Fuer Systeme, auf denen cs-team hart beendet wird (Windows), und zum Aufraeumen von Hand. Gibt true zurueck, wenn eine Adresse entfernt wurde.
func DownFromDir(dir string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, ".csteam", "failover", "group.json"))
	if err != nil {
		return false, err
	}
	var g Group
	if err := json.Unmarshal(b, &g); err != nil {
		return false, err
	}
	if g.VIP == "" {
		return false, nil
	}
	h, _ := os.Hostname()
	s := &Svc{Name: h}
	if n := os.Getenv("CS_HA_NAME"); n != "" {
		s.Name = n
	}
	_ = s.resolveName(g)
	var iface string
	for _, m := range g.Members {
		if m.Name == s.Name {
			iface = m.Iface
		}
	}
	p, err := planVIP(g.VIP, iface)
	if err != nil || !p.Local {
		return false, err
	}
	return true, runVIP(runtime.GOOS, p, false)
}

// VIPActive: cs-team hat die Dienst-IP gesetzt und muss sie beim Beenden wieder wegnehmen.
func (s *Svc) VIPActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vip != nil && s.vipOwned
}

func (s *Svc) setVIP(p *vipPlan, own bool, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vip, s.vipOwned, s.vipMsg = p, own, msg
}

// vipText: Anzeige in /ha/status.
func (s *Svc) vipText() string {
	if s.vipMsg != "" {
		return s.vipMsg
	}
	if s.vip != nil {
		return fmt.Sprintf("%s up on %s", s.vip.IP, s.vip.Iface)
	}
	return ""
}

// resolveName ordnet diesen Rechner einem Mitglied der HA-Gruppe zu: genauer Name, Name ohne Beachtung der
// Gross-/Kleinschreibung, Kurzname (vor dem ersten Punkt), zuletzt die Adresse des Mitglieds = eigene Adresse.
// Eine leere Gruppe (erster Start) laesst den Namen, wie er ist.
func (s *Svc) resolveName(g Group) error {
	if len(g.Members) == 0 || s.Force {
		return nil
	}
	short := func(x string) string {
		x = strings.ToLower(strings.TrimSpace(x))
		if i := strings.IndexByte(x, '.'); i > 0 && net.ParseIP(x) == nil {
			return x[:i]
		}
		return x
	}
	for _, m := range g.Members {
		if m.Name == s.Name {
			return nil
		}
	}
	for _, m := range g.Members {
		if strings.EqualFold(m.Name, s.Name) {
			s.Name = m.Name
			return nil
		}
	}
	for _, m := range g.Members {
		if short(m.Name) == short(s.Name) {
			s.Name = m.Name
			return nil
		}
	}
	local := map[string]bool{}
	if as, err := net.InterfaceAddrs(); err == nil {
		for _, a := range as {
			if ipn, ok := a.(*net.IPNet); ok {
				local[ipn.IP.String()] = true
			}
		}
	}
	for _, m := range g.Members {
		h := m.Addr
		if i := strings.Index(h, "://"); i >= 0 {
			h = h[i+3:]
		}
		if i := strings.IndexAny(h, "/"); i >= 0 {
			h = h[:i]
		}
		if hh, _, err := net.SplitHostPort(h); err == nil {
			h = hh
		}
		if local[h] || short(h) == short(s.Name) {
			s.Name = m.Name
			return nil
		}
	}
	var names []string
	for _, m := range g.Members {
		names = append(names, m.Name)
	}
	return fmt.Errorf("this host %q is not in the HA group (%s): add it in Settings > High availability, or set CS_HA_NAME", s.Name, strings.Join(names, ", "))
}
