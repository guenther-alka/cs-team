package ai

// Optionaler Suchdienst fuer die cs-team-Quellensuche (Recherche Stufe 2, search.go): Brave Search API (Schluessel) oder SearXNG (eigene Instanz).
// Er ergaenzt Wikipedia und OpenAlex um das offene Web (Nachrichten, Behoerden-, Firmen- und Schulseiten). Eingerichtet nur vom Admin
// (Einstellungen > KI), gespeichert in ai-search.json. Die Suchbegriffe gehen an den Dienst (Brave: api.search.brave.com; SearXNG: die Instanz des Admins,
// auch im lokalen Netz); Benutzerdaten gehen nie mit. Ohne Einrichtung bleibt es bei Wikipedia und OpenAlex.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"cs-team/chat"
)

type SearchCfg struct {
	Provider string `json:"provider"`      // "" | brave | searxng
	URL      string `json:"url,omitempty"` // SearXNG: Adresse der Instanz
	Key      string `json:"key,omitempty"` // Brave: Subscription Token
}

const searchCfgKey = "ai-search.json"

var (
	braveAPI = "https://api.search.brave.com/res/v1/web/search"
	reTags   = regexp.MustCompile(`<[^>]*>`)
	scMu     sync.Mutex
)

func (s *Svc) searchCfg() SearchCfg {
	var c SearchCfg
	if s.St == nil {
		return c
	}
	if b, _, err := s.St.Get(context.Background(), searchCfgKey); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

func engineName(c SearchCfg) string {
	switch c.Provider {
	case "brave":
		return "Brave"
	case "searxng":
		return "SearXNG"
	}
	return ""
}

// saveSearch: key nil = unveraendert (nur bei Brave), "" = loeschen.
func (s *Svc) saveSearch(provider, urlStr string, key *string) error {
	scMu.Lock()
	defer scMu.Unlock()
	n := SearchCfg{Provider: provider}
	switch provider {
	case "":
	case "searxng":
		urlStr = strings.TrimRight(strings.TrimSpace(urlStr), "/")
		if urlStr == "" {
			return errors.New("SearXNG needs the address of the instance")
		}
		if err := checkEndpoint(urlStr); err != nil {
			return err
		}
		n.URL = urlStr
	case "brave":
		k := s.searchCfg().Key
		if key != nil {
			k = *key
		}
		if k == "" {
			return errors.New("Brave needs an API key")
		}
		if strings.ContainsAny(k, "\r\n\x00 ") || len(k) > 200 {
			return errors.New("invalid key")
		}
		n.Key = k
	default:
		return errors.New("provider: brave, searxng or empty")
	}
	b, _ := json.Marshal(n)
	_, err := s.St.Put(context.Background(), searchCfgKey, b, "")
	return err
}

func plain(s string, n int) string { return cleanText(html.UnescapeString(reTags.ReplaceAllString(s, " ")), n) }

func hostOfURL(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Hostname()
	}
	return ""
}

// Sprachcodes der Oberflaeche -> Brave (search_lang; "" = nicht angeben, weil nicht unterstuetzt) und SearXNG (language).
func braveLang(l string) string {
	switch l {
	case "cn":
		return "zh-hans"
	case "ja":
		return "jp"
	case "el":
		return ""
	}
	return l
}

func searxLang(l string) string {
	if l == "cn" {
		return "zh"
	}
	return l
}

func searchBrave(ctx context.Context, key, q, lang string) []hitT {
	v := url.Values{"q": {q}, "count": {"8"}, "safesearch": {"moderate"}, "text_decorations": {"false"}}
	if bl := braveLang(lang); bl != "" {
		v.Set("search_lang", bl)
	}
	var r struct {
		Web struct {
			Results []struct {
				Title string `json:"title"`
				URL   string `json:"url"`
				Desc  string `json:"description"`
				Age   string `json:"age"`
				Page  string `json:"page_age"`
			} `json:"results"`
		} `json:"web"`
	}
	if getJSONX(ctx, braveAPI+"?"+v.Encode(), map[string]string{"X-Subscription-Token": key}, false, &r) != nil {
		return nil
	}
	var out []hitT
	for _, x := range r.Web.Results {
		if cleanURL(x.URL) == "" || strings.TrimSpace(x.Title) == "" {
			continue
		}
		d := first10(x.Page)
		if d == "" {
			d = cleanText(x.Age, 30)
		}
		out = append(out, hitT{Title: plain(x.Title, 200), URL: x.URL, Text: plain(x.Desc, 700), Date: d, Note: "Web (Brave): " + hostOfURL(x.URL)})
	}
	return out
}

func searchSearx(ctx context.Context, base, q, lang string) []hitT {
	v := url.Values{"q": {q}, "format": {"json"}, "safesearch": {"1"}, "language": {searxLang(lang)}}
	var r struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Date    string `json:"publishedDate"`
			Engine  string `json:"engine"`
		} `json:"results"`
	}
	if getJSONX(ctx, strings.TrimRight(base, "/")+"/search?"+v.Encode(), nil, true, &r) != nil { // Adresse vom Admin: lokales Netz erlaubt
		return nil
	}
	var out []hitT
	for i, x := range r.Results {
		if i >= 8 {
			break
		}
		if cleanURL(x.URL) == "" || strings.TrimSpace(x.Title) == "" {
			continue
		}
		out = append(out, hitT{Title: plain(x.Title, 200), URL: x.URL, Text: plain(x.Content, 700), Date: first10(x.Date), Note: "Web (SearXNG" + map[bool]string{true: ": " + cleanText(x.Engine, 30), false: ""}[x.Engine != ""] + "): " + hostOfURL(x.URL)})
	}
	return out
}

func (s *Svc) webSearch(ctx context.Context, c SearchCfg, q, lang string) []hitT {
	switch c.Provider {
	case "brave":
		return searchBrave(ctx, c.Key, q, lang)
	case "searxng":
		return searchSearx(ctx, c.URL, q, lang)
	}
	return nil
}

func getJSONX(ctx context.Context, u string, hdr map[string]string, private bool, v any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "cs-team research (server-side; no user data)")
	req.Header.Set("Accept", "application/json")
	for k, x := range hdr {
		req.Header.Set(k, x)
	}
	resp, err := chat.SafeClient(private || searchPrivate, teamTimeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return decodeLimited(resp.Body, v)
}

func (s *Svc) searchSettingsRoutes(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/ai/search-settings", admin(func(w http.ResponseWriter, r *http.Request) {
		c := s.searchCfg()
		json.NewEncoder(w).Encode(map[string]any{"provider": c.Provider, "url": c.URL, "keySet": c.Key != ""})
	}))
	mux.Handle("POST /api/ai/search-settings", admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Provider, URL string
			Key           *string
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := s.saveSearch(in.Provider, in.URL, in.Key); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}))
	mux.Handle("POST /api/ai/search-settings/test", admin(func(w http.ResponseWriter, r *http.Request) {
		c := s.searchCfg()
		if c.Provider == "" {
			http.Error(w, "no search service set", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		h := s.webSearch(ctx, c, "climate change", "en")
		if len(h) == 0 {
			http.Error(w, "no results (check the key or the address; SearXNG needs the json format enabled)", http.StatusBadGateway)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"count": len(h), "first": h[0].Title, "engine": engineName(c)})
	}))
}

func decodeLimited(r io.Reader, v any) error { return json.NewDecoder(io.LimitReader(r, 2<<20)).Decode(v) }
