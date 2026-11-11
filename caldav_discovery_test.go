package main

import (
	"net/http"
	"strings"
	"testing"
)

// TestCalDAVDiscovery (0.69.2): Erkennung wie DAVx5 (RFC 6764): /.well-known/caldav und PROPFIND auf "/" leiten auf
// /dav/ um; dort current-user-principal -> calendar-home-set.
func TestCalDAVDiscovery(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: srv.Client().Transport}
	for _, m := range []string{"PROPFIND", "GET"} {
		r, _ := http.NewRequest(m, srv.URL+"/.well-known/caldav", nil)
		resp, err := noFollow.Do(r)
		if err != nil || resp.StatusCode != 301 || resp.Header.Get("Location") != "/dav/" {
			t.Fatalf("%s well-known: %v %v", m, err, resp)
		}
	}
	r, _ := http.NewRequest("PROPFIND", srv.URL+"/", nil)
	if resp, err := noFollow.Do(r); err != nil || resp.StatusCode != 301 || resp.Header.Get("Location") != "/dav/" {
		t.Fatalf("PROPFIND /: %v %v", err, resp)
	}
	if resp, _ := rawReq(t, srv.URL, "anna", "GET", "/", ""); resp.StatusCode != 200 { // Web-UI bleibt
		t.Fatalf("GET /: %d", resp.StatusCode)
	}
	cup := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:current-user-principal/></d:prop></d:propfind>`
	if resp, b := rawReq(t, srv.URL, "anna", "PROPFIND", "/dav/", cup, "Depth", "0", "Content-Type", "text/xml"); resp.StatusCode != 207 || !strings.Contains(b, "<href>/dav/anna/</href>") {
		t.Fatalf("principal: %d %s", resp.StatusCode, b)
	}
	home := `<?xml version="1.0"?><d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><c:calendar-home-set/></d:prop></d:propfind>`
	if resp, b := rawReq(t, srv.URL, "anna", "PROPFIND", "/dav/anna/", home, "Depth", "0", "Content-Type", "text/xml"); resp.StatusCode != 207 || !strings.Contains(b, "/dav/anna/cal/") {
		t.Fatalf("home-set: %d %s", resp.StatusCode, b)
	}
}
