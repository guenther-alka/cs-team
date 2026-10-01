package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// modelAI: Attrappe mit /models und /chat/completions; merkt sich den Authorization-Header je Pfad.
type modelAI struct {
	mu     sync.Mutex
	auth   map[string]string
	status int
	reply  string
}

func (m *modelAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	if m.auth == nil {
		m.auth = map[string]string{}
	}
	m.auth[r.URL.Path] = r.Header.Get("Authorization")
	st := m.status
	m.mu.Unlock()
	if st != 0 {
		w.WriteHeader(st)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/models") {
		w.Write([]byte(`{"data":[{"id":"zeta-1"},{"id":"gpt-4o"},{"id":"or-vis","architecture":{"input_modalities":["text","image"]}}]}`))
		return
	}
	w.Write([]byte(`{"choices":[{"message":{"content":"` + m.reply + `"}}]}`))
}
func (m *modelAI) got(p string) string { m.mu.Lock(); defer m.mu.Unlock(); return m.auth[p] }

func TestAIPresets(t *testing.T) {
	srv, st := setup(t)
	defer srv.Close()
	c, b := req(t, srv, "anna", "GET", "/api/ai/providers", "")
	if c != 200 || !strings.Contains(b, `"Anthropic"`) || !strings.Contains(b, `"Z.AI / Zhipu (China)"`) || strings.Count(b, `"protocol"`) != 15 {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "bob", "GET", "/api/ai/providers", ""); c != 403 {
		t.Fatal("nur Admin:", c)
	}
	// eigene Liste im Datenspeicher ersetzt die eingebaute; ungültige Zeilen werden übersprungen
	st.Put(t.Context(), "providers.txt", []byte("# eigene Liste\nSchule-KI\topenai\thttp://10.0.0.5:8080/v1/chat/completions\nkaputt\tabc\nX\tfoo\thttp://a/b\nCloud\tanthropic\thttps://api.anthropic.com/v1/messages\n"), "")
	_, b = req(t, srv, "anna", "GET", "/api/ai/providers", "")
	if strings.Count(b, `"protocol"`) != 2 || !strings.Contains(b, "Schule-KI") {
		t.Fatal(b)
	}
}

func TestAIModelsAndKeyMemory(t *testing.T) {
	m1, m2 := &modelAI{reply: "von-eins"}, &modelAI{reply: "von-zwei"}
	s1, s2 := httptest.NewServer(m1), httptest.NewServer(m2)
	defer s1.Close()
	defer s2.Close()
	srv, _ := setup(t)
	defer srv.Close()
	set := func(body string) {
		t.Helper()
		if c, b := req(t, srv, "anna", "POST", "/api/ai/settings", body); c != 200 {
			t.Fatal(c, b)
		}
	}
	set(`{"mode":"provider","provider":"openai","endpoint":"` + s1.URL + `/v1/chat/completions","model":"m","key":"KEY-EINS","private":true}`)

	// Modell-Liste: Schlüssel gemerkt (nicht eingegeben), Bilderkennung erkannt, nur Admin
	c, b := req(t, srv, "anna", "POST", "/api/ai/models", `{"provider":"openai","endpoint":"`+s1.URL+`/v1/chat/completions","private":true}`)
	if c != 200 || !strings.Contains(b, `"zeta-1"`) || !strings.Contains(b, `"id":"gpt-4o","vision":true`) || !strings.Contains(b, `"id":"or-vis","vision":true`) || !strings.Contains(b, `"id":"zeta-1","vision":false`) {
		t.Fatal(c, b)
	}
	if got := m1.got("/v1/models"); got != "Bearer KEY-EINS" {
		t.Fatal("gemerkter Schlüssel nicht benutzt:", got)
	}
	if strings.Contains(b, "KEY-EINS") {
		t.Fatal("Schlüssel im Ergebnis")
	}
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/models", `{"provider":"openai","endpoint":"`+s1.URL+`","private":true}`); c != 403 {
		t.Fatal("nur Admin:", c)
	}
	// lokale Adresse ohne Erlaubnis: abgelehnt, ohne Netzwerkdetails
	if c, b := req(t, srv, "anna", "POST", "/api/ai/models", `{"provider":"openai","endpoint":"`+s1.URL+`/v1/chat/completions","private":false}`); c != 502 || !strings.Contains(b, "address not allowed") || strings.Contains(b, "127.0.0.1") {
		t.Fatal(c, b)
	}
	if c, _ := req(t, srv, "anna", "POST", "/api/ai/models", `{"provider":"x","endpoint":"","private":true}`); c != 400 {
		t.Fatal("ungültiger Anbieter:", c)
	}

	// Schlüssel je Zielrechner: Wechsel zu Server 2 (anderer Port = anderer Host) mit neuem Schlüssel, zurück ohne Eingabe
	set(`{"mode":"provider","provider":"openai","endpoint":"` + s2.URL + `/v1/chat/completions","model":"m","key":"KEY-ZWEI","private":true}`)
	req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("hallo", ""))
	if got := m2.got("/v1/chat/completions"); got != "Bearer KEY-ZWEI" {
		t.Fatal("Server 2:", got)
	}
	set(`{"mode":"provider","provider":"openai","endpoint":"` + s1.URL + `/v1/chat/completions","model":"m","private":true}`)
	_, b = req(t, srv, "anna", "GET", "/api/ai/settings", "")
	if !strings.Contains(b, `"keySet":true`) || strings.Contains(b, "KEY-") {
		t.Fatal("Schlüssel von Server 1 muss wieder da sein, aber nie ausgegeben werden:", b)
	}
	c, b = req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("hallo", ""))
	if c != 200 || !strings.Contains(b, "von-eins") || m1.got("/v1/chat/completions") != "Bearer KEY-EINS" {
		t.Fatal(c, b, m1.got("/v1/chat/completions"))
	}
	// Schlüssel ausdrücklich löschen: weg und bleibt weg
	set(`{"mode":"provider","provider":"openai","endpoint":"` + s1.URL + `/v1/chat/completions","model":"m","key":"","private":true}`)
	if _, b = req(t, srv, "anna", "GET", "/api/ai/settings", ""); !strings.Contains(b, `"keySet":false`) {
		t.Fatal(b)
	}
	set(`{"mode":"provider","provider":"openai","endpoint":"` + s2.URL + `/v1/chat/completions","model":"m","private":true}`)
	set(`{"mode":"provider","provider":"openai","endpoint":"` + s1.URL + `/v1/chat/completions","model":"m","private":true}`)
	if _, b = req(t, srv, "anna", "GET", "/api/ai/settings", ""); !strings.Contains(b, `"keySet":false`) {
		t.Fatal("gelöschter Schlüssel kam zurück:", b)
	}
}

func TestAIFallbackAndLimits(t *testing.T) {
	m1, m2 := &modelAI{status: 500}, &modelAI{reply: "ausweich-antwort"}
	s1, s2 := httptest.NewServer(m1), httptest.NewServer(m2)
	defer s1.Close()
	defer s2.Close()
	srv, _ := setup(t)
	defer srv.Close()
	body := `{"mode":"provider","provider":"openai","endpoint":"` + s1.URL + `/v1/chat/completions","model":"m","key":"K1","private":true,
	  "altProvider":"openai","altEndpoint":"` + s2.URL + `/v1/chat/completions","altModel":"m2","altKey":"K2","history":4,"context":1000}`
	if c, b := req(t, srv, "anna", "POST", "/api/ai/settings", body); c != 200 {
		t.Fatal(c, b)
	}
	_, b := req(t, srv, "anna", "GET", "/api/ai/settings", "")
	if !strings.Contains(b, `"altModel":"m2"`) || !strings.Contains(b, `"altKeySet":true`) || !strings.Contains(b, `"history":4`) || !strings.Contains(b, `"context":1000`) || strings.Contains(b, "K2") {
		t.Fatal(b)
	}
	// erster Anbieter fällt aus: zweiter antwortet, mit seinem eigenen Schlüssel
	c, b := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("hallo", ""))
	if c != 200 || !strings.Contains(b, "ausweich-antwort") || m2.got("/v1/chat/completions") != "Bearer K2" || m1.got("/v1/chat/completions") != "Bearer K1" {
		t.Fatal(c, b)
	}
	// beide fallen aus: Fehler
	m2.mu.Lock()
	m2.status = 500
	m2.mu.Unlock()
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody("hallo", "")); c != 502 {
		t.Fatal("beide aus:", c)
	}
	// Grenzen: Nachricht länger als "context"; Widget-Konfiguration nennt den Verlauf
	if c, _ := req(t, srv, "bob", "POST", "/api/ai/chat", chatBody(strings.Repeat("x", 1001), "")); c != 400 {
		t.Fatal("context:", c)
	}
	if _, b = req(t, srv, "bob", "GET", "/api/ai/config", ""); !strings.Contains(b, `"history":4`) {
		t.Fatal(b)
	}
	// ohne Modell kein zweiter Anbieter
	req(t, srv, "anna", "POST", "/api/ai/settings", `{"mode":"provider","provider":"openai","endpoint":"`+s1.URL+`/v1/chat/completions","model":"m","private":true,"altProvider":"openai","altEndpoint":"`+s2.URL+`","altModel":""}`)
	if _, b = req(t, srv, "anna", "GET", "/api/ai/settings", ""); !strings.Contains(b, `"altModel":""`) {
		t.Fatal(b)
	}
}
