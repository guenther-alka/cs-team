package conv

// Einfacher Antworttext (KI oder Eingabe) -> Absaetze mit Formaten: "# Ueberschrift" (fett), "- " / "* " Aufzaehlung, "1. " Nummerierung
// (Einrueckung je 2 fuehrende Leerzeichen), **fett**; Leerzeilen werden zu einem leeren Absatz. Alles andere bleibt Text.

import (
	"regexp"
	"strings"
)

var (
	reMdHead   = regexp.MustCompile(`^#{1,6}\s+`)
	reMdBullet = regexp.MustCompile(`^[-*•]\s+`)
	reMdNum    = regexp.MustCompile(`^[0-9]{1,3}[.)]\s+`)
)

// BoldPara: ein Absatz, der ganz fett ist (Titelzeile).
func BoldPara(text string) Para {
	return Para{V: text, R: runsFrom([]rSeg{{text, rFmt{b: true, d: -1}}})}
}

func mdSegs(s string, bold bool) []rSeg {
	var segs []rSeg
	for i, part := range strings.Split(s, "**") {
		if part != "" {
			segs = append(segs, rSeg{part, rFmt{b: bold || i%2 == 1, d: -1}})
		}
	}
	return segs
}

func ParasFromMarkdown(s string) []Para {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var out []Para
	blank := false
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimRight(line, " \t")
		if strings.TrimSpace(t) == "" {
			if len(out) > 0 && !blank {
				out = append(out, Para{})
			}
			blank = true
			continue
		}
		blank = false
		tt := strings.TrimLeft(t, " \t")
		lead := len(t) - len(tt)
		f, head := "", false
		switch {
		case reMdHead.MatchString(tt):
			tt, head = reMdHead.ReplaceAllString(tt, ""), true
		case reMdBullet.MatchString(tt):
			tt, f = reMdBullet.ReplaceAllString(tt, ""), "l"
		case reMdNum.MatchString(tt):
			tt, f = reMdNum.ReplaceAllString(tt, ""), "o"
		}
		if f != "" && lead >= 2 {
			f += " n" + string(rune('0'+min(lead/2, 8)))
		}
		segs := mdSegs(tt, head)
		var v strings.Builder
		for _, sg := range segs {
			v.WriteString(sg.text)
		}
		out = append(out, Para{V: v.String(), F: f, R: runsFrom(segs)})
		if len(out) >= MaxParas {
			break
		}
	}
	for len(out) > 0 && out[len(out)-1].V == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		out = []Para{{}}
	}
	return out
}
