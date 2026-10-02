package cal

import "testing"

func TestInviteLimit(t *testing.T) {
	if !inviteAllow("limit-test", 60) || !inviteAllow("limit-test", 40) {
		t.Fatal("bis zum Limit erlaubt")
	}
	if inviteAllow("limit-test", 1) {
		t.Fatal("über dem Limit")
	}
	if !inviteAllow("anderer", 100) {
		t.Fatal("Limit gilt je Benutzer")
	}
}

func TestTriggerStr(t *testing.T) {
	for min, want := range map[int]string{0: "PT0S", 5: "-PT5M", 60: "-PT1H", 90: "-PT90M", 1440: "-P1D", 10080: "-P1W", 2880: "-P2D"} {
		if got := triggerStr(min); got != want {
			t.Fatalf("%d: %s != %s", min, got, want)
		}
	}
}

func TestRuleParts(t *testing.T) {
	b, c := ruleParts("FREQ=WEEKLY;COUNT=6;INTERVAL=2")
	if b != "FREQ=WEEKLY;INTERVAL=2" || c != 6 {
		t.Fatal(b, c)
	}
	if u := ruleUntil("FREQ=DAILY;UNTIL=20270430T000000Z"); u != "20270430T000000Z" {
		t.Fatal(u)
	}
	if ruleUntil("FREQ=DAILY") != "" {
		t.Fatal("kein UNTIL")
	}
}
