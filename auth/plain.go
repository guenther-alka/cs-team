package auth

// OnPlain (0.72): wird mit Name und Klartext-Passwort aufgerufen, wenn cs-team das Passwort im Klartext kennt:
// erfolgreiche Anmeldung (Web, Basic, LDAP-Bindung), eigener Passwortwechsel, Startpasswort durch Admin, adduser.
// osacct setzt damit das SMB-Passwort (illumos) bzw. das Passwort des lokalen Windows-Kontos. Nil = aus.
// Startpasswoerter aus CSV-Import, Benutzerliste und Initialpasswort laufen ueber die Pflichtaenderung beim ersten Login.
// Der Aufruf ist synchron, damit die Reihenfolge stimmt (beim Wechsel erst das alte, dann das neue Passwort);
// OnPlain selbst darf nicht blockieren (osacct reiht nur in eine Warteschlange ein).
func (a *Auth) plain(name, pass string) {
	if f := a.OnPlain; f != nil && pass != "" {
		f(name, pass)
	}
}
