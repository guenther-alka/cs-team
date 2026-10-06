package chat

import (
	"testing"

	"cs-team/store"
)

// 0.60: Obergrenze für öffentliche Links und 2FA-Pflicht für Admins: Vorgabe, Grenzen, Dauerhaftigkeit.
func TestSettingsPubAnd2FA(t *testing.T) {
	st := store.NewMem()
	s := NewSettings(st, SMTP{}, false)
	if s.PubMaxDays() != 0 || s.Enforce2FA() {
		t.Fatal("Vorgabe: unbegrenzt erlaubt, keine Pflicht")
	}
	bad := -1
	if s.setPub2FA(&bad, nil) == nil {
		t.Fatal("negative Tage angenommen")
	}
	big := 4000
	if s.setPub2FA(&big, nil) == nil {
		t.Fatal("zu viele Tage angenommen")
	}
	d, on := 30, true
	if err := s.setPub2FA(&d, &on); err != nil {
		t.Fatal(err)
	}
	s2 := NewSettings(st, SMTP{}, false) // neu geladen
	if s2.PubMaxDays() != 30 || !s2.Enforce2FA() {
		t.Fatal("nicht gespeichert:", s2.PubMaxDays(), s2.Enforce2FA())
	}
	off := false
	if err := s2.setPub2FA(nil, &off); err != nil || s2.PubMaxDays() != 30 || s2.Enforce2FA() {
		t.Fatal("einzelnes Feld ändern")
	}
}
