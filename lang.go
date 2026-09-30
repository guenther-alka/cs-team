package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Sprachdateien: web/lang/<code>.json (eingebettet; Schlüssel = deutscher Text, Wert = Übersetzung; "_name" = Anzeigename).
// Optional CS_LANGDIR=<ordner>: dort liegende <code>.json ergänzen/überschreiben die eingebetteten Texte (auch neue Sprachen).
var langCode = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z]{2,4})?$`)

func loadLang(code string) (map[string]string, bool) {
	m := map[string]string{}
	found := false
	if b, err := webFS.ReadFile("web/lang/" + code + ".json"); err == nil {
		found = json.Unmarshal(b, &m) == nil
	}
	if d := os.Getenv("CS_LANGDIR"); d != "" {
		if b, err := os.ReadFile(filepath.Join(d, code+".json")); err == nil {
			ov := map[string]string{}
			if json.Unmarshal(b, &ov) == nil {
				for k, v := range ov {
					m[k] = v
				}
				found = true
			}
		}
	}
	return m, found
}

func langList() map[string]string { // code -> Anzeigename
	out := map[string]string{"de": "Deutsch"}
	add := func(names []string, read func(string) ([]byte, error)) {
		for _, n := range names {
			if !strings.HasSuffix(n, ".json") {
				continue
			}
			c := strings.TrimSuffix(n, ".json")
			if !langCode.MatchString(c) {
				continue
			}
			if m, ok := loadLang(c); ok {
				nm := m["_name"]
				if nm == "" {
					nm = c
				}
				out[c] = nm
			}
		}
	}
	if es, err := webFS.ReadDir("web/lang"); err == nil {
		var n []string
		for _, e := range es {
			n = append(n, e.Name())
		}
		add(n, nil)
	}
	if d := os.Getenv("CS_LANGDIR"); d != "" {
		if es, err := os.ReadDir(d); err == nil {
			var n []string
			for _, e := range es {
				n = append(n, e.Name())
			}
			add(n, nil)
		}
	}
	return out
}

// GET /lang/index.json -> {"default":"de","langs":[{"code":"de","name":"Deutsch"},...]}; GET /lang/<code>.json -> Textliste
func langHandler(w http.ResponseWriter, r *http.Request) {
	f := strings.TrimPrefix(r.URL.Path, "/lang/")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if f == "index.json" {
		l := langList()
		codes := make([]string, 0, len(l))
		for c := range l {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		type row struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}
		rows := []row{}
		for _, c := range codes {
			rows = append(rows, row{c, l[c]})
		}
		def := os.Getenv("CS_LANG")
		if _, ok := l[def]; !ok {
			def = "de"
		}
		json.NewEncoder(w).Encode(map[string]any{"default": def, "langs": rows})
		return
	}
	c := strings.TrimSuffix(f, ".json")
	if !strings.HasSuffix(f, ".json") || !langCode.MatchString(c) {
		http.NotFound(w, r)
		return
	}
	if c == "de" {
		w.Write([]byte("{}")) // Deutsch = Quelltext
		return
	}
	m, ok := loadLang(c)
	if !ok {
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(m)
}
