package cal

import (
	"strings"

	"github.com/emersion/go-ical"
)

// rruleTooFine: Wiederholungen feiner als täglich (FREQ=SECONDLY/MINUTELY/HOURLY) sind für Kalenderansichten
// unnötig und binden bei großen Zeiträumen die CPU aller Leser (Audit K-03).
func rruleTooFine(v string) bool {
	for _, p := range strings.Split(strings.ToUpper(v), ";") {
		if k, val, ok := strings.Cut(strings.TrimSpace(p), "="); ok && k == "FREQ" {
			switch strings.TrimSpace(val) {
			case "SECONDLY", "MINUTELY", "HOURLY":
				return true
			}
		}
	}
	return false
}

// hasFineRRule meldet, ob eine Komponente (oder ihre Unterkomponenten) so eine Regel enthält.
func hasFineRRule(c *ical.Component) bool {
	for _, p := range c.Props.Values(ical.PropRecurrenceRule) {
		if rruleTooFine(p.Value) {
			return true
		}
	}
	for _, ch := range c.Children {
		if hasFineRRule(ch) {
			return true
		}
	}
	return false
}

// dropFineRRule entfernt solche Regeln (Abo-Import: der Termin bleibt als Einzeltermin erhalten).
func dropFineRRule(c *ical.Component) {
	if hasFineRRuleOwn(c) {
		c.Props.Del(ical.PropRecurrenceRule)
	}
	for _, ch := range c.Children {
		dropFineRRule(ch)
	}
}

func hasFineRRuleOwn(c *ical.Component) bool {
	for _, p := range c.Props.Values(ical.PropRecurrenceRule) {
		if rruleTooFine(p.Value) {
			return true
		}
	}
	return false
}
