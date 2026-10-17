package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Denkmodelle (DeepSeek V4 Flash): "thinking" wird abgeschaltet; ein leerer Antworttext ist ein Fehler (nicht Erfolg) und wird einmal mit doppelter Ausgabegrenze wiederholt.
func TestThinkingOffAndEmptyAnswer(t *testing.T) {
	old := thinkOffMark
	thinkOffMark = "127.0.0.1" // die Testserver laufen lokal
	defer func() { thinkOffMark = old }()
	var bodies []string
	rejectThinking, emptyBelow, alwaysEmpty := false, 0, false
	prov := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		var rq struct {
			MaxTokens int `json:"max_tokens"`
		}
		json.Unmarshal(b, &rq)
		switch {
		case rejectThinking && strings.Contains(string(b), "thinking"):
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"unknown field thinking"}}`)
		case alwaysEmpty || rq.MaxTokens < emptyBelow:
			io.WriteString(w, `{"choices":[{"message":{"content":null,"reasoning_content":"..."},"finish_reason":"length"}]}`)
		default:
			io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
		}
	}))
	defer prov.Close()
	s := &Svc{}
	c := cfgFor(prov, "openai") // MaxTokens 100
	c.MaxTokens = 1024
	ctx := context.Background()
	reset := func() { bodies = nil; rejectThinking, emptyBelow, alwaysEmpty = false, 0, false }

	// 1. "thinking" abgeschaltet wird mitgeschickt
	reset()
	if out, err := s.complete(ctx, c, "s", oneMsg); err != nil || out != "ok" || len(bodies) != 1 || !strings.Contains(bodies[0], `"thinking":{"type":"disabled"}`) {
		t.Fatal(out, err, bodies)
	}
	// 2. der Anbieter lehnt den Parameter ab (HTTP 400): zweiter Versuch ohne
	reset()
	rejectThinking = true
	if out, err := s.complete(ctx, c, "s", oneMsg); err != nil || out != "ok" || len(bodies) != 2 || !strings.Contains(bodies[0], "thinking") || strings.Contains(bodies[1], "thinking") {
		t.Fatal(out, err, bodies)
	}
	// 3. leerer Text (Ausgabegrenze vom Nachdenken aufgebraucht): zweiter Versuch mit doppelter Grenze
	reset()
	emptyBelow = 2000
	out, err := s.complete(ctx, c, "s", oneMsg)
	var rq struct {
		MaxTokens int `json:"max_tokens"`
	}
	if err != nil || out != "ok" || len(bodies) != 2 || json.Unmarshal([]byte(bodies[1]), &rq) != nil || rq.MaxTokens != 2048 {
		t.Fatal(out, err, bodies)
	}
	// 4. dauerhaft leer: Fehler mit Grund, kein stiller Erfolg
	reset()
	alwaysEmpty = true
	if _, err := s.complete(ctx, c, "s", oneMsg); err == nil || !errors.Is(err, errEmpty) || !errors.Is(err, errProvider) || !strings.Contains(err.Error(), `finish reason "length"`) || len(bodies) != 2 {
		t.Fatal(err, len(bodies))
	}
	// 5. Anthropic bekommt kein "thinking"
	reset()
	anth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}]}`)
	}))
	defer anth.Close()
	if out, err := s.complete(ctx, cfgFor(anth, "anthropic"), "s", oneMsg); err != nil || out != "ok" || strings.Contains(bodies[0], "thinking") {
		t.Fatal(out, err, bodies)
	}
	// 6. Die Uebersetzung meldet die leere Antwort als Fehler (502 mit Grund) statt eines leeren Textes
	reset()
	alwaysEmpty = true
	ts := newTrSvc(prov)
	rec := trPost(ts, `{"text":"Hallo Welt","lang":"en","from":"de"}`)
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "empty answer") {
		t.Fatal(rec.Code, rec.Body.String())
	}
}
