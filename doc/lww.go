// Package doc: gemeinsame Dokumente (Calc = Zellen, Text = Absätze) als LWW-Map.
// Ein Dokument ist eine Map key -> Item. Jede Änderung trägt einen vom Server vergebenen
// Zeitstempel; der höhere gewinnt (Zelle bzw. Absatz). Gelöschtes bleibt als Tombstone.
package doc

import (
	"regexp"
	"sort"
	"strings"
)

type Item struct {
	V   string  `json:"v"`
	Pos float64 `json:"pos,omitempty"` // nur Text: Reihenfolge der Absätze
	F   string  `json:"f,omitempty"`   // Zelle: Zeichenformat der ganzen Zelle ("b i u s18 c#ff0000 g#ffff00"); Absatz: Absatzformat ("l" Aufzählung, "o" Nummerierung, "n2" Einrückung)
	R   string  `json:"r,omitempty"`   // nur Text: Zeichenformate im Absatz als Läufe "anzahl:format;anzahl:format" (Anzahl in UTF-16-Einheiten, Format wie F ohne l/o/n)
	TS  int64   `json:"ts"`
	By  string  `json:"by"`
	Del bool    `json:"del,omitempty"`
}

type Doc struct {
	Items map[string]Item `json:"items"`
}

func NewDoc() *Doc { return &Doc{Items: map[string]Item{}} }

// wins: true, wenn a den Stand b ablöst (bei Gleichstand entscheidet der Benutzername deterministisch).
func wins(a, b Item) bool {
	if a.TS != b.TS {
		return a.TS > b.TS
	}
	return a.By > b.By
}

// Apply übernimmt it, falls neuer. Liefert true bei Änderung.
func (d *Doc) Apply(k string, it Item) bool {
	if cur, ok := d.Items[k]; ok && !wins(it, cur) {
		return false
	}
	d.Items[k] = it
	return true
}

// Merge: alle Items aus o übernehmen, soweit neuer. Kommutativ/idempotent.
func (d *Doc) Merge(o *Doc) {
	for k, it := range o.Items {
		d.Apply(k, it)
	}
}

// Text liefert die Absätze sortiert nach Pos (ohne Tombstones).
func (d *Doc) Paragraphs() []string {
	type kv struct {
		k string
		i Item
	}
	var l []kv
	for k, i := range d.Items {
		if !i.Del {
			l = append(l, kv{k, i})
		}
	}
	sort.Slice(l, func(a, b int) bool {
		if l[a].i.Pos != l[b].i.Pos {
			return l[a].i.Pos < l[b].i.Pos
		}
		return l[a].k < l[b].k
	})
	out := make([]string, len(l))
	for i := range l {
		out[i] = l[i].i.V
	}
	return out
}

var (
	reCell = regexp.MustCompile(`^[A-Z]{1,3}[0-9]{1,5}$`)
	rePar  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
)

func ValidKey(typ, k string) bool {
	if typ == "sheet" {
		return reCell.MatchString(k)
	}
	return rePar.MatchString(k)
}

// reFmt: erlaubte Formatierung (Whitelist, landet im Client in style-Attributen): b fett, i kursiv, u unterstrichen,
// l Aufzählung, o Nummerierung, sNN Schriftgröße (px), nN Einrückung, c#rrggbb Textfarbe, g#rrggbb Hintergrund.
var reFmt = regexp.MustCompile(`^(?:(?:b|i|u|l|o|s[0-9]{1,2}|n[0-8]|[cg]#[0-9a-fA-F]{6})(?: |$))*$`)

func ValidFmt(f string) bool { return len(f) <= 80 && reFmt.MatchString(f) }

var (
	reCharFmt = regexp.MustCompile(`^(?:(?:b|i|u|s[0-9]{1,2}|[cg]#[0-9a-fA-F]{6})(?: |$))*$`)
	reRun     = regexp.MustCompile(`^[0-9]{1,6}:(.*)$`)
)

// ValidRuns prüft die Zeichenformat-Läufe eines Absatzes ("5:b;3:;4:b c#ff0000").
func ValidRuns(r string) bool {
	if r == "" {
		return true
	}
	if len(r) > 4000 {
		return false
	}
	n := 0
	for _, seg := range strings.Split(r, ";") {
		m := reRun.FindStringSubmatch(seg)
		if m == nil || len(m[1]) > 80 || !reCharFmt.MatchString(m[1]) {
			return false
		}
		if n++; n > 400 {
			return false
		}
	}
	return true
}
