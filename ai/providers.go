package ai

// Anbieter-Vorlagen (providers.txt im Datenspeicher) und Modell-Liste live vom Anbieter.
// Format der Datei: Name<TAB>Protokoll<TAB>Endpunkt je Zeile, # = Kommentar. Fehlt sie, gilt die eingebaute Liste.
// Der Admin kann die Datei kürzen oder erweitern; geändert wird sie nicht von cs-team.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"cs-team/chat"
)

const presetsKey = "providers.txt"

type Preset struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // anthropic | openai | ollama
	Endpoint string `json:"endpoint"`
	Local    bool   `json:"local"` // Rechner im lokalen Netz (Ollama, eigener Server): Endpunkt-Adresse frei wählbar
}

var defaultPresets = []Preset{
	{"OpenAI", "openai", "https://api.openai.com/v1/chat/completions", false},
	{"Anthropic", "anthropic", "https://api.anthropic.com/v1/messages", false},
	{"Ollama (local)", "ollama", "http://127.0.0.1:11434/v1/chat/completions", true},
	{"Inhouse Provider (OpenAI compat)", "openai", "http://192.168.0.10:8080/v1/chat/completions", true},
	{"OpenRouter", "openai", "https://openrouter.ai/api/v1/chat/completions", false},
	{"Groq", "openai", "https://api.groq.com/openai/v1/chat/completions", false},
	{"Mistral", "openai", "https://api.mistral.ai/v1/chat/completions", false},
	{"DeepSeek", "openai", "https://api.deepseek.com/chat/completions", false},
	{"xAI (Grok)", "openai", "https://api.x.ai/v1/chat/completions", false},
	{"Together AI", "openai", "https://api.together.ai/v1/chat/completions", false},
	{"Fireworks AI", "openai", "https://api.fireworks.ai/inference/v1/chat/completions", false},
	{"Cerebras", "openai", "https://api.cerebras.ai/v1/chat/completions", false},
	{"Google Gemini (OpenAI-compat)", "openai", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions", false},
	{"Z.AI / Zhipu (international)", "openai", "https://api.z.ai/api/paas/v4/chat/completions", false},
	{"Z.AI / Zhipu (China)", "openai", "https://open.bigmodel.cn/api/paas/v4/chat/completions", false},
}

// parsePresets: Zeilen "Name<TAB>Protokoll<TAB>Endpunkt"; ungültige Zeilen werden übersprungen.
func parsePresets(b []byte) []Preset {
	var out []Preset
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 3 {
			continue
		}
		p := Preset{Name: strings.TrimSpace(f[0]), Protocol: strings.TrimSpace(f[1]), Endpoint: strings.TrimSpace(f[2])}
		if p.Name == "" || len(p.Name) > 80 || (p.Protocol != "openai" && p.Protocol != "anthropic" && p.Protocol != "ollama") || checkEndpoint(p.Endpoint) != nil || p.Endpoint == "" {
			continue
		}
		p.Local = p.Protocol == "ollama" || strings.Contains(strings.ToLower(p.Name), "inhouse")
		out = append(out, p)
	}
	return out
}

func (s *Svc) presets(ctx context.Context) []Preset {
	if b, _, err := s.St.Get(ctx, presetsKey); err == nil {
		if l := parsePresets(b); len(l) > 0 {
			return l
		}
	}
	return defaultPresets
}

// modelsURL: Adresse der Modell-Liste aus der Chat-Adresse (…/chat/completions -> …/models, …/messages -> …/models).
func modelsURL(c Config) string {
	e := c.endpoint()
	for _, suf := range []string{"/chat/completions", "/messages", "/api/chat"} {
		if strings.HasSuffix(e, suf) {
			return strings.TrimSuffix(e, suf) + "/models"
		}
	}
	return strings.TrimRight(e, "/") + "/models"
}

var reVision = regexp.MustCompile(`(?i)claude|gpt-4o|gpt-4\.1|gpt-5|gemini|llava|vision|[-_]vl\b|pixtral|gemma-?3|grok.*vision|llama-?4|qwen.*vl`)

var (
	errAddr  = errors.New("address not allowed (allow local network for local servers)")
	errReach = errors.New("not reachable")
	errBad   = errors.New("unexpected answer (no model list)")
)

func errStatus(c int) error { return fmt.Errorf("HTTP %d (key missing or wrong?)", c) }

type modelInfo struct {
	ID     string `json:"id"`
	Vision bool   `json:"vision"`
}

// listModels fragt die Modell-Liste des Anbieters ab (mit dem gemerkten bzw. eingegebenen Schlüssel; Server-Adressprüfung wie beim Chat).
func (s *Svc) listModels(ctx context.Context, c Config) ([]modelInfo, error) {
	cl := chat.SafeClient(c.Private, 20*time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", modelsURL(c), nil)
	req.Header.Set("User-Agent", "cs-team")
	if c.Provider == "anthropic" {
		req.Header.Set("x-api-key", c.Key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := cl.Do(req)
	if err != nil {
		var ue *url.Error // enthält die Adresse: nicht weitergeben
		if errors.As(err, &ue) {
			err = ue.Err
		}
		if strings.Contains(err.Error(), "address not allowed") {
			return nil, errAddr
		}
		return nil, errReach
	}
	defer resp.Body.Close()
	var raw struct {
		Data []struct {
			ID           string `json:"id"`
			Architecture struct {
				In []string `json:"input_modalities"`
			} `json:"architecture"`
		} `json:"data"`
		Models []struct { // Ollama-Format /api/tags
			Name string `json:"name"`
		} `json:"models"`
	}
	if resp.StatusCode/100 != 2 {
		return nil, errStatus(resp.StatusCode)
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw) != nil {
		return nil, errBad
	}
	var out []modelInfo
	seen := map[string]bool{}
	add := func(id string, v bool) {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 200 || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, modelInfo{id, v || reVision.MatchString(id)})
	}
	for _, d := range raw.Data {
		v := false
		for _, m := range d.Architecture.In {
			if m == "image" {
				v = true
			}
		}
		add(d.ID, v)
	}
	for _, m := range raw.Models {
		add(m.Name, false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > 500 {
		out = out[:500]
	}
	return out, nil
}
