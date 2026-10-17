package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func cfgFor(srv *httptest.Server, provider string) Config {
	return Config{Provider: provider, Endpoint: srv.URL, Model: "test-model", Key: "k", MaxTokens: 100, Private: true}
}

var oneMsg = []msg{{Role: "user", Text: "Frage"}}

func init() { teamEnabled = false } // die Tests dieser Datei pruefen Stufe 1 und den Rueckfall; die eigene Quellensuche testet search_test.go

func TestAnthropicWebSources(t *testing.T) {
	var gotBody, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotKey = string(b), r.Header.Get("x-api-key")
		io.WriteString(w, `{"stop_reason":"end_turn","content":[
		 {"type":"server_tool_use","name":"web_search","input":{"query":"x"}},
		 {"type":"web_search_tool_result","content":[
		   {"type":"web_search_result","url":"https://example.org/a","title":"A","page_age":"April 2025"},
		   {"type":"web_search_result","url":"javascript:alert(1)","title":"boese"},
		   {"type":"web_search_result","url":"https://example.org/b","title":"B"}]},
		 {"type":"text","text":"Antwort ","citations":[{"type":"web_search_result_location","url":"https://example.org/b","title":"B"}]},
		 {"type":"text","text":"Ende"}]}`)
	}))
	defer srv.Close()
	out, w, err := anthropicWeb(context.Background(), cfgFor(srv, "anthropic"), "system", oneMsg)
	if err != nil || out != "Antwort Ende" {
		t.Fatal(out, err)
	}
	if !w.Used || w.Searches != 1 || len(w.Sources) != 2 || w.Sources[0].URL != "https://example.org/b" || !w.Sources[0].Cited ||
		w.Sources[1].URL != "https://example.org/a" || w.Sources[1].Age != "April 2025" || w.Sources[1].Cited {
		t.Fatalf("%+v", w)
	}
	var req struct {
		MaxTokens int `json:"max_tokens"`
		Tools     []struct {
			Type    string `json:"type"`
			Name    string `json:"name"`
			MaxUses int    `json:"max_uses"`
		} `json:"tools"`
	}
	if json.Unmarshal([]byte(gotBody), &req) != nil || len(req.Tools) != 1 || req.Tools[0].Type != "web_search_20250305" || req.Tools[0].Name != "web_search" ||
		req.Tools[0].MaxUses != webMaxUses || req.MaxTokens < webMinTokens || gotKey != "k" {
		t.Fatalf("Anfrage: %s", gotBody)
	}
}

func TestAnthropicWebPauseTurn(t *testing.T) {
	var n int32
	var second string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if atomic.AddInt32(&n, 1) == 1 {
			io.WriteString(w, `{"stop_reason":"pause_turn","content":[{"type":"server_tool_use","name":"web_search","input":{}},{"type":"text","text":"Teil1 "}]}`)
			return
		}
		second = string(b)
		io.WriteString(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"Teil2"}]}`)
	}))
	defer srv.Close()
	out, w, err := anthropicWeb(context.Background(), cfgFor(srv, "anthropic"), "s", oneMsg)
	if err != nil || out != "Teil1 Teil2" || n != 2 || !w.Used {
		t.Fatal(out, err, n)
	}
	if !strings.Contains(second, `"role":"assistant"`) || !strings.Contains(second, "server_tool_use") {
		t.Fatalf("Fortsetzung ohne die bisherige Antwort: %s", second)
	}
}

func TestResearchFallbacks(t *testing.T) {
	// Der Anbieter lehnt die Suche ab (Werkzeug nicht freigeschaltet): Antwort aus dem Wissen, ausdruecklich gekennzeichnet
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "web_search") {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"web search is not enabled"}}`)
			return
		}
		io.WriteString(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"Wissen"}]}`)
	}))
	defer srv.Close()
	s := &Svc{}
	out, w, err := s.research(context.Background(), cfgFor(srv, "anthropic"), "s", oneMsg, "de", "de")
	if err != nil || out != "Wissen" || w == nil || w.Used || !strings.Contains(w.Reason, "failed") || w.Provider != "anthropic" {
		t.Fatal(out, w, err)
	}
	// Anbieter ohne Websuche (Ollama / OpenAI-kompatibel): sofort Wissen der KI
	var body2 string
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body2 = string(b)
		io.WriteString(w, `{"choices":[{"message":{"content":"lokal"}}]}`)
	}))
	defer srv2.Close()
	out, w, err = s.research(context.Background(), cfgFor(srv2, "ollama"), "s", oneMsg, "de", "de")
	if err != nil || out != "lokal" || w == nil || w.Used || w.Reason != "provider has no web search" {
		t.Fatal(out, w, err)
	}
	var rq struct {
		MaxTokens int `json:"max_tokens"`
	}
	if json.Unmarshal([]byte(body2), &rq) != nil || rq.MaxTokens < webMinTokens { // cfgFor setzt 100: die Recherche braucht mehr Platz
		t.Fatalf("max_tokens im Rueckfall: %s", body2)
	}
	// ein Anbieter, der gar nicht antwortet: Fehler wie bisher
	if _, _, err := s.research(context.Background(), Config{Provider: "ollama", Endpoint: "http://127.0.0.1:1/v1/chat/completions", Model: "m", Private: true}, "s", oneMsg, "de", "de"); err == nil {
		t.Fatal("Fehler erwartet")
	}
	// die KI hat nicht gesucht: Used=false mit Grund
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"ohne Suche"}]}`)
	}))
	defer srv3.Close()
	out, w, err = anthropicWeb(context.Background(), cfgFor(srv3, "anthropic"), "s", oneMsg)
	if err != nil || out != "ohne Suche" || w.Used || w.Reason != "the model did not search" || len(w.Sources) != 0 {
		t.Fatal(out, w, err)
	}
}

func TestOpenAIWebAnnotations(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, `{"choices":[{"message":{"content":"Text","annotations":[{"type":"url_citation","url_citation":{"url":"https://example.org/x","title":"X"}},{"type":"other"}]}}]}`)
	}))
	defer srv.Close()
	out, w, err := openaiWeb(context.Background(), cfgFor(srv, "openai"), "s", oneMsg)
	if err != nil || out != "Text" || !w.Used || len(w.Sources) != 1 || w.Sources[0].URL != "https://example.org/x" || !strings.Contains(body, "web_search_options") {
		t.Fatal(out, w, err, body)
	}
	// Websuche nur bei Anthropic und bei OpenAI mit Such-Modell an api.openai.com (Standard-Adresse); sonst nicht
	for c, want := range map[Config]bool{
		{Provider: "openai", Model: "gpt-4o-search-preview"}:                                         true,
		{Provider: "openai", Model: "gpt-4o"}:                                                       false,
		{Provider: "openai", Model: "gpt-4o-search-preview", Endpoint: "https://llm.example.org/v1"}: false,
		{Provider: "ollama", Model: "search"}:                                                       false,
		{Provider: "anthropic", Model: "claude"}:                                                    true,
	} {
		if c.webCapable() != want {
			t.Fatalf("webCapable(%+v) != %v", c, want)
		}
	}
}

func TestWebHelpers(t *testing.T) {
	for u, want := range map[string]string{"https://a.org/x?y=1": "https://a.org/x?y=1", " http://a.org ": "http://a.org", "javascript:alert(1)": "", "https://a b": "", "ftp://x": "", "https://": "", "https://a.org/<s>": "",
		"https://" + strings.Repeat("a", 600): ""} {
		if got := cleanURL(u); got != want {
			t.Fatalf("cleanURL(%q) = %q", u, got)
		}
	}
	if s := cleanText("  Titel\n mit\tUmbruch\x00 ", 100); s != "Titel mit Umbruch" {
		t.Fatalf("%q", s)
	}
	var set srcSet
	for i := 0; i < 20; i++ {
		set.add("https://a.org/"+string(rune('a'+i)), "t", "", i == 15)
	}
	set.add("https://a.org/a", "x", "2025", true) // doppelt: nur zusammenfuehren
	f := set.final()
	if len(f) != webMaxSources || f[0].URL != "https://a.org/a" || !f[0].Cited || f[0].Age != "2025" || f[1].URL != "https://a.org/p" || !f[1].Cited || f[2].Cited {
		t.Fatalf("%+v", f)
	}
}
