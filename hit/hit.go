// Package hit: Trefferliste der globalen Suche (GET /api/search). Jeder Dienst (Aufgaben, Chat, Dateien, Dokumente, Kalender)
// liefert seine Treffer selbst und prueft dabei die Rechte des Benutzers; die Suche ist eine einfache Teilzeichenfolge-Suche ohne Index.
package hit

import (
	"strings"
	"unicode/utf8"
)

type Hit struct {
	Kind    string `json:"kind"` // task | chat | file | text | sheet | event
	ID      string `json:"id"`
	Title   string `json:"title"`
	Snippet string `json:"snippet,omitempty"`
	Where   string `json:"where,omitempty"`
}

// Query bringt die Eingabe in die Suchform (klein, getrimmt); ok=false bei weniger als 2 oder mehr als 80 Zeichen.
func Query(s string) (string, bool) {
	q := strings.ToLower(strings.TrimSpace(s))
	n := utf8.RuneCountInString(q)
	return q, n >= 2 && n <= 80
}

// Snip: Ausschnitt (etwa 120 Zeichen) um den ersten Treffer von q (schon klein geschrieben) in s; ok=false ohne Treffer.
func Snip(s, q string) (string, bool) {
	if s == "" {
		return "", false
	}
	ls := strings.ToLower(s)
	i := strings.Index(ls, q)
	if i < 0 {
		return "", false
	}
	rs := []rune(s)
	if len(ls) != len(s) { // die Kleinschreibung hat die Bytelaenge geaendert: Anfang des Textes nehmen
		return cut(rs, 0, 120), true
	}
	start := utf8.RuneCountInString(s[:i])
	from := max(0, start-40)
	return cut(rs, from, start+utf8.RuneCountInString(q)+80), true
}

func cut(rs []rune, from, to int) string {
	to = min(len(rs), to)
	out := strings.Join(strings.Fields(string(rs[from:to])), " ")
	if from > 0 {
		out = "…" + out
	}
	if to < len(rs) {
		out += "…"
	}
	return out
}
