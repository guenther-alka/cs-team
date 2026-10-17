package ai

// Echte Recherche: die Websuche des KI-Anbieters. Die Suche macht der Anbieter (cs-team schickt nur die Frage und die Anweisungen);
// zurueck kommen der Text und die vom Suchdienst gelieferten Quellen (Adresse, Titel, Alter). Diese Liste kann die KI nicht erfinden:
// sie stammt aus den Suchergebnissen bzw. den Zitaten des Anbieters. Unterstuetzt: Anthropic (Werkzeug web_search) und OpenAI mit
// Such-Modellen (Modellname enthaelt "search"). Ollama und andere Anbieter haben hier keine Websuche; dann (und wenn der Anbieter die
// Suche ablehnt, z. B. nicht freigeschaltet) antwortet die KI aus ihrem Wissen, und die Antwort sagt das ausdruecklich (webInfo.Used=false).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"cs-team/chat"
)

const (
	webMaxUses    = 5  // Suchanfragen je Recherche
	webRounds     = 4  // Fortsetzungen, wenn der Anbieter eine lange Suche unterbricht (pause_turn)
	webMaxSources = 12 // so viele Quellen gibt cs-team zurueck
	webMinTokens  = 6000 // Platz fuer das ausfuehrliche Analysereferat (Stufe 3) auch in Sprachen mit vielen Token je Wort
)

type webSource struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	Age   string `json:"age,omitempty"`
	Cited bool   `json:"cited,omitempty"` // die Antwort beruft sich ausdruecklich darauf (Zitat des Anbieters bzw. [n] im Text)
	Note  string `json:"note,omitempty"`  // Art der Quelle (cs-team-Quellensuche: Wikipedia / OpenAlex mit Zeitschrift, Autoren, Zitierungen)
}

type webInfo struct {
	Used     bool        `json:"used"`
	Searches int         `json:"searches"`
	Sources  []webSource `json:"sources,omitempty"`
	Provider string      `json:"provider,omitempty"`
	Model    string      `json:"model,omitempty"`
	Reason   string      `json:"reason,omitempty"`
	Method   string      `json:"method,omitempty"`  // provider = Websuche des Anbieters, cs-team = eigene Quellensuche (search.go), leer = nur Wissen der KI
	Queries  []string    `json:"queries,omitempty"` // Suchbegriffe der cs-team-Quellensuche
	Engines  []string    `json:"engines,omitempty"` // abgefragte Quellen der cs-team-Quellensuche (Wikipedia, OpenAlex, Brave/SearXNG)
}

var errNoWeb = errors.New("no web search at this provider")

func (c Config) webCapable() bool {
	switch c.Provider {
	case "anthropic":
		return true
	case "openai":
		return strings.Contains(hostOf(c), "api.openai.com") && strings.Contains(strings.ToLower(c.Model), "search")
	}
	return false
}

func cleanURL(u string) string {
	u = strings.TrimSpace(u)
	if len(u) > 500 || !(strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) {
		return ""
	}
	for _, r := range u {
		if r <= 32 || r == 127 || r == '<' || r == '>' || r == '"' {
			return ""
		}
	}
	if p, err := url.Parse(u); err != nil || p.Host == "" {
		return ""
	}
	return u
}

func cleanText(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 32 || r == 127 {
			r = ' '
		}
		b.WriteRune(r)
	}
	return clip(strings.Join(strings.Fields(b.String()), " "), n)
}

type srcSet struct {
	list []webSource
	idx  map[string]int
}

func (s *srcSet) add(u, title, age string, cited bool) {
	u = cleanURL(u)
	if u == "" {
		return
	}
	if s.idx == nil {
		s.idx = map[string]int{}
	}
	if i, ok := s.idx[u]; ok {
		s.list[i].Cited = s.list[i].Cited || cited
		if s.list[i].Title == "" {
			s.list[i].Title = cleanText(title, 200)
		}
		if s.list[i].Age == "" {
			s.list[i].Age = cleanText(age, 40)
		}
		return
	}
	s.idx[u] = len(s.list)
	s.list = append(s.list, webSource{URL: u, Title: cleanText(title, 200), Age: cleanText(age, 40), Cited: cited})
}

// final: zitierte Quellen zuerst, dann die uebrigen Suchergebnisse, hoechstens webMaxSources.
func (s *srcSet) final() []webSource {
	out := append([]webSource{}, s.list...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Cited && !out[j].Cited })
	if len(out) > webMaxSources {
		out = out[:webMaxSources]
	}
	return out
}

// postProvider schickt einen JSON-Aufruf an den Anbieter (mit denselben Fehlermeldungen wie complete1).
func postProvider(ctx context.Context, c Config, body []byte, headers map[string]string) ([]byte, error) {
	cl := chat.SafeClient(c.Private, 170*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.endpoint(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "cs-team")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := cl.Do(req)
	if err != nil {
		var ue *url.Error // enthaelt die URL: nicht weitergeben
		if errors.As(err, &ue) {
			err = ue.Err
		}
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
			return nil, fmt.Errorf("%w: timeout", errProvider)
		}
		if strings.Contains(err.Error(), "address not allowed") {
			return nil, fmt.Errorf("%w: address not allowed (allow local network in the settings for Ollama)", errProvider)
		}
		return nil, fmt.Errorf("%w: not reachable", errProvider)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal(raw, &e)
		return nil, fmt.Errorf("%w: HTTP %d %s", errProvider, resp.StatusCode, clip(strings.TrimSpace(e.Error.Message), 300))
	}
	return raw, nil
}

// research: Antwort mit Websuche, wenn der Anbieter sie kann; sonst (oder wenn er sie ablehnt) aus dem Wissen der KI, mit webInfo.Used=false.
func (s *Svc) research(ctx context.Context, c Config, system string, ms []msg, lang, olang string) (string, *webInfo, error) {
	try := func(cfg Config) (string, *webInfo, error) {
		switch {
		case !cfg.webCapable():
			return "", nil, errNoWeb
		case cfg.Provider == "anthropic":
			return anthropicWeb(ctx, cfg, system, ms)
		}
		return openaiWeb(ctx, cfg, system, ms)
	}
	out, w, err := try(c)
	if err == nil {
		return out, w, nil
	}
	if a, ok := c.altConfig(); ok && ctx.Err() == nil {
		if o2, w2, e2 := try(a); e2 == nil {
			log.Printf("ai: fallback provider used for web search (%s)", hostOf(a))
			return o2, w2, nil
		}
	}
	if ctx.Err() != nil {
		return "", nil, err
	}
	// Stufe 2: eigene Quellensuche von cs-team (Wikipedia + wissenschaftliche Literatur), unabhaengig von Anbieter und Modell
	teamTried := teamEnabled
	if o, w2, e := s.teamResearch(ctx, c, system, ms, lang, olang); e == nil {
		return o, w2, nil
	} else if !errors.Is(e, errNoSources) {
		return "", nil, e // der Anbieter selbst antwortet nicht
	}
	reason := "provider has no web search"
	if !errors.Is(err, errNoWeb) {
		reason = "web search failed or not enabled at the provider"
		log.Printf("ai: web search failed, answering without it: %v", err)
	}
	if teamTried {
		reason = "no sources found"
	}
	cc := c
	cc.MaxTokens = max(c.MaxTokens, webMinTokens) // eine Recherche braucht mehr Platz als die Standardgrenze (1024): sonst bricht die Antwort mitten im Text ab
	o, e := s.complete(ctx, cc, system, ms)
	return o, &webInfo{Reason: reason, Provider: c.Provider, Model: c.Model}, e
}

func anthropicWeb(ctx context.Context, c Config, system string, ms []msg) (string, *webInfo, error) {
	var arr []map[string]any
	for _, m := range ms {
		arr = append(arr, map[string]any{"role": m.Role, "content": []map[string]any{{"type": "text", "text": m.Text}}})
	}
	info := &webInfo{Provider: c.Provider, Model: c.Model, Method: "provider"}
	var set srcSet
	var text strings.Builder
	hdr := map[string]string{"x-api-key": c.Key, "anthropic-version": "2023-06-01"}
	for round := 0; round < webRounds; round++ {
		body, _ := json.Marshal(map[string]any{"model": c.Model, "max_tokens": max(c.MaxTokens, webMinTokens), "system": system, "messages": arr,
			"tools": []map[string]any{{"type": "web_search_20250305", "name": "web_search", "max_uses": webMaxUses}}})
		raw, err := postProvider(ctx, c, body, hdr)
		if err != nil {
			return "", nil, err
		}
		var r struct {
			StopReason string            `json:"stop_reason"`
			Content    []json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &r) != nil {
			return "", nil, fmt.Errorf("%w: bad response", errProvider)
		}
		for _, blk := range r.Content {
			var b struct {
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				Name      string          `json:"name"`
				Content   json.RawMessage `json:"content"`
				Citations []struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"citations"`
			}
			if json.Unmarshal(blk, &b) != nil {
				continue
			}
			switch b.Type {
			case "text":
				text.WriteString(b.Text)
				for _, ct := range b.Citations {
					set.add(ct.URL, ct.Title, "", true)
				}
			case "server_tool_use":
				if b.Name == "web_search" {
					info.Searches++
				}
			case "web_search_tool_result":
				var res []struct {
					URL     string `json:"url"`
					Title   string `json:"title"`
					PageAge string `json:"page_age"`
				}
				if json.Unmarshal(b.Content, &res) == nil { // bei einem Fehlerobjekt (Suche nicht moeglich) bleibt die Liste leer
					for _, x := range res {
						set.add(x.URL, x.Title, x.PageAge, false)
					}
				}
			}
		}
		if r.StopReason != "pause_turn" {
			break
		}
		arr = append(arr, map[string]any{"role": "assistant", "content": r.Content}) // der Anbieter setzt die unterbrochene Suche fort
	}
	out := strings.TrimSpace(text.String())
	if out == "" {
		return "", nil, fmt.Errorf("%w: empty response", errProvider)
	}
	info.Sources = set.final()
	info.Used = info.Searches > 0
	if !info.Used {
		info.Reason = "the model did not search"
	}
	return out, info, nil
}

func openaiWeb(ctx context.Context, c Config, system string, ms []msg) (string, *webInfo, error) {
	arr := []map[string]any{{"role": "system", "content": system}}
	for _, m := range ms {
		arr = append(arr, map[string]any{"role": m.Role, "content": m.Text})
	}
	body, _ := json.Marshal(map[string]any{"model": c.Model, "max_completion_tokens": max(c.MaxTokens, webMinTokens), "messages": arr, "web_search_options": map[string]any{}})
	raw, err := postProvider(ctx, c, body, map[string]string{"Authorization": "Bearer " + c.Key})
	if err != nil {
		return "", nil, err
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content     string `json:"content"`
				Annotations []struct {
					Type    string `json:"type"`
					Citaton struct {
						URL   string `json:"url"`
						Title string `json:"title"`
					} `json:"url_citation"`
				} `json:"annotations"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.Choices) == 0 || strings.TrimSpace(r.Choices[0].Message.Content) == "" {
		return "", nil, fmt.Errorf("%w: bad response", errProvider)
	}
	var set srcSet
	for _, a := range r.Choices[0].Message.Annotations {
		if a.Type == "url_citation" {
			set.add(a.Citaton.URL, a.Citaton.Title, "", true)
		}
	}
	info := &webInfo{Provider: c.Provider, Model: c.Model, Sources: set.final(), Method: "provider"}
	info.Used = len(info.Sources) > 0
	info.Searches = 0
	if info.Used {
		info.Searches = 1 // die Zahl der Suchen nennt dieser Anbieter nicht
	} else {
		info.Reason = "the model did not search"
	}
	return strings.TrimSpace(r.Choices[0].Message.Content), info, nil
}
