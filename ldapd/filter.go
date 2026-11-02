package ldapd

import (
	"errors"
	"strings"
)

type filt func(e *entry) bool

var errFilter = errors.New("ldap: bad filter")

// parseFilter wertet einen Suchfilter (RFC 4511) aus: and, or, not, Gleichheit, Teilstring, vorhanden.
// Groesser/kleiner/aehnlich/erweitert treffen nie; bei Gleichheit wird Gross-/Kleinschreibung nicht beachtet.
func parseFilter(t tlv) (filt, error) {
	switch t.tag {
	case 0xa0, 0xa1: // and, or
		subs, err := items(t.val)
		if err != nil {
			return nil, err
		}
		var fs []filt
		for _, s := range subs {
			f, err := parseFilter(s)
			if err != nil {
				return nil, err
			}
			fs = append(fs, f)
		}
		and := t.tag == 0xa0
		return func(e *entry) bool {
			for _, f := range fs {
				if f(e) != and {
					return !and
				}
			}
			return and
		}, nil
	case 0xa2: // not
		s, _, err := parse(t.val)
		if err != nil {
			return nil, err
		}
		f, err := parseFilter(s)
		if err != nil {
			return nil, err
		}
		return func(e *entry) bool { return !f(e) }, nil
	case 0xa3, 0xa8: // equalityMatch, approxMatch
		av, err := items(t.val)
		if err != nil || len(av) != 2 {
			return nil, errFilter
		}
		attr, want := strings.ToLower(string(av[0].val)), string(av[1].val)
		return func(e *entry) bool {
			for _, v := range e.attrs[attr] {
				if strings.EqualFold(v, want) {
					return true
				}
			}
			return false
		}, nil
	case 0xa4: // substrings
		av, err := items(t.val)
		if err != nil || len(av) != 2 {
			return nil, errFilter
		}
		attr := strings.ToLower(string(av[0].val))
		parts, err := items(av[1].val)
		if err != nil {
			return nil, err
		}
		return func(e *entry) bool {
			for _, v := range e.attrs[attr] {
				if subMatch(strings.ToLower(v), parts) {
					return true
				}
			}
			return false
		}, nil
	case 0x87: // present
		attr := strings.ToLower(string(t.val))
		return func(e *entry) bool { return len(e.attrs[attr]) > 0 }, nil
	case 0xa5, 0xa6, 0xa9:
		return func(*entry) bool { return false }, nil
	}
	return nil, errFilter
}

func subMatch(v string, parts []tlv) bool {
	for _, p := range parts {
		s := strings.ToLower(string(p.val))
		switch p.tag {
		case 0x80: // initial
			if !strings.HasPrefix(v, s) {
				return false
			}
			v = v[len(s):]
		case 0x81: // any
			i := strings.Index(v, s)
			if i < 0 {
				return false
			}
			v = v[i+len(s):]
		case 0x82: // final
			if !strings.HasSuffix(v, s) {
				return false
			}
			v = ""
		default:
			return false
		}
	}
	return true
}
