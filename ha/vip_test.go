package ha

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strings"
	"testing"

	"cs-team/store"
)

func TestPlanVIPParse(t *testing.T) {
	for _, bad := range []string{"x", "300.1.1.1", "0.0.0.0", "127.0.0.1", "10.0.0.1/0", "10.0.0.1/x", "224.0.0.1"} {
		if _, err := planVIP(bad, ""); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
	if _, err := planVIP("10.9.9.9", "bad'name"); err == nil {
		t.Fatal("quote in interface name must be refused")
	}
}

func TestVIPCmds(t *testing.T) {
	p := vipPlan{IP: netip.MustParseAddr("192.168.2.50"), Prefix: 24, Iface: "igb0"}
	join := func(goos string, up bool) string {
		var s []string
		for _, c := range vipCmds(goos, p, up) {
			s = append(s, strings.Join(c.args, " "))
		}
		return strings.Join(s, " ; ")
	}
	want := map[string]string{
		"linux/up":     "ip addr add 192.168.2.50/24 dev igb0 ; arping -U -c 2 -I igb0 192.168.2.50",
		"linux/down":   "ip addr del 192.168.2.50/24 dev igb0",
		"freebsd/up":   "ifconfig igb0 inet 192.168.2.50/24 alias",
		"freebsd/down": "ifconfig igb0 inet 192.168.2.50 -alias",
		"darwin/up":    "ifconfig igb0 inet 192.168.2.50 netmask 0xffffff00 alias",
		"darwin/down":  "ifconfig igb0 inet 192.168.2.50 -alias",
		"illumos/up":   "ipadm delete-addr igb0/csvip ; ipadm create-addr -t -T static -a 192.168.2.50/24 igb0/csvip",
		"solaris/up":   "ipadm delete-addr igb0/csvip ; ipadm create-addr -t -T static -a local=192.168.2.50/24 igb0/csvip",
		"illumos/down": "ipadm delete-addr igb0/csvip",
	}
	for k, w := range want {
		g, up := strings.Split(k, "/")[0], strings.HasSuffix(k, "/up")
		if got := join(g, up); got != w {
			t.Fatalf("%s:\n got  %s\n want %s", k, got, w)
		}
	}
	if w := join("windows", true); !strings.Contains(w, ".Dhcp -eq 'Enabled') { throw") || !strings.Contains(w, "New-NetIPAddress -IPAddress '192.168.2.50' -PrefixLength 24 -InterfaceAlias 'igb0' -SkipAsSource $true -PolicyStore ActiveStore") {
		t.Fatal(w)
	}
	if w := join("windows", false); !strings.Contains(w, "Remove-NetIPAddress -IPAddress '192.168.2.50'") {
		t.Fatal(w)
	}
	if vipCmds("plan9", p, true) != nil {
		t.Fatal("unsupported OS must give no commands")
	}
}

// Master setzt die Dienst-IP beim Start, nur wenn der Dienstport nicht von einem anderen Rechner beantwortet wird.
func TestVIPUpAndInUse(t *testing.T) {
	ctx := context.Background()
	var ran []string
	oldRun, oldDial := runCmd, dialInUse
	defer func() { runCmd, dialInUse = oldRun, oldDial }()
	runCmd = func(name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return "", nil
	}
	dialInUse = func(ip, port string) bool { return false }

	// eine Adresse im Subnetz einer vorhandenen Schnittstelle suchen
	ip, iface := localSubnetIP(t)
	st := store.NewMem()
	a := New(st, "master", "A", false, false)
	if err := a.Start(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveGroup(ctx, Group{Members: []Member{{Name: "A", Addr: "127.0.0.1:1"}}, VIP: ip}); err != nil {
		t.Fatal(err)
	}
	if err := a.VIPUp(ctx, "8080"); err != nil {
		t.Fatal(err)
	}
	if len(ran) == 0 || !a.VIPActive() || !strings.Contains(a.Status().VIP, iface) {
		t.Fatal("not set:", ran, a.Status())
	}
	a.VIPDown()
	if a.VIPActive() || a.Status().VIP != "" {
		t.Fatal("still set after VIPDown")
	}

	dialInUse = func(ip, port string) bool { return true }
	if err := a.VIPUp(ctx, "8080"); !errors.Is(err, ErrVIPInUse) {
		t.Fatal("expected in-use refusal, got", err)
	}
	a.Force = true // CS_HA_FORCE
	if err := a.VIPUp(ctx, "8080"); err != nil {
		t.Fatal("force", err)
	}

	// Standby setzt nie
	ran = nil
	s := New(store.ReadOnly(st), "standby", "A", false, false)
	if err := s.VIPUp(ctx, "8080"); err != nil || len(ran) != 0 {
		t.Fatal("standby must not set the IP", err, ran)
	}
	_ = runtime.GOOS
}

func TestResolveName(t *testing.T) {
	g := Group{Members: []Member{{Name: "omnios58", Addr: "10.0.0.58:9004"}, {Name: "Backup", Addr: "10.0.0.59:9004"}}}
	for in, want := range map[string]string{"omnios58": "omnios58", "OMNIOS58": "omnios58", "omnios58.schule.local": "omnios58", "backup": "Backup"} {
		s := New(store.NewMem(), "master", in, false, false)
		if err := s.resolveName(g); err != nil || s.Name != want {
			t.Fatalf("%q -> %q (%v), want %q", in, s.Name, err, want)
		}
	}
	s := New(store.NewMem(), "master", "other", false, false)
	if err := s.resolveName(g); err == nil || !strings.Contains(err.Error(), "CS_HA_NAME") {
		t.Fatal("unknown host must be refused with a hint:", err)
	}
	s = New(store.NewMem(), "master", "other", false, false)
	if err := s.resolveName(Group{}); err != nil {
		t.Fatal("empty group (first start) must not refuse:", err)
	}
}

// localSubnetIP: eine freie Adresse im Subnetz einer aktiven Schnittstelle (ohne etwas zu setzen); sonst Test uebersprungen.
func localSubnetIP(t *testing.T) (ip, iface string) {
	t.Helper()
	ifs, _ := net.Interfaces()
	for _, in := range ifs {
		if in.Flags&net.FlagUp == 0 || in.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := in.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ones, bits := ipn.Mask.Size()
			if bits != 32 || ones > 30 || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			v := ipn.IP.To4()
			c := net.IP{v[0], v[1], v[2], v[3]}
			if c[3] < 200 {
				c[3] += 40
			} else {
				c[3] -= 40
			}
			return c.String(), in.Name
		}
	}
	t.Skip("no active IPv4 interface")
	return "", ""
}
