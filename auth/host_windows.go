//go:build windows

package auth

// Anmeldung über das Betriebssystem, wenn kein Verzeichnis (LDAP-URL) eingerichtet ist. Windows prüft Benutzer und
// Passwort selbst: LogonUser fragt die lokale Kontendatenbank (Rechner\Konto) oder einen Domänencontroller.
// Die Gruppen des Kontos kommen aus dem Anmeldetoken, damit die Aufnahme-Gruppen (Identity.AdmitGroups) auch ohne
// LDAP greifen. Das Passwort wird nur an Windows übergeben und nirgends gespeichert.

import (
	"context"
	"log"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Anmeldearten (winbase.h): Netzwerkanmeldung (Regelfall) und Netzwerkanmeldung mit Klartext-Passwort.
const (
	logonNetwork          = 3
	logonNetworkCleartext = 8
)

var procLogonUser = syscall.NewLazyDLL("advapi32.dll").NewProc("LogonUserW")

// hostCheck: Anmeldung gegen das Betriebssystem. realm ist der eingestellte Namensraum (Namensraum = Domäne);
// ohne Namensraum wird nur die lokale Kontendatenbank des Rechners befragt.
func hostCheck(ctx context.Context, realm, user, pass string) (DirUser, error) {
	if user == "" || pass == "" {
		return DirUser{}, ErrBadDir
	}
	domain := realm
	if domain == "" {
		domain = "." // "." = ausschließlich lokale Konten (Rechner\Konto)
	}
	tok, err := logon(user, domain, pass, logonNetwork)
	if err != nil && logonRefused(err) {
		// Diese Anmeldeart ist dem Konto nicht erlaubt (typisch für lokale Konten ohne "Zugriff vom Netzwerk"):
		// zweiter Versuch mit Klartext-Anmeldung - dieselben Kontodaten, andere Anmeldeart.
		tok, err = logon(user, domain, pass, logonNetworkCleartext)
	}
	if err != nil {
		return DirUser{}, logonError(err, user, domain)
	}
	defer tok.Close()
	return tokenUser(tok, user), nil
}

// logon: LogonUserW (advapi32). Das gelieferte Token muss geschlossen werden.
func logon(user, domain, pass string, typ uint32) (windows.Token, error) {
	u, err := syscall.UTF16PtrFromString(user)
	if err != nil {
		return 0, err
	}
	d, err := syscall.UTF16PtrFromString(domain)
	if err != nil {
		return 0, err
	}
	p, err := syscall.UTF16PtrFromString(pass)
	if err != nil {
		return 0, err
	}
	var tok uintptr
	r, _, e := procLogonUser.Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(p)),
		uintptr(typ), 0, uintptr(unsafe.Pointer(&tok)))
	if r == 0 {
		if e == nil {
			e = syscall.EINVAL
		}
		return 0, e
	}
	return windows.Token(tok), nil
}

// logonRefused: die Anmeldung scheiterte nicht an den Kontodaten, sondern weil diese Anmeldeart dem Konto nicht
// erlaubt ist - ein zweiter Versuch mit einer anderen Anmeldeart ist sinnvoll.
func logonRefused(err error) bool {
	return err == windows.ERROR_LOGON_TYPE_NOT_GRANTED || err == windows.ERROR_PRIVILEGE_NOT_HELD
}

// logonError: Windows-Fehler in eine Anmeldeantwort übersetzen. Falsche, gesperrte oder abgelaufene Konten:
// ErrBadDir (keine Anmeldung); technische Störungen (kein Domänencontroller, fehlendes Recht): ErrDirDown.
func logonError(err error, user, domain string) error {
	switch err {
	case windows.ERROR_LOGON_FAILURE, windows.ERROR_NO_SUCH_USER, windows.ERROR_ACCOUNT_RESTRICTION,
		windows.ERROR_INVALID_LOGON_HOURS, windows.ERROR_INVALID_WORKSTATION, windows.ERROR_PASSWORD_EXPIRED,
		windows.ERROR_ACCOUNT_DISABLED, windows.ERROR_ACCOUNT_EXPIRED, windows.ERROR_PASSWORD_MUST_CHANGE,
		windows.ERROR_ACCOUNT_LOCKED_OUT:
		log.Printf("host: %s\\%s refused by the operating system: %v", domain, user, err)
		return ErrBadDir
	case windows.ERROR_LOGON_TYPE_NOT_GRANTED, windows.ERROR_PRIVILEGE_NOT_HELD:
		log.Printf("host: %s\\%s may not log on here (%v): grant the account \"Access this computer from the network\" or set up the directory (LDAP)", domain, user, err)
	}
	log.Printf("host: logon of %s\\%s failed: %v", domain, user, err)
	return ErrDirDown
}

// tokenUser: Name und Gruppen aus dem Anmeldetoken. Die Gruppen sind ein Zusatz (Aufnahme-Prüfung): Lassen sie sich
// nicht lesen, wird die Anmeldung nicht verhindert, aber gemeldet.
func tokenUser(tok windows.Token, user string) DirUser {
	du := DirUser{Name: user}
	if tu, err := tok.GetTokenUser(); err == nil && tu.User.Sid != nil {
		if name, _, _, err := tu.User.Sid.LookupAccount(""); err == nil && name != "" {
			du.Name = name
		}
	}
	tg, err := tok.GetTokenGroups()
	if err != nil {
		log.Printf("host: groups of %q cannot be read (%v): group admission cannot be checked", du.Name, err)
		return du
	}
	for _, g := range tg.AllGroups() {
		if g.Sid == nil || g.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY != 0 {
			continue
		}
		name, dom, _, err := g.Sid.LookupAccount("")
		if err != nil || name == "" {
			continue
		}
		du.Groups = append(du.Groups, name) // Kurzname, z.B. "Lehrer"
		if dom != "" {
			du.Groups = append(du.Groups, dom+"\\"+name) // qualifiziert, z.B. "MY-W11\\Lehrer"
		}
	}
	if len(du.Groups) == 0 {
		log.Printf("host: no groups readable from the logon token of %q: group admission cannot be checked", du.Name)
	}
	return du
}
