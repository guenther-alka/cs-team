package main

import (
	"net/http/httptest"
	"testing"
)

// 0.60: Freigabe-Links und 2FA-Änderungen stehen im Audit-Protokoll.
func TestAudited60(t *testing.T) {
	for _, p := range []string{"/api/filesshare/anna/a.txt", "/api/me/2fa/activate", "/api/me/2fa/disable", "/api/me/apppass", "/api/me/apppass/abc123", "/api/users/bob/2fa/reset"} {
		if !audited(httptest.NewRequest("POST", p, nil)) {
			t.Fatal("nicht im Audit:", p)
		}
	}
}
