package main

import (
	"strings"
	"testing"
)

// TestTaskGroupsNoSysGroups (0.69.2): die Gruppenauswahl der Aufgaben enthält nie die Systemgruppen globaladmins/groupadmins,
// auch nicht für globale Admins; Anlegen dort wird abgelehnt.
func TestTaskGroupsNoSysGroups(t *testing.T) {
	srv, _ := setup(t)
	defer srv.Close()
	pa := "passwort-anna"
	c, b := calDo(t, srv, "anna", pa, "GET", "/api/tasks", "")
	if c != 200 {
		t.Fatalf("tasks: %d %s", c, b)
	}
	if strings.Contains(b, `"name":"globaladmins"`) || strings.Contains(b, `"name":"groupadmins"`) {
		t.Fatalf("system groups offered: %s", b)
	}
	for _, g := range []string{"globaladmins", "groupadmins"} {
		if c, _ := calDo(t, srv, "anna", pa, "POST", "/api/tasks", `{"title":"x","group":"`+g+`"}`); c == 200 {
			t.Fatalf("task in %s accepted", g)
		}
	}
}
