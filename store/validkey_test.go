package store

import "testing"

func TestValidKey(t *testing.T) {
	good := []string{"a", "users/users.json", "files/g~x/a b.txt", "x/y/z"}
	bad := []string{"", "/a", "a//b", "a/../b", "../a", "a/./b", "a\\b", "a\x00b", "a/"}
	for _, k := range good {
		if !ValidKey(k) {
			t.Errorf("gut abgelehnt: %q", k)
		}
	}
	for _, k := range bad {
		if ValidKey(k) {
			t.Errorf("schlecht angenommen: %q", k)
		}
	}
	for _, p := range []string{"", "a/", "a/b/", "a/b"} {
		if !validKey(p, true) {
			t.Errorf("Präfix abgelehnt: %q", p)
		}
	}
	for _, p := range []string{"/a", "a//", "../", "a/../"} {
		if validKey(p, true) {
			t.Errorf("Präfix angenommen: %q", p)
		}
	}
}
