package chat

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/netip"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"cs-team/auth"
	"cs-team/store"
)

// SMTP: Zugang zum Mailserver (alles leer = E-Mail-Versand aus).
type SMTP struct {
	Host, Port, User, Pass, From string
	TLS                          string // starttls (Standard bei Port 587), ssl (Port 465), none
}

func (s SMTP) Enabled() bool { return s.Host != "" && s.From != "" }

func noCRLF(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

// Send versendet eine Textmail an alle Empfänger (Bcc: jeder sieht nur sich selbst als Empfänger).
func (s SMTP) Send(to []string, subject, body, replyTo string) error {
	if !s.Enabled() {
		return errors.New("smtp not configured")
	}
	port := s.Port
	mode := strings.ToLower(s.TLS)
	if port == "" {
		port = "587"
		if mode == "ssl" {
			port = "465"
		}
	}
	if mode == "" {
		switch port {
		case "465":
			mode = "ssl"
		case "25":
			mode = "none"
		default:
			mode = "starttls"
		}
	}
	addr := net.JoinHostPort(s.Host, port)
	d := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if mode == "ssl" {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err = c.Hello("cs-team"); err != nil {
		return err
	}
	if mode == "starttls" {
		if err = c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.User != "" {
		if err = c.Auth(smtp.PlainAuth("", s.User, s.Pass, s.Host)); err != nil {
			return err
		}
	}
	fromAddr := s.From
	if i := strings.LastIndex(fromAddr, "<"); i >= 0 && strings.HasSuffix(fromAddr, ">") {
		fromAddr = fromAddr[i+1 : len(fromAddr)-1]
	}
	if err = c.Mail(fromAddr); err != nil {
		return err
	}
	ok := 0
	var lastErr error
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			lastErr = err
			continue
		}
		ok++
	}
	if ok == 0 {
		return fmt.Errorf("no recipient accepted: %v", lastErr)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	rnd := make([]byte, 8)
	rand.Read(rnd)
	var h strings.Builder
	h.WriteString("From: " + noCRLF(s.From) + "\r\n")
	h.WriteString("To: undisclosed-recipients:;\r\n")
	if replyTo != "" {
		h.WriteString("Reply-To: " + noCRLF(replyTo) + "\r\n")
	}
	h.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", noCRLF(subject)) + "\r\n")
	h.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	h.WriteString("Message-ID: <" + hex.EncodeToString(rnd) + "@cs-team>\r\n")
	h.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	if _, err = io.WriteString(w, h.String()); err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(w)
	if _, err = io.WriteString(qp, strings.ReplaceAll(body, "\n", "\r\n")); err != nil {
		return err
	}
	qp.Close()
	if err = w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// ---- externe Chat-Adresse: HTTP-POST (ntfy, Slack-, Discord-, Telegram-Webhook ...) ----

// safeClient verbindet nur zu öffentlichen Adressen (Schutz vor Zugriffen auf das interne Netz), außer allowPrivate.
func safeClient(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 8 * time.Second, Control: func(network, address string, _ syscall.RawConn) error {
		if allowPrivate {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return err
		}
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("address not allowed")
		}
		return nil
	}}
	return &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{DialContext: d.DialContext, DisableKeepAlives: true},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return errors.New("too many redirects")
			}
			return nil
		}}
}

func hook(ctx context.Context, cl *http.Client, target, subject, text string) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("bad url")
	}
	msg := text
	if subject != "" {
		msg = subject + "\n\n" + text
	}
	var body, ctype = msg, "text/plain; charset=utf-8"
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.HasSuffix(host, "slack.com"):
		b, _ := json.Marshal(map[string]string{"text": msg})
		body, ctype = string(b), "application/json"
	case strings.HasSuffix(host, "discord.com") || strings.HasSuffix(host, "discordapp.com"):
		if len(msg) > 1900 {
			msg = msg[:1900]
		}
		b, _ := json.Marshal(map[string]string{"content": msg})
		body, ctype = string(b), "application/json"
	case host == "api.telegram.org":
		body, ctype = url.Values{"text": {msg}}.Encode(), "application/x-www-form-urlencoded"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", target, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("User-Agent", "cs-team")
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return nil
}

// ---- Nachricht an eine Gruppe ----

type Mailer struct {
	St           store.Store
	Chat         *Svc
	SMTP         SMTP
	Cfg          *Settings // Einstellungen der Oberfläche (Vorrang vor SMTP/AllowPrivate aus den Startparametern)
	AllowPrivate bool      // Chat-Adressen im internen Netz erlauben (CS_CHAT_ALLOW_PRIVATE=1)

	mu   sync.Mutex
	rate map[string][]time.Time
}

type logEntry struct {
	TS    int64  `json:"ts"`
	By    string `json:"by"`
	Group string `json:"group"`
	Subj  string `json:"subject"`
	Mail  int    `json:"mail"`
	Hook  int    `json:"hook"`
	Chat  bool   `json:"chat,omitempty"`
}

const logKey = "msg/log.json"

func (m *Mailer) limited(user string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rate == nil {
		m.rate = map[string][]time.Time{}
	}
	now := time.Now()
	l := m.rate[user][:0]
	for _, t := range m.rate[user] {
		if now.Sub(t) < time.Minute {
			l = append(l, t)
		}
	}
	if len(l) >= 6 {
		m.rate[user] = l
		return true
	}
	m.rate[user] = append(l, now)
	return false
}

// canSend: Mitglied der Gruppe und laut Gruppeneinstellung berechtigt (member = alle Mitglieder, admin = Gruppen-Admins).
func canSend(user, g string) bool {
	info, ok := auth.GroupInfoOf(g)
	if !ok || info.Msg == auth.ModeOff || !auth.IsMember(user, g) {
		return false
	}
	return info.Msg == auth.ModeMember || auth.IsGroupAdmin(user, g)
}

type sendIn struct {
	Group, Subject, Body string
	Mail, Hook, Chat     bool
}

type sendOut struct {
	MailSent, MailSkipped int
	MailError             string
	HookSent, HookSkipped int
	HookFailed            []string
	ChatPosted            bool
	ChatError             string
}

func (m *Mailer) send(ctx context.Context, user string, in sendIn) (*sendOut, int, error) {
	in.Subject, in.Body = strings.TrimSpace(noCRLF(in.Subject)), strings.TrimSpace(strings.ReplaceAll(in.Body, "\r\n", "\n"))
	if !canSend(user, in.Group) {
		return nil, http.StatusForbidden, errors.New("not allowed")
	}
	if in.Body == "" || utf8.RuneCountInString(in.Body) > 20000 || utf8.RuneCountInString(in.Subject) > 200 || (!in.Mail && !in.Hook && !in.Chat) {
		return nil, http.StatusBadRequest, errors.New("bad message")
	}
	if m.limited(user) {
		return nil, http.StatusTooManyRequests, ErrRate
	}
	out := &sendOut{}
	members := auth.MembersOf(in.Group)
	footer := "\n\n-- \n" + user + " (" + in.Group + ")"
	subj := in.Subject
	if subj == "" {
		subj = "Nachricht von " + user
	}
	if in.Mail {
		var to []string
		for _, mb := range members {
			if mb.Mail != "" {
				to = append(to, mb.Mail)
			} else {
				out.MailSkipped++
			}
		}
		if len(to) > 0 {
			replyTo := ""
			for _, mb := range members {
				if mb.Name == user {
					replyTo = mb.Mail
				}
			}
			var err error
			for i := 0; i < len(to) && err == nil; i += 100 {
				end := i + 100
				if end > len(to) {
					end = len(to)
				}
				if err = m.smtp().Send(to[i:end], "["+in.Group+"] "+subj, in.Body+footer, replyTo); err == nil {
					out.MailSent += end - i
				}
			}
			if err != nil {
				out.MailError = err.Error()
			}
		}
	}
	if in.Hook {
		cl := safeClient(m.allowPrivate())
		for _, mb := range members {
			if mb.Chat == "" {
				out.HookSkipped++
				continue
			}
			if err := hook(ctx, cl, mb.Chat, "["+in.Group+"] "+subj, in.Body+footer); err != nil {
				out.HookFailed = append(out.HookFailed, mb.Name+": "+err.Error())
			} else {
				out.HookSent++
			}
		}
	}
	if in.Chat && m.Chat != nil {
		text := in.Body
		if in.Subject != "" {
			text = in.Subject + "\n" + in.Body
		}
		if _, err := m.Chat.Post(ctx, user, in.Group, DefaultCh, text, nil); err != nil {
			out.ChatError = err.Error()
		} else {
			out.ChatPosted = true
		}
	}
	e := logEntry{TS: time.Now().UnixNano(), By: user, Group: in.Group, Subj: in.Subject, Mail: out.MailSent, Hook: out.HookSent, Chat: out.ChatPosted}
	store.Update(ctx, m.St, logKey, func(cur []byte) ([]byte, error) {
		var l []logEntry
		if cur != nil {
			json.Unmarshal(cur, &l)
		}
		l = append(l, e)
		if len(l) > 200 {
			l = l[len(l)-200:]
		}
		return json.Marshal(l)
	})
	return out, 200, nil
}

func (m *Mailer) Routes(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	h := func(fn http.HandlerFunc) http.Handler { return wrap(http.HandlerFunc(fn)) }
	// Gruppen, an die der Benutzer schreiben darf, mit Zahl der erreichbaren Mitglieder
	mux.Handle("GET /api/message/groups", h(func(w http.ResponseWriter, r *http.Request) {
		me := auth.User(r.Context())
		type row struct {
			Name    string `json:"name"`
			Members int    `json:"members"`
			Mail    int    `json:"mail"`
			Hook    int    `json:"hook"`
			Chat    bool   `json:"chat"` // Gruppen-Chat beschreibbar
		}
		out := []row{}
		for _, g := range auth.GroupsOf(me) {
			if !canSend(me, g) {
				continue
			}
			rw := row{Name: g}
			for _, mb := range auth.MembersOf(g) {
				rw.Members++
				if mb.Mail != "" {
					rw.Mail++
				}
				if mb.Chat != "" {
					rw.Hook++
				}
			}
			_, rw.Chat = access(me, g)
			out = append(out, rw)
		}
		json.NewEncoder(w).Encode(map[string]any{"groups": out, "smtp": m.smtp().Enabled()})
	}))
	mux.Handle("POST /api/message", h(func(w http.ResponseWriter, r *http.Request) {
		var in sendIn
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if in.Mail && !m.smtp().Enabled() {
			http.Error(w, "mail is not configured on the server", 400)
			return
		}
		out, code, err := m.send(r.Context(), auth.User(r.Context()), in)
		if err != nil {
			http.Error(w, err.Error(), code)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	// Versandprotokoll: eigene Nachrichten; Gruppen-Admins die ihrer Gruppen; globale Admins alle
	mux.Handle("GET /api/message/log", h(func(w http.ResponseWriter, r *http.Request) {
		me := auth.User(r.Context())
		b, _, _ := m.St.Get(r.Context(), logKey)
		var l []logEntry
		json.Unmarshal(b, &l)
		out := []logEntry{}
		for i := len(l) - 1; i >= 0 && len(out) < 50; i-- {
			if l[i].By == me || auth.IsAdmin(r.Context()) || auth.CanManage(r.Context(), l[i].Group) {
				out = append(out, l[i])
			}
		}
		json.NewEncoder(w).Encode(out)
	}))
}

// Notify schickt eine kurze Nachricht an einen einzelnen Benutzer (E-Mail und/oder Chat-Adresse, soweit hinterlegt und
// eingerichtet). Wird von den Aufgaben benutzt; Fehler werden ignoriert (Hinweis in der App gibt es ohnehin).
func (m *Mailer) Notify(ctx context.Context, user, subject, text string) {
	mail, chat, ok := auth.ContactOf(user)
	if !ok {
		return
	}
	subject = noCRLF(subject)
	if mail != "" && m.smtp().Enabled() {
		m.smtp().Send([]string{mail}, subject, text, "")
	}
	if chat != "" {
		hook(ctx, safeClient(m.allowPrivate()), chat, subject, text)
	}
}

func (m *Mailer) smtp() SMTP {
	if m.Cfg != nil {
		return m.Cfg.SMTP()
	}
	return m.SMTP
}

func (m *Mailer) allowPrivate() bool {
	if m.Cfg != nil {
		return m.Cfg.AllowPrivate()
	}
	return m.AllowPrivate
}
