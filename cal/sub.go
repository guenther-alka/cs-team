package cal

// Kalender-Abos: ein Kalender kann einen Internet-Kalender (ICS-URL) spiegeln. Nur lesen; der Server holt den Feed
// beim Anlegen, bei "Aktualisieren" und beim Öffnen, wenn er älter als subTTL ist. Jeder Termin (UID) wird als eigenes
// Objekt gespeichert, damit API und CalDAV unverändert funktionieren.

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/emersion/go-ical"

	"cs-team/auth"
	"cs-team/netguard"
)

const (
	subTTL     = 30 * time.Minute
	subMaxSize = 8 << 20
)

// checkURL: nur http(s) mit Host.
func checkURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.Replace(raw, "webcal://", "https://", 1) // webcal:// ist üblicherweise https
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 2000 {
		return "", errors.New("url: http(s)://... expected")
	}
	return raw, nil
}

// safeDial: verbindet nur zu öffentlichen Adressen (kein Loopback/privat/link-local), außer CS_ICS_PRIVATE=1.
// Die Prüfung erfolgt in Control, also nach der DNS-Auflösung (schützt gegen DNS-Tricks).
func subClient() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if os.Getenv("CS_ICS_PRIVATE") != "1" {
		d.Control = func(network, address string, c syscall.RawConn) error {
			h, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(h)
			if err != nil || netguard.Blocked(ip) {
				return errors.New("address not allowed")
			}
			return nil
		}
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: d.DialContext, Proxy: nil},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 4 {
				return errors.New("too many redirects")
			}
			return nil
		}}
}

var fetchICS = func(ctx context.Context, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cs-team")
	resp, err := subClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("feed: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, subMaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > subMaxSize {
		return nil, errors.New("feed too large")
	}
	return b, nil
}

// refresh: Feed holen und die Termine des Kalenders ersetzen. Bei Fehlern bleiben die alten Termine erhalten.
func (b *Backend) refresh(ctx context.Context, owner, kal string, m meta) error {
	raw, err := fetchICS(ctx, m.URL)
	if err != nil {
		return err
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return errors.New("feed: no valid iCalendar")
	}
	var tz []*ical.Component
	byUID := map[string][]*ical.Component{}
	var order []string
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompTimezone:
			tz = append(tz, c)
		case ical.CompEvent:
			uid, _ := c.Props.Text(ical.PropUID)
			if uid == "" {
				continue
			}
			dropFineRRule(c)
			if _, ok := byUID[uid]; !ok {
				order = append(order, uid)
			}
			byUID[uid] = append(byUID[uid], c)
		}
	}
	if len(order) > 5000 {
		order = order[:5000]
	}
	want := map[string]bool{}
	for _, uid := range order {
		sum := sha1.Sum([]byte(uid))
		file := hex.EncodeToString(sum[:10]) + ".ics"
		one := ical.NewCalendar()
		one.Props.SetText(ical.PropVersion, "2.0")
		one.Props.SetText(ical.PropProductID, "-//cs-team//sub//EN")
		one.Children = append(one.Children, tz...)
		one.Children = append(one.Children, byUID[uid]...)
		var buf bytes.Buffer
		if ical.NewEncoder(&buf).Encode(one) != nil {
			continue
		}
		if _, err := b.St.Put(ctx, key(owner, kal, file), buf.Bytes(), ""); err != nil {
			return err
		}
		want[file] = true
	}
	prefix := key(owner, kal, "")
	infos, _ := b.St.List(ctx, prefix)
	for _, i := range infos {
		f := strings.TrimPrefix(i.Key, prefix)
		if strings.HasSuffix(f, ".ics") && !want[f] {
			b.St.Delete(ctx, i.Key)
		}
	}
	m.Fetched = time.Now().Unix()
	m.Tried, m.Err = m.Fetched, ""
	mb, _ := json.Marshal(m)
	_, err = b.St.Put(ctx, key(owner, kal, "_meta.json"), mb, "")
	return err
}

// subFailed merkt Zeit und Art des Fehlers, damit die Oberfläche den Zustand zeigen kann.
func (b *Backend) subFailed(ctx context.Context, owner, kal string, m meta, err error) {
	m.Tried, m.Err = time.Now().Unix(), feedErr(err)
	mb, _ := json.Marshal(m)
	b.St.Put(ctx, key(owner, kal, "_meta.json"), mb, "")
}

// autoRefresh: beim Öffnen, wenn der Feed älter als subTTL ist (Fehler unkritisch: alte Termine bleiben).
// Nach einem Fehler wird frühestens nach zwei Minuten neu versucht.
func (b *Backend) autoRefresh(ctx context.Context, c *calRef) {
	if c.m.URL == "" {
		return
	}
	wait, last := subTTL, c.m.Fetched
	if c.m.Err != "" {
		wait, last = 2*time.Minute, c.m.Tried
	}
	if time.Since(time.Unix(last, 0)) < wait {
		return
	}
	if err := b.refresh(ctx, c.owner, c.kal, c.m); err != nil {
		b.subFailed(ctx, c.owner, c.kal, c.m, err)
	}
}

func (b *Backend) apiRefresh(w http.ResponseWriter, r *http.Request) {
	c, err := b.open(r.Context(), r.PathValue("kal"))
	if err != nil || c.m.URL == "" {
		http.Error(w, "not found", 404)
		return
	}
	if !c.manage {
		http.Error(w, "forbidden", 403)
		return
	}
	if err := b.refresh(r.Context(), c.owner, c.kal, c.m); err != nil {
		b.subFailed(r.Context(), c.owner, c.kal, c.m, err)
		http.Error(w, feedErr(err), http.StatusBadGateway)
	}
}

var _ = auth.User
