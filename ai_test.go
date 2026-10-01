package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeAI: Provider-Attrappe; merkt sich die letzte Anfrage (Header + Body) und antwortet im OpenAI- oder Anthropic-Format.
type fakeAI struct {
	mu     sync.Mutex
	body   string
	hdr    http.Header
	status int
	anth   bool
}

func (f *fakeAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.body, f.hdr = string(b), r.Header.Clone()
	st := f.status
	f.mu.Unlock()
	if st != 0 {
		w.WriteHeader(st)
		w.Write([]byte(`{"error":{"message":"bad key"}}`))
		return
	}
	if f.anth {
		w.Write([]byte(`{"content":[{"type":"text","text":"antwort-anthropic"}]}`))
		return
	}
	w.Write([]byte(`{"choices":[{"message":{"content":"antwort-openai"}}]}`))
}

func (f *fakeAI) last() (string, http.Header) { f.mu.Lock(); defer f.mu.Unlock(); return f.body, f.hdr }

const png1px = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func aiSetup(t *testing.T, f *fakeAI, provider string, extra string) (*httptest.Server, string) {
	srv, _ := setup(t)
	fs := httptest.NewServer(f)
	t.Cleanup(fs.Close)
	cfg := `{"mode":"provider","provider":"` + provider + `","endpoint":"` + fs.URL + `/v1","model":"m1","key":"SECRET-KEY","private":true` + extra + `}`
	if c, b := req(t, srv, "anna", "POST", "/api/ai/settings", cfg); c != 200 {
		t.Fatal(c, b)
	}
	return srv, fs.URL
}

func chatBody(text, topic string, imgs ...string) string {
	m := map[string]any{"role": "user", "text": text}
	if len(imgs) > 0 {
		m["images"] = imgs
	}
	b, _ := json.Marshal(map[string]any{"messages": []any{m}, "topic": topic, "lang": "en"})
	return string(b)
}

func TestAISettingsAndPlainChat(t *testing.T) {
	f := &fakeAI{}
	srv, _ := setup(t)
	defer srv.Close()
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("hi", "")); c != 503 {
		t.Fatal("KI aus muss 503 liefern:", c)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/ai/settings", ""); c != 403 {
		t.Fatal("Einstellungen nur für Admins:", c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/settings", `{"mode":"provider"}`); c != 403 {
		t.Fatal("Einstellungen schreiben nur Admin:", c)
	}
	srv.Close()
	srv, base := aiSetup(t, f, "openai", "")
	defer srv.Close()
	_ = base
	_, b := req(t, srv, "anna", "GET", "/api/ai/settings", "")
	if strings.Contains(b, "SECRET-KEY") || !strings.Contains(b, `"keySet":true`) {
		t.Fatal("Schlüssel darf nie zurückgegeben werden:", b)
	}
	_, b = req(t, srv, "bob", "GET", "/api/ai/config", "")
	if strings.Contains(b, "SECRET") || strings.Contains(b, "127.0.0.1") || !strings.Contains(b, `"enabled":true`) {
		t.Fatal(b)
	}
	c, b := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("Hallo?", ""))
	if c != 200 || !strings.Contains(b, "antwort-openai") {
		t.Fatal(c, b)
	}
	body, hdr := f.last()
	if hdr.Get("Authorization") != "Bearer SECRET-KEY" || !strings.Contains(body, "Hallo?") || !strings.Contains(body, "English") {
		t.Fatalf("Anfrage an Provider: %v %s", hdr, body)
	}
	if strings.Contains(body, "DATA (") {
		t.Fatal("ohne Knopf keine Daten")
	}
	// Schlüssel wird verworfen, wenn das Ziel wechselt und kein neuer Schlüssel kommt
	req(t, srv, "anna", "POST", "/api/ai/settings", `{"mode":"provider","provider":"openai","endpoint":"http://example.invalid/v1","model":"m1","private":true}`)
	_, b = req(t, srv, "anna", "GET", "/api/ai/settings", "")
	if !strings.Contains(b, `"keySet":false`) {
		t.Fatal("Schlüssel bei neuem Ziel nicht verworfen:", b)
	}
}

func TestAITopicScope(t *testing.T) {
	f := &fakeAI{}
	srv, _ := aiSetup(t, f, "openai", "")
	defer srv.Close()
	for _, n := range []string{"cara"} {
		req(t, srv, "anna", "POST", "/api/users", `{"name":"`+n+`","password":"passwort-`+n+`","groups":["alluser"]}`)
	}
	req(t, srv, "bob", "POST", "/api/tasks", `{"title":"BOBSECRET"}`)
	req(t, srv, "cara", "POST", "/api/tasks", `{"title":"CARATASK"}`)
	ask := func(user, topic string) (int, string) {
		c, b := req(t, srv, user, "POST", "/api/ai/chat", chatBody("Fasse zusammen", topic))
		body, _ := f.last()
		return c, b + "\n" + body
	}
	if c, out := ask("cara", "tasks"); c != 200 || strings.Contains(out, "BOBSECRET") || !strings.Contains(out, "CARATASK") {
		t.Fatal("normaler Benutzer sieht nur eigene Aufgaben:", c, out)
	}
	if c, out := ask("bob", "tasks"); c != 200 || !strings.Contains(out, "BOBSECRET") || strings.Contains(out, "CARATASK") {
		t.Fatal("bob sieht nur seine:", c, out)
	}
	if c, out := ask("anna", "tasks"); c != 200 || !strings.Contains(out, "BOBSECRET") || !strings.Contains(out, "CARATASK") {
		t.Fatal("globaler Admin sieht alles:", c, out)
	}
	// Gruppen: normaler Benutzer sieht nicht alle Benutzer
	req(t, srv, "anna", "POST", "/api/groups", `{"name":"geheim","areas":["files"],"admins":["anna"]}`)
	if c, out := ask("cara", "groups"); c != 200 || strings.Contains(out, "geheim") {
		t.Fatal("fremde Gruppe sichtbar:", c, out)
	}
	if c, out := ask("anna", "groups"); c != 200 || !strings.Contains(out, "geheim") {
		t.Fatal("Admin sieht Gruppen:", c, out)
	}
	// System nur für Admins
	if c, _ := ask("cara", "system"); c != 400 {
		t.Fatal("system für Nicht-Admin:", c)
	}
	if c, out := ask("anna", "system"); c != 200 || !strings.Contains(out, "version: "+version) {
		t.Fatal("system:", c, out)
	}
	if c, _ := ask("cara", "unbekannt"); c != 400 {
		t.Fatal("unbekannter Knopf:", c)
	}
	_, b := req(t, srv, "cara", "GET", "/api/ai/config", "")
	if !strings.Contains(b, `"account"`) || strings.Contains(b, `"system"`) {
		t.Fatal("Knöpfe Benutzer:", b)
	}
	_, b = req(t, srv, "anna", "GET", "/api/ai/config", "")
	if !strings.Contains(b, `"system"`) {
		t.Fatal("Knöpfe Admin:", b)
	}
}

func TestAIAnthropicImagesAndErrors(t *testing.T) {
	f := &fakeAI{anth: true}
	srv, _ := aiSetup(t, f, "anthropic", "")
	defer srv.Close()
	c, b := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("Was ist das?", "", png1px))
	if c != 200 || !strings.Contains(b, "antwort-anthropic") {
		t.Fatal(c, b)
	}
	body, hdr := f.last()
	if hdr.Get("x-api-key") != "SECRET-KEY" || hdr.Get("anthropic-version") == "" || !strings.Contains(body, `"type":"image"`) || !strings.Contains(body, `"media_type":"image/png"`) {
		t.Fatalf("Anthropic-Anfrage: %v %s", hdr, body)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("x", "", "data:image/svg+xml;base64,AAAA")); c != 400 {
		t.Fatal("SVG darf nicht durch:", c)
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("x", "", "data:image/png;base64,@@@")); c != 400 {
		t.Fatal("kaputtes Bild:", c)
	}
	// Providerfehler: Status ja, URL und Schlüssel nie
	f.mu.Lock()
	f.status = 401
	f.mu.Unlock()
	c, b = req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("x", ""))
	if c != 502 || !strings.Contains(b, "401") || strings.Contains(b, "127.0.0.1") || strings.Contains(b, "SECRET") {
		t.Fatal("Providerfehler:", c, b)
	}
}

func TestAIVisionOffAndRate(t *testing.T) {
	f := &fakeAI{}
	srv, _ := aiSetup(t, f, "openai", `,"vision":"no","rate":2`)
	defer srv.Close()
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("x", "", png1px)); c != 400 {
		t.Fatal("Bild trotz vision=no:", c)
	}
	_, b := req(t, srv, "bob", "GET", "/api/ai/config", "")
	if !strings.Contains(b, `"vision":false`) {
		t.Fatal(b)
	}
	req(t, srv, "cara", "POST", "/api/ai/chat", chatBody("x", ""))
	codes := []int{}
	for i := 0; i < 3; i++ {
		c, _ := req(t, srv, "anna", "POST", "/api/ai/chat", chatBody("x", ""))
		codes = append(codes, c)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatal("Rate-Limit:", codes)
	}
	// Konversation muss mit user beginnen/enden
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/chat", `{"messages":[{"role":"assistant","text":"x"}]}`); c != 400 {
		t.Fatal("assistant zuerst:", c)
	}
}
