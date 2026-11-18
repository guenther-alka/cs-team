# cs-team: zentrale Benutzerquelle und Failover (Spezifikation)

Stand: 2026-11-02. Entscheidungen aus dem Gespraech mit Gea. Nichts davon ist umgesetzt, ausser wo "erledigt" steht.
Grundsatz: KISS, eine Quelle der Wahrheit (cs-team), Betriebssystemkonten werden daraus abgeleitet.

## 1. Ziele
- cs-team haelt Benutzer, Gruppen und Passwoerter (bcrypt) zentral. Andere Rechner nutzen cs-team als Quelle (LDAPS).
- cs-team-Zugangsdaten sind zugleich SMB-Zugangsdaten auf dem Rechner, auf dem cs-team laeuft (Windows, illumos, Linux ...).
- Failover: cs-team kann auf einem anderen Rechner gleichen Betriebssystems mit den replizierten Daten (ZFS) als Master
  starten und uebernimmt, soweit moeglich, lokale Konten, SMB-Passwoerter und SID.
- Nicht Ziel: Domaenenbeitritt, Kerberos, Gruppenrichtlinien, Windows-Anmeldung an fremden PCs.

## 2. Datenmodell (Schritt 1)
- `Account.Uid`: feste numerische ID ab 10000, fortlaufend, nie wiederverwendet, aendert sich bei Umbenennung nicht.
- `Group.Gid`: wie Uid (eigener Zaehler), `Group.Short`: Kurzname fuer SMB-Gruppen (bis 8 Kleinbuchstaben/Ziffern), optional.
- `Group.Export`: Gruppe in LDAP und Betriebssystem anbieten (Standard aus, Systemgruppen nie).
- Gruppen in Gruppen werden beim Ausgeben aufgeloest (Account.Member existiert, resolveMembers); nach aussen nur flache Listen.
- Zaehler und Zuordnung stehen im Datenbestand (users.json/groups.json), werden also mit repliziert.
- Import/Export (bulk) fuehrt Uid/Gid mit; fehlen sie, werden sie beim Import vergeben.

## 3. LDAPS-Dienst (Schritt 3, nur lesen)
- Bibliothek gldap (MIT) oder eigener Handler auf glauth/ldap (BSD-3); go-ldap ist nur Client.
- Bind gegen bcrypt, Suche nach posixAccount/posixGroup, memberOf, memberUid (flach, aufgeloest).
- Lesekonto fuer Dienste, Sperre nach Fehlversuchen, nur LDAPS. Standard aus.
- Clients: SSSD/nslcd, illumos ldapclient, Anwendungen. Windows-PCs nur mit Zusatzsoftware (Credential Provider), nicht Teil des Plans.

Stand Umsetzung: Abschnitt 2 (uid/gid, Kurzname, Export-Schalter) ist erledigt in 0.66.0 (auth/ids.go, users/ids.json).

## 4. HA-Gruppe und Rollen
- Rollen nach Zustand, nicht nach Rechner: Master (aktiv: bedient Clients, hat die Dienst-IP, schreibt), Standby (schreibgeschuetzt
  auf der Replik), Aus. "Primary/Secondary" wird nicht verwendet; jeder Rechner der Gruppe kann Master werden.
- Einstellungen: HA-Gruppe = Liste der moeglichen Mitglieder (Name, Adresse, Netzwerkschnittstelle), Dienstname, virtuelle IP (optional).
  Die Liste ist auf allen Rechnern gleich und darf im replizierten Datenbestand liegen; cs-team erkennt sich selbst daran, dass eine
  Mitgliedsadresse auf diesem Rechner liegt.
- Anzeige in den cs-team-Einstellungen (nur lesen): Failover-Modus, eigener Zustand, Zustand der anderen Mitglieder, Epoche, Alter der Daten,
  was uebernommen wird (SMB-Passwoerter, SID).
- Rechner-lokale Angaben (zum Beispiel Schnittstelle, falls abweichend) liegen NICHT im replizierten Dataset (failover.cfg unter _cfg\team).

## 5. Startoptionen (CS_FAILOVER)
1. `master`: Master starten, Failover erlauben. Schreibt den Failover-Zustand in den Datenbestand (failover/), prueft beim Start und
   regelmaessig, ob ein anderes Mitglied ebenfalls Master meldet.
2. `standby`: nur lesen. Anmeldung (ohne Schreiben von "zuletzt gesehen"/Sperrzaehler), Lesezugriffe, LDAP-Bind. Schreibzugriffe werden mit
   Hinweis abgelehnt; keine Hintergrundauftraege, keine Benachrichtigungen. Standard, wenn CS_VIP gesetzt ist und die Adresse nicht auf diesem
   Rechner liegt. Zeigt das Alter der Daten. Prueft regelmaessig users.json/groups.json auf Lesbarkeit und doppelte uid/gid ("Replik in Ordnung").
3. `takeover`: Sekundaerrechner wird Master und aktualisiert lokale Daten (siehe Abschnitt 7), danach Start mit hoeherer Epoche.
4. Ohne Option: Failover aus, alles wie bisher.

## 6. Pruefungen beim Start als Master
1. Dienst-IP darf nicht von einem anderen Rechner belegt sein (ping/TCP auf den Dienstport); liegt sie lokal, ist das in Ordnung.
2. Kein anderes Mitglied meldet sich als Master (Standby ist erlaubt). Antwortet ein Mitglied nicht, fragt das napp-it-Menue nach
   ("Mitglied X antwortet nicht. Ist er sicher aus?"). Echter Schutz gegen Netzwerktrennung nur mit Zeuge oder Ausschalten per IPMI (nicht KISS).
3. Eigene Epoche >= gemeldete Epoche des Masters. Ein ersetzter alter Master startet deshalb nur als Standby; Zurueckwechseln ist ein bewusster Schritt.
4. Hochstufen im selben Dataset (ohne Klon): vorher pruefen, dass die ZFS-Eigenschaft `written` = 0 ist (nichts seit dem letzten Snapshot
   geschrieben); sonst Rueckfall auf Klon vom letzten Snapshot. Dataset auf dem Standby: readonly=on, atime=off.
5. Beim Wechsel Zwischenspeicher verwerfen oder cs-team neu starten (Neustart ist am einfachsten; Web-Sitzungen gehen dabei verloren).
- Geplanter Wechsel: Master schreibgeschuetzt/anhalten, letzten Snapshot senden, dann hochstufen (kein Datenverlust).
  Ausfall: es gilt der letzte empfangene Snapshot.

## 7. Export und Uebernahme (privilegiert)
- Unterbefehle derselben Programmdatei, als root/Administrator vom napp-it-Menue, Cron oder Taskplaner aufgerufen; der Webdienst bekommt
  keine Administratorrechte: `cs-team failover export | adopt | vip up|down`.
- `export` schreibt in den Datenbestand (failover/, mit repliziert): Rechnerkennung/Version/Zeit, Konfiguration (Geheimnisse nur verschluesselt
  auf eigenen Schalter), Liste der verwalteten Betriebssystemkonten und Gruppen mit uid/gid, SMB-Zustand je System.
- `adopt` (nur gleiches Betriebssystem): legt Konten/Gruppen mit denselben uid/gid an, spielt den SMB-Zustand ein, erhoeht die Epoche, startet cs-team.
- Nur Konten anfassen, die cs-team selbst angelegt und gekennzeichnet hat (Kontobeschreibung "cs-team"); nie bestehende Administratorkonten.
- Pro System (soweit moeglich):
  - illumos: /var/smb/smbpasswd kopieren, SMF-Eigenschaften system/idmap config/machine_sid und config/machine_uuid uebernehmen
    (gleiche Rechner-SID; mit gleichen uid/gid ergeben sich gleiche lokale SIDs), idmap-Namensregeln (idmap list) export/import,
    Konten per useradd -u / groupadd -g, SMB-Gruppen per smbadm aus den cs-team-Daten neu aufbauen. Nicht ausprobiert, Test auf zwei OmniOS-Rechnern noetig.
  - Linux mit Samba: Samba-Passwortdatenbank und lokale Domaenen-SID exportieren/einspielen.
  - Windows: keine Hash- und SID-Uebernahme moeglich; Konten per Windows-API anlegen, Passwoerter beim ersten cs-team-Login ins System schreiben.
    Konzept fuer Windows-HA mit erhaltenen SMB-Rechten (Name als Schluessel, SID-Uebersetzung beim Hochstufen): Abschnitt 12.
  - Wo ein SMB-Passwort nicht uebernommen werden kann, markiert cs-team die Konten; beim ersten Login mit Klartext wird es ins System geschrieben.
- Passwort-Gewinn: cs-team speichert KEINEN NT-Hash; es setzt das Passwort beim Setzen/bei der ersten Anmeldung mit den Werkzeugen des
  Betriebssystems. Ein NT-Hash waere nur ein ausdruecklicher, getrennt gespeicherter Schalter fuer mehrere SMB-Server.
- Aufwand grob: Einstellung/Exportgeruest/adopt/Epoche rund eine Woche; je System 2-4 Tage.

**Stand 0.72 (umgesetzt fuer illumos und Windows, Paket osacct, Schalter CS_OSACCOUNTS=1):** Entscheidung Gea 2026-11-18: Konten und
Passwoerter im laufenden cs-team (laeuft ohnehin als root/Administrator), nicht nur als Unterbefehl; nur Mitglieder exportierter Gruppen.
- Abgleich beim Start, jede Minute und bei einem Passwort fuer einen unbekannten Namen; Konten mit Kommentar "cs-team", sperren statt loeschen.
- Passwort ueber auth.OnPlain (Anmeldung, Wechsel, Startpasswort); illumos NT-Hash in /var/smb/smbpasswd (libsmb-Format: Halbbytes je Byte
  vertauscht), Windows NetUserSetInfo. Kein NT-Hash in users.json; die smbpasswd-Zeilen der verwalteten Konten stehen aber in
  failover/osaccounts.json (repliziert, noetig fuer die Uebernahme der SMB-Passwoerter auf illumos).
- export = laufender Abgleich des Masters (failover/osaccounts.json: Konten, Gruppen, gesperrte, je Rechner SID/UUID/smbpasswd bzw. Windows-SIDs,
  Last = letzter Master). adopt = automatisch beim Start als master auf einem anderen Rechner als Last, oder `cs-team osaccounts adopt|adopt-dry`.
- Nicht umgesetzt: Linux/FreeBSD/macOS, SMB-Gruppen per smbadm (Unix-Gruppen reichen fuer die ACL), Weiterreichen der Windows-Passwoerter an den Standby.
- Livetest 2026-11-18 (0.72.0-test1, zwei OmniOS-Rechner): Ziel = ZFS-Klon von .189 (pve VM 104, DHCP 192.168.2.33, Name omnios58b),
  vorher eigene Rechner-SID/-UUID gesetzt, anna/ben, Gruppen und smbpasswd-Zeilen entfernt; SMB von Debian .190 auf das Ziel abgelehnt.
  Start mit CS_FAILOVER=takeover: adopt setzte machine_sid/machine_uuid von .189, legte anna 10002/ben 10003 und alluser/lehrer/klasse5a an,
  spielte beide smbpasswd-Zeilen ein, startete idmap und smb/server neu (Dauer ca. 10 s); danach SMB von .190 mit den alten Passwoertern:
  anna lesen ja/schreiben nein, ben schreiben ja; idmap uid:10002 -> gleiche SID wie auf .189 (-11002). last = omnios58b.

## 8. Dienst-IP (virtuell)
- Setzen nicht dauerhaft (muss einen Neustart nicht ueberleben), vorher auf "frei" pruefen, nachher bekanntmachen (ARP).
- Windows: New-NetIPAddress -SkipAsSource $true -PolicyStore ActiveStore / Remove-NetIPAddress.
- Linux: ip addr add ... dev eth0, danach arping -U; ip addr del.
- illumos und Solaris 11: ipadm create-addr -t -T static -a ... <if>/csvip (Solaris 11: -a local=...) / ipadm delete-addr.
- FreeBSD: ifconfig <if> alias ... / -alias. macOS: sudo ifconfig <if> alias ... netmask ... / -alias.
- Solaris: nur Solaris 11 (napp-it kennt kein Solaris 10). Auf anderen Systemen startet cs-team ohne virtuelle IP; Umschalten dann per DNS.
- Nur im selben Subnetz. Dienst muss auf allen Adressen lauschen; Zertifikat auf den Dienstnamen.

## 9. napp-it-Menue (System > Services > cs-team)
- Eingabe: HA-Gruppe, Dienstname, virtuelle IP, Schnittstelle, Dataset. Aktionen: Export jetzt, Uebernahme pruefen (Probelauf),
  Hochstufen (nur auf einem Standby, mit Rueckfrage; Abbruch, solange ein Master antwortet), Zurueckstufen auf dem alten Master nach Neuaufsetzen.
- Replikationsjob: getrennt von cs-team und von Hand angelegt und verwaltet (Master -> Standby); das Menue legt keinen an.
  Es zeigt nur das Datenalter (Alter des letzten Snapshots auf dem Standby) und erinnert nach dem Hochstufen daran, die Richtung des Jobs umzukehren.
  Dasselbe Hinweis steht in den cs-team-Einstellungen (Feld Hochverfuegbarkeit / Failover).

## 10. Reihenfolge
1. uid/gid, Kurzname, Export-Schalter, aufgeloeste Gruppen (erledigt in 0.66.0).
2. Startoptionen, Standby, Epoche, Pruefungen, Anzeige in den Einstellungen (erledigt in 0.67.0, Test mit zwei Instanzen bestanden).
3. napp-it-Menue, privilegierte Unterbefehle, Dienst-IP. **Dienst-IP erledigt in 0.69.0** (Master setzt sie beim Start vorlaeufig, entfernt sie beim Beenden; `cs-team vipdown`; getestet OmniOS, macOS, Windows; Linux/FreeBSD/Solaris nur als Befehl geprueft). Eigener Name wird automatisch erkannt.
4. illumos: SMB-Passwoerter, SID, Konten (danach Linux/Samba, dann Windows).
5. LDAPS-Dienst (Abschnitt 3), unabhaengig von 2-4. **Erledigt in 0.68.0** (eigener Minimal-LDAPv3-Server in ldapd/, Env CS_LDAP/CS_LDAP_BASE/CS_LDAP_READER, auf OmniOS mit ldapsearch geprueft; offen: ob der illumos-SMB-Kernel LDAP-Gruppen liest). Hinweis: Failover mit Erhalt der SMB-Rechte funktioniert am besten mit OmniOS (SID als ZFS-Attribut, ACLs in Replikaten).
6. Test mit zwei virtuellen Rechnern je System.

## 11. Offene Punkte (noch zu pruefen)
- Ob SMB-Dienst auf illumos die Gruppen ueber nsswitch (ldap) liest (_getgroupsbymember): im Test pruefen.
- Maschinen-SID auf illumos wirklich per SMF-Eigenschaft uebernehmbar; Neustart von idmap und smb/server noetig?
- Passwort in smbpasswd ohne Terminal setzen (passwd ueber PAM aus einem Programm).
- Ob ZFS recv inkrementell auf ein eingehaengtes, schreibgeschuetztes Dataset laeuft, waehrend cs-team liest.
- Ob smbadm lokale Gruppen in Gruppen kann (wird nicht gebraucht, da aufgeloest wird).
- Gruppen mit mehr als 16 gids: NFS (AUTH_SYS) und illumos ngroups_max (Standard 16) abschneiden; Schalter "Export" je Gruppe haelt die Liste klein.

## 12. Windows als SMB-Server mit HA: Name als Schluessel, SID-Uebersetzung (Konzept 2026-11-17, umgesetzt 0.72 ohne Passwort-Weitergabe)
Stand 0.72: Konten/Gruppen per netapi32, SID-Tabelle in failover/osaccounts.json (hosts.<name>.winsid), beim Hochstufen ersetzt cs-team
Besitzer und explizite ACL-Eintraege in CS_OSACCOUNTS_SHARES ueber die Windows-API (SubstTree), nicht mit icacls: /substitute wirkt nur mit
/save + /restore und macht dabei geerbte Eintraege explizit (Test .103). Passwoerter werden nicht an den Standby weitergereicht (Punkt 2
unten): nach dem Hochstufen gilt das SMB-Passwort ab der naechsten cs-team-Anmeldung. Test auf einem Rechner (.103) mit simuliertem altem Master.
Ausgangslage (Gespraech mit Gea 2026-11-17, Tests auf my-w11 und w11.103):
- Windows speichert in ACLs (NTFS und OpenZFS on Windows) nur SIDs, nie Namen. Lokale SIDs = Rechner-SID + RID; die RID vergibt die SAM,
  sie ist nicht vorgebbar. Jeder Windows-Server hat eine andere Rechner-SID, also hat derselbe Benutzer auf Server A und B verschiedene SIDs.
- pGina hilft nicht: es greift nur bei der Anmeldung am Bildschirm (nicht bei SMB-Netzwerkanmeldungen) und kann keine SID setzen; mit
  remove_profiles=yes entsteht bei jeder Anmeldung ein neues Konto (neue RID, belegt: anna -1007 am 03.11. angelegt und geloescht), mit no
  bleibt die SID nur auf diesem einen Rechner gleich. Fuer SMB-Rechte ist das egal: der Server prueft mit seiner eigenen SID.
- OpenZFS on Windows (zfswin-2.4.4rc9) ignoriert uid/gid und POSIX-ACLs eines Pools (Test: Pool mit acltype=posix von pve importiert,
  Besitzer = angemeldeter Windows-Benutzer, Standard-DACL). Ein Pool-Wechsel Windows <-> Linux mit erhaltenen Rechten ist kein Ziel.
- Ein SID-Feld in cs-team/LDAP haette keinen Abnehmer (Windows uebernimmt SIDs nur von einem Domaenencontroller). Nicht bauen.

Prinzip:
- Der **Benutzername** (und Gruppenname) ist der feste Schluessel. Die **uid/gid** kommt aus cs-team (ids.go, unabhaengig von jeder SID) und
  gilt fuer Unix-Clients und OmniOS. Die **SID** ist je Windows-Server verschieden und wird ueber den Namen uebersetzt.
- cs-team fuehrt eine Tabelle im Datenbestand (mit repliziert), z.B. failover/winsid.json:
  `{"users":{"anna":{"uid":10005,"sid":{"srvA":"S-1-5-21-<A>-1007","srvB":"S-1-5-21-<B>-1012"}}},"groups":{"klasse5a":{"gid":10010,"sid":{...}}}}`
  Schluessel des Servers = HA-Mitgliedsname. Die alte SID von A muss vorher bekannt sein: nach dem Ausfall von A kann B sie nicht mehr aufloesen.

Ablauf:
1. Konten auf allen HA-Mitgliedern: cs-team legt fuer jeden lokalen cs-team-Benutzer (Name ohne @, hoechstens 20 Zeichen) ein lokales
   Windows-Konto an (Windows-API NetUserAdd/NetUserSetInfo, Beschreibung "cs-team"), fuer jede exportierte Gruppe eine lokale Gruppe mit
   den Mitgliedern. Auf dem Master sofort, auf Standby-Mitgliedern ebenfalls (Standby fuehrt nur diese Kontoaktionen aus, sonst nur lesen).
   Gesperrt/abgelaufen/geloescht in cs-team -> lokales Konto sperren, NIE loeschen (sonst SID weg). Die SID jedes Kontos landet in der Tabelle.
2. Passwort: cs-team kennt nur bcrypt; es setzt das Windows-Passwort im Klartextmoment (Anmeldung, Aenderung, Initialpasswort, Reset) lokal
   und reicht es ueber die HA-Verbindung (TLS, nur an Mitglieder der HA-Gruppe) an die Standby-Mitglieder weiter. War ein Standby aus,
   wird das Passwort nach dem Hochstufen bei der naechsten cs-team-Anmeldung nachgezogen (reine SMB-Nutzer bis dahin mit altem Passwort).
   Kein NT-Hash im Datenbestand (Windows hat keine Schnittstelle zum Setzen eines Hashes).
3. Hochstufen von B (napp-it-Menue / `cs-team failover adopt`): fuer jeden Namen der Tabelle alte SID (A) -> neue SID (B) in allen
   Share-Pfaden ersetzen, mit Windows-Bordmitteln: `icacls <share> /substitute <SID-A> <SID-B> [<SID-A2> <SID-B2> ...] /T /C /Q`
   (mehrere Paare je Aufruf). Danach gelten auf B dieselben Rechte fuer dieselben Namen. Dauer waechst mit der Dateizahl
   (Minuten bis Stunden); bis zum Ende stimmen die Rechte nicht. Fortschritt und Fehler ins Log, Probelauf ohne Aenderung moeglich.
   Neu angelegte Dateien auf B tragen B-SIDs; beim Zurueckschwenken auf A dieselbe Uebersetzung in Gegenrichtung.
4. Freigaben (Share-Namen, Share-Rechte) gehoeren zum Export-Zustand (Abschnitt 7) und werden auf B mit den B-SIDs neu angelegt.

Alternative ohne Umschreiben (heikel, nicht empfohlen): Standby als Klon ohne Sysprep (gleiche Rechner-SID) und Konten auf beiden Servern in
exakt derselben Reihenfolge anlegen -> gleiche RIDs, gleiche SIDs. Ein einziges von Hand angelegtes lokales Konto verschiebt alle folgenden
RIDs; cs-team muesste die SIDs aller Mitglieder laufend vergleichen und bei Abweichung warnen.

Bewertung: funktioniert ohne Domaene und ohne Kerberos (KISS), aber mit Umschaltzeit fuer die ACL-Uebersetzung. Failover mit sofort gueltigen
Rechten bleibt mit OmniOS einfacher (ACLs mit uid, SID aus uid + uebernommener Rechner-SID, siehe Livetest unten). Mit Domaene (Samba-AD)
waeren die SIDs auf allen Servern gleich, das ist aber nicht Ziel (Abschnitt 1).

Offene Punkte Windows:
- Ob OpenZFS on Windows unter Windows gesetzte ACLs (SIDs lokaler Konten) dauerhaft speichert und repliziert (Test noetig; im Importtest kamen
  nur Standard-ACLs). Bei NTFS als Share-Ablage: Replikation nicht ueber ZFS, dann robocopy /MIR /SEC oder Storage Replica.
- Laufzeit von icacls /substitute bei 10^5 bis 10^6 Dateien messen.
- Unix-Clients: pam_mount in cs-ldapclient (Share beim Login mit dem LDAP-Passwort mounten, mount.cifs username=%u).


## Livetest Failover OmniOS .189 -> .44 (November 2026)

Aufbau: Replikation per zfs send/recv, Takeover auf .44, danach Tests mit Benutzern, Passwoertern, SMB-Passwoertern, Unix-Gruppen, SMB-Gruppen und SMB-Datei-ACL.

**Phase A (nur Replikation + Takeover):**
- OK: cs-team-Benutzer, -Gruppen, -Passwoerter; ACL im Dataset erhalten (zunaechst nur numerisch).
- Fehlt auf dem Ziel: Unix-Konten/-Gruppen, SMB-Passwoerter (/var/smb/smbpasswd), SMB-Gruppen (smbgroup.db), Maschinen-SID. SMB-Anmeldung scheitert.

**Phase B (manuelles Uebernehmen, validiert):**
1. useradd -u / groupadd -g mit denselben ids wie auf dem Master.
2. idmap-SMF-Eigenschaften config/machine_sid und config/machine_uuid setzen; idmap und smb/server neu starten.
3. smbpasswd-Zeilen der Benutzer zusammenfuehren.
4. SMB-Gruppen mit smbadm create + add-member neu anlegen (gleiche SID).
- Ergebnis: Anmeldung mit alten SMB-Passwoertern OK, falsches Passwort abgelehnt, Lesen/Anlegen/Ueberschreiben gemaess ACL wie auf dem Master, ACL loesen wieder in Namen auf.

**Hinweise fuer ein eingebautes `adopt`:**
- cs-team-uid (z.B. 10001) kann von der OS-uid (z.B. 20001) abweichen; adopt muss die ids angleichen.
- idmap und smb/server muessen neu gestartet werden.
- Die Uhr des Standby muss stimmen (Test: .44 stand auf Maerz 2026).
- Testumgebung wurde zurueckgebaut (.44 SID/smbpasswd wiederhergestellt, Test-Datasets entfernt).
