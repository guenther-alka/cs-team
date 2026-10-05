package main

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Die Oberfläche baut alle Texte aus deutschen Vorlagen: t('...') in web/index.html, die Übersetzung kommt aus
// web/lang/<code>.json (Schlüssel = deutscher Text). Fehlt ein Schlüssel im Katalog web/lang/_template.json, bleibt
// die Stelle in jeder Sprache deutsch und ein Tippfehler fällt nie auf - deshalb prüft dieser Test den Abgleich.
var reTKey = regexp.MustCompile(`(?:^|[^\w$.'"\\\n])t\('((?:[^'\\]|\\.)*)'`)

// Formularfelder der Anmeldung (idPanel) und Zugriffe darauf (save): beide Seiten müssen zusammenpassen,
// sonst schreibt das Speichern eine leere Einstellung oder ein Feld bleibt wirkungslos.
var (
	reIDDef = regexp.MustCompile(`id="(sid[A-Za-z0-9]+)"`)
	reIDSel = regexp.MustCompile(`modeSel\('(sid[A-Za-z0-9]+)'`) // Auswahlfelder baut modeSel mit der Kennung im Code
	reIDUse = regexp.MustCompile(`\$\('#(sid[A-Za-z0-9]+)'\)`)
)

func webFile(t *testing.T, name string) string {
	t.Helper()
	b, err := webFS.ReadFile(name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(b)
}

func TestWebUITranslationsAndAuthFields(t *testing.T) {
	html := webFile(t, "web/index.html")

	// 1. jeder t('...')-Text muss im Sprachkatalog stehen (Der Katalog beginnt mit "_" und liegt deshalb
	// nicht im eingebetteten Dateisystem - er wird als Quelldatei gelesen.)
	raw, err := os.ReadFile("web/lang/_template.json")
	if err != nil {
		t.Fatal(err)
	}
	cat := map[string]string{}
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatalf("web/lang/_template.json: %v", err)
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, m := range reTKey.FindAllStringSubmatch(html, -1) {
		k := strings.ReplaceAll(m[1], `\'`, `'`)
		if seen[k] || strings.ContainsAny(k, `\`) { // unklare Escapes überspringen
			continue
		}
		seen[k] = true
		if _, ok := cat[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(seen) < 500 {
		t.Fatalf("nur %d Texte gefunden - Muster passt nicht mehr zur Oberfläche", len(seen))
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d Texte fehlen in web/lang/_template.json:\n\t%s", len(missing), strings.Join(missing, "\n\t"))
	}

	// 2. die Anmeldung ist in den Einstellungen eingebunden
	if !strings.Contains(html, "idPanel(c.identity||{})") || !strings.Contains(html, "'/api/settings/auth'") {
		t.Error("Einstellungen: idPanel wird nicht angezeigt oder /api/settings/auth wird nicht gespeichert")
	}

	// 3. Formularfelder: jede verwendete Kennung braucht genau ein Feld (und umgekehrt)
	def := map[string]int{}
	for _, m := range reIDDef.FindAllStringSubmatch(html, -1) {
		def[m[1]]++
	}
	for _, m := range reIDSel.FindAllStringSubmatch(html, -1) {
		def[m[1]]++
	}
	use := map[string]int{}
	for _, m := range reIDUse.FindAllStringSubmatch(html, -1) {
		use[m[1]]++
	}
	if len(def) < 8 || len(use) < 8 {
		t.Fatalf("Anmeldefelder nicht gefunden (Felder %d, Zugriffe %d)", len(def), len(use))
	}
	var bad []string
	for id, n := range def {
		if use[id] == 0 {
			bad = append(bad, id+": Feld ohne Zugriff beim Speichern")
		}
		if n > 1 {
			bad = append(bad, id+": Feld doppelt angelegt")
		}
	}
	for id := range use {
		if def[id] == 0 {
			bad = append(bad, id+": Zugriff ohne Feld")
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("Anmeldefelder passen nicht zusammen:\n\t%s", strings.Join(bad, "\n\t"))
	}
}
