package doc

import "sort"

// ParagraphItems liefert die Absaetze (ohne Tombstones) samt Absatzformat (F) und Zeichenformaten (R), sortiert nach Pos.
func (d *Doc) ParagraphItems() []Item {
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
	out := make([]Item, len(l))
	for i := range l {
		out[i] = l[i].i
	}
	return out
}

// CellItems liefert die Zellen (Calc) mit Wert oder Format (ohne Tombstones).
func (d *Doc) CellItems() map[string]Item {
	out := map[string]Item{}
	for k, it := range d.Items {
		if !it.Del && (it.V != "" || it.F != "") {
			out[k] = it
		}
	}
	return out
}
