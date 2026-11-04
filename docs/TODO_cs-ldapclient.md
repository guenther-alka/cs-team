# TODO: cs-ldapclient (Stand 2026-11-03)

Ziel: kleines Go-Programm `cs-ldapclient.exe` im cs-team-Repo (eigenes Programm, eigene Release-Datei),
das ein lokaler Administrator startet und das pGina einrichtet. Interaktiv UND mit Parametern.

## Funktionen
1. Prueft Administratorrechte und ob ein lokales Admin-Konto mit Passwort existiert (Rueckweg).
2. Installiert pGina 3.9.9.12 still (Inno Setup, /VERYSILENT /SUPPRESSMSGBOXES /NORESTART); Installer mitgeliefert oder per Download.
3. Fragt: LDAP-Server (IP), Basis-DN (Standard dc=csteam,dc=local), Leser-Konto + Passwort (CS_LDAP_READER).
   Holt das Server-Zertifikat per TLS, zeigt den Fingerprint, speichert PEM nach C:\Program Files\pGina.fork\.
4. Gruppenregeln (z.B. klasse5a -> Benutzer, lehrer -> Administratoren); lokale Gruppen ueber SIDs aufloesen
   (S-1-5-32-545 Users, S-1-5-32-544 Administrators), sprachunabhaengig.
5. Probeanmeldung (LDAPS-Bind) vor dem Scharfschalten; Vorab: Uhrzeit, Port 636 erreichbar.
6. `cs-ldapclient --remove` nimmt alles zurueck (Richtlinien, pGina deinstallieren, Einstellungen).
7. Parameter-Modus fuer Verteilung auf viele Rechner (--server, --base, --reader, --group klasse5a=Users ...).

## Einstellungen (aus dem Test 2026-11-03, Windows 11, pGina 3.9.9.12)
- Plugins: LDAP {0f52390b-c781-43ae-bd62-553c77fa4cf7} Maske 15 (UI+Auth+Authz+Gateway),
  LocalMachine {12fa152d-a2e3-4c8d-9535-5dcd49dfcb6d} Maske 27 (UI+Auth+Gateway+Notification). Bits: UI=1 Auth=2 Authz=4 Gateway=8 Notification=16 ChangePw=32.
- Reihenfolge Authentication UND Gateway: LDAP vor LocalMachine (sonst keine Gruppen).
- LDAP-Plugin: LdapHost, LdapPort=636, UseSsl=1, RequireCert=1, ServerCertFile, DnPattern uid=%u,ou=people,<base>,
  DoSearch=0, SearchDN=<Leser-DN>, SearchPW (verschluesselt, SetEncryptedSetting), UseAuthBindForAuthzAndGateway=0,
  AuthzRequireAuth=1, AuthzDefault=1.
- Gruppenregel-Format (Tabs): `0<T>0<T>cn=<gruppe>,ou=groups,<base><T>(memberUid=%u)<T><lokale Gruppe>`.
- LocalMachine: RemoveProfiles=true, ScramblePasswords=true (Konto + Profil nach Abmeldung loeschen).
- Windows-Richtlinien: HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System dontdisplaylastusername=1,
  DontDisplayLockedUserId=3 -> Anmeldebildschirm nur "Anderer Benutzer" + pGina.
- Kurze Kontoerstellung (NOCH NICHT GETESTET): OOBE DisablePrivacyExperience=1, System EnableFirstLogonAnimation=0,
  CloudContent DisableWindowsConsumerFeatures=1.

## Haken / zu testen
- Registry allein wirkte bei Gateway-Reihenfolge und Gruppenregeln nicht zuverlaessig; gesetzt wurde es ueber pGinas
  Schnittstelle (pGinaDynamicSettings, PowerShell/C# mit den DLLs in C:\Program Files\pGina.fork). Pruefen: Dienst
  `pgina` anhalten, Registry schreiben, Dienst starten - sonst PowerShell-Skript aus Go heraus.
- Test-VM: Proxmox VM 108 "w11" (192.168.2.103, user me, SSH). Snapshot `pre-pgina` zum Zuruecksetzen.
- Offen: Test "Konto/Profil nach Abmeldung geloescht" (anna), Rueckweg ueber lokales Konto `me` ("Anderer Benutzer"),
  Richtlinien fuer kurze Kontoerstellung; Rueckweg im Handbuch (PDF) beschreiben (Kap. 19.7 DE+EN).
- Testserver fuer pGina-Tests: auf .189 per C:\opt\tmp\cs-team\ldap_srv.ps1 starten (laeuft aktuell, Dataset tank/csldap;
  danach abbauen: pkill cs-team, zfs destroy -r tank/csldap); lehrer189.sh stellt Gruppe lehrer ein (ben).
- Offener Vorschlag: cs-team-LDAP so aendern, dass Benutzer die Gruppen sehen, in denen sie selbst Mitglied sind
  (dann braeuchte pGina kein Leser-Konto).
- Neue PDFs (pGina getestet) liegen nur lokal in C:\opt\nappweb\pdf, nicht im GitHub-Release 0.68.0 / napp-it.org.
- Proxmox .112 rpool noch ~79% voll (ISO 14 GB, vm-101-state-Reste ~11.7 GB) - nicht geloescht.

## Update 2026-11-03: ldap.cfg + Menue
- cs-ldapclient zeigt Menue: Install / Deinstall / Einstellungen anzeigen+aendern / Test. Parameter: install -y, uninstall, show, set k=v, test, -cfg.
- Einstellungen in ldap.cfg neben der exe (USB-Stick): nach Erstinstall genuegt Klick auf exe -> Install.
- -y verlangt cert_sha256 in ldap.cfg (kein blindes Vertrauen).
- Stand: Windows-Code kompiliert, Update-Pfad auf VM 108 getestet; offen: frische Installation, Deinstall, Profilloeschung, Rueckweg, Policies, Handbuch/PDF, Unit-Tests, build-all.ps1.

## Weitere Clients im Classroom (KISS): FreeBSD, macOS, Linux
Ziel: gleiche ldap.cfg, gleiche Idee wie pGina: Anmeldung mit cs-team-Konto, Heimatverzeichnis beim ersten Login, Login-Screen nur Name+Passwort, lokaler Admin als Rueckweg.
- Linux: SSSD (id_provider=ldap, ldaps, ldap_schema=rfc2307, ldap_group_member=memberUid, Lesekonto als Bind, CA nach /etc/sssd), pam_mkhomedir, GDM/LightDM disable-user-list. Offline-Cache = Rueckweg.
- FreeBSD: nss_ldap/pam_ldap (nslcd via nss-pam-ldapd) oder SSSD aus ports, mkhomedir.
- macOS: LDAPv3 in Open Directory (dsconfigldap), CA in System-Keychain, RFC2307-Mapping, Login-Fenster Name+Passwort, ggf. Mobile Account.
- Alle: cs-team muss POSIX-Attribute liefern (uidNumber, gidNumber, homeDirectory, loginShell) oder Client ueberschreibt sie (override_homedir, default_shell). UID-Strategie klaeren.
- Profil beim Logout loeschen: nicht eingebaut, per Skript/tmpfs-Home.
- Umsetzung: cs-ldapclient mit GOOS=linux/freebsd/darwin bauen, run_unix.go schreibt Konfiguration aus ldap.cfg (gleiche Befehle/Menue).
- Erst pruefen: liefert cs-team die POSIX-Attribute schon?

## Entscheidung 2026-11-03 (Gea)
- Ein Go-Installer je OS (Windows, Linux, FreeBSD, macOS), ldap.cfg daneben, Start als Admin/root -> fertig. Gleiches Menue/Verhalten wie unter Windows (Install / Deinstall / Einstellungen / Test, -y).
- Kein Perl-Skript. Gemeinsamer Code: cfg.go, ldapcheck.go, Menue; pro OS nur die Umsetzung (run_linux.go, run_freebsd.go, run_darwin.go).
- Reihenfolge: Windows fertig testen -> POSIX-Attribute in cs-team pruefen -> Linux -> FreeBSD -> macOS.


## Namen (Gea 2026-11-03)
Installer je Plattform: cs-ldapclient-windows.exe, cs-ldapclient-linux, cs-ldapclient-freebsd, cs-ldapclient-osx (+ ldap.cfg daneben). USB-Stick mit allen Installern + einer ldap.cfg einstecken, den passenden starten (als Admin/root).


Regel: Was geladen werden muss (z.B. pGinaSetup*.exe, sssd-Pakete-Hinweis), zuerst im Programmordner suchen, erst dann Download; Download wird dort abgelegt.

## USB-Stick / ZIP (Gea 2026-11-03)
- Eine ZIP mit allen Installern: cs-ldapclient-windows.exe, -linux, -freebsd, -osx, ldap.cfg, start-linux.sh, start-freebsd.sh, start-osx.command. Entpacken auf FAT-Stick, starten.
- FAT hat keine Unix-Rechte (kein x-Bit, ZIP verliert es auch), Linux haengt Sticks oft noexec ein -> Startskript: Programm nach /tmp kopieren, chmod +x, als root starten mit -cfg <Stick>/ldap.cfg. Aufruf: sudo sh start-linux.sh (FreeBSD analog, macOS .command).
- Zertifikat (ldap.pem) und Installer-/Paketsuche relativ zum Ordner der ldap.cfg, nicht zur exe (Programm laeuft aus /tmp). Auch unter Windows umstellen.
- Download-Regel bleibt: erst im Ordner der ldap.cfg suchen, dann laden und dort ablegen.
- build-all.ps1: ZIP cs-ldapclient.zip mit allen Plattformen + Startskripten + Beispiel-ldap.cfg erzeugen.

## Update 2026-11-03 (spaet): Stand
- Windows, Linux (Debian 12), FreeBSD (15.1) getestet; ZIP + Startskripte in build-all.ps1 (cmd/cs-ldapclient/pack).
- macOS: GELOEST in 0.68.2 (ODConfiguration-API mit Timeouts 30/15/120, vollstaendige ODRecordMaps, anonymer Bind mit CS_LDAP_ANON=1; Admin-Rechte ueber sudoers.d, weil verschachtelte Gruppen in admin nicht greifen). Offen: arm64 auf echter Hardware, Mehrfach-Server, Hostname statt IP. Alte Notiz: macOS war NICHT unterstuetzt. Open Directory (ODConfiguration-API und Verzeichnisdienstprogramm) legt den Knoten an, bleibt aber offline: opendirectoryd "Failed to get rootDSE with error -9" - gleich mit LDAPS (packetEncryption 3), ohne Verschluesselung (Proxy auf 389) und mit Hostname statt IP; Zertifikat/Vertrauen ok (security verify-cert, ldapsearch ok). Mitschnitt: Apple sucht rootDSE (defaultNamingContext, namingContexts), subschemaSubentry, dann objectClasses von cn=Subschema, trennt dann. Ideen: Schema vollstaendiger (ldapSyntaxes, matchingRules), anderes macOS testen, Referenzserver (OpenLDAP) zum Vergleich.
- Test-Knoten /LDAPv3/192.168.2.189 und /LDAPv3/omnios58 auf der Mac-VM 102 im Verzeichnisdienstprogramm entfernen.
