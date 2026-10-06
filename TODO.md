# TODO

## Arbeitsstand (zum Wiederaufnehmen nach Unterbrechung)

Gea 2026-10-02: Zaehlung ab jetzt 0.50.0 (0.1x = erste Tests, 1.0 wenn ausgereifter). TODOs einzeln abarbeiten, je mit Empfehlung (AskUserQuestion) und Umsetzung nach Bestaetigung; nur wichtige Punkte, kleine zuerst; TODO.md nach jedem Punkt aktualisieren. Veroeffentlichung nur nach Gea-Go (Sync zuvor automatisch). Release-Skripte: rel/all<NNNN>.ps1 (Vorlage rel/all0153.ps1), Quellen/Skripte nach C:\opt\tmp\cs-team per device_commit_files (neue Dateinamen!).
RELEASED 2026-10-02: 0.50.0 (vorher kurz als 0.5.0 veroeffentlicht, ersetzt; git f5964c2 + Umbenennung). Naechste Punkte: 4 Aufgaben, 5 Kalender-Stundenraster, 6 Calc-Ausbau, 7 Aufgaben-UI.
PLAN ZUM ABSCHLUSS VON 0.50.0 (Gea 2026-10-02, Nutzungslimit knapp): nur noch (a) Handbuch de/en + README fuer Zahlenformate, (b) Release 0.50.0 nach Geas Go (Skripte wie 0.15.3). Punkte 4-7 (Aufgaben, Kalender-Stundenraster, Calc-Ausbau, Aufgaben-UI) NACH 0.50.0 als 0.6.x.
0.51 CALC-AUSBAU (Gea 2026-10-02, alle vier bestaetigt; je Punkt einzeln, nach jedem TODO aktualisieren):
 A. [erledigt, unveroeffentlicht; Tests calc4-7 gruen; Handbuch+CHANGELOG ok] Funktionen SUMIF/COUNTIF/AVERAGEIF, IFERROR, VLOOKUP, CONCAT, LEFT/RIGHT/MID, TEXT, TODAY/DATE, MEDIAN (+ deutsche Namen)
 B. [erledigt, unveroeffentlicht; Tests calc4-7 gruen; Handbuch+CHANGELOG ok] Spaltenbreite per Maus (geteilt), Ausrichtung links/Mitte/rechts, Zeilenumbruch
 C. [erledigt, unveroeffentlicht; Tests calc4-7 gruen; Handbuch+CHANGELOG ok] Sortieren (Bereich auf/ab) + erste Zeile/Spalte fixieren
 D. [erledigt, unveroeffentlicht; Tests calc4-7 gruen; Handbuch+CHANGELOG ok] Kontextmenue (Rechtsklick) Inhalt/Format/Alles loeschen
 Alle vier fertig; offen nur: main.go version 0.51.0, PDFs, Release 0.51.0 nach Geas Go (Skripte *0510, entry53, h510pdf).
0.52 KALENDER-STUNDENRASTER (Gea 2026-10-02, 0.51.0 ist auf GitHub verifiziert): Umfang vollstaendig, Raster 15 Min: Zeitachse Tag/Woche mit Terminbloecken, Klick/Ziehen auf Leerflaeche = neuer Termin mit Zeit,
 Termin ziehen = Tag+Uhrzeit, unteren Rand ziehen = Dauer, Jetzt-Linie, Standard-Scroll 7:00. [erledigt, unveroeffentlicht: 0.52.0; neue Termine nur per Doppelklick (Gea); Tests cal/cal2/cal3 gruen; Handbuch+CHANGELOG ok; offen: Release nach Go; Touch-Bedienung; Jetzt-Linie aktualisiert sich nur beim Neuzeichnen] Danach Freie-Zeiten-Pruefung, Belegungsplan, Mehrfachbuchung.
0.53 AUFGABEN-RECHTE (Gea 2026-10-02): [erledigt, unveroeffentlicht] globale Admins legen in jeder Gruppe an; Anfrage-Modus fuer Mitglieder bei 'nur Gruppen-Admins' (direkt sichtbar+uebernehmbar). Offen: Release nach Go; Aufgaben-UI (Schnellzeile, Tafel, Abhaken, Kalender-Markierung), KI-Vorschlaege.
0.54 ANMELDUNG AM VERZEICHNIS (IDENTITY) (Gea 2026-10-05): Phasen 1-5 [erledigt, unveroeffentlicht] Anmeldung per Verzeichnis (LDAP/AD) und per Windows-Domaene neben den lokalen Konten: Name@Namensraum, Anzeige-Namensraum, Aufnahme-/cs-team-Gruppe, Zwischenspeicher.
 Belege: auth/ldap.go, auth/dir.go, auth/host_windows.go, auth/host_other.go, auth/identity.go, chat/settings.go (POST /api/settings/auth), web/index.html (idPanel, Anmeldehinweis auf der Konto-Seite), web/lang/_template.json + en.json (27 neue Texte), webui_test.go (neu: jeder t('...')-Text muss im Katalog stehen, sid*-Felder zwischen Anlegen und Speichern geprueft).
 Stand: go vet + go test ./... gruen (auth: Dir/LDAP/Windows- und Cache-Tests), Builds windows/amd64 + linux/amd64 ok, JS-Syntax von index.html ok; howto.ai\cs-team.info um die 0.54-Abschnitte ergaenzt (Sicherung C:\opt\tmp\cs-team\bak\cs-team.info.pre_csteam0540); Commits 996fde9 (Phase 4) und 72a8934 (Phase 5).
 Offen: (a) Release 0.54.0 nach Geas Go: main.go version, CHANGELOG.md, C:\opt\changelog.txt, PDFs (Skripte *0540, entry54, h540pdf; Vorlage rel\all0153.ps1), Tag + GitHub; (b) Handbuch de/en + README: Anmeldung, Einstellungen und Startparameter CS_IDENTITY_* beschreiben (steht noch in keiner Datei); (c) Uebersetzungen der 27 neuen Texte fuer ar, cn, es, fr, it, ru, tr (Helfer C:\opt\tmp-lang\lang-keys.js + add-keys.js; bis dahin Anzeige auf Deutsch); (d) Handpruefung im Browser (Einstellungen, Konto-Seite) und Live-Test gegen echtes AD/LDAP (auth/ldap_live_test.go laeuft nur mit gesetzten Env-Variablen, Windows-Anmeldung mit Domaenenkonto).
0.55 ROLLEN UND ZUGEHOERIGKEIT NACH DER KISS-REGEL (Gea 2026-10-05, in Plan-Mode abgestimmt; Umsetzung offen):
 Drei Quellen, drei Zwecke (Leitlinie, siehe auch unten):
 (1) cs-team-Konten (users.json) sind fuer Verantwortung und Gastzugriff da: globaler Admin ("admin":true) und
     Gruppen-Admin (roles "ga:<gruppe>", am Konto, nur fuer cs-team-Konten) sowie Gaeste ohne Verzeichniskonto.
     Lokale cs-team-Konten sind in JEDEM Modus anmeldefaehig (Notfallzugang; immer mindestens ein aktiver lokaler Admin).
 (2) cs-team-Gruppen (groups.json) sind die Basis der cs-team-Kommunikation (Chat, Kalender, Aufgaben, Text, Calc,
     Dateien, Freigaben, Units); ihre Mitglieder duerfen aus dem Verzeichnis kommen.
 (3) Normale Nutzer werden in host/AD/LDAP-Gruppen verwaltet; cs-team liest sie nur beim Login und bildet sie auf
     Gruppenmitgliedschaft ab (Textliste: "@csg" = cs-team-Untergruppe, "DOMAENE\gruppe" = Verzeichnisgruppe,
     cs-team-Konto/Gruppenname = cs-team).
 Merksatz: Zugehoerigkeit darf aus dem Verzeichnis kommen, Verantwortung nie.
 Betriebsarten - die Regel gilt in allen, ohne Sonderfall:
 (a) SOHO / kleines Team: NUR lokale cs-team-Konten (Mode leer bzw. "local" = Standard). Keine host/AD/LDAP-Quelle
     noetig, keine Realm-/URL-Pflicht; Konten, Gruppen, Rollen und Rechte sind lokal. Erster Admin per
     CS_ADMIN_USER/CS_ADMIN_PASS, Anmeldung "anna" (bzw. "anna@local").
 (b) Schule mit Verzeichnis: normale Nutzer in host/AD/LDAP-Gruppen (Satz 3), Verwaltung und Gaeste als lokale
     cs-team-Konten (Satz 1) - das Verzeichnis liefert nur Zugehoerigkeit.
 (c) Gross (Ausbau): zusaetzlich verschachtelte Verzeichnisgruppen und Host-Token-Gruppen. Unveraendert: Verantwortung
     bleibt lokal.
 Invariante: Ohne Verzeichnis ist cs-team vollstaendig, das Verzeichnis ist immer nur eine ZUSATZ-Quelle fuer
 Zugehoerigkeit, und kein Einrichtungsschritt darf ein Verzeichnis verlangen.
 Phasen:
 1. KERN (klein, hoher Nutzen): (a) lokale Konten in jedem Modus erlauben - Identity.LocalOK immer true, Login-Zweig
    auth.go:486 (ErrNoLocal) entfaellt, Default Mode ""/"local" und DefaultGroup-Fallback bleiben, UI-Checkbox
    "lokale Konten zusaetzlich erlauben" nur bei eingerichtetem Verzeichnis zeigen; (b) Rollen am Konto
    (Account.Roles "ga:<gruppe>", globaler Admin bleibt "admin:true"), nur fuer Konten mit Source != "dir"; adminOf
    liest das Konto, adminsOf(group) als abgeleitete Liste fuer Anzeige/CSV/Jahrgangswechsel; Verwalter-Rollen darf
    nur der globale Admin setzen; (c) letzter aktiver LOKALER Admin geschuetzt (ErrLastAdm auf lokale Konten
    erweitern, SetFlags/Loeschen/Sperren); (d) Zugehoerigkeit aus dem Verzeichnis: du.Groups (LDAP/AD bzw. Windows-
    Host-Token) ueber die Textlisten der Gruppen auf Mitgliedschaft abbilden, Account.DirGroups am Konto fuehren
    (ensureDir/dirStale: Ein-Gruppen-Zwang entfaellt); (e) Handbuch/README.
    Belege: auth/identity.go (LocalOK 99, ensureDir 325, dirStale 380, verifyDir 269), auth/auth.go (Login 486,
    ErrLastAdm 318, dropAdminRefs 349), auth/groups.go (SetMembers 742, adminOf 188, SetGroupAdmins 796),
    auth/routine.go (stay 585), main.go:74.
 2. VERSCHACHTELTE VERZEICHNISGRUPPEN + HOST-TOKEN: AD rekursiv (1.2.840.113556.1.4.1941), OpenLDAP-Fallback,
    AD-Primaergruppe; unter LDAP zusaetzlich Windows-Token-Gruppen per CS_IDENTITY_HOST_GROUPS=1; Windows-Host-Pfad
    (Realm = Rechnername) als vollwertige Quelle.
 3. ANZEIGE-CACHE + ROLLENVERWEIS: Cache group->members nur fuer die Anzeige (TTL 30-60 s, nie Rechtequelle);
    Rollenverweis "ga:@cs-team-gruppe" fuer den Fachschaftsfall (eine Zeile statt vieler Konten).
 4. JAHRGANGSWECHSEL traegt die .dir-Zuordnungen mit (rename.go/routine.go).
 STAND 2026-10-06 (Gea entschied (b)-(e) wie empfohlen): Phase 1 (a),(c),(d),(e) sind umgesetzt und seit 0.57.0 veroeffentlicht (lokale Konten in jedem Modus, hasLocalAdmin, Group.Dir/Sub/effGroups, "dir"=="mixed", Verzeichnis-Eintraege nur durch globale Admins).
 (b) Rollen (Gea 2026-10-06): Gruppen sind immer lokal, Konten auch remote. SYSADMIN-Konto = Konto aus CS_ADMIN_USER (Altbestand: aeltestes aktives lokales Admin-Konto), lokal, immer Admin, nicht loeschbar/sperrbar/herabstufbar, Passwort nur selbst oder per Kommandozeile ("cs-team sysadmin NAME"); weitere globale Admins und Gruppen-Admins beliebig (lokal oder Verzeichnis, Rolle immer lokal vergeben). [erledigt, unveroeffentlicht 0.57.1; Handbuch Rollen-Tabelle ergaenzt]
 Offen: Phase 2-4 (verschachtelte Verzeichnisgruppen, Anzeige-Cache + Rollenverweis, Jahrgangswechsel mit .dir).
 Offen (Entscheidungen von Gea) - erledigt: (b) Praefix-Regel blank/"\"/"@" in den Textlisten, (c) Verzeichnis-Eintraege in
 Mitgliederlisten nur durch den globalen Admin, (d) Account.Groups bleibt Handliste vs. alles in der Gruppenliste,
 (e) "nur Verzeichnis" und "Verzeichnis + lokal" zusammenlegen. Zwischenspeicher-Empfehlung 7-14 Tage (CacheDays).
 Doku: howto.ai\cs-team.info um den 0.55-Abschnitt "Who is managed where" ergaenzt (Sicherung
 C:\opt\tmp\cs-team\bak\cs-team.info.pre_csteam0550) und Fehler in Zeile 113 korrigiert (CS_IDENTITY_CACHE_DAYS
 ist eine Tageszahl, "1 = on" war falsch).
0.56 HIERARCHISCHE KALENDER (Gea 2026-10-05, in Arbeit; Bereichspraefixe <user>, @<gruppe>, +<organisation>, _global):
 eigene, Gruppen-, Organisations- und globale Kalender plus externe Abos (nur lesbar) in einer Liste, von innen
 nach aussen sortiert. Verantwortlich aendern und freigeben nur: persoenlich der Benutzer, Gruppe die Gruppen-Admins,
 Organisation/global die globalen Admins; Mitglieder lesen, "rw" erlaubt ihnen das Eintragen, "off" (nicht
 freigegeben) zeigt den Kalender nur den Verantwortlichen. Gruppen-Admins sehen ihren Gruppenkalender auch ohne
 Mitgliedschaft. Externe ICS-Abos (nur lesbar, alle 30 Min.) kann jeder fuer sich anlegen; fuer die Gruppe der
 Gruppen-Admin, fuer Organisation/global die globalen Admins.
 Stand 2026-10-05: [umgesetzt, unveroeffentlicht; go build/vet + go test ./... gruen]
   - cal/caldav.go: access0 mit released() ("off"/""/"ro"/"rw"), list() beruecksichtigt AdminOf (Gruppen-Admins
     ohne Mitgliedschaft), Doku-Kopf.
   - cal/api.go: row()-Helfer, apiCreate (scope user|group|unit|global, mode off|ro|rw), apiEdit = PUT /api/cal/{kal}
     (Name, Beschreibung, Freigabe, Ressource, Abo-URL setzen/wechseln/beenden; Abos bleiben immer nur lesbar).
   - web/index.html: Kalender-Leiste zeigt Freigabe + "Bearbeiten", Cal-Maske mit "nicht freigegeben",
     Listen-Hinweis "(nicht freigegeben)"; web/lang/*.json um 11 Texte ergaenzt (Helfer C:\opt\langadd2.ps1,
     Pruefung C:\opt\langcheck.ps1).
   - cal_scope_test.go (neu): Gruppen-Admin ohne Mitgliedschaft, Freigabe off/ro/rw, globaler und
     Organisations-Kalender (Gruppe der Organisation zugeordnet), Abo fuer sich und fuer die Gruppe.
   - cal_scope_test.go: Helfer calDo/calList auf Paketebene gezogen (TestCalendarScope nutzt sie jetzt) und neu
     TestCalDAVScope - der Blick des CalDAV-Clients auf dieselben Rechte: PROPFIND zeigt Gruppe + freigegebenes
     Global, der Entwurf ("off") fehlt und antwortet per direktem Zugriff 404, PUT/GET im Gruppenkalender,
     403 bei "ro" und bei Abos, Export enthaelt den per CalDAV geschriebenen Termin.
   - cal_scope_test.go: TestCalDAVOwnCalendar - die eigenen Kalender per CalDAV: PUT/GET/DELETE erlaubt, zweiter
     eigener Kalender per MKCOL (MKCALENDAR 405), eigenes Abo 403, Bereich "Kalender" nur lesend (Vorlage klasse) 403.
   - Doku (c) 2026-10-05: README.de.md/README.md (CalDAV-Abschnitt: Sichtbarkeit und Schreibrecht je Freigabe, Abos nur
     lesbar, Export/Import je Kalender; Abschnitt "Grenzen" korrigiert: Freigaben gibt es jetzt), Handbuch de/en
     (man_src/content_de.py + content_en.py, Abschnitt 4.4 Kalender: hierarchische Liste, Freigabe, Abo und
     "Thunderbird, Apple Kalender, iPhone (CalDAV)" mit Export/Import), CHANGELOG.md (0.56.0). Pruefung der
     Oberflaeche: doppelte Element-IDs nur in Dialogen, die nie gleichzeitig sichtbar sind (z.B. "cgo"
     Kontakt/Anlegen, "cdel" nur innerhalb eines Ternarys); die neuen Kalender-Kennungen sind eindeutig.
   - mitgezogen: main_test.go TestUnits (Antwort enthaelt members -> ohne schliessende Klammer),
     auth/members_test.go (Untergruppe @grundschule; Selbstbezug @klasse5a in klasse5a jetzt 400).
   - Befund + Fix: web/index.html hatte in der Gruppen-Ansicht einen JS-Syntaxfehler aus der vorigen Sitzung
     (zwei Anweisungen in einer Zeile: '...'+ h+='...'), der die GESAMTE Oberflaeche lahmlegte (der Browser
     fuehrt ein Skript mit Syntaxfehler nicht aus). Neu: webui_test.go TestWebUISyntax prueft jeden <script>-Block
     mit "node --check" (wird uebersprungen, wenn Node.js fehlt); Hilfsskripte C:\\opt\\jscheck.ps1, C:\\opt\\langcheck.ps1.
   - cal: Gruppenkalender auch aus den Gruppen-Einstellungen verwalten (POST /api/groups/{name} kennt jetzt "cal"):
     "" = keiner bzw. entfernen (nur solange er leer ist, sonst 409), "off" = Entwurf, "ro", "rw". Neu
     cal.GroupCalState + cal.SetGroupCalendar (meta.Mode bzw. anlegen/entfernen), auth.GroupCalState + auth.OnGroupCal
     (Haken in main.go), auth.ErrCalUsed (409 in fail_), GET /api/groups nennt "cal" je Gruppe (ausserhalb der Sperre
     gelesen), Oberflaeche: Auswahl "Gruppenkalender (Freigabe)" im Gruppendialog + Anzeige in der Gruppenliste
     (web/index.html, 2 neue Texte in web/lang/*.json, Sprachdateien per jsoncheck geprueft).
     Pruefung: cal_scope_test.go TestGroupCalendarRelease (Gruppen-Admin 403, ungueltiger Wert 400, rw -> Mitglied
     schreibt, ro -> 403, off -> Entwurf nur fuer Verantwortliche, entfernen mit Termin 409 / ohne 200, neu freigeben);
     go vet ./... und go test ./... gruen, auch mit CS_TEST_FS=1.
 Offen: (a) Handpruefung im Browser, (b) Handbuch-PDFs bauen (Texte sind fertig; nur auf dem Linux-Rechner moeglich,
 weil man_src/build.py DejaVu-Schriften unter /usr/share/fonts/truetype/dejavu erwartet: python3 build.py de
 ../h560pdf/cs-team_de.pdf, dann en; Vorlage/Ergebnis-Ordner h530pdf), (c) Release nach Geas Go: main.go version, CHANGELOG.md
 (Eintrag 0.56.0 vorbereitet, bei gemeinsamer Veroeffentlichung mit 0.54/0.55 umzunummerieren), changelog.txt,
 Binaries/PDFs, Tag + GitHub.

 0.58 (Gea 2026-10-06, "hoch umsetzen" + "0.58 bauen"): [erledigt, unveroeffentlicht] Dunkelmodus + Tastaturfokus + Tastaturbedienung der Listen, Lang-Druck im Kalender (Touch-Ersatz fuer Doppelklick), Aufgaben-Faelligkeiten im Kalender, Sprachen uk/pl/el/ja, Handbuch Kapitel 10 (Einstellungen) + 10.6 Sprachen. Im Browser per DevTools geprueft (Dunkelmodus, Touch, Fokus); offen: Handpruefung auf echtem Handy/Tablet, Screenreader-Test, manuelles Gegenlesen der neuen Sprachen, Release 0.58.0 nach Geas Go (build-all.ps1 -Test, Skripte *0580, PDFs sind gebaut).

Reihenfolge (nach Nutzen):
1. [erledigt, veroeffentlicht 0.50.0] Version-Tooltip am Titel "cs-team" + Versionsstand 0.50.0 (/api/me liefert version).
2. [erledigt, veroeffentlicht 0.50.0] Calc Strg+Z/Y.
3. [erledigt, veroeffentlicht 0.50.0] Calc Zahlenformate: Knoepfe EUR/%/.0+/.0-, Tokens d0-d9/pc/cu (reFmt), Eingabe-Erkennung (12,5% / 12,50 EUR, in Prozentzellen 5 = 5 %), Tests doc/lww_test.go + e2e calc3.py gruen. Offen: Handbuch de/en + CHANGELOG-Text fuer Zahlenformate; xlsx-Export/Import schreibt Formate (auch fett etc.) generell noch nicht - eigener Punkt; Einfuegen intern uebernimmt Zellformat noch nicht.
4. Aufgaben: globale Admins duerfen fuer jede Gruppe anlegen; "Bitte bearbeiten"-Aufgabe fuer Mitglieder in Gruppen mit Modus "nur Gruppen-Admins" (sichtbar/uebernehmbar fuer alle Mitglieder).
5. Kalender: Stundenraster (Tages-/Wochenansicht), danach Freie-Zeiten-Pruefung, Belegungsplan, Mehrfachbuchung.
6. Calc: Spaltenbreite, Ausrichtung, Rahmen, Funktionen (SUMIF, COUNTIF, VLOOKUP, IFERROR, CONCAT, LEFT/RIGHT/MID, TEXT, TODAY/DATE, MEDIAN), Sortieren/Filtern/Fixieren, Diagramme, KI-Anbindung.
7. Aufgaben-UI (Schnellzeile, Tafel, Abhaken, Kalender-Markierung), KI-Aufgabenvorschlaege.
Nach jedem Punkt: Test (Go + Browser), Handbuch de/en, CHANGELOG, diese Liste.

- (nichts offen) Handbuch cs-team_de.pdf / cs-team_en.pdf am 2026-10-01 auf Stand 0.13 aktualisiert (Quellen: C:\opt\tmp\cs-team\man_src).
- Audit 0.11-Liste in 0.13.2 umgesetzt (S-03/04, S-06, S-07, S-08, S-10, K-02/C-10, K-03, C-06, F7). Mittel-Punkte S-09, S-11, K-05, F4, F8, F9 in 0.13.9 umgesetzt; A-04 (beabsichtigt), C-07, C-08/C-09, C-11, C-13/C-14 in 0.14.1. Offen aus AUDIT.md: C-12 (nur Dokumentation), WebDAV-Range im RAM-Speicher; LOCK und Papierkorb in 0.14.2 umgesetzt; Quota-Uebersicht fuer Admins in 0.15.0.
- KI Stufe 2 erledigt fuer neue Dokumente (0.13.3); KI liest PDFs erledigt (0.13.5, Text-PDFs, kein OCR); offen: Aufgaben/Termine per KI vorschlagen, KI-Vorlagenentwuerfe, OCR fuer gescannte PDFs.
- Chat: Videochat. Umgesetzt: 0.13.7 drei externe Plaetze (Jitsi, MiroTalk, eigene URL mit {room}; fester Raum je Gruppe, Ad-hoc Gruppen-Admins, Ad-hoc alle Schreiber; Raumname per HMAC, Einladung 24 h); 0.13.8 Platz 4 eingebautes WebRTC (Mesh, bis 6, STUN/TURN in den Einstellungen). Offen: spaeter BigBlueButton (Ad-hoc durch Gruppen-Admin, API mit Geheimnis); Einladung optional per "Nachricht" (E-Mail/Webhook); Moderation/JWT bei Link-Anbietern; WebRTC: Bildschirm teilen, Sprecher-Hervorhebung, SFU fuer mehr als 6 (nur bei Bedarf).

- Kalender 0.14.0: Serientermine in der Anzeige, Zeitzonen-Fallback und Terminbearbeitung (nur dieser / alle) umgesetzt. In 0.15.0 umgesetzt: Serien "dieser und folgende", Erinnerungen (VALARM), Teilnehmer mit Einladungsmail, Abo-Status/Aktualisieren-Knopf, ICS-Import/-Export, VTIMEZONE. Offen: Handpruefung mit Thunderbird, iOS, Outlook sowie Explorer/Word/Excel (LOCK); Zu-/Absagen der Eingeladenen (iMIP-Antworten) auswerten; Einladungen an Teilnehmer einzelner Serien-Ausnahmen.
- Text-Editor 0.15.2 (durchgehendes Feld): offen/zu beobachten: Eingabemethoden (IME) und Mobilgeraete, Firefox/Safari, sehr grosse Texte (Neuzeichnen nur geaenderter Absaetze); Sperren auf Zeichenbereiche statt Absaetze waere ein eigener grosser Schritt.
- Kalender 0.15.3: Maussteuerung (Klick/Umschalt+Klick, Ziehen, Strg+X/C/V). Offen: Verschieben der Uhrzeit per Maus (braucht eine Zeitachse in Wochen-/Tagesansicht), Touch-Bedienung (Ziehen/Lang-Druck), Kopieren in einen anderen Kalender.
- Calc: Sperre beim Markieren und Kopieren aus gesperrten Zellen in 0.15.3 umgesetzt.
- Leitlinie Kalender (Gea, 2026-10-02): flexibel und KISS, intuitives Arbeiten hat Vorrang; fuer komplexe Vorgaenge (Terminsuche mit mehreren Personen/Ressourcen, Mehrfachbuchung, Serien verschieben) KI-Unterstuetzung einplanen.
- Leitlinie Rollen und Zugehoerigkeit (Gea, 2026-10-05, KISS): Zugehoerigkeit darf aus dem Verzeichnis kommen, Verantwortung nie. cs-team-Konten (users.json) fuer Admin-Rollen und Gaeste, cs-team-Gruppen (groups.json) fuer die Kommunikation, normale Nutzer in host/AD/LDAP-Gruppen; ohne Verzeichnis (SOHO/kleines Team) bleibt alles lokal. Einzelheiten, Betriebsarten und Phasen: 0.55.
- Kalender/Ressourcen (Ideen, Reihenfolge): 1 Stundenraster in Tages-/Wochenansicht (Ziehen/Verschieben mit Uhrzeit), 3 Freie-Zeiten-Pruefung im Formular (wer hat gebucht, naechster freier Termin), 2 Belegungsplan je Ressource, 4 Mehrfachbuchung Raum+Beamer, 5 Eigenschaften/Gruppen von Ressourcen, 6 Genehmigung, 7 Schule: Vertretung, Ferien auslassen, Druck/PDF. Komplexes per KI.
- Calc: Spalten-/Zeilenkopf-Klick markiert schon die ganze Spalte/Zeile (Entf = Inhalt, x-Knopf = Format loeschen). Offen: Kontextmenue 'Inhalt/Format/Alles loeschen'; spaeter Zeilen/Spalten einfuegen+loeschen mit Formelanpassung (gross). Entf loescht heute nur Inhalt, Format bleibt (Gea 2026-10-02).
- Calc-Ausbau (Vorschlag, Gea fragte 2026-10-02): Version 'Calc 1': Strg+Z/Y, Zahlenformate (Waehrung, Prozent, Datum, Nachkommastellen), Spaltenbreite per Maus, Ausrichtung, Rahmen, Zeilenumbruch, Funktionen SUMIF/COUNTIF/AVERAGEIF, VLOOKUP/XLOOKUP, IFERROR, CONCAT, LEFT/RIGHT/MID, TEXT, TODAY/DATE, MEDIAN; danach Sortieren/Filtern/Fixieren; Diagramme und KI-Anbindung (Zellen setzen/formatieren/erklaeren); gross: mehrere Blaetter.
- Aufgaben (Fragen Gea 2026-10-02): (1) globale Admins duerfen Aufgaben fuer jede Gruppe anlegen (heute nur als Mitglied, canCreate); (2) 'Bitte bearbeiten'-Aufgabe fuer Mitglieder in Gruppen mit Modus 'nur Gruppen-Admins': fuer alle Gruppenmitglieder sichtbar und uebernehmbar (Filter 'Bitte bearbeiten' und Uebernehmen gibt es schon), Ersteller/Admins aendern und nehmen ab; optional Admin-Freigabe vorher. UI: Schnellzeile (Titel+Enter, Kuerzel/KI fuer @Person, Datum, Prioritaet), Tafelansicht mit Ziehen, Abhaken in der Liste + Zaehler, Faelligkeiten im Kalender, Gruppieren/Sortieren/Filter nach Gruppe+Person, Projekt-Zeitleiste; KI: Aufgaben aus Chat/Text vorschlagen, Wochenueberblick, Aufgabe in Meilensteine zerlegen. Wartet auf Gea-Freigabe.
- Versionierung (Gea 2026-10-02): ab jetzt 0.50.0 (0.1x = erste Tests), 1.0 wenn ausgereifter. Punkt 'Version beim Mouseover auf cs-team' umgesetzt (0.50.0, noch nicht veroeffentlicht).
- Abarbeitung (Gea): alle TODOs einzeln, je mit Empfehlung und Umsetzung nach Bestaetigung; Reihenfolge nach Nutzen, kleine zuerst.
- Calc Strg+Z/Y umgesetzt (0.50.0, noch nicht veroeffentlicht). Naechster Punkt: Zahlenformate.

KONZEPTE ZU DEN GROSSEN THEMEN (Review 2026-10-06; nur Konzept, nichts umgesetzt; Gea entscheidet):
 A. LOGIN-FORMULAR STATT BASIC AUTH (Logout fehlt, Browser behaelt Zugangsdaten, CSRF haengt an Browser-Headern, Passwort geht bei jeder Anfrage mit)
    1. Session-Cookie fuer die Weboberflaeche (HttpOnly, SameSite=Strict, Secure bei TLS), Basic Auth bleibt fuer WebDAV/CalDAV und Tools.
       Pro: Logout, Sitzungsende, CSRF-Schutz grundsaetzlich (Token + SameSite), Passwort nur beim Login (LDAP-Last sinkt). Contra: Sitzungsspeicher
       (Neustart/Cluster), mehr Code in auth/main/index.html, zwei Anmeldewege zu testen. Aufwand mittel bis gross.
    2. Wie 1, aber zustandslos (signiertes Token mit Ablauf, Schluessel in der Konfiguration). Pro: kein Sitzungsspeicher. Contra: Widerruf
       (Konto sperren, Passwort aendern) nur ueber kurze Laufzeit/Konto-Zaehler. Aufwand mittel.
    3. Basic Auth behalten, nur "Abmelden" per 401-Trick und Header-Pruefung verschaerfen. Pro: klein. Contra: Logout in Firefox/Safari unsicher,
       Grundproblem bleibt.
    Empfehlung: 1 (Serverseitige Sitzungen im Speicher, Ablauf 8 h, Widerruf bei Passwortwechsel/Sperre; Neustart = neu anmelden ist fuer die
    Zielgruppe akzeptabel).
 B. BARRIEREFREIHEIT UND DUNKELMODUS (heute: 0 aria-label, 0 role, 1 tabindex, kein prefers-color-scheme, 3 @media; wichtig fuer Schulen)
    1. Schrittweise nur in index.html: Rollen/aria-Labels fuer Navigation, Tabellen, Dialoge (die neuen Dialoge sind schon role=dialog), sichtbarer
       Tastaturfokus, Farbvariablen (CSS custom properties) + @media (prefers-color-scheme: dark), prefers-reduced-motion. Pro: wenig Risiko, sofort
       nutzbar. Contra: viele Einzelstellen, Farben sind heute fest im CSS (erst auf Variablen umstellen). Aufwand mittel.
    2. Nur Dunkelmodus + Fokus, Barrierefreiheit spaeter. Pro: schnell. Contra: Schulen brauchen zuerst Tastatur/Screenreader.
    3. Eigenes Mobil-Layout (Touch: Kalender-Doppelklick-Ersatz, groessere Ziele). Pro: Handy-Nutzung. Contra: gross, getrennt planen.
    Empfehlung: 1, danach 3 fuer Touch (Kalender: Lang-Druck als Ersatz fuer Doppelklick).
 C. CSP OHNE 'unsafe-inline' (Skript steckt inline in index.html; Schutz vor XSS beruht heute nur auf esc(): 392 Aufrufe, 94 innerHTML)
    1. Nonce je Antwort: index.html wird beim Ausliefern mit einem Zufallswert in <script nonce=...> und CSP versehen; Inline-Event-Attribute
       (onclick="...") muessen zu addEventListener werden. Pro: echter XSS-Schutz. Contra: pruefen, wie viele Inline-Attribute es gibt; Aufwand gross.
    2. Skript und Stil in eigene Dateien (/app.js, /app.css), CSP script-src 'self'. Pro: einfach, cachebar. Contra: Inline-Attribute wie bei 1;
       Einzeldatei-Charakter geht verloren (Gea: Binary ist ein Binary, Dateien sind eingebettet - bleibt per go:embed).
    3. Beibehalten + Absicherung per Test: webui_test prueft, dass jeder innerHTML-Wert esc() nutzt (Muster). Pro: klein. Contra: kein harter Schutz.
    Empfehlung: 3 sofort (klein), 2 mit Teil A zusammen planen (Login-Seite bekommt ohnehin eigene Datei).
 D. WEITERE PUNKTE AUS DEM REVIEW (klein, noch offen): HTTP ohne TLS nur mit Warnung (Option: nicht-loopback ohne TLS/Proxy nur mit CS_ALLOW_HTTP=1);
    Audit-Log fuer Rechteaenderungen/Passwort-Resets (nur Chat hat eines); Touch-Ersatz fuer Doppelklick im Kalender; Handbuch/README fuer
    CS_IDENTITY_* und CS_MAX_FAILS_IP; bcrypt-Kosten 12 erst zusammen mit Rehash-beim-Login und angeglichener Dummy-Pruefung.
