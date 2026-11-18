//go:build windows

package osacct

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// aces: SIDs der Allow/Deny-Eintraege eines Objekts, getrennt nach explizit und geerbt.
func aces(t *testing.T, p string) (explicit, inherited map[string]bool) {
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, _ := sd.DACL()
	explicit, inherited = map[string]bool{}, map[string]bool{}
	for i := 0; dacl != nil && i < int(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, uint32(i), &ace) != nil || ace.Header.AceType > 1 {
			continue
		}
		s := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			inherited[s] = true
		} else {
			explicit[s] = true
		}
	}
	return
}

func TestSubstTree(t *testing.T) {
	const guests, users = "S-1-5-32-546", "S-1-5-32-545" // gleiche Laenge, auf jedem Windows vorhanden
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	os.MkdirAll(sub, 0755)
	f := filepath.Join(sub, "f.txt")
	os.WriteFile(f, []byte("x"), 0644)
	g, _ := windows.StringToSid(guests)
	// expliziter, vererbbarer Eintrag fuer Gaeste auf root (Kinder erben ihn)
	ea := []windows.EXPLICIT_ACCESS{{AccessPermissions: windows.GENERIC_READ, AccessMode: windows.GRANT_ACCESS,
		Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeValue: windows.TrusteeValueFromSID(g)}}}
	sd, _ := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	old, _, _ := sd.DACL()
	acl, err := windows.ACLFromEntries(ea, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if _, inh := aces(t, f); !inh[guests] {
		t.Fatal("setup: file does not inherit the guests entry")
	}
	u, _ := windows.StringToSid(users)
	n, ch, fails := SubstTree(root, map[string]*windows.SID{guests: u}, t.Logf)
	if fails != 0 || ch != 1 || n != 3 {
		t.Fatalf("SubstTree: n=%d changed=%d fails=%d (want 3/1/0)", n, ch, fails)
	}
	ex, _ := aces(t, root)
	if ex[guests] || !ex[users] {
		t.Fatalf("root explicit: %v", ex)
	}
	fe, fi := aces(t, f)
	if len(fe) != 0 || fi[guests] || !fi[users] {
		t.Fatalf("file: explicit %v inherited %v (inherited entry must follow, nothing explicit)", fe, fi)
	}
}
