package files

// WebDAV-Sperren (LOCK/UNLOCK, DAV-Klasse 2): Windows-Explorer (Netzlaufwerk) und Word/Excel öffnen Dateien nur dann zum Schreiben,
// wenn der Server LOCK kann. Umgesetzt wird die exklusive Schreibsperre für eine einzelne Datei (Tiefe 0), im Speicher des Prozesses:
// nach einem Neustart sperren die Clients neu. Gesperrt ist die Datei für alle anderen Benutzer, im Browser wie per WebDAV
// (Svc.Put/Remove melden ErrLocked = HTTP 423); der Sperrende selbst darf auch ohne Token schreiben (kein Aussperren nach Absturz des Clients).

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"cs-team/auth"
)

var ErrLocked = errors.New("locked by another user")

const (
	lockDefault = 10 * time.Minute
	lockMax     = time.Hour
	maxLocks    = 5000
)

type dlock struct {
	token, user, key, owner, root string
	exp                           time.Time
}

type lockTable struct {
	mu sync.Mutex
	m  map[string]*dlock // Schlüssel: "<besitzer>/<name>"
}

func lockKey(owner, name string) string { return owner + "/" + name }

func (t *lockTable) get(key string) *dlock {
	l := t.m[key]
	if l != nil && time.Now().After(l.exp) {
		delete(t.m, key)
		return nil
	}
	return l
}

// lockedByOther: ist die Datei von einem anderen Benutzer gesperrt?
func (s *Svc) lockedByOther(owner, name, actor string) bool {
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	l := s.locks.get(lockKey(owner, name))
	return l != nil && l.user != actor
}

func newLockToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	h := hex.EncodeToString(b)
	return "opaquelocktoken:" + h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

type lockInfo struct {
	XMLName xml.Name `xml:"lockinfo"`
	Scope   struct {
		Exclusive *struct{} `xml:"exclusive"`
		Shared    *struct{} `xml:"shared"`
	} `xml:"lockscope"`
	Type struct {
		Write *struct{} `xml:"write"`
	} `xml:"locktype"`
	Owner struct {
		Inner string `xml:",innerxml"`
	} `xml:"owner"`
}

func lockTimeout(h string) time.Duration {
	for _, p := range strings.Split(h, ",") {
		p = strings.TrimSpace(p)
		if v, ok := strings.CutPrefix(p, "Second-"); ok {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				return min(time.Duration(n)*time.Second, lockMax)
			}
		}
	}
	return lockDefault
}

func lockXML(l *dlock, ownerXML string, d time.Duration) string {
	if ownerXML == "" {
		ownerXML = "<D:href>" + xmlEsc(l.user) + "</D:href>"
	}
	return `<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<D:prop xmlns:D="DAV:"><D:lockdiscovery><D:activelock><D:locktype><D:write/></D:locktype><D:lockscope><D:exclusive/></D:lockscope>` +
		`<D:depth>0</D:depth><D:owner>` + ownerXML + `</D:owner><D:timeout>Second-` + strconv.Itoa(int(d.Seconds())) + `</D:timeout>` +
		`<D:locktoken><D:href>` + l.token + `</D:href></D:locktoken><D:lockroot><D:href>` + xmlEsc(l.root) + `</D:href></D:lockroot>` +
		`</D:activelock></D:lockdiscovery></D:prop>`
}

func xmlEsc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// ifTokens: Sperr-Tokens aus dem If-Header (Aktualisieren einer Sperre).
func ifTokens(h string) []string {
	var out []string
	for {
		i := strings.Index(h, "<")
		if i < 0 {
			return out
		}
		j := strings.Index(h[i:], ">")
		if j < 0 {
			return out
		}
		out = append(out, h[i+1:i+j])
		h = h[i+j+1:]
	}
}

func (s *Svc) davLock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := auth.User(ctx)
	l := resolve(me, parts(r.URL.Path))
	if (l.kind != "own" && l.kind != "shared" && l.kind != "groups") || l.sub == "" || !ValidName(l.sub) {
		http.Error(w, "cannot lock here", http.StatusForbidden)
		return
	}
	key := lockKey(l.owner, l.sub)
	d := lockTimeout(r.Header.Get("Timeout"))
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	var info lockInfo
	hasBody := len(strings.TrimSpace(string(body))) > 0
	if hasBody {
		if xml.Unmarshal(body, &info) != nil || info.Type.Write == nil {
			http.Error(w, "bad lockinfo", http.StatusBadRequest)
			return
		}
		if info.Scope.Exclusive == nil {
			http.Error(w, "only exclusive write locks", http.StatusBadRequest)
			return
		}
	}
	s.locks.mu.Lock()
	cur := s.locks.get(key)
	if !hasBody { // Aktualisieren
		var hit *dlock
		for _, tk := range ifTokens(r.Header.Get("If")) {
			if cur != nil && cur.token == tk {
				hit = cur
			}
		}
		if hit == nil || hit.user != me {
			s.locks.mu.Unlock()
			http.Error(w, "no such lock", http.StatusPreconditionFailed)
			return
		}
		hit.exp = time.Now().Add(d)
		s.locks.mu.Unlock()
		writeLock(w, http.StatusOK, hit, "", d)
		return
	}
	if cur != nil && cur.user != me {
		s.locks.mu.Unlock()
		http.Error(w, "locked", http.StatusLocked)
		return
	}
	s.locks.mu.Unlock()
	// Datei prüfen: vorhanden -> Schreibrecht nötig; sonst leere Datei anlegen (RFC 4918 "lock-null", Office legt so Dateien an)
	status := http.StatusOK
	if m, err := s.Meta(ctx, l.owner, l.sub); err == nil {
		if _, wr := m.Level(me); !wr {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	} else if errors.Is(err, ErrNotFound) {
		if _, err := s.Put(ctx, me, l.owner, l.sub, strings.NewReader(""), 0); err != nil {
			fail(w, err)
			return
		}
		status = http.StatusCreated
	} else {
		fail(w, err)
		return
	}
	s.locks.mu.Lock()
	if s.locks.m == nil {
		s.locks.m = map[string]*dlock{}
	}
	if len(s.locks.m) >= maxLocks {
		for k := range s.locks.m {
			s.locks.get(k) // abgelaufene entfernen
		}
		if len(s.locks.m) >= maxLocks {
			s.locks.mu.Unlock()
			http.Error(w, "too many locks", http.StatusServiceUnavailable)
			return
		}
	}
	if c := s.locks.get(key); c != nil && c.user != me { // inzwischen von jemand anderem gesperrt
		s.locks.mu.Unlock()
		http.Error(w, "locked", http.StatusLocked)
		return
	}
	nl := &dlock{token: newLockToken(), user: me, key: key, owner: l.owner, root: (&url.URL{Path: r.URL.Path}).EscapedPath(), exp: time.Now().Add(d)}
	s.locks.m[key] = nl
	s.locks.mu.Unlock()
	writeLock(w, status, nl, strings.TrimSpace(info.Owner.Inner), d)
}

func writeLock(w http.ResponseWriter, code int, l *dlock, ownerXML string, d time.Duration) {
	h := w.Header()
	h.Set("Content-Type", `application/xml; charset="utf-8"`)
	h.Set("Lock-Token", "<"+l.token+">")
	w.WriteHeader(code)
	fmt.Fprint(w, lockXML(l, ownerXML, d))
}

func (s *Svc) davUnlock(w http.ResponseWriter, r *http.Request) {
	me := auth.User(r.Context())
	l := resolve(me, parts(r.URL.Path))
	tok := strings.Trim(strings.TrimSpace(r.Header.Get("Lock-Token")), "<>")
	if (l.kind != "own" && l.kind != "shared" && l.kind != "groups") || l.sub == "" || tok == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	key := lockKey(l.owner, l.sub)
	cur := s.locks.get(key)
	if cur == nil || cur.token != tok || cur.user != me {
		http.Error(w, "no such lock", http.StatusConflict)
		return
	}
	delete(s.locks.m, key)
	w.WriteHeader(http.StatusNoContent)
}

// davHead: OPTIONS meldet DAV-Klasse 2 und die Methoden LOCK/UNLOCK.
type davHead struct {
	http.ResponseWriter
	opts bool
	done bool
}

func (d *davHead) WriteHeader(code int) {
	if d.opts && !d.done {
		d.done = true
		h := d.Header()
		h.Set("DAV", "1, 2, 3")
		if a := h.Get("Allow"); a != "" && !strings.Contains(a, "LOCK") {
			h.Set("Allow", a+", LOCK, UNLOCK")
		}
		h.Set("MS-Author-Via", "DAV")
	}
	d.ResponseWriter.WriteHeader(code)
}

func (d *davHead) Write(b []byte) (int, error) {
	if !d.done {
		d.WriteHeader(http.StatusOK)
	}
	return d.ResponseWriter.Write(b)
}
