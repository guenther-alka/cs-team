package auth

// Verzeichnisprüfung über LDAP/AD. Die Anmeldung läuft bevorzugt über NTLM (das Passwort verlässt den Server nur
// versiegelt), danach wird der Benutzereintrag gelesen (DN, E-Mail, Gruppen). Die Aufnahme-Prüfung macht der Aufrufer.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strings"
	"time"

	ldap "github.com/go-ldap/ldap/v3"
)

const (
	dirDialTimeout = 10 * time.Second       // Verbindungsaufbau (eigenes Zeitlimit, siehe dialTCP)
	dirTimeout     = 8 * time.Second        // einzelne Anfrage (Bind/Suche)
	dirTotal       = 30 * time.Second       // Obergrenze für eine komplette Prüfung
	dirTries       = 2                      // Verbindungsversuche (der DC lässt nach Leerlauf gelegentlich keine neue zu)
	dirBackoff     = 250 * time.Millisecond // Pause vor dem zweiten Versuch
	dirWarmEvery   = 30 * time.Second       // Abstand des Warmhaltens (siehe WarmDir)
	// dirWarmTimeout: Zeitgrenze eines Warmhaltens. Sie darf großzügig sein: der erste Aufbau auf Port 389 braucht nach
	// Leerlauf gemessen 7 s bis 57 s (Windows wiederholt das SYN-Paket erst dann). Im Vordergrund wäre das zu lang, im
	// Hintergrund stört es niemanden.
	dirWarmTimeout = 75 * time.Second
)

// userAttrs: Attribute eines Benutzereintrags.
var userAttrs = []string{"sAMAccountName", "uid", "cn", "mail", "userPrincipalName", "memberOf"}

// ldapCheck: Verzeichnisprüfung gegen LDAP/AD (Einstellung: URL, Base, BindDN/BindPW, StartTLS).
type ldapCheck struct {
	url, base      string
	bindDN, bindPW string
	realm          string // Domäne für NTLM
	startTLS       bool
}

// ldapFrom: Prüfung für eine Einstellung; nil, wenn keine Adresse eingerichtet ist.
func ldapFrom(id Identity) DirChecker {
	if strings.TrimSpace(id.URL) == "" {
		return nil
	}
	return ldapCheck{url: strings.TrimSpace(id.URL), base: strings.TrimSpace(id.Base), bindDN: strings.TrimSpace(id.BindDN),
		bindPW: id.BindPW, realm: id.DirRealm(), startTLS: id.StartTLS}
}

// ldapSecure: läuft die Verbindung verschlüsselt?
func ldapSecure(c ldapCheck) bool {
	return c.startTLS || strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.url)), "ldaps://")
}

// hostOnly: Adresse zum Protokollieren (ohne Zugangsdaten und Pfad).
func hostOnly(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// CheckDir meldet den Benutzer am Verzeichnis an und liest seinen Eintrag (DN, E-Mail, Gruppen). Falsche Zugangsdaten
// ergeben ErrBadDir, technische Fehler ErrDirDown.
func (c ldapCheck) CheckDir(ctx context.Context, user, pass string) (DirUser, error) {
	user = strings.TrimSpace(user)
	if user == "" || pass == "" {
		return DirUser{}, ErrBadDir
	}
	ctx, cancel := context.WithTimeout(ctx, dirTotal) // Obergrenze: eine Anfrage darf nie beliebig lange hängen
	defer cancel()
	conn, err := bindDir(ctx, c, user, pass)
	if err != nil {
		return DirUser{}, err
	}
	defer conn.Close()
	stop := watchCtx(conn, ctx)
	defer stop()
	du, err := ldapUser(ctx, conn, c, user)
	if err != nil {
		return DirUser{}, err
	}
	if len(du.Groups) == 0 {
		du.Groups = ldapGroups(ctx, conn, c, du)
	}
	return du, nil
}

// bindDir: Benutzer am Verzeichnis anmelden. Zuerst NTLM mit den üblichen Domänenschreibweisen; spricht das
// Verzeichnis kein NTLM (oder ist NTLM nicht eingerichtet), wird ein einfacher Bind versucht.
// Wichtig: bei LDAP-Code 49 (ungültige Zugangsdaten) wird kein weiterer Bind versucht - sonst sperrt ein einziges
// falsches Passwort das Konto im Verzeichnis.
func bindDir(ctx context.Context, c ldapCheck, user, pass string) (*ldap.Conn, error) {
	conn, err := dialDir(ctx, c)
	if err != nil {
		log.Printf("directory: %s unreachable: %v", hostOnly(c.url), err)
		return simpleBind(ctx, c, user, pass)
	}
	conn.SetTimeout(dirTimeout)
	bad := false
	for _, dom := range ntlmDomains(c.realm) {
		err = conn.NTLMBind(dom, user, pass)
		if err == nil {
			return conn, nil
		}
		if errors.Is(dirErr(err), ErrBadDir) {
			bad = true // andere Domänenschreibweise probieren
			continue
		}
		log.Printf("directory: NTLM (%s, Domäne %q) nicht möglich: %v", hostOnly(c.url), dom, err)
		break
	}
	conn.Close()
	if bad {
		return nil, ErrBadDir
	}
	return simpleBind(ctx, c, user, pass)
}

// simpleBind: einfacher Bind (DN, UPN "name@realm" oder "DOMÄNE\name"). Eine fehlgeschlagene NTLM-Aushandlung
// hinterlässt eine unbrauchbare Verbindung, deshalb wird hier neu verbunden.
func simpleBind(ctx context.Context, c ldapCheck, user, pass string) (*ldap.Conn, error) {
	conn, err := dialDir(ctx, c)
	if err != nil {
		return nil, down(err)
	}
	conn.SetTimeout(dirTimeout)
	if c.bindDN != "" {
		if err := conn.Bind(c.bindDN, c.bindPW); err != nil {
			conn.Close()
			log.Printf("directory: Dienstkonto %s: %v", c.bindDN, err)
			return nil, down(err)
		}
	}
	names := bindNames(c.realm, user)
	if c.bindDN != "" { // manche Verzeichnisse können keinen UPN-Bind: den DN über das Dienstkonto suchen
		if dn := userDN(ctx, conn, c, user); dn != "" {
			names = append([]string{dn}, names...)
		}
	}
	last := error(ErrBadDir)
	for _, n := range names {
		err := conn.Bind(n, pass)
		if err == nil {
			return conn, nil
		}
		if last = dirErr(err); errors.Is(last, ErrDirDown) {
			break
		}
	}
	conn.Close()
	return nil, last
}

// dialDir: Verbindung aufbauen, mit einem zweiten Versuch nach kurzer Pause (siehe dirTries).
func dialDir(ctx context.Context, c ldapCheck) (*ldap.Conn, error) {
	var last error
	for i := 0; i < dirTries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(dirBackoff):
			}
		}
		conn, err := ldapDial(ctx, c)
		if err == nil {
			return conn, nil
		}
		last = err
		log.Printf("directory: Verbindung %s (%d/%d): %v", hostOnly(c.url), i+1, dirTries, err)
	}
	return nil, last
}

// ldapDial: Verbindung aufbauen (ldap:// oder ldaps://, optional StartTLS). Der Aufbau läuft mit eigenem Zeitlimit
// (dialTCP): Windows gibt einen hängenden Verbindungsversuch sonst erst nach etwa einer Minute auf.
func ldapDial(ctx context.Context, c ldapCheck) (*ldap.Conn, error) {
	addr, secure, err := dirAddr(c.url)
	if err != nil {
		return nil, err
	}
	nc, err := dialTCP(ctx, addr)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(addr)
	tc := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	if secure { // ldaps:// : TLS ab dem ersten Byte
		tlsConn := tls.Client(nc, tc)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			nc.Close()
			return nil, err
		}
		nc = tlsConn
	}
	conn := ldap.NewConn(nc, secure)
	conn.Start()
	conn.SetTimeout(dirTimeout)
	if c.startTLS {
		if err := conn.StartTLS(tc); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// dialTCP: TCP-Verbindung mit hartem Zeitlimit. net.Dialer.Timeout und DialContext greifen auf Windows bei einem
// hängenden Verbindungsversuch nicht zuverlässig, deshalb entscheidet hier ein eigener Zeitgeber; der Versuch im
// Hintergrund läuft aus und wird verworfen (Rückgabe über einen gepufferten Kanal, kein Goroutine-Leck).
func dialTCP(ctx context.Context, addr string) (net.Conn, error) {
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan net.Conn, 1)
	fail := make(chan error, 1)
	go func() {
		conn, err := (&net.Dialer{Timeout: dirDialTimeout}).DialContext(dctx, "tcp", addr)
		if err != nil {
			fail <- err
			return
		}
		done <- conn
	}()
	tmr := time.NewTimer(dirDialTimeout)
	defer tmr.Stop()
	select {
	case conn := <-done:
		return conn, nil
	case err := <-fail:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-tmr.C:
		return nil, fmt.Errorf("directory: connect %s: timeout after %s", addr, dirDialTimeout)
	}
}

// dirAddr: Adresse und Verschlüsselung aus der Einstellung ("ldap://host[:port]" oder "ldaps://host[:port]").
func dirAddr(raw string) (string, bool, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false, fmt.Errorf("directory address: %v", err)
	}
	if u.Host == "" {
		return "", false, errors.New("directory address: host missing")
	}
	secure := strings.EqualFold(u.Scheme, "ldaps")
	switch {
	case secure:
	case strings.EqualFold(u.Scheme, "ldap"):
	default:
		return "", false, errors.New("directory address: ldap://host[:port] or ldaps://host[:port]")
	}
	host := u.Host
	if u.Port() == "" {
		port := "389"
		if secure {
			port = "636"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	return host, secure, nil
}

// watchCtx: schließt die Verbindung, wenn die Anfrage endet (die LDAP-Bibliothek kennt keine Kontexte). Der
// zurückgegebene Aufruf beendet die Überwachung.
func watchCtx(conn *ldap.Conn, ctx context.Context) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// ntlmDomains: Schreibweisen der Domäne für NTLM. Active Directory akzeptiert je nach Einrichtung den DNS-Namen
// (local.de) oder den NetBIOS-Namen (LOCAL), deshalb werden beide probiert.
func ntlmDomains(realm string) []string {
	realm = strings.TrimSpace(realm)
	if realm == "" {
		return []string{""}
	}
	short := realm
	if i := strings.Index(realm, "."); i > 0 {
		short = realm[:i]
	}
	return dedup([]string{realm, short, strings.ToUpper(short)})
}

// bindNames: Namen für den einfachen Bind: UPN (Active Directory), NetBIOS-Form, zuletzt der Name allein.
func bindNames(realm, user string) []string {
	if strings.ContainsAny(user, "@\\") {
		return []string{user}
	}
	var out []string
	if realm = strings.TrimSpace(realm); realm != "" {
		short := realm
		if i := strings.Index(realm, "."); i > 0 {
			short = realm[:i]
		}
		out = append(out, user+"@"+realm, strings.ToUpper(short)+"\\"+user)
	}
	return append(dedup(out), user)
}

// dedup: Reihenfolge behalten, leere Einträge und Wiederholungen entfernen (uniq sortiert).
func dedup(l []string) []string {
	var out []string
	for _, x := range l {
		if x = strings.TrimSpace(x); x != "" && !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// firstOf: der erste nicht leere Wert.
func firstOf(xs ...string) string {
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			return x
		}
	}
	return ""
}

// userFilters: Suchfilter für einen Anmeldenamen - Active Directory zuerst, dann Verzeichnisse mit uid/cn.
func userFilters(user string) []string {
	u := ldap.EscapeFilter(user)
	return []string{
		"(&(objectClass=user)(sAMAccountName=" + u + "))",
		"(&(objectClass=person)(uid=" + u + "))",
		"(uid=" + u + ")",
		"(cn=" + u + ")",
	}
}

// ldapUser: Eintrag des Benutzers lesen. Findet die Suche nichts, genügt der gelungene Bind: dann gibt es nur den
// Namen (und keine Gruppen aus dem Eintrag).
func ldapUser(ctx context.Context, conn *ldap.Conn, c ldapCheck, user string) (DirUser, error) {
	du := DirUser{Name: user}
	if c.base == "" {
		return du, nil
	}
	for _, f := range userFilters(user) {
		res, err := conn.Search(ldap.NewSearchRequest(c.base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 0, false, f, userAttrs, nil))
		if err != nil {
			if errors.Is(dirErr(err), ErrDirDown) {
				return du, down(err)
			}
			continue // Filter passt nicht zu diesem Verzeichnis
		}
		if len(res.Entries) == 0 {
			continue
		}
		e := res.Entries[0]
		du.DN = e.DN
		du.Name = firstOf(e.GetAttributeValue("sAMAccountName"), e.GetAttributeValue("uid"), e.GetAttributeValue("cn"), user)
		du.Mail = firstOf(e.GetAttributeValue("mail"), e.GetAttributeValue("userPrincipalName"))
		for _, m := range e.GetAttributeValues("memberOf") {
			if g := cn(m); g != "" && !contains(du.Groups, g) {
				du.Groups = append(du.Groups, g)
			}
		}
		return du, nil
	}
	return du, nil
}

// userDN: DN des Benutzers (für den einfachen Bind; braucht die Suchbasis).
func userDN(ctx context.Context, conn *ldap.Conn, c ldapCheck, user string) string {
	du, _ := ldapUser(ctx, conn, c, user)
	return du.DN
}

// ldapGroups: Gruppen des Benutzers suchen, wenn der Eintrag kein memberOf liefert (z.B. OpenLDAP).
func ldapGroups(ctx context.Context, conn *ldap.Conn, c ldapCheck, du DirUser) []string {
	if du.DN == "" || c.base == "" {
		return nil
	}
	f := "(&(|(objectClass=group)(objectClass=groupOfNames)(objectClass=groupOfUniqueNames)(objectClass=posixGroup))(member=" +
		ldap.EscapeFilter(du.DN) + "))"
	res, err := conn.Search(ldap.NewSearchRequest(c.base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 500, 0, false, f, []string{"cn"}, nil))
	if err != nil {
		log.Printf("directory: Gruppen von %s: %v", du.DN, err)
		return nil
	}
	var out []string
	for _, e := range res.Entries {
		if g := firstOf(e.GetAttributeValue("cn"), cn(e.DN)); g != "" && !contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// dirErr: LDAP-Fehler auf die Fehler der Verzeichnisprüfung abbilden. Nur "ungültige Zugangsdaten" (Code 49) ist ein
// falsches Passwort; alles andere ist eine Störung - eine Störung darf nie als Passwortfehler gezählt werden.
func dirErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ErrDirDown
	case ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials):
		return ErrBadDir
	}
	return ErrDirDown
}

// down: technische Störung mit Ursache (errors.Is(err, ErrDirDown) bleibt wahr).
func down(err error) error {
	if err == nil {
		return ErrDirDown
	}
	return fmt.Errorf("%w: %v", ErrDirDown, err)
}

// WarmDir: hält den Weg zum Verzeichnis offen, solange der Prozess läuft (einmal beim Start aufrufen; weitere
// Aufrufe sind unschädlich). Hintergrund: Der Domänencontroller nimmt nach Leerlauf die erste Verbindung auf Port 389
// erst nach Sekunden an - Windows wiederholt das SYN-Paket erst dann (gemessen: 7 s bis 57 s) -, während andere Ports
// sofort antworten. Ohne Warmhalten wartet die erste Anmeldung nach einer Pause oder läuft in die Zeitgrenze.
func (a *Auth) WarmDir(ctx context.Context) {
	a.mu.Lock()
	if a.warm != nil {
		close(a.warm)
	}
	stop := make(chan struct{})
	a.warm = stop
	a.mu.Unlock()
	go a.warmDir(ctx, stop)
}

func (a *Auth) warmDir(ctx context.Context, stop chan struct{}) {
	var lastLog time.Time
	warm := func() {
		c, ok := a.dirCheck().(ldapCheck)
		if !ok {
			return // kein Verzeichnis eingerichtet (oder feste Prüfung in Tests)
		}
		if err := warmOnce(ctx, c); err != nil && time.Since(lastLog) > 10*time.Minute {
			lastLog = time.Now()
			log.Printf("directory: Warmhalten %s: %v", hostOnly(c.url), err)
		}
	}
	warm() // beim Start sofort, dann regelmäßig
	tick := time.NewTicker(dirWarmEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-tick.C:
			warm()
		}
	}
}

// warmOnce: eine Verbindung aufbauen und gleich wieder schließen (es werden keine Daten ausgetauscht).
func warmOnce(ctx context.Context, c ldapCheck) error {
	addr, _, err := dirAddr(c.url)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, dirWarmTimeout)
	defer cancel()
	conn, err := dialTCP(ctx, addr)
	if err != nil {
		return err
	}
	return conn.Close()
}
