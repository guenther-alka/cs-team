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
  - Wo ein SMB-Passwort nicht uebernommen werden kann, markiert cs-team die Konten; beim ersten Login mit Klartext wird es ins System geschrieben.
- Passwort-Gewinn: cs-team speichert KEINEN NT-Hash; es setzt das Passwort beim Setzen/bei der ersten Anmeldung mit den Werkzeugen des
  Betriebssystems. Ein NT-Hash waere nur ein ausdruecklicher, getrennt gespeicherter Schalter fuer mehrere SMB-Server.
- Aufwand grob: Einstellung/Exportgeruest/adopt/Epoche rund eine Woche; je System 2-4 Tage.

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
