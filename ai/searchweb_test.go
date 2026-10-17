package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"cs-team/store"
)

func TestSaveSearchSettings(t *testing.T) {
	s := &Svc{St: store.NewMem()}
	str := func(x string) *string { return &x }
	for _, tc := range []struct {
		p, u string
		k    *string
		ok   bool
		what string
	}{
		{"google", "", nil, false, "unbekannter Dienst"},
		{"searxng", "", nil, false, "SearXNG ohne Adresse"},
		{"searxng", "ftp://x", nil, false, "keine http-Adresse"},
		{"searxng", "http://u:p@h/", nil, false, "Zugangsdaten in der Adresse"},
		{"brave", "", nil, false, "Brave ohne Schluessel"},
		{"brave", "", str("abc def"), false, "Schluessel mit Leerzeichen"},
		{"brave", "", str(strings.Repeat("k", 201)), false, "Schluessel zu lang"},
	} {
		if err := s.saveSearch(tc.p, tc.u, tc.k); (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.what, err)
		}
	}
	if c := s.searchCfg(); c.Provider != "" {
		t.Fatalf("nach Fehlern bleibt nichts gespeichert: %+v", c)
	}
	if err := s.saveSearch("searxng", " http://searx.local:8080/ ", nil); err != nil || s.searchCfg().URL != "http://searx.local:8080" || s.searchCfg().Provider != "searxng" {
		t.Fatal(err, s.searchCfg())
	}
	if err := s.saveSearch("brave", "", str("BSA-123")); err != nil || s.searchCfg().Key != "BSA-123" || s.searchCfg().URL != "" {
		t.Fatal(err, s.searchCfg())
	}
	if err := s.saveSearch("brave", "", nil); err != nil || s.searchCfg().Key != "BSA-123" { // Schluessel bleibt, wenn keiner mitkommt
		t.Fatal("Schluessel behalten:", err, s.searchCfg())
	}
	if err := s.saveSearch("", "", nil); err != nil || s.searchCfg() != (SearchCfg{}) { // aus: nichts bleibt gespeichert
		t.Fatal(err, s.searchCfg())
	}
	if engineName(SearchCfg{Provider: "brave"}) != "Brave" || engineName(SearchCfg{Provider: "searxng"}) != "SearXNG" || engineName(SearchCfg{}) != "" {
		t.Fatal("engineName")
	}
}

func TestSearchBraveAndSearx(t *testing.T) {
	searchPrivate = true
	ob := braveAPI
	defer func() { searchPrivate, braveAPI = false, ob }()
	var braveQ, searxQ []string
	var braveTok string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/brave":
			braveTok = r.Header.Get("X-Subscription-Token")
			braveQ = append(braveQ, r.URL.RawQuery)
			io.WriteString(w, `{"web":{"results":[
			 {"title":"Klima <strong>Bericht</strong>","url":"https://www.umweltbundesamt.de/klima","description":"Der &amp; Bericht <b>zeigt</b> Trends.","page_age":"2026-03-04T10:00:00","age":"7 months ago"},
			 {"title":"Alt","url":"https://example.org/alt","description":"x","age":"2 years ago"},
			 {"title":"Boese","url":"javascript:alert(1)","description":"x"},
			 {"title":"","url":"https://example.org/leer","description":"x"}]}}`)
		case "/search":
			searxQ = append(searxQ, r.URL.RawQuery)
			io.WriteString(w, `{"results":[{"title":"Schulnetz","url":"https://schule.example.org/a","content":"Inhalt <i>fett</i>","publishedDate":"2026-09-01T00:00:00","engine":"duckduckgo"},{"title":"ohne Adresse","url":"","content":"x"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	braveAPI = srv.URL + "/brave"

	h := searchBrave(context.Background(), "TOK", "klima", "cn")
	if braveTok != "TOK" || len(h) != 2 || !strings.Contains(braveQ[0], "search_lang=zh-hans") || !strings.Contains(braveQ[0], "text_decorations=false") {
		t.Fatalf("%q %q %+v", braveTok, braveQ, h)
	}
	if h[0].Title != "Klima Bericht" || h[0].Text != "Der & Bericht zeigt Trends." || h[0].Date != "2026-03-04" || h[0].Note != "Web (Brave): www.umweltbundesamt.de" || h[1].Date != "2 years ago" {
		t.Fatalf("%+v", h)
	}
	searchBrave(context.Background(), "TOK", "x", "ja")
	searchBrave(context.Background(), "TOK", "x", "el")
	if !strings.Contains(braveQ[1], "search_lang=jp") || strings.Contains(braveQ[2], "search_lang") {
		t.Fatalf("Sprachcodes: %q", braveQ)
	}
	searchPrivate = false // SearXNG ist die Instanz des Admins: lokale Adressen gehen immer
	x := searchSearx(context.Background(), srv.URL+"/", "schule", "cn")
	if len(x) != 1 || x[0].Title != "Schulnetz" || x[0].Text != "Inhalt fett" || x[0].Date != "2026-09-01" || x[0].Note != "Web (SearXNG: duckduckgo): schule.example.org" {
		t.Fatalf("%+v", x)
	}
	if !strings.Contains(searxQ[0], "format=json") || !strings.Contains(searxQ[0], "language=zh") {
		t.Fatalf("%q", searxQ)
	}
	// Brave ist nie ein lokaler Rechner: ohne searchPrivate geht der Aufruf an 127.0.0.1 nicht durch
	if h := searchBrave(context.Background(), "TOK", "x", "de"); len(h) != 0 {
		t.Fatal("Brave an lokale Adresse muss scheitern")
	}
	if (&Svc{}).webSearch(context.Background(), SearchCfg{}, "x", "de") != nil {
		t.Fatal("ohne Dienst keine Treffer")
	}
}

// Mit eingerichtetem Suchdienst kommen Web-Treffer dazu; der Quellenblock sagt das und nennt die Reichweite nicht mehr als "nur Wikipedia/OpenAlex".
func TestTeamResearchWithSearx(t *testing.T) {
	teamEnabled, searchPrivate = true, true
	ow, oo := wikiAPI, openAlexAPI
	defer func() { teamEnabled, searchPrivate, wikiAPI, openAlexAPI = false, false, ow, oo }()
	var finalBody string
	var mu sync.Mutex
	var langs []string
	prov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "JSON array of 3 short search queries") {
			io.WriteString(w, `{"choices":[{"message":{"content":"[\"klima ursachen\",\"klimawandel mensch\",\"climate change causes\"]"}}]}`)
			return
		}
		finalBody = string(b)
		io.WriteString(w, `{"choices":[{"message":{"content":"Antwort [1]."}}]}`)
	}))
	defer prov.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/w/"):
			io.WriteString(w, `{"query":{"pages":[]}}`)
		case r.URL.Path == "/oa":
			io.WriteString(w, `{"results":[{"id":"https://openalex.org/W1","title":"Paper","doi":"https://doi.org/10.1/p","type":"article","publication_year":2020,"cited_by_count":3}]}`)
		case r.URL.Path == "/search":
			mu.Lock()
			langs = append(langs, r.URL.Query().Get("language"))
			mu.Unlock()
			io.WriteString(w, `{"results":[{"title":"Amt `+r.URL.Query().Get("q")+`","url":"https://amt.example.org/`+strings.ReplaceAll(r.URL.Query().Get("q"), " ", "-")+`","content":"Mitteilung","publishedDate":"2026-10-01T00:00:00","engine":"x"}]}`)
		}
	}))
	defer srv.Close()
	wikiAPI, openAlexAPI = srv.URL+"/w/%s", srv.URL+"/oa"
	s := &Svc{St: store.NewMem()}
	if err := s.saveSearch("searxng", srv.URL, nil); err != nil {
		t.Fatal(err)
	}
	ms := []msg{{Role: "user", Text: "Aufgabe <<<Ist der Klimawandel menschengemacht?>>>"}}
	out, w, err := s.research(context.Background(), cfgFor(prov, "ollama"), "system", ms, "de", "de")
	if err != nil || out != "Antwort [1]." || w == nil || w.Method != "cs-team" {
		t.Fatal(out, w, err)
	}
	if strings.Join(w.Engines, ",") != "Wikipedia,OpenAlex,SearXNG" || w.Searches != 2+3+3 {
		t.Fatalf("Quellen/Abfragen: %v %d", w.Engines, w.Searches)
	}
	if len(langs) != 3 || strings.Count(strings.Join(langs, ","), "de") != 2 || strings.Count(strings.Join(langs, ","), "en") != 1 {
		t.Fatalf("Sprachen der Websuche (2 Landessprache + 1 englisch): %v", langs)
	}
	webHits := 0
	for _, x := range w.Sources {
		if strings.HasPrefix(x.Note, "Web (SearXNG") {
			webHits++
		}
	}
	if webHits != 3 || len(w.Sources) != 4 {
		t.Fatalf("%d Web-Treffer, %d Quellen: %+v", webHits, len(w.Sources), w.Sources)
	}
	if !strings.Contains(finalBody, "and web search via SearXNG") || !strings.Contains(finalBody, "web results come from SearXNG") || strings.Contains(finalBody, "NOT searched") {
		t.Fatalf("Quellenblock: %s", finalBody)
	}
}
