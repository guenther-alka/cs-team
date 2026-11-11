package cal

import (
	"bytes"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Kalenderfarbe für CalDAV-Clients (0.69.2): go-webdav kennt die Apple-Erweiterung calendar-color nicht und meldet sie
// als 404. davColor fängt PROPFIND-Antworten ab und setzt für jeden Kalender die in cs-team gespeicherte Farbe ein
// (sonst die automatische Farbe der Web-UI), als "#RRGGBBFF" wie Apple, DAVx5 und Thunderbird sie erwarten.

// palette und autoColor wie in web/app.js (PALETTE, S.cal.autoColor), damit Handy und Web-UI dieselbe Farbe zeigen.
var palette = []string{"#3b82f6", "#16a34a", "#f97316", "#a855f7", "#e11d48", "#0891b2", "#ca8a04", "#64748b", "#db2777", "#4d7c0f"}

func autoColor(cid string) string {
	var h uint32
	for _, u := range utf16.Encode([]rune(cid)) {
		h = h*31 + uint32(u)
	}
	return palette[h%uint32(len(palette))]
}

const (
	ccEmpty = `<calendar-color xmlns="http://apple.com/ns/ical/"></calendar-color>`
	ps404E  = `<propstat xmlns="DAV:"><prop xmlns="DAV:"></prop><status>HTTP/1.1 404 Not Found</status></propstat>`
)

type bufRW struct {
	h    http.Header
	code int
	buf  bytes.Buffer
}

func (w *bufRW) Header() http.Header         { return w.h }
func (w *bufRW) WriteHeader(c int)           { w.code = c }
func (w *bufRW) Write(p []byte) (int, error) { return w.buf.Write(p) }

// davColor: PROPFIND puffern und calendar-color einsetzen; alle anderen Anfragen unverändert.
func (b *Backend) davColor(h http.Handler, w http.ResponseWriter, r *http.Request) {
	if r.Method != "PROPFIND" {
		h.ServeHTTP(w, r)
		return
	}
	rw := &bufRW{h: http.Header{}, code: http.StatusOK}
	h.ServeHTTP(rw, r)
	out := rw.buf.Bytes()
	if rw.code == http.StatusMultiStatus && bytes.Contains(out, []byte(ccEmpty)) {
		out = []byte(b.fillColors(r, string(out)))
	}
	for k, v := range rw.h {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(rw.code)
	w.Write(out)
}

// fillColors: in jeder <response> eines Kalenders (href /dav/<user>/cal/<cid>/) die leere 404-Farbe durch die echte ersetzen.
func (b *Backend) fillColors(r *http.Request, s string) string {
	const open, cls = `<response xmlns="DAV:">`, `</response>`
	var sb strings.Builder
	for {
		i := strings.Index(s, open)
		if i < 0 {
			sb.WriteString(s)
			return sb.String()
		}
		j := strings.Index(s[i:], cls)
		if j < 0 {
			sb.WriteString(s)
			return sb.String()
		}
		j += i + len(cls)
		sb.WriteString(s[:i])
		sb.WriteString(b.colorResp(r, s[i:j]))
		s = s[j:]
	}
}

func (b *Backend) colorResp(r *http.Request, resp string) string {
	if !strings.Contains(resp, ccEmpty) {
		return resp
	}
	hs, he := strings.Index(resp, "<href>"), strings.Index(resp, "</href>")
	if hs < 0 || he < hs {
		return resp
	}
	href, err := url.PathUnescape(resp[hs+6 : he])
	if err != nil || !strings.HasSuffix(href, "/") {
		return resp
	}
	cid, obj, err := b.parse(r.Context(), href)
	if err != nil || cid == "" || obj != "" { // nur Kalender-Sammlungen, keine Objekte und nicht das Kalender-Home
		return resp
	}
	c, err := b.open(r.Context(), cid)
	if err != nil {
		return resp
	}
	col := c.m.Color
	if col == "" {
		col = autoColor(cid)
	}
	resp = strings.Replace(resp, ccEmpty, "", 1)
	resp = strings.Replace(resp, ps404E, "", 1) // 404-Block war nur wegen der Farbe da
	ok := `<propstat xmlns="DAV:"><prop xmlns="DAV:"><calendar-color xmlns="http://apple.com/ns/ical/">` +
		strings.ToUpper(col) + `FF</calendar-color></prop><status>HTTP/1.1 200 OK</status></propstat>`
	he = strings.Index(resp, "</href>") + len("</href>")
	return resp[:he] + ok + resp[he:]
}
