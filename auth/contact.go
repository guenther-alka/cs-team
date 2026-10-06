package auth

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strings"
)

// Externe Kontaktadressen (E-Mail, Chat-URL) und Gruppenmodi für Chat und Nachrichten.

var (
	ErrBadMail = errors.New("mail: invalid address")
	ErrBadChat = errors.New("chat: http(s) URL expected")
	ErrBadMode = errors.New(`mode: "member", "admin" or "off"`)
)

// Modi für Gruppen-Chat und Gruppen-Nachrichten.
const (
	ModeMember = "member" // alle Mitglieder
	ModeAdmin  = "admin"  // nur Gruppen-Admins (beim Chat: Mitglieder lesen, nur Admins schreiben)
	ModeOff    = "off"    // abgeschaltet
)

func validMode(m string) bool { return m == ModeMember || m == ModeAdmin || m == ModeOff }

// ChatMode/MsgMode: Vorgabe ohne Eintrag: Chat für Mitglieder, Nachrichten nur für Admins.
func chatMode(g Group) string {
	if g.Chat == "" {
		return ModeMember
	}
	return g.Chat
}
func chansMode(g Group) string {
	if g.Chans == "" {
		return ModeAdmin
	}
	return g.Chans
}
func tasksMode(g Group) string {
	if g.Tasks == "" {
		return ModeMember
	}
	return g.Tasks
}

// aiMode: KI-Dokumente für Mitglieder: "member" oder "off" (Vorgabe).
func aiMode(g Group) string {
	if g.AI == ModeMember {
		return ModeMember
	}
	return ModeOff
}

// SetGroupAI: mode "member" | "off".
func (a *Auth) SetGroupAI(ctx_ context.Context, group, mode string) error {
	if mode != ModeMember && mode != ModeOff {
		return ErrBadMode
	}
	return a.mutateGroups(ctx_, func(m map[string]Group) error {
		g, ok := m[group]
		if !ok {
			return ErrNoGroup
		}
		g.AI = ""
		if mode == ModeMember {
			g.AI = ModeMember
		}
		m[group] = g
		return nil
	})
}

// AICreateOK: darf der Benutzer den KI-Assistenten Dokumente vorschlagen und anlegen lassen?
// Globale Admins und Gruppen-Admins immer, Mitglieder nur, wenn eine ihrer Gruppen "KI für Mitglieder" eingeschaltet hat.
func AICreateOK(ctx context.Context) bool {
	if IsAdmin(ctx) || len(AdminOf(ctx)) > 0 {
		return true
	}
	if std == nil {
		return false
	}
	gs := GroupsOf(User(ctx))
	std.refresh(ctx)
	std.mu.Lock()
	defer std.mu.Unlock()
	for _, n := range gs {
		if g, ok := std.groups[n]; ok && g.AI == ModeMember {
			return true
		}
	}
	return false
}

func msgMode(g Group) string {
	if g.Msg == "" {
		return ModeAdmin
	}
	return g.Msg
}

func cleanContact(mailAddr, chat string) (string, string, error) {
	mailAddr, chat = strings.TrimSpace(mailAddr), strings.TrimSpace(chat)
	if mailAddr != "" {
		a, err := mail.ParseAddress(mailAddr)
		if err != nil || a.Name != "" || len(mailAddr) > 254 || strings.ContainsAny(mailAddr, "\r\n") {
			return "", "", ErrBadMail
		}
		mailAddr = a.Address
	}
	if chat != "" {
		u, err := url.Parse(chat)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(chat) > 500 || strings.ContainsAny(chat, "\r\n ") {
			return "", "", ErrBadChat
		}
	}
	return mailAddr, chat, nil
}

// SetContact setzt E-Mail und Chat-Adresse eines Benutzers.
func (a *Auth) SetContact(ctx_ context.Context, name, mailAddr, chat string) error {
	mailAddr, chat, err := cleanContact(mailAddr, chat)
	if err != nil {
		return err
	}
	return a.mutate(ctx_, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		u.Mail, u.Chat = mailAddr, chat
		m[name] = u
		return nil
	})
}

// SetMail setzt nur die E-Mail-Adresse (Gruppen-Admins dürfen die Webhook-Adresse eines anderen nicht ändern, S-09).
func (a *Auth) SetMail(ctx_ context.Context, name, mailAddr string) error {
	mailAddr, _, err := cleanContact(mailAddr, "")
	if err != nil {
		return err
	}
	return a.mutate(ctx_, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok {
			return ErrNoUser
		}
		u.Mail = mailAddr
		m[name] = u
		return nil
	})
}

func (a *Auth) SetGroupModes(ctx_ context.Context, group string, chat, msg, chans, tasks *string) error {
	if (chat != nil && !validMode(*chat)) || (msg != nil && !validMode(*msg)) || (chans != nil && !validMode(*chans)) || (tasks != nil && !validMode(*tasks)) {
		return ErrBadMode
	}
	return a.mutateGroups(ctx_, func(m map[string]Group) error {
		g, ok := m[group]
		if !ok {
			return ErrNoGroup
		}
		if chat != nil {
			g.Chat = *chat
		}
		if msg != nil {
			g.Msg = *msg
		}
		if chans != nil {
			g.Chans = *chans
		}
		if tasks != nil {
			g.Tasks = *tasks
		}
		m[group] = g
		return nil
	})
}

// MinRetentionDays: kleinster erlaubter Aufbewahrungswert (außer 0 = aus); schützt vor Tippfehlern, die Daten sofort löschen.
const MinRetentionDays = 7

// ErrBadDays: Aufbewahrung nicht 0 und nicht in MinRetentionDays..36500.
var ErrBadDays = errors.New("retention: 0 (off) or 7..36500 days")

// SetGroupChatDays: Aufbewahrung Chat der Gruppe in Tagen (0 = unbegrenzt).
func (a *Auth) SetGroupChatDays(ctx_ context.Context, group string, days int) error {
	if days < 0 || (days > 0 && days < MinRetentionDays) || days > 36500 {
		return ErrBadDays
	}
	return a.mutateGroups(ctx_, func(m map[string]Group) error {
		g, ok := m[group]
		if !ok {
			return ErrNoGroup
		}
		g.ChatDays = days
		m[group] = g
		return nil
	})
}

// ---- Abfragen für die Pakete chat/message (ohne Request-Kontext) ----

type GInfo struct {
	Name   string
	Chat   string // member | admin | off
	Msg    string
	Chans  string // wer Kanäle anlegen darf: member | admin | off
	Tasks  string // wer Aufgaben anlegen darf: member | admin | off
	ChatDays int  // Aufbewahrung Chat in Tagen (0 = unbegrenzt)
	Admins []string
}

func GroupInfoOf(name string) (GInfo, bool) {
	if std == nil {
		return GInfo{}, false
	}
	std.refresh(context.Background())
	std.mu.Lock()
	defer std.mu.Unlock()
	g, ok := std.groups[name]
	if !ok {
		return GInfo{}, false
	}
	return GInfo{Name: name, Chat: chatMode(g), Msg: msgMode(g), Chans: chansMode(g), Tasks: tasksMode(g), ChatDays: g.ChatDays, Admins: adminsOf(std, name, g)}, true
}

// AllGroupNames: Namen aller Gruppen (sortiert).
func AllGroupNames() []string {
	if std == nil {
		return nil
	}
	std.refresh(context.Background())
	std.mu.Lock()
	defer std.mu.Unlock()
	out := make([]string, 0, len(std.groups))
	for n := range std.groups {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// adminsOf: Gruppen-Admins; bei der Standardgruppe die aktiven globalen Admins. (Aufrufer hält a.mu)
func adminsOf(a *Auth, name string, g Group) []string {
	if name != DefaultGroup {
		return append([]string{}, g.Admins...)
	}
	var out []string
	for n, u := range a.users {
		if u.Admin && !u.Disabled {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// IsMember: aktiver Benutzer ist Mitglied der Gruppe.
func IsMember(user, group string) bool {
	if std == nil {
		return false
	}
	u, ok := std.get(context.Background(), user)
	return ok && !u.Disabled && contains(effGroups(u), group)
}

// IsGroupAdmin: Gruppen-Admin dieser Gruppe oder (für die Standardgruppe) globaler Admin.
func IsGroupAdmin(user, group string) bool {
	info, ok := GroupInfoOf(group)
	return ok && contains(info.Admins, user)
}

// MemberContact: ein Mitglied mit seinen Adressen.
type MemberContact struct{ Name, Mail, Chat string }

// MembersOf liefert die aktiven Mitglieder einer Gruppe mit ihren Kontaktadressen.
func MembersOf(group string) []MemberContact {
	if std == nil {
		return nil
	}
	std.refresh(context.Background())
	std.mu.Lock()
	defer std.mu.Unlock()
	var out []MemberContact
	for n, u := range std.users {
		if !u.Disabled && contains(effGroups(u), group) {
			out = append(out, MemberContact{n, u.Mail, u.Chat})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *Auth) contactRoutes(mux *http.ServeMux, adm func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/me/contact", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Mail, Chat string }
		if !body(w, r, &in) {
			return
		}
		if err := a.SetContact(r.Context(), User(r.Context()), in.Mail, in.Chat); err != nil {
			fail_(w, err)
		}
	})))
	mux.Handle("POST /api/users/{name}/contact", adm(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Mail, Chat string }
		if !body(w, r, &in) {
			return
		}
		if !a.manages(r.Context(), r.PathValue("name")) { // globaler Admin oder Gruppen-Admin des Benutzers
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !IsAdmin(r.Context()) && r.PathValue("name") != User(r.Context()) { // Gruppen-Admin: nur die E-Mail-Adresse; Webhook (enthält Token) bleibt unberührt
			if strings.TrimSpace(in.Chat) != "" {
				http.Error(w, "chat address: only the owner or a global admin", http.StatusForbidden)
				return
			}
			if err := a.SetMail(r.Context(), r.PathValue("name"), in.Mail); err != nil {
				fail_(w, err)
			}
			return
		}
		if err := a.SetContact(r.Context(), r.PathValue("name"), in.Mail, in.Chat); err != nil {
			fail_(w, err)
		}
	}))
}

// ContactOf: Adressen eines aktiven Benutzers; ok=false wenn unbekannt oder gesperrt.
func ContactOf(user string) (mail, chat string, ok bool) {
	if std == nil {
		return
	}
	u, found := std.get(context.Background(), user)
	if !found || u.Disabled {
		return
	}
	return u.Mail, u.Chat, true
}

// IsAdminUser: aktiver globaler Admin (ohne Request-Kontext).
func IsAdminUser(user string) bool {
	if std == nil {
		return false
	}
	u, ok := std.get(context.Background(), user)
	return ok && !u.Disabled && u.Admin
}

// PeersOf: aktive Benutzer, die mit user mindestens eine Gruppe teilen (sortiert, inkl. user).
func PeersOf(user string) []string {
	set := map[string]bool{}
	for _, g := range GroupsOf(user) {
		for _, m := range MembersOf(g) {
			set[m.Name] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
