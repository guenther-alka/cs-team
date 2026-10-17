package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Die Oberfläche baut alle Texte aus deutschen Vorlagen: t('...') in web/app.js (und web/index.html), die Übersetzung kommt aus
// web/lang/<code>.json (Schlüssel = deutscher Text). Fehlt ein Schlüssel im Katalog web/lang/_template.json, bleibt
// die Stelle in jeder Sprache deutsch und ein Tippfehler fällt nie auf - deshalb prüft dieser Test den Abgleich.
var reTKey = regexp.MustCompile(`(?:^|[^\w$.'"\\\n])t\('((?:[^'\\]|\\.)*)'`)

// reScript: jeder <script>-Block der Seite. Das Skript liegt in web/app.js; ein Inline-Block mit Inhalt ist ein Fehler,
// weil die CSP (script-src 'self') ihn nicht ausführen würde.
var reScript = regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)

// Formularfelder der Anmeldung (idPanel) und Zugriffe darauf (save): beide Seiten müssen zusammenpassen,
// sonst schreibt das Speichern eine leere Einstellung oder ein Feld bleibt wirkungslos.
var (
	reIDDef = regexp.MustCompile(`id="(sid[A-Za-z0-9]+)"`)
	reIDSel = regexp.MustCompile(`modeSel\('(sid[A-Za-z0-9]+)'`) // Auswahlfelder baut modeSel mit der Kennung im Code
	reIDUse = regexp.MustCompile(`\$\('#(sid[A-Za-z0-9]+)'\)`)
)

func TestWebUISyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node nicht gefunden: JavaScript-Syntax wird nicht geprüft")
	}
	html := webFile(t, "web/index.html")
	if !strings.Contains(html, `<script src="app.js"></script>`) {
		t.Error("web/index.html lädt web/app.js nicht")
	}
	for i, m := range reScript.FindAllStringSubmatch(html, -1) {
		if strings.TrimSpace(m[1]) != "" {
			t.Errorf("web/index.html, Skript-Block %d enthält Inline-Code: die CSP (script-src 'self') führt ihn nicht aus - nach web/app.js verschieben", i+1)
		}
	}
	js := webFile(t, "web/app.js")
	if len(js) < 100000 {
		t.Fatalf("web/app.js ist nur %d Zeichen lang - Auslagerung unvollständig?", len(js))
	}
	f := filepath.Join(t.TempDir(), "app.js")
	if err := os.WriteFile(f, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
		t.Errorf("web/app.js: %v\n%s", err, out)
	}
	_ = fmt.Sprint
}

// Inline-Ereignis-Attribute (onclick="...") führt die CSP ohne 'unsafe-inline' nicht aus; die Oberfläche setzt Handler
// per Eigenschaft (el.onclick = ...) oder addEventListener. Dieser Test hält das fest (Tag-Kontext, auch in Zeichenketten des Skripts).
var reInlineHandler = regexp.MustCompile(`<[a-zA-Z][^<>]*?\son[a-z]+\s*=\s*\\?["']`)

func TestWebUINoInlineHandlers(t *testing.T) {
	for _, name := range []string{"web/index.html", "web/app.js"} {
		src := webFile(t, name)
		for i, line := range strings.Split(src, "\n") {
			if m := reInlineHandler.FindString(line); m != "" {
				t.Errorf("%s:%d: Inline-Handler-Attribut (blockiert von der CSP): %.80s", name, i+1, m)
			}
			if strings.Contains(line, "href=\"javascript:") || strings.Contains(line, "href='javascript:") {
				t.Errorf("%s:%d: javascript:-URL (blockiert von der CSP)", name, i+1)
			}
		}
	}
}

func webFile(t *testing.T, name string) string {
	t.Helper()
	b, err := webFS.ReadFile(name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(b)
}

func TestWebUITranslationsAndAuthFields(t *testing.T) {
	html := webFile(t, "web/index.html") + "\n" + webFile(t, "web/app.js") // Seite und Skript zusammen

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
