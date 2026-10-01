package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func rawReq(t *testing.T, base, user, method, path, body string, hdr ...string) (*http.Response, string) {
	r, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	r.SetBasicAuth(user, "passwort-"+user)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

const lockBody = `<?xml version="1.0"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner><D:href>mailto:anna@x</D:href></D:owner></D:lockinfo>`

func TestWebDAVLock(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	u := srv.URL

	// OPTIONS: DAV-Klasse 2 + LOCK in Allow
	resp, _ := rawReq(t, u, "anna", "OPTIONS", "/webdav/", "")
	if resp.Header.Get("DAV") != "1, 2, 3" || !strings.Contains(resp.Header.Get("Allow"), "LOCK") {
		t.Fatalf("options: DAV=%q Allow=%q", resp.Header.Get("DAV"), resp.Header.Get("Allow"))
	}

	if c, _ := req(t, srv, "anna", "PUT", "/webdav/a.txt", "eins"); c != 201 {
		t.Fatalf("put: %d", c)
	}
	// Sperren
	resp, body := rawReq(t, u, "anna", "LOCK", "/webdav/a.txt", lockBody, "Timeout", "Second-600")
	if resp.StatusCode != 200 {
		t.Fatalf("lock: %d %s", resp.StatusCode, body)
	}
	tok := strings.Trim(resp.Header.Get("Lock-Token"), "<>")
	if !strings.HasPrefix(tok, "opaquelocktoken:") || !strings.Contains(body, tok) || !strings.Contains(body, "Second-600") || !strings.Contains(body, "mailto:anna@x") {
		t.Fatalf("lock reply: %q %s", tok, body)
	}
	// Fremde: Schreiben, Löschen, Sperren -> 423; Lesen geht
	if c, _ := req(t, srv, "anna", "POST", "/api/settings/trash", `{"days":30}`); c != 200 {
		t.Fatal("trash setting", c)
	}
	// bob hat keinen Zugriff auf annas Datei; deshalb Gruppenordner-/Freigabepfad separat testen. Eigene Datei: bob sperrt seine eigene.
	// Der Sperrende darf ohne Token schreiben
	if c, _ := req(t, srv, "anna", "PUT", "/webdav/a.txt", "zwei"); c != 204 && c != 201 {
		t.Fatalf("holder put: %d", c)
	}
	// Erneutes LOCK desselben Benutzers ersetzt die Sperre
	resp, _ = rawReq(t, u, "anna", "LOCK", "/webdav/a.txt", lockBody)
	if resp.StatusCode != 200 {
		t.Fatalf("relock: %d", resp.StatusCode)
	}
	tok = strings.Trim(resp.Header.Get("Lock-Token"), "<>")
	// Aktualisieren per If-Header ohne Body
	resp, body = rawReq(t, u, "anna", "LOCK", "/webdav/a.txt", "", "If", "(<"+tok+">)", "Timeout", "Second-1200")
	if resp.StatusCode != 200 || !strings.Contains(body, "Second-1200") {
		t.Fatalf("refresh: %d %s", resp.StatusCode, body)
	}
	if resp, _ = rawReq(t, u, "anna", "LOCK", "/webdav/a.txt", "", "If", "(<opaquelocktoken:falsch>)"); resp.StatusCode != 412 {
		t.Fatalf("refresh wrong token: %d", resp.StatusCode)
	}
	// UNLOCK: falsches Token 409, richtiges 204, danach nochmal 409
	if resp, _ = rawReq(t, u, "anna", "UNLOCK", "/webdav/a.txt", "", "Lock-Token", "<opaquelocktoken:nein>"); resp.StatusCode != 409 {
		t.Fatalf("unlock wrong: %d", resp.StatusCode)
	}
	if resp, _ = rawReq(t, u, "anna", "UNLOCK", "/webdav/a.txt", "", "Lock-Token", "<"+tok+">"); resp.StatusCode != 204 {
		t.Fatalf("unlock: %d", resp.StatusCode)
	}
	if resp, _ = rawReq(t, u, "anna", "UNLOCK", "/webdav/a.txt", "", "Lock-Token", "<"+tok+">"); resp.StatusCode != 409 {
		t.Fatalf("unlock twice: %d", resp.StatusCode)
	}

	// lock-null: nicht vorhandene Datei -> 201 und leere Datei
	resp, body = rawReq(t, u, "anna", "LOCK", "/webdav/neu.docx", lockBody)
	if resp.StatusCode != 201 {
		t.Fatalf("lock-null: %d %s", resp.StatusCode, body)
	}
	if c, b := req(t, srv, "anna", "GET", "/webdav/neu.docx", ""); c != 200 || b != "" {
		t.Fatalf("lock-null get: %d %q", c, b)
	}
	// Ordner/Wurzel/ungültige Namen lassen sich nicht sperren
	if resp, _ = rawReq(t, u, "anna", "LOCK", "/webdav/", lockBody); resp.StatusCode != 403 {
		t.Fatalf("lock root: %d", resp.StatusCode)
	}
	if resp, _ = rawReq(t, u, "anna", "LOCK", "/webdav/.trash", lockBody); resp.StatusCode == 200 || resp.StatusCode == 201 {
		t.Fatalf("lock .trash: %d", resp.StatusCode)
	}
	if resp, _ = rawReq(t, u, "anna", "LOCK", "/webdav/x", `<lockinfo xmlns="DAV:"><lockscope><shared/></lockscope><locktype><write/></locktype></lockinfo>`); resp.StatusCode != 400 {
		t.Fatalf("shared lock: %d", resp.StatusCode)
	}
	if resp, _ = rawReq(t, u, "anna", "LOCK", "/webdav/x", "kein xml"); resp.StatusCode != 400 {
		t.Fatalf("bad xml: %d", resp.StatusCode)
	}
}

func TestWebDAVLockForeign(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	u := srv.URL
	// Gruppenordner, in dem anna und bob schreiben dürfen
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"team","areas":["files"],"folder":"rw"}`)
	req(t, srv, "anna", "POST", "/api/users/import?update=1", "bob;;team\nanna;;team\n")
	if c, b := req(t, srv, "anna", "PUT", "/webdav/groups/team/doc.txt", "start"); c != 201 {
		t.Fatal("group dav path:", c, b)
	}
	resp, _ := rawReq(t, u, "anna", "LOCK", "/webdav/groups/team/doc.txt", lockBody)
	if resp.StatusCode != 200 {
		t.Fatalf("lock: %d", resp.StatusCode)
	}
	tok := strings.Trim(resp.Header.Get("Lock-Token"), "<>")
	if resp, _ = rawReq(t, u, "bob", "LOCK", "/webdav/groups/team/doc.txt", lockBody); resp.StatusCode != 423 {
		t.Fatalf("bob lock: %d", resp.StatusCode)
	}
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/groups/team/doc.txt", "bob"); c != 423 {
		t.Fatalf("bob put: %d", c)
	}
	if c, _ := req(t, srv, "bob", "DELETE", "/webdav/groups/team/doc.txt", ""); c != 423 {
		t.Fatalf("bob delete: %d", c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/files?name=doc.txt&owner=@team", "bob"); c != 423 {
		t.Fatalf("bob browser upload: %d", c)
	}
	if c, _ := req(t, srv, "bob", "DELETE", "/api/files/@team/doc.txt", ""); c != 423 {
		t.Fatalf("bob browser delete: %d", c)
	}
	if resp, _ = rawReq(t, u, "bob", "UNLOCK", "/webdav/groups/team/doc.txt", "", "Lock-Token", "<"+tok+">"); resp.StatusCode != 409 {
		t.Fatalf("bob unlock: %d", resp.StatusCode)
	}
	if c, b := req(t, srv, "bob", "GET", "/webdav/groups/team/doc.txt", ""); c != 200 || b != "start" {
		t.Fatalf("bob read: %d %q", c, b)
	}
	if resp, _ = rawReq(t, u, "anna", "UNLOCK", "/webdav/groups/team/doc.txt", "", "Lock-Token", "<"+tok+">"); resp.StatusCode != 204 {
		t.Fatalf("unlock: %d", resp.StatusCode)
	}
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/groups/team/doc.txt", "bob"); c != 204 && c != 201 {
		t.Fatalf("bob put after unlock: %d", c)
	}
}

func TestWebDAVLockExpiry(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	u := srv.URL
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"team","areas":["files"],"folder":"rw"}`)
	req(t, srv, "anna", "POST", "/api/users/import?update=1", "bob;;team\nanna;;team\n")
	req(t, srv, "anna", "PUT", "/webdav/groups/team/e.txt", "x")
	if resp, _ := rawReq(t, u, "anna", "LOCK", "/webdav/groups/team/e.txt", lockBody, "Timeout", "Infinite, Second-1"); resp.StatusCode != 200 {
		t.Fatal("lock", resp.StatusCode)
	}
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/groups/team/e.txt", "y"); c != 423 {
		t.Fatal("während Sperre:", c)
	}
	time.Sleep(1200 * time.Millisecond)
	if c, _ := req(t, srv, "bob", "PUT", "/webdav/groups/team/e.txt", "y"); c != 204 && c != 201 {
		t.Fatal("nach Ablauf:", c)
	}
}
