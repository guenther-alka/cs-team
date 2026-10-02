package files

// PROPFIND-Ergänzung für DAV-Klasse 2: die WebDAV-Bibliothek kennt keine Sperren, Windows-Mini-Redirector, Office und Finder
// fragen aber "supportedlock" und "lockdiscovery" ab (bei 404 behandeln sie den Server als nicht sperrfähig). Die Antwort der
// Bibliothek hat ein festes Format; hier werden die beiden Eigenschaften je Eintrag ergänzt (allprop) bzw. von 404 auf 200 gesetzt.

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"cs-team/auth"
)

var (
	reResp   = regexp.MustCompile(`(?s)<response xmlns="DAV:">.*?</response>`)
	reHref   = regexp.MustCompile(`<href xmlns="DAV:">(.*?)</href>|<href>(.*?)</href>`)
	reNF     = regexp.MustCompile(`(?s)<propstat xmlns="DAV:"><prop xmlns="DAV:">(.*?)</prop><status>HTTP/1\.1 404 Not Found</status></propstat>`)
	reSL     = regexp.MustCompile(`<supportedlock xmlns="DAV:"></supportedlock>|<supportedlock xmlns="DAV:"/>`)
	reLD     = regexp.MustCompile(`<lockdiscovery xmlns="DAV:"></lockdiscovery>|<lockdiscovery xmlns="DAV:"/>`)
	reOK     = regexp.MustCompile(`(?s)(<propstat xmlns="DAV:"><prop xmlns="DAV:">.*?)(</prop><status>HTTP/1\.1 200 OK</status></propstat>)`)
	supFile  = `<D:supportedlock xmlns:D="DAV:"><D:lockentry><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype></D:lockentry></D:supportedlock>`
	supEmpty = `<D:supportedlock xmlns:D="DAV:"></D:supportedlock>`
)

// lockProps: supportedlock + lockdiscovery für einen Eintrag (href = URL-Pfad).
func (s *Svc) lockProps(me, href string, dir bool) (sup, disc string) {
	if dir {
		return supEmpty, `<D:lockdiscovery xmlns:D="DAV:"></D:lockdiscovery>`
	}
	sup = supFile
	disc = `<D:lockdiscovery xmlns:D="DAV:"></D:lockdiscovery>`
	p, err := url.PathUnescape(href)
	if err != nil {
		return
	}
	l := resolve(me, parts(p))
	if l.sub == "" || (l.kind != "own" && l.kind != "shared" && l.kind != "groups") {
		return
	}
	s.locks.mu.Lock()
	k := s.locks.get(lockKey(l.owner, l.sub))
	var cp dlock
	if k != nil {
		cp = *k
	}
	s.locks.mu.Unlock()
	if k != nil {
		d := time.Until(cp.exp)
		if d < time.Second {
			d = time.Second
		}
		disc = `<D:lockdiscovery xmlns:D="DAV:"><D:activelock><D:locktype><D:write/></D:locktype><D:lockscope><D:exclusive/></D:lockscope><D:depth>0</D:depth>` +
			`<D:owner><D:href>` + xmlEsc(cp.user) + `</D:href></D:owner><D:timeout>Second-` + itoa(int(d.Seconds())) + `</D:timeout>` +
			`<D:locktoken><D:href>` + cp.token + `</D:href></D:locktoken><D:lockroot><D:href>` + xmlEsc(cp.root) + `</D:href></D:lockroot></D:activelock></D:lockdiscovery>`
	}
	return
}

// addLockProps schreibt die 207-Antwort um.
func (s *Svc) addLockProps(me string, body []byte, allprop bool) []byte {
	return reResp.ReplaceAllFunc(body, func(resp []byte) []byte {
		hm := reHref.FindSubmatch(resp)
		if hm == nil {
			return resp
		}
		href := string(hm[1])
		if href == "" {
			href = string(hm[2])
		}
		dir := bytes.Contains(resp, []byte("<collection"))
		sup, disc := s.lockProps(me, href, dir)
		out := resp
		// ausdrücklich angefragt, aber von der Bibliothek mit 404 beantwortet
		var add string
		out = reNF.ReplaceAllFunc(out, func(ps []byte) []byte {
			m := reNF.FindSubmatch(ps)
			inner := string(m[1])
			ni := inner
			if reSL.MatchString(ni) {
				ni = reSL.ReplaceAllString(ni, "")
				add += sup
			}
			if reLD.MatchString(ni) {
				ni = reLD.ReplaceAllString(ni, "")
				add += disc
			}
			if ni == inner {
				return ps
			}
			if ni == "" {
				return nil
			}
			return []byte(`<propstat xmlns="DAV:"><prop xmlns="DAV:">` + ni + `</prop><status>HTTP/1.1 404 Not Found</status></propstat>`)
		})
		if add != "" {
			ok := `<propstat xmlns="DAV:"><prop xmlns="DAV:">` + add + `</prop><status>HTTP/1.1 200 OK</status></propstat>`
			i := bytes.LastIndex(out, []byte("</response>"))
			out = append(append(append([]byte{}, out[:i]...), ok...), out[i:]...)
		} else if allprop {
			out = reOK.ReplaceAll(out, []byte("${1}"+strings.ReplaceAll(sup+disc, "$", "$$")+"${2}"))
		}
		return out
	})
}

// propfind: führt die Bibliothek aus und ergänzt die Sperr-Eigenschaften.
func (s *Svc) propfind(h http.Handler, w http.ResponseWriter, r *http.Request) {
	rb, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(bytes.NewReader(rb))
	allprop := len(bytes.TrimSpace(rb)) == 0 || (bytes.Contains(rb, []byte("allprop")) && !bytes.Contains(rb, []byte("propname")))
	rec := &capture{h: http.Header{}}
	h.ServeHTTP(rec, r)
	for k, v := range rec.h {
		if k != "Content-Length" {
			w.Header()[k] = v
		}
	}
	body := rec.b.Bytes()
	if rec.code == http.StatusMultiStatus && len(body) < 8<<20 {
		body = s.addLockProps(auth.User(r.Context()), body, allprop)
	}
	if rec.code == 0 {
		rec.code = http.StatusOK
	}
	w.WriteHeader(rec.code)
	w.Write(body)
}

type capture struct {
	h    http.Header
	code int
	b    bytes.Buffer
}

func (c *capture) Header() http.Header         { return c.h }
func (c *capture) WriteHeader(code int)        { c.code = code }
func (c *capture) Write(p []byte) (int, error) { return c.b.Write(p) }
