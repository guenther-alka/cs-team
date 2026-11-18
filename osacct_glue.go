package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"

	"cs-team/auth"
	"cs-team/osacct"
	"cs-team/store"
)

// newOSAccounts (0.72): CS_OSACCOUNTS=1 legt fuer Mitglieder exportierter Gruppen Betriebssystem-Konten an (illumos, Windows),
// damit der cs-team-Rechner mit denselben Namen und Passwoertern SMB-Server sein kann (osacct, HA_SPEC Abschnitt 7/12).
// CS_OSACCOUNTS_SHARES: Freigabepfade (";"), deren ACL beim Failover unter Windows umgeschrieben werden.
func newOSAccounts(st store.Store, a *auth.Auth, host string, readOnly bool) *osacct.Mgr {
	if os.Getenv("CS_OSACCOUNTS") != "1" {
		return nil
	}
	b := osacct.New()
	if b == nil {
		log.Printf("osaccounts: not supported on %s (CS_OSACCOUNTS ignored)", runtime.GOOS)
		return nil
	}
	return &osacct.Mgr{St: st, B: b, Host: host, ReadOnly: readOnly, Logf: log.Printf,
		Shares: osacct.SplitShares(os.Getenv("CS_OSACCOUNTS_SHARES")), Src: osSrc(a)}
}

func osSrc(a *auth.Auth) func(context.Context) ([]osacct.User, []osacct.Group, osacct.Group) {
	return func(ctx context.Context) ([]osacct.User, []osacct.Group, osacct.Group) {
		us, gs := a.ExportDataOS(ctx) // mit Spiegelkonten (0.72): Passwort ab der naechsten Anmeldung
		member := map[string][]string{}
		var groups []osacct.Group
		for _, g := range gs {
			groups = append(groups, osacct.Group{Name: g.Name, Gid: g.Gid})
			for _, n := range g.Members {
				member[n] = append(member[n], g.Name)
			}
		}
		prim := osacct.Group{Name: auth.DefaultGroup}
		var users []osacct.User
		for _, u := range us {
			if u.Gid > 0 {
				prim.Gid = u.Gid
			}
			if len(member[u.Name]) == 0 { // nur Mitglieder exportierter Gruppen
				continue
			}
			users = append(users, osacct.User{Name: u.Name, Uid: u.Uid, Gid: u.Gid, Groups: member[u.Name]})
		}
		return users, groups, prim
	}
}

// osAccountsCmd: cs-team osaccounts sync | adopt | adopt-dry | status
func osAccountsCmd(m *osacct.Mgr, cmd string) {
	ctx := context.Background()
	if m == nil {
		log.Fatal("osaccounts: CS_OSACCOUNTS=1 not set or not supported on this system")
	}
	var err error
	switch cmd {
	case "sync":
		err = m.Reconcile(ctx)
	case "adopt":
		err = m.Adopt(ctx, false)
	case "adopt-dry":
		err = m.Adopt(ctx, true)
	case "status":
		var s osacct.State
		s, err = m.LoadState(ctx)
		if err == nil {
			for n, h := range s.Hosts { // NT-Hashes nicht anzeigen
				for k := range h.SMB {
					h.SMB[k] = "(set)"
				}
				s.Hosts[n] = h
			}
			b, _ := json.MarshalIndent(s, "", " ")
			fmt.Println(string(b))
			var us []string
			for u := range s.Users {
				us = append(us, u)
			}
			sort.Strings(us)
			fmt.Printf("this host: %s, last master: %s, managed accounts: %v\n", m.Host, s.Last, us)
		}
	default:
		log.Fatal("usage: cs-team osaccounts sync|adopt|adopt-dry|status")
	}
	if err != nil {
		log.Fatal("osaccounts: ", err)
	}
	fmt.Println("ok: osaccounts", cmd)
}
