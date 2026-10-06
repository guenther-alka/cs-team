cs-team changelog (newest first)

2026-10-06  0.60.0 Audit bestanden (Handbuch Kapitel 14, Nachtrag 0.60; keine HOCH-Punkte offen). Handbuch de/en aktualisiert (neue Kapitel 13.5/13.6 und 15 Ziel von cs-team, 2FA, Umfragen, Versionen, Office-Formate), Teil 1: A) oeffentliche Datei-Links mit Ablauf, B) Zwei-Faktor-Anmeldung (TOTP) mit App-Passwoertern:
                   A. Oeffentliche Links (/pub/{token}) laufen jetzt ab. Beim Erzeugen waehlt der Benutzer "Gueltig fuer: 1 Tag / 7 Tage / 30 Tage / unbegrenzt"
                   (Vorgabe 7 Tage; POST /api/filesshare/{owner}/{name} mit neuem Feld days, 0 = unbegrenzt, fehlt = unveraendert). Der Ablauf (unix s) steht in den
                   Metadaten der Datei (Meta.Exp), der Token-Schluessel filestok/<token> bleibt unveraendert. Abgelaufene Links liefern 404 wie unbekannte Token
                   (der Ablauf wird nicht verraten); ein Link ohne Ablauf (alle bisherigen) gilt weiter und wird als "unbegrenzt" angezeigt. Die Frist laesst sich
                   im Teilen-Dialog jederzeit aendern oder verlaengern (derselbe Link, Frist beginnt neu), der Haken widerruft. Der stuendliche Lauf des Papierkorbs
                   (RunTrash) loescht zusaetzlich abgelaufene Links samt Token (PurgeLinks); abgelaufene Links erscheinen in der Liste nicht mehr als Link.
                   Neue globale Einstellung "Maximale Gueltigkeit oeffentlicher Links (Tage, 0 = unbegrenzt erlaubt)" (POST /api/settings/pub2fa, Feld pubDays,
                   0..3650): ist sie gesetzt, gibt es kein "unbegrenzt" und nichts darueber (Wunsch wird auf die Obergrenze gekuerzt, auch ohne Angabe); bereits
                   bestehende Links ohne Ablauf bleiben unberuehrt. Audit: die Pfade /api/filesshare werden von logMW als audit-Zeile protokolliert, dazu je Aenderung
                   eine Zeile "audit: public link created|changed|revoked file=.. by=.. expires=YYYY-MM-DD" (nie der Token).
                   B. Zwei-Faktor-Anmeldung (nur lokale Konten; Verzeichniskonten LDAP/AD sind ausgenommen). TOTP nach RFC 6238 (SHA1, 6 Stellen, 30 s, +-1 Schritt,
                   Vergleich in konstanter Zeit, Wiederholungsschutz: je Benutzer wird der zuletzt benutzte Schritt gemerkt) nur mit der Standardbibliothek
                   (crypto/hmac, crypto/sha1, encoding/base32), neue Datei auth/twofa.go. Entwurf wegen HTTP Basic Auth auf jeder Anfrage (Begruendung: kein Cookie,
                   keine Sitzungsverwaltung, WebDAV/CalDAV/API bleiben unveraendert nutzbar; das ist der einfachste Weg, der zum bestehenden Anmeldeverfahren passt):
                   - Anmeldung mit "Passwort" direkt gefolgt vom 6-stelligen Code im Passwortfeld (z.B. geheim123456) oder mit einem Wiederherstellungscode
                   (geheim123ABCD-EFGH-IJKL). Nach der ersten erfolgreichen Pruefung merkt sich der Server nur im Speicher (wie cachedGood) den Hash der Zugangsdaten
                   je Benutzer und Adresse 12 Stunden (Konstante sessTTL); derselbe Authorization-Kopf des Browsers gilt dann ohne neuen Code. Der Eintrag traegt
                   einen Hash aus Passwort-Hash und Schluessel und wird bei jeder Anfrage mit dem Konto verglichen: Passwortaenderung, 2FA ausschalten/zuruecksetzen,
                   Benutzer sperren/loeschen und Neustart beenden die Sitzung. Parallele Anfragen eines Browsers werden serialisiert (sonst wuerde der Wiederholungs-
                   schutz die zweite Anfrage ablehnen). Ein falscher Code sieht aus wie ein falsches Passwort (401 mit gleichem Text, gleicher Fehlzaehler/Sperre
                   Benutzer+Adresse, Adresse, Benutzer; genau ein bcrypt je Versuch, Passwort ohne Code genuegt nie und verraet nichts).
                   - Einrichten im Konto (POST /api/me/2fa/setup, .../activate mit Passwort + erstem Code): zeigt Schluessel (Base32, gruppiert), otpauth-Adresse und
                   QR-Code; der QR-Code entsteht im Browser mit einem eigenen kleinen Encoder in web/index.html (Byte-Modus, Fehlerkorrektur M, Version 1-10, SVG,
                   keine Bibliothek, keine externe Anfrage; gegen 213 Laengen mit OpenCV als Leser geprueft). Bei der Aktivierung entstehen 8 Wiederherstellungscodes
                   (einmalig angezeigt, nur als SHA-256-Hash gespeichert, je Code einmal einloesbar). Ausschalten (POST /api/me/2fa/disable) braucht Passwort + Code;
                   die Zaehler sind dieselben wie bei Anmeldungen (429 bei Sperre). GET /api/me/2fa zeigt Stand und App-Passwoerter.
                   - App-Passwoerter fuer WebDAV/CalDAV und andere Programme ohne Code-Eingabe (POST /api/me/apppass mit Passwort, DELETE /api/me/apppass/{id}):
                   24 Zeichen, nur einmal angezeigt, bcrypt-gehasht, benannt, widerrufbar, hoechstens 10 je Konto, Liste mit Name/angelegt/zuletzt benutzt (stuendlich
                   nachgefuehrt). Sie gelten nur fuer /webdav/, /dav/, /.well-known/caldav und /api/files* (auth.appPathOK), nie fuer /api/users, /api/groups,
                   /api/settings, /api/me*; auf anderen Pfaden zaehlen sie als Fehlversuch. Die ersten 6 Zeichen sind der Suchschluessel (so faellt hoechstens ein
                   bcrypt an). App-Passwoerter gibt es nur bei eingeschalteter 2FA und verschwinden beim Ausschalten/Zuruecksetzen.
                   - Admin-Pflicht: globale Einstellung "Zwei-Faktor fuer Admin-Konten verlangen" (POST /api/settings/pub2fa, Feld enforce). Lokale Admin-Konten ohne
                   2FA erreichen danach nur /api/me*, /lang/ und die Startseite (403 mit Kopf X-2FA-Required, analog zur Aenderungspflicht des Startpassworts); die
                   Oberflaeche zeigt nur die Konto-Seite mit Hinweis. Ausschalten ist dann fuer Admins gesperrt.
                   - Zuruecksetzen: globale Admins koennen die 2FA eines anderen Kontos zuruecksetzen (POST /api/users/{name}/2fa/reset, Knopf in der Benutzer-
                   ansicht, Audit-Zeile; nicht fuer das eigene Konto und nicht fuer das Sysadmin-Konto). Die Kommandozeile (cs-team adduser/sysadmin NAME mit
                   Passwort) entfernt die 2FA des Kontos mit (Notfallzugang). Neue Audit-Pfade: /api/me/2fa, /api/me/apppass, /api/filesshare.
                   Neue Felder im Konto (users.json): totp, recovery, appPw. 48 neue Texte in allen 13 Sprachdateien (maschinell uebersetzt).
                   Tests: files/expiry_test.go (abgelaufen, unbegrenzt, verlaengern, Bereinigung, Obergrenze), auth/twofa_test.go (RFC-6238-Vektoren, Fenster und
                   Wiederholung, Anmeldung mit Code, parallele Anfragen, falscher Code gleich falschem Passwort und Sperre, Wiederherstellungscode einmalig,
                   App-Passwort nur fuer Dateien/Kalender, Widerruf, Cache-Ende bei Passwortaenderung/Sperre, Pflicht fuer Admins, Zuruecksetzen), chat/settings60_test.go,
                   audit60_test.go. Teil 2 von 0.60.0 folgt.

                   Nachbesserung Teil 2 nach unabhaengiger Pruefung (Claude claude-sonnet-5-5):
                   H1 Anonyme Umfragen: der Server sendet fuer anonyme Umfragen nie Namen (kein done) und solange sie offen sind auch keine Zaehler (cnt); jeder Empfaenger erhaelt nur das eigene
                      Feld me (hat abgestimmt ja/nein) und die Teilnehmerzahl n; Zaehler erst nach Beenden/Ende (Chat, Verlauf, Update-Rahmen gleich, Aufbereitung je Empfaenger in Poll.wire).
                      Auf der Platte bleiben Zaehler und Teilnehmerliste, sie verlassen den Server nie. Oberflaeche: Hinweis 'Ergebnis erst nach Ende der Umfrage'. Benannte Umfragen unveraendert.
                      Export/Anonymisieren angepasst; Test poll60_test.go (zwei Stimmen, offene Rahmen und Verlauf ohne Zaehler/Namen, nach Beenden Zaehler da).
                   M1 auth/twofa.go: Aktivieren legt keine Sitzung mehr mit dem reinen Passwort an; Oberflaeche: 'Bitte jetzt mit Passwort + Code neu anmelden' und Abmelden.
                   M2 Sitzungen tragen den Kontonamen; dropSessions verwirft nur Sitzungen dieses Kontos (Einschalten/Ausschalten/Zuruecksetzen); App-Passwort widerrufen verwirft keine.
                   M3 Gueltiges App-Passwort auf Pfad ausserhalb des Geltungsbereichs: 403 'app password not allowed here', ohne Fehlversuch-Zaehler; ungueltige Angaben zaehlen weiter.
                   M4 Dateiversionen (Liste/Download) nur fuer Eigentuemer oder mit Schreibrecht (Gruppenordner rw oder Gruppen-Admin); nur-lesende Freigabe 403, ohne Zugriff 404; Knopf 'Versionen' dort ausgeblendet.
                   Klein: (a) Konto-Hinweis, dass ein benutzter Code nicht erneut gilt; (b) Warnung beim Einschalten von 'Zwei-Faktor fuer Admin-Konten verlangen'; (c) Umbenennen/Verschieben behaelt den
                      oeffentlichen Link (files.go moveLink, movelink_test.go); (d) Kommentar: nur-lesende Chat-Mitglieder duerfen abstimmen, aber keine Umfrage anlegen. 4 neue UI-Texte in allen 13 Katalogen.
                      Tests: auth/twofa_test.go, poll60_test.go, versions60_test.go angepasst, files/movelink_test.go neu; go vet und runtests.ps1 gruen.
                   Teil 2 (Claude claude-sonnet-5-5): C) Umfragen im Chat, D) Dateiversionen aus ZFS-Snapshots, E) Office-Import und Bildschirmfreigabe:
                   C. Umfragen: eine Umfrage ist eine normale Chat-Nachricht mit dem neuen Feld poll (chat/poll.go) - gleicher Speicher (chat/<gruppe>/<kanal>.json), gleicher
                   Verlauf, WebSocket-Weg, Aufbewahrung und Anonymisierung; keine neue Ablage. Neue WebSocket-Nachrichten poll (anlegen), vote (abstimmen), pollclose (beenden);
                   Knopf "Umfrage" neben dem Eingabefeld. Anlegen: wer im Kanal schreiben darf (Chat-Modus der Gruppe), Frage <= 200, 2-10 verschiedene Antworten <= 100 Zeichen,
                   Mehrfachauswahl, anonym, optionales Ende (hoechstens 366 Tage voraus), gleiches Tempolimit wie Nachrichten. Abstimmen: jedes Mitglied mit Leserecht im Chat der
                   Gruppe (auch im Nur-Admin-Chat), solange die Umfrage offen ist (nicht beendet, Ende nicht erreicht); Nichtmitglieder erhalten "chat not available". Benannt:
                   Stimme aenderbar oder zuruecknehmbar, die Wahl ist fuer alle Leser des Kanals sichtbar (Namen je Antwort). Anonym: der Server speichert nur Zaehler je Antwort und
                   je Person die Marke "hat abgestimmt" (alphabetisch, ohne Wahl); es gibt kein votes-Feld, weder auf der Platte noch im Netz, auch Admins sehen keine Zuordnung;
                   deshalb sind anonyme Stimmen nicht aenderbar (Hinweis in der Oberflaeche). Rest-Risiko: wer Zugriff auf den Speicher hat und vor/nach einer Stimme vergleicht,
                   sieht, welcher Zaehler stieg. Beenden: Ersteller oder Gruppen-Admin (Logzeile "audit: poll closed by group admin" bei Beenden durch einen Admin); Umfragen werden
                   nicht bearbeitet, mit der Nachricht entfaellt auch die Umfrage. Datenauskunft (chat.json): neues Feld votes - benannte Umfragen mit der eigenen Wahl, anonyme nur
                   "participated": true. Benutzer loeschen mit Anonymisierung: Name in Stimmen bzw. Teilnehmerliste ersetzt, Zaehler und Teilnehmerzahl bleiben. Aufbewahrung: Umfragen
                   fallen wie Nachrichten nach Ablauf der Frist weg. Aenderungen an einer Umfrage geschehen an einer Kopie (kein Datenwettlauf beim Senden).
                   D. Dateiversionen aus ZFS-Snapshots (files/versions.go), nur lesend, ohne zfs-Befehl: GET /api/filesversions/{owner}/{name} liefert
                   {"supported":..,"versions":[{snap,size,mod}]} (neueste zuerst, hoechstens 50; aufeinanderfolgende Snapshots mit gleicher Groesse+Zeit zaehlen einmal; ein Stand wie
                   die aktuelle Datei entfaellt; angesehen werden nur die 300 neuesten Snapshots), mit ?snap=<name> das Herunterladen dieser Version (gleiche Header wie der normale
                   Download), POST /api/filesrestore/{owner}/{name}?snap=<name> stellt wieder her: der bisherige Stand kommt als Kopie in den Papierkorb (wenn eingeschaltet), Freigaben
                   und oeffentlicher Link der Datei bleiben, Kontingent/Sperren/Groessenlimit wie beim Hochladen. Mountpunkt: naechster uebergeordneter Ordner der Speicherwurzel
                   (CS_DIR/.csteam) mit Unterordner .zfs, oder CS_SNAP_ROOT; S3/RAM-Speicher oder Ordner ohne Snapshots: supported:false. Sicherheit: Rechte wie beim Lesen der Datei
                   (Eigentuemer, Freigabe, Gruppenordner; sonst 404), Wiederherstellen wie Schreiben (403), Snapshot-Name ein einzelnes Pfadsegment (kein / \ .. Steuerzeichen), im
                   Snapshot werden Symlinks nie verfolgt (Lstat je Teilstueck), nur normale Dateien. Auch fuer geloeschte Dateien (nur Eigentuemer bzw. Gruppenordner-Mitglied). Audit:
                   Zeile "audit: file restored file=.. snap=.. by=..", dazu /api/filesrestore in audited() (audit-failed/-denied). store.FS bekommt Root() und Path(). Oberflaeche:
                   Knopf "Versionen" im Dateidialog mit eigenem Dialog (neuer Helfer uiBox): Datum, Groesse, Herunterladen, Wiederherstellen; ohne Snapshots Hinweis auf das Handbuch.
                   E. Office-Import (conv): xlsx-Daten und Uhrzeiten (eingebaute und eigene Datumsformate aus styles.xml) kommen als Text JJJJ-MM-TT [hh:mm] statt als Zahl,
                   Wahrheitswerte als TRUE/FALSE; docx-Tabellenzeilen werden ein Absatz (Zellen mit Tab), mc:Fallback (doppelter Text in Textfeldern) wird uebersprungen. Videochat
                   (eigenes WebRTC): Knopf "Bildschirm teilen" (getDisplayMedia + replaceTrack, auch fuer spaeter Beitretende).
                   28 neue Texte in allen 13 Sprachdateien. Neue Tests: chat/poll_test.go, poll60_test.go, versions60_test.go, conv/office60_test.go.
                   Sicherungen: C:\opt\old\cs-team\2026.10.06_v060b.
2026-10-06  0.59.0 Audit-Nachbesserung: Anmelde-Erfolgszeile nur einmal nach Fehlversuchen des gleichen Kontos; Gruppenkalender fuer unbekannte Gruppe wird mit 404 abgelehnt; Handbuch Kapitel 14 Audit Result 0.59 (keine Punkte der Kategorie hoch offen). Datenschutz-Nachbesserung nach der DSGVO-Pruefung von 0.58 (Punkte 1-4):
                   1. Benutzer loeschen mit Option "anonymisieren" (Standard an: Feld anonymize im POST /api/users/{name}/delete, Haken im Dialog, auf Wunsch aus):
                   der Name des Kontos wird in Chat-Nachrichten (Autor, Reaktionen, Kanalersteller, Versandprotokoll), Aufgaben (Auftraggeber, Bearbeiter,
                   Beteiligte, Verlauf) und als Teilnehmer/Organisator in Terminen fremder Kalender (Gruppen, Organisationen, global, andere Benutzer) durch den
                   festen Text "geloeschter Benutzer" ersetzt (im Programm mit Umlaut, Konstante auth.DeletedUser). Die Vorschau (GET .../delete/preview) nennt die
                   Zahlen (anon: chat, tasks, events). Alles Bisherige bleibt (Dateien, Dokumente, persoenliche Kalender, Freigaben). Das Loeschprotokoll
                   (users/_delete-log.json) enthaelt nur Name, Zeit, Admin und Zahlen, keinen Inhalt. Neue Hooks auth.AnonHooks (chat/anon.go, tasks/gdpr.go, cal/gdpr.go).
                   2. Datenauskunft Art. 15/20: GET /api/me/export liefert ein ZIP (gestreamt, Content-Disposition attachment) mit README.txt, account.json (Name,
                   Gruppen, E-Mail, Sprache, angelegt; nie das Passwort/Hash, Webhook nur als "gesetzt"), files/ (eigene Dateien), calendars/<name>.ics (persoenliche
                   Kalender, ohne Abos), tasks.json (angelegt oder bearbeitet, Obergrenze 2000), chat.json (eigene Nachrichten mit Gruppe, Kanal, Zeit, Obergrenze
                   10000). Admin-Variante GET /api/users/{name}/export (nur globale Admins; Audit-Zeile ueber die Protokollierung). Grenzen: 20000 Dateien/8 GB,
                   hoechstens 3 Exporte gleichzeitig (429), Namen im ZIP gegen zip-slip bereinigt (auth.ZipSafe). Oberflaeche: Konto "Meine Daten exportieren
                   (ZIP)", Benutzerverwaltung "Daten exportieren". Nicht enthalten (steht im README.txt): Inhalte anderer, Gruppenordner/-kalender, Calc/Text-Dokumente,
                   Server-Protokolle, Chat-Anhaenge (nur Dateiname).
                   3. Aufbewahrung: je Gruppe "Aufbewahrung Chat (Tage)" (0 = unbegrenzt, Vorgabe; Gruppen-Dialog, API chatDays) loescht ueber den stuendlichen
                   Lauf (chat/retention.go) Chat-Nachrichten samt Anhaengen, die aelter sind (Alter aus der Nachrichten-ID); global "Abgeschlossene Aufgaben:
                   Tage bis zum automatischen Loeschen" (0 = nie; Einstellungen, POST /api/settings/closedtasks; tasks/gdpr.go, im stuendlichen Aufgaben-Lauf).
                   Je Lauf eine Logzeile mit Zahlen ("retention: group=x deleted n messages", "retention: closed tasks deleted n").
                   4. Hinweis vor der ersten Nutzung von KI-Assistent und externem Videochat: eigener Dialog nennt Anbieter/Server (aus den Einstellungen), welche
                   Daten dorthin gehen (KI: Text, Bilder und gewaehlte Dateien der Anfrage; Video: Ton, Bild, Raumname, cs-team sieht nichts) und dass die Nutzung
                   freiwillig ist ("Verstanden und weiter"/"Abbrechen"). Bestaetigung je Benutzer im Konto (Account.ack, POST /api/me/ack, Hash der Adresse),
                   in /api/me als ack/ackAddr/privacy; aendert der Admin KI-Anbieter oder Videoserver, wird sie ungueltig. Neues Feld "Datenschutzhinweis
                   (zusaetzlicher Text)" in den Einstellungen (POST /api/settings/privacy, max. 2000 Zeichen) wird an die Hinweise angehaengt. Der Hinweis ist
                   nur in der Oberflaeche erzwungen (die API bleibt wie bisher benutzbar). 17 neue Texte in allen 13 Sprachdateien (maschinell uebersetzt).
                   Tests: gdpr_test.go (Anonymisierung, Export, Hinweis, Aufbewahrungs-Einstellungen), chat/retention_test.go und tasks/gdpr_test.go (mit festem
                   "jetzt"), auth/export_test.go (ZipSafe). Das Handbuch hat jetzt Kapitel 11 Datenschutz (DSGVO) (de/en, PDFs Version 0.59).
                   Nachbesserung nach unabhaengiger Pruefung: (a) Audit-Zeile fuer jeden Export ("audit: export user=.. by=.. ip=.."); (b) logMW: 5xx-Zeilen mit
                   Antworttext (body=, bereinigt, 120 Zeichen), Audit-Praefix "audit-failed:" fuer 400/404/409/422 (abgelehnte Eingabe), "audit-denied:" nur fuer 401/403/429;
                   (c) Anmeldeprotokoll: Sperr-Zeilen mit eigenem Kontingent (je 30/Minute, getrennt von den Fehlversuchen), die Zusammenfassung "N further ... messages
                   suppressed" kommt auch ohne weitere Meldung am Ende der Minute, erfolgreiche Anmeldung wird nur nach Fehlversuchen von Benutzer oder Adresse
                   protokolliert ("auth: login ok user=.. ip=.. after N failed attempts"); (d) Windows-Server: reservierte Geraetenamen (con, nul, com1 ..) und ":" werden
                   fuer Konten, Gruppen, Organisationen und Kalender-IDs abgelehnt bzw. ersetzt (store.WinBad/WinReserved, files.winBad nutzt sie);
                   Dateisystemfehler enthalten nie mehr den Serverpfad (store.ErrStorage, Einzelheiten nur im Server-Log "store: ..."); (e) Aufbewahrung: 0 oder
                   mindestens 7 Tage (Chat je Gruppe und abgeschlossene Aufgaben; 1..6 ergibt 400, Pruefung auch in der Oberflaeche, 1 neuer Text in 13 Sprachdateien);
                   POST /api/settings/closedtasks mit "null" oder ohne days ist 400 statt stillschweigend 0. Tests: audit59_test.go, auth/authlog_test.go,
                   store/fserr_test.go, gdpr_test.go erweitert.

2026-10-06  0.58.0 (enthaelt 0.57.1) Nach Multiuser-Live-Test: ICS-Export eines leeren Kalenders lieferte HTTP 500 (jetzt leeres VCALENDAR); Gruppen-Admin darf Konten ohne eigene Gruppe in seine Gruppe aufnehmen; Fehlermeldungen bei Gruppenname/Bereich praeziser; Protokoll: fehlgeschlagene Anmeldungen und Sperren (begrenzt, ohne Passwoerter), Audit-Zeilen fuer Aenderungen an Benutzern/Gruppen/Einstellungen/Passwoertern (audit:/audit-denied:), 5xx-Fehler und Abstuerze (panic abgefangen), Startzeile mit Version, optional CS_LOG_ACCESS=1 fuer ein Zugriffsprotokoll; Windows-Server: Dateinamen wie CON, NUL, a:b werden abgelehnt. Oberflaeche: Dunkelmodus (folgt prefers-color-scheme, color-scheme light dark; alle Bereiche: Listen, Calc/Text,
                   Kalender, Dateien, Chat, Aufgaben, KI-Assistent, Assistent, Dialoge), sichtbarer Tastaturfokus (:focus-visible), Eintraege der linken Liste mit Tab
                   erreichbar und per Enter/Leertaste waehlbar (role=button, aria-current), Grundlagen fuer Screenreader (Navigation, main, Beschriftung von Sprachwahl,
                   Filter, KI-Knopf, KI-Fenster), reduzierte Bewegung. Kalender: Aufgaben-Faelligkeiten (eigene offene Aufgaben und Meilensteine) als Markierung in
                   Monat, Woche/Tag (ganztaegig) und Agenda, ueberfaellig rot, Klick oeffnet die Aufgabe; Lang-Druck (550 ms) als Touch-Ersatz fuer den Doppelklick
                   (neuer Termin im Zeitraster und in Tageszellen). 4 neue Sprachen (13 insgesamt): Ukrainisch, Polnisch, Griechisch, Japanisch; 2 neue Texte in allen
                   Sprachdateien. Handbuch de/en: Kapitel 10 Einstellungen im Detail (mit Screenshot napp-it Service-Menue), 10.6 Sprachen, Darstellung/Tastatur,
                   Aufgaben im Kalender, Touch. Geprueft im Browser (headless Chrome ueber DevTools): Dunkelmodus Kalender/Einstellungen/Aufgaben, Aufgaben-Markierungen,
                   Lang-Druck (Touch-Emulation) legt Termin an, Tastaturfokus in der Liste; go test ./... gruen.

2026-10-06  0.57.1 (unveroeffentlicht) Sysadmin-Konto (Gea): genau ein lokales cs-team-Konto ist der Notfallzugang - immer Admin und aktiv, nicht loeschbar,
                   nicht sperrbar, nicht herabstufbar (HTTP 403), sein Passwort aendert nur es selbst oder die Kommandozeile (auch nicht per CSV-Import oder
                   Jahrgangswechsel). Festgelegt wird es beim Start: das Konto aus CS_ADMIN_USER (erster Start), bei Altbestaenden das aelteste aktive lokale
                   Admin-Konto; ein vorhandenes bleibt, Wechsel mit "cs-team sysadmin NAME". Alle weiteren globalen Admins und Gruppen-Admins duerfen lokale Konten
                   oder Verzeichniskonten (name@realm) sein; die Rolle wird immer lokal vergeben (Admin-Flag am Konto bzw. Gruppen-Admin-Liste an der Gruppe),
                   das Verzeichnis liefert sie nie. Benutzerliste: "Sysadmin" statt "Globaler Admin", Haken gesperrt, kein Loeschen-Knopf. 3 neue Texte in allen
                   Sprachdateien. Getestet: auth/sysadmin_test.go, auth/members_test.go TestDirAccountRoles, alle Testpakete gruen.
                   Aufgaben-Oberflaeche, Runde 1: Schnellzeile ueber der Liste ("Elternbrief schreiben @anna !hoch morgen": @Bearbeiter, @ich, !hoch/!niedrig,
                   #Gruppe, heute/morgen/uebermorgen/naechste Woche/Wochentag/24.12./2026-12-24, deutsch und englisch; Vorschau unter dem Feld, Unbekanntes bleibt im
                   Titel), Haekchen in der Liste (eigene offene Aufgabe erledigt melden, erledigte abnehmen; Rechte prueft der Server), neuer Filter "Heute und
                   ueberfaellig" und Gliederung Ueberfaellig/Heute/Diese Woche (frueheste offene Faelligkeit oder Meilenstein). 6 neue Texte in allen Sprachdateien.
                   Geprueft: Syntaxtest und Katalogtest (webui_test.go), Parser mit 12 Beispielsaetzen in Node; im Browser noch nicht von Hand getestet.

2026-10-06  0.57.0 Release: fasst 0.54-0.56 (Verzeichnis-/LDAP-Anmeldung, hierarchische Kalender) und 0.56.1 zusammen.
                   Anmeldesperre, Startpasswoerter, Uebersetzungen, eigene Dialoge (0.56.1):
                   Sperre je Adresse: Standard jetzt 60 statt 20 Fehlversuche (einstellbar mit CS_MAX_FAILS_IP). Vorher sperrten 20 Fehlversuche
                   einer Adresse alle von dort fuer 5 Minuten, auch mit richtigem Passwort (Schul-NAT, Proxy ohne CS_TRUST_PROXY=1). Wer von der
                   Adresse gerade gueltig angemeldet ist (Kurzzeit-Cache 45 s), bleibt trotz Adress-Sperre zugelassen; neue Anmeldungen und falsche
                   Passwoerter werden weiter mit 429 blockiert. Der Fehlversuchs-Zaehler verfaellt nach 15 Minuten ohne neuen Fehlversuch (vorher
                   summierten sich einzelne Tippfehler ueber Tage zu einer Sperre).
                   Startpasswoerter: Der erste Admin (CS_ADMIN_USER/CS_ADMIN_PASS) und Konten, die mit "cs-team adduser" angelegt oder zurueckgesetzt
                   werden, muessen das Passwort beim ersten Login aendern. Trivialpasswoerter (12345678, password, passwort1, ein Zeichen wiederholt
                   u.a., kurze Liste in auth/auth.go) werden beim Anlegen, Aendern, Zuruecksetzen und im CSV-Import abgelehnt (HTTP 400).
                   bcrypt-Kosten bleiben bei 10: eine Erhoehung wuerde bis zur Neuanmeldung aller Konten die Antwortzeit bekannter und unbekannter
                   Namen unterscheidbar machen (Benutzer-Aufzaehlung am Login).
                   Uebersetzungen: je 29 Texte (Anmeldung am Verzeichnis, Mitgliederlisten, Serientermine) fuer ar, cn, es, fr, it, ru, tr ergaenzt
                   (wurden bis dahin deutsch angezeigt); maschinell uebersetzt, Korrektur durch Muttersprachler empfohlen.
                   Oberflaeche: alert/confirm/prompt durch eigene Dialoge ersetzt (Bestaetigung, Eingabe, Hinweis-Leiste unten): blockieren den Browser
                   nicht, Enter = OK, Esc = Abbrechen, Tab bleibt im Dialog, Passwort-Eingaben verdeckt; nach "Passwort geaendert" und "Kein Zugriff
                   mehr" erscheint ein OK-Dialog vor dem Neuladen.
                   Getestet: auth/guard_test.go (Adress-Sperre mit Cache-Ausnahme, Zaehler-Verfall), auth/pass_test.go (neu: Trivialpasswoerter,
                   Aenderungspflicht Bootstrap/CLI), go vet ./... und alle Testpakete gruen, Syntaxpruefung index.html; Dialoge noch nicht von Hand im Browser geprueft.

2026-10-05  0.56.0 Kalender hierarchisch: eigene, Gruppen-, Organisations- und globale Kalender plus externe Abos (nur lesbar) in
                   einer Liste, von innen nach aussen sortiert. Verantwortlich (aendern und freigeben): persoenlich der Benutzer,
                   Gruppe die Gruppen-Admins (sie sehen ihren Gruppenkalender auch ohne Mitgliedschaft), Organisation und global
                   die globalen Admins. Freigabe je Kalender: "off" nicht freigegeben (Entwurf, nur die Verantwortlichen sehen
                   ihn), "ro" die Berechtigten lesen, "rw" alle Berechtigten tragen ein; die Kalender-Leiste zeigt die Freigabe,
                   die Liste kennzeichnet Entwuerfe mit "(nicht freigegeben)", die Cal-Maske bietet die drei Werte.
                   Externe ICS-Abos legt jeder fuer sich an (fuer die Gruppe der Gruppen-Admin, fuer Organisation/global die
                   globalen Admins): nur lesbar, alle 30 Minuten bzw. per "Jetzt aktualisieren", Status und Fehler sichtbar,
                   beenden = URL leeren. CalDAV (Thunderbird, iOS, macOS, DAVx5) liefert alle sichtbaren Kalender (vorher nur
                   den eigenen): nicht freigegebene fehlen im PROPFIND und antworten per Direktzugriff 404, Schreiben nur mit
                   Freigabe "rw" bzw. als Verantwortlicher/Admin, Abos antworten 403. Kalender aendern per PUT /api/cal/{kal}
                   (Name, Beschreibung, Freigabe, Ressource, Abo-URL setzen/wechseln/beenden). Einen einzelnen Kalender weiterhin
                   als Datei: GET /api/cal/{kal}/export.ics und POST /api/cal/{kal}/import (nach UID, ohne Dubletten).
                   Gruppenkalender lassen sich jetzt auch in den Gruppen-Einstellungen verwalten: POST /api/groups/{name} mit
                   "cal" = "off" (Entwurf), "ro", "rw" legt ihn an bzw. aendert nur die Freigabe, "" entfernt ihn - aber nur,
                   solange keine Termine darin liegen (sonst 409); GET /api/groups nennt die Freigabe je Gruppe.
                   Getestet: Go-Tests TestCalendarScope + TestCalendarScopes (Gruppen-Admin ohne Mitgliedschaft, off/ro/rw,
                   globaler und Organisations-Kalender, Abo fuer sich und fuer die Gruppe) und neu TestCalDAVScope (PROPFIND zeigt
                   Gruppe und freigegebenes Global, Entwurf fehlt und antwortet 404, PUT/GET im Gruppenkalender, 403 bei "ro" und
                   bei Abos, Export enthaelt den Termin) sowie TestCalDAVOwnCalendar (eigene Kalender:
                   eigener Kalender per MKCOL - MKCALENDAR 405 -, eigenes Abo 403, Bereich "Kalender" nur lesend 403);
                   TestGroupCalendarRelease (Gruppenkalender aus den Gruppen-Einstellungen: nur globale Admins, off/ro/rw,
                   entfernen nur wenn leer, Neuanlegen); go vet ./... und go test ./... gruen. Neu: webui_test.go TestWebUISyntax
                   prueft jeden Skript-Block der Oberflaeche mit "node --check" (uebersprungen ohne Node.js).

2026-10-02  0.53.0 Aufgaben: Globale Admins duerfen in jeder Gruppe mit eingeschalteten Aufgaben Aufgaben anlegen und Gruppenmitglieder zuweisen (auch ohne Mitglied zu sein).
                   Neu: Anfrage ("Bitte bearbeiten"): In Gruppen mit Modus "nur Gruppen-Admins" duerfen normale Mitglieder jetzt Aufgaben anlegen; sie werden als Anfrage ohne Zustaendigen
                   gespeichert (Feld req, Protokollzeile), sind fuer alle Gruppenmitglieder sichtbar und von jedem uebernehmbar. Der Ersteller aendert den Text, bestimmt aber keinen Zustaendigen;
                   Gruppen-Admins und globale Admins aendern und nehmen ab. Die Gruppenauswahl markiert solche Gruppen mit "Anfrage", das Feld Bearbeiter ist dann gesperrt.
                   Getestet: Go-Test TestRights, API-Test tasks1.py (Mitglied/Gruppen-Admin/globaler Admin/Fremder, Uebernehmen), Browser-Regression.

2026-10-02  0.52.0 Kalender: Stundenraster in Tages- und Wochenansicht. Zeitachse 0-24 Uhr (48 px je Stunde, Start bei 7 Uhr, Position bleibt beim Neuzeichnen), Termine als Bloecke nach Beginn und Dauer,
                   ueberlappende Termine nebeneinander, ganztaegige Termine oben, rote Linie fuer die aktuelle Zeit. Termin mit der Maus auf anderen Tag und andere Uhrzeit ziehen (Raster 15 Minuten),
                   Dauer am unteren Rand ziehen; bei Serien fragt der Dialog (nur dieser / dieser und folgende / alle). Geaendert (Gea): neue Termine nur noch per Doppelklick
                   (Monat: Tag, Woche/Tag: Zeitachse mit angeklickter Uhrzeit, 1 Stunde; im markierten Tagesbereich ganztaegiger Mehrtagestermin); ein einfacher Klick waehlt nur den Tag (Ziel fuer Strg+V).
                   Getestet: Browser cal, cal2, cal3 (Bloecke, Ueberlappung, Doppelklick, Dauer ziehen, Verschieben, Serie nur dieses Vorkommen, Tagesansicht).

2026-10-02  0.51.0 Calc-Ausbau. Neue Funktionen (englische und deutsche Namen): SUMIF/SUMMEWENN, COUNTIF/ZAEHLENWENN, AVERAGEIF/MITTELWERTWENN (Kriterien wie ">5", "<>x", "A*"), IFERROR/WENNFEHLER,
                   VLOOKUP/SVERWEIS (wie Excel), CONCAT/VERKETTEN, LEFT/RIGHT/MID, LEN, UPPER, LOWER, TRIM, MEDIAN, MOD, INT, TODAY/HEUTE, NOW/JETZT, DATE/DATUM, YEAR/MONTH/DAY, TEXT(Wert;Format)
                   (Zahlen 0,00 #.##0,00 0% und Datum tt.mm.jjjj / dd.mm.yyyy); Datumswerte sind Excel-Seriennummern, HEUTE/DATUM zeigen das Datum direkt an.
                   Neu: Ausrichtung links/zentriert/rechts und Zeilenumbruch (Format-Tokens hl, hc, hr, w; Server-Whitelist erweitert); Spaltenbreite per Maus am Kopf ziehen
                   (gemeinsam fuer alle, gespeichert als Zelle <Spalte>0, Doppelklick = Standard; Zeile 0 wird nie angezeigt oder exportiert).
                   Neu: Sortieren der markierten Zeilen A-Z / Z-A nach der ersten Spalte (Format und Formeln wandern mit, ein Undo-Schritt, bei fremd gesperrten Zellen Abbruch mit Hinweis);
                   Fixieren der ersten Zeile/Spalte (Ansicht im Browser); Rechtsklick-Menue Inhalt/Format/Alles loeschen.
                   Getestet: Browser calc4-calc7 (Funktionen, Breite/Ausrichtung mit zweitem Benutzer, Sortieren inkl. Sperre und Undo, Menue), Go-Test TestValidFmtNumber erweitert.

2026-10-02  0.50.0 Neue Zaehlung: die Versionen 0.1x waren die ersten Tests, 1.0 folgt, wenn cs-team ausgereifter ist; 0.50 (statt 0.5), damit Versionsvergleiche nach 0.15.3 richtig sortieren (0.50.0 ist inhaltlich der Stand von 0.15.3).
                   Neu: Mouseover auf den Titel "cs-team" oben links zeigt die Programmversion (Server nennt sie in /api/me; fuer Admins steht darunter "Einstellungen").
                   Neu: Calc Rueckgaengig/Wiederholen (Strg+Z, Strg+Y, Strg+Umschalt+Z): Eingabe, Einfuegen, Loeschen, Ausfuellen und Formatierung sind je ein Schritt (bis 100 je Blatt,
                   nur im Browser). Zurueckgesetzt werden nur Zellen, die seitdem niemand sonst geaendert hat und die nicht gesperrt sind; der Rest bleibt, ein Hinweis nennt die Anzahl.
                   Neu: Calc Zahlenformate. Knoepfe Waehrung, %, .0+ und .0- in der Formatleiste (Zellformat-Kuerzel d0-d9, pc, cu; Server-Whitelist erweitert). Der Zellwert bleibt eine
                   Zahl, nur die Anzeige aendert sich (Prozent = Wert x 100, Waehrung mit Tausendertrennung und 2 Stellen, Komma/Punkt und Symbol nach Sprache). Eingabe wird erkannt:
                   "12,5%" wird 0,125 im Prozentformat, "12,50 EUR"/"$12.50" wird Zahl im Waehrungsformat (auch beim Einfuegen von Text); in einer Prozentzelle bedeutet "5" 5 %.
                   Nicht enthalten: xlsx-Export/Import der Formate.
                   Getestet: Go-Test TestValidFmtNumber, Browser calc3 (24 Pruefungen, auch Deutsch und zweiter Benutzer).
                   Getestet: Go-Test TestMeVersion, livetest (Version in /api/me), Browser (Tooltip; Calc-Undo mit zwei Benutzern, fremde Aenderung bleibt erhalten).

2026-10-02  0.15.3 Kalender: Maussteuerung; Calc: Sperre schon beim Markieren. Klick in einen Tag (Monat, Woche, Tag) legt einen neuen Termin an (die Tageszahl oeffnet weiter die Tagesansicht);
                   Umschalt+Klick markiert einen Bereich von Tagen, ein Klick in den Bereich legt einen ganztaegigen Mehrtagestermin an (Esc hebt die Markierung auf).
                   Klick auf einen Termin oeffnet direkt das Bearbeiten-Formular (mit Loeschen und den Zusagen der Teilnehmer; schreibgeschuetzte Kalender zeigen
                   weiter die Einzelheiten). Termine lassen sich mit der Maus auf einen anderen Tag ziehen (Uhrzeit und Dauer bleiben; bei Mehrtagestermin wird um
                   die gezogene Tagesstrecke verschoben); bei Serien fragt ein Dialog: nur dieser Termin / dieser und folgende / ganze Serie. Strg+X und Strg+C merken
                   den zuletzt angeklickten Termin, Strg+V fuegt ihn an dem Tag unter dem Mauszeiger ein (auch nach dem Blaettern in einen anderen Monat): Ausschneiden
                   verschiebt, Kopieren legt eine Kopie an mit allem ausser dem Tag (Uhrzeit, Ort, Beschreibung, Erinnerung, Teilnehmer, einfache Wiederholung);
                   hat die Kopie Teilnehmer, wird vor dem Versand der Einladungen nachgefragt.
                   Calc: Schon die Markierung sperrt ihre Zellen fuer andere (orange, "X schreibt gerade"); der Server haelt die Sperre eines markierten Bereichs
                   (Nachricht lockr, Heartbeat alle 10 s, bis 5000 Zellen, Meldung der Fremdsperren nur an den Anfragenden, Sammelmeldungen locks/unlocks) und loest sie
                   beim Wechsel der Markierung, beim Verlassen des Blatts (Fokus/Fenster) und beim Trennen. Kopieren aus gesperrten Zellen uebernimmt den aktuellen
                   Inhalt; Einfuegen, Loeschen und Ausfuellen lassen gesperrte Zellen aus und nennen Anzahl und Benutzer; Bearbeiten einer gesperrten Zelle zeigt einen Hinweis.
                   Behoben: Beim Bearbeiten der ganzen Serie von einem spaeteren Vorkommen aus wurde der Serienbeginn auf dieses Vorkommen gesetzt (die Serie rutschte,
                   frueher Vorkommen gingen verloren). Die API nimmt bei scope=all jetzt auch rid: start/end beschreiben dann dieses Vorkommen, der Serienbeginn wird um
                   denselben Abstand in Ortszeit verschoben (sommerzeitfest, ganztaegig und schwebend richtig); ohne rid verhaelt sich die API wie bisher.
                   Behoben: Ein Klick auf einen Termin konnte bei ganztaegigen Terminen den falschen Termin anzeigen (Index der Chips nach dem Sortieren).
                   Getestet mit echtem Browser (Chromium/Playwright; Kalender Monat, Woche, Tag, Zeitzonen UTC und Berlin; Calc mit zwei Benutzern) und Go-Tests TestCalSeriesShift und TestSheetRectLock.
                   Nicht getestet: Touch-Geraete (Ziehen und Strg-Tasten gibt es dort nicht; Termin oeffnen und Neuer Termin gehen wie bisher).

2026-10-02  0.15.2 Text: durchgehender Editor. Der ganze Text ist ein einziges bearbeitbares Feld (Absaetze als Bloecke darin): Strg+A, Ziehen mit der Maus und
                   Umschalt+Pfeile markieren ueber Absatzgrenzen; Loeschen, Ueberschreiben, Ausschneiden, Kopieren und Einfuegen funktionieren ueber mehrere
                   Absaetze (eigene Zwischenablage behaelt die Absaetze und ihr Absatzformat), Enter/Rueckschritt/Entf an der Absatzgrenze teilen bzw. verbinden
                   Absaetze. Das Absatz-Locking bleibt: ein Absatz, in dem ein anderer schreibt, ist contenteditable=false (orange); enthaelt die Markierung einen
                   gesperrten Absatz, werden Loeschen, Ueberschreiben und Ausschneiden mit Hinweis abgelehnt (Kopieren geht); Strg+A, Strg+Umschalt+Pos1/Ende und
                   Umschalt+Pfeil ueber einen gesperrten Absatz hinweg werden selbst gesetzt, weil der Browser dort nicht markieren kann. Alle Aenderungen ueber
                   Absatzgrenzen laufen ueber das Absatzmodell (beforeinput abgefangen), nicht ueber das DOM; Tippen innerhalb eines Absatzes bleibt beim Browser
                   (Eingabemethoden/IME, Autokorrektur, Rechtschreibpruefung). Neuzeichnen nur der geaenderten Absaetze (Cursor und Markierung bleiben bei Fremd-
                   aenderungen und Sperrmeldungen stehen; der Absatz mit dem Cursor wird beim Tippen nie aus dem Modell ueberschrieben). Eigenes Rueckgaengig/
                   Wiederholen (Strg+Z, Strg+Y; der Browser-Verlauf ist abgeschaltet): nur solange der Absatz inzwischen nicht von jemand anderem geaendert wurde.
                   Absaetze sind dezent grau unterlegt (Cursorabsatz leicht blau) mit etwas Abstand; Ziehen/Ablegen von Text im Editor ist abgeschaltet.
                   Getestet mit echtem Browser (Chromium/Playwright, zwei Benutzer): Tippen, Enter, Rueckschritt/Entf an Grenzen, Markieren/Loeschen/
                   Ueberschreiben ueber 3-4 Absaetze, Strg+A, Undo/Redo, Kopieren/Einfuegen, Sperre durch zweiten Benutzer (Loeschen/Tippen/Ausschneiden
                   abgelehnt), gleichzeitiges schnelles Tippen in zwei Absaetzen, Fett/Nummerierung ueber mehrere Absaetze, Links, nur-lesen, Calc unberuehrt.
                   Nicht getestet: Eingabemethoden (IME) und Mobilgeraete, Firefox/Safari.
                   Ausserdem: beim schnellen Wechsel der Bereiche konnte die Liste von Files bzw. Kalender die Liste des neuen Bereichs ueberschreiben (behoben).

2026-10-02  0.15.1 WebDAV-Sperren nach litmus-Pruefung (WebDAV-Konformitaetssuite auf Proxmox .112) und Zeitzonen unter Windows. Der If-Header (RFC 4918, 10.4) wird
                   bei PUT, DELETE, MKCOL, MOVE, COPY und PROPPATCH ausgewertet: ein Sperr-Token, das nicht zur aktuellen Sperre der Datei passt (oder bei einer
                   nicht gesperrten Datei), ergibt 412; Listen (ODER), "Not", DAV:no-lock und Ressourcen-Tags werden beachtet, Listen fuer andere Ressourcen
                   zaehlen nicht; ETag-Bedingungen werden gegen den aktuellen ETag geprueft; nennt der Header Sperr-Tokens und keines passt zur Sperre, ergibt das 412 (auch neben "Not <DAV:no-lock>"). Der Besitzer (<owner>) einer Sperre wird gespeichert und in LOCK-Antwort und PROPFIND
                   (lockdiscovery) genannt; nur als Text oder href, nie als rohes Client-XML (kein ungueltiges XML durch fremde Namensraeume). Ein erneutes LOCK desselben
                   Benutzers verlaengert die bestehende Sperre und liefert dasselbe Token (statt sie zu ersetzen und das Token eines anderen Fensters zu entwerten).
                   Unveraendert und gewollt: der Sperrende darf ohne Token schreiben (kein Aussperren nach Absturz des Clients); nicht unterstuetzt bleiben Sperren auf
                   Ordner, geteilte Sperren und PROPPATCH (Explorer und Office sperren Dateien). Tests: davlock_test.go TestWebDAVLockIfHeader; litmus: locks
                   und http-Suite geprueft. Kalender: Test mit Fremd-Software (Python icalendar/caldav/recurring-ical-events, 5 Zeitzonen mit je 29000 Stichproben,
                   CalDAV-Client, Einladungsmail an Test-SMTP) bestanden.

2026-10-02  0.15.0 Kalender: Erinnerungen, Teilnehmer mit Einladung, "dieser und folgende", Import/Export, Abo-Status; Zeitzonen fuer Apple/Outlook; Belegungsuebersicht;
                   WebDAV-Sperren fuer Windows/Office. Erinnerung (VALARM): ein Auswahlfeld im Termin (zum Beginn, 5/10/15/30 Min., 1/2 Std., 1/2 Tage, 1 Woche; hoechstens
                   4 Wochen), gespeichert im Termin (TRIGGER relativ zum Beginn); fremde Erinnerungen (auch RELATED=START) werden gelesen und angezeigt; ohne Angabe
                   bleibt sie beim Bearbeiten erhalten; der Server verschickt keine Erinnerungs-Mails. Teilnehmer: ATTENDEE/ORGANIZER (Benutzername ueber auth.ContactOf oder
                   E-Mail-Adresse, bis 50, Pruefung der Adresse); mit eingerichtetem SMTP gehen Einladung (neue Teilnehmer, METHOD:REQUEST mit .ics), Aktualisierung
                   (bleibende Teilnehmer bei Aenderung von Titel/Ort/Zeit/Regel, SEQUENCE steigt) und Absage (entfernte Teilnehmer, ganzer Termin geloescht, METHOD:CANCEL)
                   im Hintergrund hinaus; Zu-/Absagen werden nicht ausgewertet; Teilnehmer gelten fuer die ganze Serie; Missbrauchsschutz: je Benutzer hoechstens 100
                   Empfaenger pro Stunde (darueber wird gespeichert, nicht gemailt, Antwort "mails":-1 mit Hinweis). chat.SMTP.SendICS und Mailer.Invite/MailEnabled neu.
                   "Dieser und folgende" (PUT scope=following mit rid, DELETE ?scope=following&rid=): die alte Serie endet davor (UNTIL, spaetere Einzeltermine und
                   Ausnahmen entfallen), beim Aendern beginnt eine neue Serie (neue Datei und UID) mit den neuen Angaben; eine feste Anzahl wird auf den Rest
                   umgerechnet, ein Enddatum bleibt; erster Termin = ganze Serie. Antworten von POST/PUT liefern {"file","mails"}. Import/Export: GET /api/cal/{kal}/export.ics
                   (alle Termine mit Zeitzonen) und POST /api/cal/{kal}/import (.ics bis 8 MB, 5000 Termine; bekannte UID wird aktualisiert, Dubletten entstehen nicht;
                   zu feine Wiederholungen entschaerft, Doppelbuchung in Ressourcen uebersprungen; Antwort added/updated/skipped); Leiste unter dem Kalender fuer den
                   gewaehlten Kalender. Abo-Status: meta.Tried/Err, calRow fetched/suberr; Fehler bleiben sichtbar, alte Termine erhalten, neuer Versuch nach 2 Min.,
                   Knopf "Jetzt aktualisieren" (Verwalter); apiRefresh meldet nur noch bereinigte Fehlertexte. VTIMEZONE (cal/vtimezone.go): fuer Termine mit Zone
                   wird die Definition (Sommer-/Winterzeit-Regeln 2007-2030, nur wenn die Regel stabil ist, sonst feste Uebergaenge; die Umstellungen werden durch Abtasten ermittelt, weil ZoneBounds bei den schlanken Zeitzonendaten unter Windows an Jahresgrenzen versagt) eingebettet, damit iOS, macOS und Outlook
                   die Uhrzeit richtig zeigen. Belegungsuebersicht: GET /api/filesusage (nur Admins): je Besitzer Dateien, belegt, davon Papierkorb, Anteil am Kontingent,
                   Summe; Einstellungen > Dateien > "Belegung anzeigen". WebDAV: PROPFIND nennt supportedlock und lockdiscovery (files/davprops.go), ohne die
                   Windows und Office nicht schreiben; echter Test mit Explorer/Word/Excel steht aus (Handpruefung). Oberflaeche: Formularfelder Erinnerung und Teilnehmer,
                   Wahl "Dieser und folgende", Knopf "Diesen und folgende loeschen", Anzeige von Erinnerung/Teilnehmern/Organisator im Termin; 31 neue Texte in allen
                   Sprachen. Tests: cal_extras_test.go (Erinnerung, Teilnehmer mit Test-SMTP-Server, Serie teilen/loeschen, Import/Export), cal/extras_test.go (Limit,
                   Trigger, Regelteile), cal_vtz_test.go, davlock_test.go (PROPFIND-Eigenschaften), trash_test.go (Belegung), TestCalendarSubscription (Status);
                   go test -race ./... gruen; Browsertest (Chromium). Handbuch (de/en) Kapitel 4.4, 4.7, 4.11; README (de/en).

2026-10-01  0.14.2 Papierkorb und WebDAV-Sperren. Papierkorb: Loeschen im Browser, per WebDAV und REST verschiebt die Datei (bei Ordnern jede Datei) nach
                   files/<besitzer>/.trash/<id> mit Metadaten (Ursprungspfad, Loescher, Zeit); der Name ".trash" ist fuer Dateien reserviert (ValidName). Neu in
                   store: Move (FS rename, Mem, S3 Kopie + Loeschen). Aufbewahrung in Tagen: Einstellungen > Dateien (Standard 30, Vorgabe CS_TRASH_DAYS, 0 = kein
                   Papierkorb), stuendlicher Lauf (RunTrash) entfernt Abgelaufene. Der Papierkorb zaehlt zum Kontingent (belegt, Upload-Pruefung); fehlt Platz fuer
                   einen Upload, werden zuerst die aeltesten Eintraege endgueltig geloescht (evictTrash), erst dann 507. Gruppenordner: Verwalter (Gruppen-Admin,
                   bei "alle schreiben" jedes Mitglied) sehen und stellen wieder her; ein Gruppenordner mit Papierkorb-Eintraegen laesst sich erst nach dem Leeren
                   loeschen. Wiederherstellen legt die Datei an den Ursprungspfad zurueck, bei belegtem Namen mit neuem Namen (Antwort {"name"}). REST: GET /api/trash,
                   POST /api/trash/{besitzer}/{id}/restore, DELETE /api/trash/{besitzer}/{id}, DELETE /api/trash[?owner=] (leeren), POST /api/settings/trash {"days"};
                   GET /api/files liefert trash/trashDays, used enthaelt den Papierkorb. Oberflaeche: Eintrag "Papierkorb" in der Seitenleiste (Ort, Groesse, geloescht
                   von/am, laeuft ab; Wiederherstellen, Endgueltig loeschen, Papierkorb leeren), Hinweis beim Loeschen, Kontingentanzeige mit Anteil im Papierkorb,
                   Feld "Papierkorb: Tage" in den Einstellungen; 17 neue Texte in allen Sprachen. WebDAV LOCK/UNLOCK (DAV-Klasse 2): exklusive Schreibsperre je Datei im
                   Speicher (Standard 10 Min., hoechstens 1 Std., Erneuerung per If-Token, lock-null legt eine leere Datei an, 201); OPTIONS meldet "DAV: 1, 2, 3"
                   und Allow mit LOCK/UNLOCK (Windows-Explorer, Word, Excel). Die Sperre gilt fuer alle Wege (WebDAV, Browser-Upload, Loeschen, Verschieben): andere
                   Benutzer erhalten 423, der Sperrende darf auch ohne Token schreiben (kein Aussperren nach Absturz des Clients); Sperren sind nur im Speicher
                   (Neustart: Clients sperren neu), hoechstens 5000. Tests: store TestMove, files/trash_test.go (Ablauf, Verdraengung), trash_test.go (Loeschen,
                   Wiederherstellen, Namenskonflikt, Ordner, Gruppen, Einstellung, Rechte, Kontingent), davlock_test.go (LOCK, Erneuerung, UNLOCK, lock-null, fremde
                   Benutzer 423, Ablauf, OPTIONS); livetest: LOCK und Papierkorb als feste Pruefungen; Browsertest (Chromium, de/en). Handbuch (de/en) Kapitel 4.7,
                   4.11, 6, 9 und Umgebungsvariable CS_TRASH_DAYS; README (de/en). Offen: WebDAV-Range im RAM-Speicher (Mem ist kein ReadSeeker; FS und S3 gehen).

2026-10-01  0.14.1 Haertung und Dateien. KI-Widget: Knopf erscheint nach dem Speichern der KI-Einstellungen sofort (und verschwindet beim Ausschalten), kein Neuladen
                   mehr noetig. Chat (Audit C-07, C-08/C-09, C-13/C-14): Bearbeiten und Reaktionen verlangen Schreibrecht im Kanal (bisher nur Leserecht) und zaehlen
                   zum Tempolimit (20 je 10 s mit dem Senden); Speicherfehler beim Senden, Bearbeiten und in der Auswertung werden gemeldet statt verschluckt (die
                   Aenderung wird zurueckgenommen, "storage error"); ein geloeschter Kanal wird von laufenden Schreibern nicht mehr neu angelegt (Rennen in
                   RemoveChannel); WebSocket mit Lebenszeichen (Ping alle 30 s, 20 s Frist), tote Verbindungen werden freigegeben; hoechstens 8 Verbindungen je
                   Benutzer, die aelteste weicht. Nachricht (C-11): Kuerzung fuer Discord nach Zeichen (1900), nie mitten im UTF-8-Zeichen; Weiterleitungen bei POST
                   waren schon gesperrt (307/308 geben den Text nicht weiter). Versandprotokoll: je Gruppe die letzten 200 Eintraege (eine aktive Gruppe verdraengt
                   die anderen nicht), insgesamt 2000. Aufgaben (A-04): "offen" mit Bearbeiter ist der gewollte Zustand "zugewiesen, noch nicht begonnen" - keine
                   Aenderung, im Audit als beabsichtigt vermerkt. Dateien: Download mit Teilbereichen (Range, einzelner Bereich, If-Range; 206/416, Accept-Ranges) und
                   bedingten Abrufen (If-None-Match -> 304, Cache-Control private,no-cache) - Videos lassen sich spulen, grosse Dateien setzen nach Abbruch fort;
                   Zugriffspruefung vor jedem 304, fremde Dateien bleiben 404. Test: tasks.TestSpawnAndTick ohne Datenrasur (Fehler lag im Test). Neue Tests:
                   harden_test.go (Range/304, Rechte und Tempolimit bei Reaktionen, Verbindungslimit), chat/harden_test.go (Protokoll je Gruppe, aelteste Verbindung);
                   Browsertest KI-Widget (Chromium). Offen bleiben C-12 (nur Dokumentation) und WebDAV-Range (x/net/webdav).

2026-10-01  0.14.0 Kalender: Serientermine, Zeitzonen und Terminbearbeitung. Anzeige: GET /api/cal/{kal}/events?from=&to= (RFC 3339 oder YYYY-MM-DD, hoechstens 800 Tage,
                   ohne Angabe -31/+400 Tage) loest Serien serverseitig auf (RRULE, EXDATE, RDATE, RECURRENCE-ID, STATUS:CANCELLED; je Serie bis 1500 Vorkommen); Zeilen
                   tragen rec/ovr/rid/rule/tz/float/desc. Zeitzonen: IANA und gaengige Windows-Namen (z.B. "W. Europe Standard Time") werden erkannt, Zeitzonen sind
                   eingebettet (time/tzdata, auch unter Windows ohne tzdata); Termine ohne Zone ("schwebend") und unbekannte Zonen erscheinen als Ortszeit des
                   Betrachters statt als UTC. Oberflaeche: Termine werden fuer den sichtbaren Zeitraum nachgeladen; Serien mit Kreispfeil; Klick zeigt Einzelheiten
                   mit Bearbeiten/Loeschen. Neu: PUT /api/cal/{kal}/events/{datei} (Titel, Ort, Beschreibung, Beginn, Ende, ganztaegig, Wiederholung taeglich/woechentlich/
                   monatlich/jaehrlich mit Intervall und Ende nie/Anzahl/Datum; scope=one aendert nur ein Vorkommen als Einzeltermin mit RECURRENCE-ID, scope=all die
                   ganze Serie), DELETE ...?scope=one&rid= (EXDATE) bzw. ganze Serie; POST /api/cal/{kal}/events nimmt tz, description und rule an. Neue Termine werden mit
                   TZID der Browser-Zeitzone gespeichert, damit eine Serie ueber die Sommerzeit-Umstellung gleich bleibt. Die Regel baut der Server selbst (keine fremde
                   RRULE aus der Anfrage; 1..99 Intervall, bis 999 Mal). Aenderung von Beginn oder Wiederholung der ganzen Serie verwirft Einzeltermine und Ausnahmen;
                   fremde Zonen und komplexe Regeln (BYDAY ...) bleiben beim Aendern von Text/Zeit unveraendert. Aenderungen laufen unter der Sperre je Kalender (Ressourcen-
                   Pruefung beim Bearbeiten, ETag-Pruefung, 409 bei zwischenzeitlicher Aenderung). Die KI-Terminuebersicht sieht Serien jetzt korrekt. 22 neue Texte in
                   allen Sprachen. Tests: cal_events_test.go (Serie ueber Sommerzeit, Ausnahmen, Windows-/schwebende/unbekannte Zonen, Bearbeiten/Loeschen, fremde Daten
                   bleiben erhalten, Rechte, Ressource); Browsertest (Chromium, Zeitzone Europe/Berlin). Handbuch (de/en) Kapitel 4.4. Nicht getestet: Thunderbird/iOS/Outlook
                   mit den neuen Terminen (TZID ohne VTIMEZONE), Serien "dieser und folgende".

2026-10-01  0.13.9 Audit "mittel" abgearbeitet (S-09, S-11, K-05, F4, F8, F9). S-09: Chat-/Webhook-Adressen enthalten Zugangsschluessel und sind nur noch fuer den
                   Benutzer selbst und globale Admins sichtbar und aenderbar; Gruppen-Admins sehen "gesetzt/nicht gesetzt" (chatSet), duerfen bei anderen nur die
                   E-Mail aendern; CSV-Export ohne fremde Adressen, Import lehnt fremde Adressen ab, neuer Benutzer durch Gruppen-Admin ohne Adresse.
                   S-11: Auth.refresh laedt ohne Sperre (Einzelflug, Zeitlimit 5 s, 2 s Pause nach Fehler); ein haengender Speicher blockiert keine Anmeldungen
                   mehr, abgelaufene Daten werden weiterverwendet, nach Aenderungen wird frisch geladen. K-05: Ressourcen-Kalender pruefen und speichern unter
                   einer Sperre je Kalender (keine Doppelbuchung bei gleichzeitigen Anfragen, CalDAV und Oberflaeche); Serien (RRULE, EXDATE, RECURRENCE-ID,
                   abgesagte Termine) werden bis 2 Jahre voraus geprueft; Meldung in der Zeitzone des Termins bzw. UTC mit Zonenangabe. F4: Rechteentzug
                   (Freigabe, Dokument geloescht, Konto/Bereich) trennt offene Calc-/Text-Verbindungen sofort bzw. spaetestens nach 15 s; die Oberflaeche
                   meldet "Kein Zugriff mehr auf dieses Dokument." und laedt neu. F9: Dateimetadaten werden parallel (16) und im Einzelflug geladen. F8:
                   Kontingent je Benutzer und Gruppenordner (Einstellungen > Dateien, MB, 0 = unbegrenzt; Startparameter CS_QUOTA_MB als Vorgabe) fuer Web und
                   WebDAV, Antwort 507, parallele Uploads ueberschreiten es nicht, ersetzte Datei zaehlt nicht doppelt; Anzeige "belegt X von Y" in Dateien.
                   Neu: POST /api/settings/quota (Admin), GET /api/settings liefert quotaMB, GET /api/files liefert quota/used. Tests: audit_mid_test.go
                   (TestChatURLPrivacy, TestResourceRace, TestWSRevoke, TestQuota), auth/refresh_test.go, files/files_test.go; auch mit -race. 7 neue Texte in
                   allen Sprachen. Handbuch (de/en) aktualisiert. csweb-gui: admin.pl-Version/Zeitstempel angepasst.

2026-10-01  0.13.8 Videochat Platz 4: eingebautes Ad-hoc-WebRTC (nur Browser, bis 6 Teilnehmer). In den Einstellungen (Panel "Videochat eingebaut") einschalten,
                   STUN-Server (vorbelegt: oeffentlicher STUN, aenderbar/leerbar) und optional TURN-Server mit coturn-Geheimnis (use-auth-secret) eintragen.
                   Im Chat steht unter "Videochat" zusaetzlich "WebRTC (Browser)": jeder mit Schreibrecht im Kanal kann starten; es entsteht eine Einladung
                   (24 Stunden, Karte mit "Beitreten"); laeuft schon ein Anruf, tritt ein erneuter Start diesem bei. Ansicht: Vollbild-Ueberlagerung mit
                   Kachelraster, Mikrofon aus/an, Kamera aus/an, Auflegen; Geraete ohne Kamera nehmen nur mit Ton teil. Bild und Ton laufen als Mesh direkt
                   zwischen den Browsern (Neuer ruft die Anwesenden an), der Server vermittelt nur Angebot/Antwort/ICE ueber den vorhandenen Chat-WebSocket
                   (rtcjoin, rtcsig, rtcleave; Antworten rtcjoined, rtcpeer, rtcsig, rtcleft). Beitritt nur mit Leserecht im Kanal und gueltiger Einladung,
                   hoechstens 6 je Raum ("Raum voll"), Signal hoechstens 14 KB und 300 je 10 s und Verbindung, eine Verbindung = ein Raum; Verlassen beim
                   Schliessen der Verbindung. TURN-Zugang zeitlich begrenzt (12 Stunden, HMAC-SHA1 aus dem Geheimnis, an den Benutzer gebunden); das Geheimnis
                   wird nie ausgegeben. Neu: POST /api/settings/rtc (Admin), GET /api/settings liefert "rtc"; POST /api/chat/{g}/{c}/video mit slot 3 liefert
                   {rtc,id}. Neu: chat/rtc.go, rtc_test.go (Rechte, Signalisierung, Voll, Rate-Limit, Ablauf, Ausschalten; auch mit -race). Browsertest mit 3 und
                   6 Teilnehmern (Chromium, Testkamera): Bild und Ton kommen bei allen an. 24 neue Texte in allen Sprachen. Handbuch (de/en): Chat 4.9 und
                   Einstellungen 4.11. Nicht getestet: echte Firewalls/TURN, Safari/Firefox, Mobilgeraete, Bandbreite bei 6 Teilnehmern (Mesh).

2026-10-01  0.13.7 Videochat im Chat ueber externe Server (Schritt 1). In den Einstellungen (Panel "Videochat") drei Optionen, je Option Anbieter waehlen (Jitsi,
                   MiroTalk oder eigene Adresse mit {room}) und Server eintragen oder abschalten: 1 fester Raum je Gruppe (alle Mitglieder), 2 Ad-hoc-Raum nur
                   fuer Gruppen-Admins, 3 Ad-hoc-Raum fuer alle mit Schreibrecht im Kanal. Im Chat erscheint links unter der Gruppenliste der Abschnitt
                   "Videochat" mit den erlaubten Optionen; Klick oeffnet den Raum im neuen Tab und stellt eine Einladung (Karte mit "Beitreten") in den Kanal.
                   Ad-hoc-Einladungen gelten 24 Stunden (danach 410/abgelaufen), zweiter Klick innerhalb 1 Minute (Ad-hoc) bzw. 30 Minuten (fester Raum) erzeugt keine
                   zweite Einladung. Raumname = Gruppe + HMAC aus einem Server-Geheimnis (nicht erratbar), steht nie in der Nachricht und wird beim Beitritt neu
                   berechnet (Leserecht + Ablaufpruefung). cs-team ruft die Videoserver nie auf. Adresse muss http(s) mit genau einem {room} sein, keine
                   Zugangsdaten. Neu: POST /api/settings/video (Admin), POST /api/chat/{g}/{c}/video, GET /api/chat/{g}/{c}/video/{id}; Nachrichten und
                   Gruppenliste tragen "vid" bzw. "video". Einladungen sind nicht bearbeitbar. 16 neue Texte in allen Sprachen. Test: video_test.go. Handbuch
                   (de/en): Chat 4.9 und Einstellungen 4.11. Offen: Platz 4 eingebautes Ad-hoc-WebRTC (4-6 Nutzer), BigBlueButton (siehe TODO.md).

2026-10-01  0.13.6 KI-Einstellungen vereinfacht: gefuehrter Ablauf statt vieler Felder. Schalter "KI aus / KI ein"; Anbieter aus der Liste waehlen, ein Popup fragt
                   den API-Schluessel (bei Ollama/Inhouse stattdessen IP:Port, Schluessel optional) und prueft ihn sofort ueber die Modell-Liste des Anbieters:
                   falscher Schluessel -> Meldung im Popup, Popup bleibt offen; gueltig -> Auswahlliste der Modelle (Bilderkennung markiert) mit "Eigenes Modell
                   eingeben ..." als Ausweg; ohne Pruefmoeglichkeit "Ohne Pruefung uebernehmen"; "Abbrechen" stellt den alten Stand wieder her. Kurzzeile
                   "Anbieter [..] Schluessel geprueft [Schluessel aendern ...]"; Protokoll, Endpunkt, IP:Port und Schluesselfeld stehen eingeklappt unter
                   "Erweitert"; gespeicherte Konfiguration laedt die Modell-Liste still nach; Speichern ohne Modell wird abgelehnt. Zweiter Anbieter mit demselben
                   Ablauf. Nur Oberflaeche (web/index.html, 18 neue Texte in allen Sprachen); Server und Schnittstellen unveraendert. Handbuch (de/en) mit neuem
                   Ablauf und Popup-Bild. TODO: Videochat im Chat als Option vorgemerkt (Jitsi, MiroTalk, eigene URL, eingebautes WebRTC, spaeter BigBlueButton).

2026-10-01  0.13.5 KI liest PDF-Dateien: PDFs aus der Dateiablage lassen sich im KI-Widget wie andere Dateien zur Auswertung waehlen. Der Text wird im Server
                   gelesen (eigener Leser, nur Go-Standardbibliothek, keine neue Abhaengigkeit, kein OCR): Flate/ASCIIHex/ASCII85, Objektstroeme, Schriften
                   mit ToUnicode-Tabelle (Chrome/Word/LibreOffice/reportlab), WinAnsi-Einzelbyteschriften, Ligaturen aufgeloest, Seiten durch Leerzeilen
                   getrennt. Abgelehnt mit klarer Meldung: verschluesselte PDFs, gescannte PDFs ohne Text, beschaedigte Dateien. Grenzen wie bei anderen
                   Dateien: 2 MB, 40000 Zeichen je Datei, 4 Elemente je Frage; Entpackgrenzen gegen Zip-Bomben (32 MB je Stream, 128 MB gesamt, 500 Seiten).
                   Es wird nichts ausgefuehrt (kein JavaScript, keine Anhaenge, keine Formulare); Rechte wie bisher die des Fragenden. Tests: conv/pdf_test.go
                   (inkl. Zip-Bombe, abgeschnittene/beschaedigte Dateien), TestAIFiles um PDF erweitert.

2026-10-01  0.13.4 KI-Einstellungen wie beim napp-it AI Helpdesk: Anbieter-Vorlagen (15 Anbieter, Liste als Datei providers.txt im Datenspeicher, kuerzbar; fehlt
                   sie, gilt die eingebaute Liste), Modell-Liste live vom Anbieter ("Modelle laden", Server fragt mit dem gemerkten Schluessel, Modelle mit
                   Bilderkennung markiert), Adressfeld IP:Port fuer eigene Server (Ollama, Inhouse), Schluessel werden je Zielrechner gemerkt (beim Zurueckwechseln
                   wieder da, nie angezeigt, "Schluessel loeschen" entfernt ihn), zweiter Anbieter springt ein, wenn der erste nicht antwortet, einstellbare
                   Grenzen (Nachrichten Verlauf, Zeichen je Nachricht). Neu: GET /api/ai/providers, POST /api/ai/models (nur Admin, mit Adresspruefung). Tests:
                   ai_provider_test.go. Handbuch (de/en) aktualisiert: Benutzer loeschen mit Snapshot, Windows-Snapshot/CS_SNAPSHOT_DATASET, Sicherheit,
                   KI-Dokumente, Gruppenschalter, KI-Einstellungen.

2026-10-01  0.13.3 KI Stufe 2: Dokumente vorschlagen, Anlegen nur nach Bestaetigung. Die KI hat weiterhin keine Schreibrechte: sie darf in ihrer Antwort EINEN
                   Vorschlag (Text-Dokument oder Calc-Tabelle) machen, der Browser zeigt eine Vorschau (vom Server geprueft), erst "Anlegen" speichert.
                   Der Server legt ein NEUES Dokument im Besitz des Benutzers an (Recht Text/Calc des Benutzers, Rate-Limit); Vorhandenes wird nie
                   geaendert oder geloescht. Grenzen: 300 Absaetze / 60000 Zeichen, 200 Zeilen x 26 Spalten, 3000 Zellen; Steuerzeichen werden entfernt;
                   Formeln nur mit Zahlen/Zellbezuegen und der Calc-Funktionsliste (SUM, AVERAGE, MIN, MAX, COUNT, COUNTA, PRODUCT, ROUND, ABS, SQRT, AND,
                   OR, NOT, IF). Schalter: Einstellungen "KI darf Dokumente vorschlagen" (global, Standard aus) UND je Gruppe "KI fuer Mitglieder"
                   (Gruppen-Admin oder globaler Admin schaltet, Standard aus); Admins und Gruppen-Admins duerfen immer, Mitglieder nur in einer
                   freigeschalteten Gruppe. Die Anweisung an die KI erscheint nur fuer Berechtigte. Protokoll: nur Benutzer, Typ, Anzahl (nie Inhalte).
                   Neu: POST /api/ai/create (confirm=false Vorschau, true anlegen), POST /api/groups/{name}/ai. Tests: ai_create_test.go.

2026-10-01  0.13.2 Haertung nach Audit (S-03/S-04, S-06, S-07, S-08, S-10, K-02/C-10, K-03, C-06, F7, C-11): Anmelde-Schutz: bei CS_TRUST_PROXY=1 zaehlt nur das
                   LETZTE Element von X-Forwarded-For; zusaetzliche Sperre je Adresse (20 Fehlversuche, alle Namen) und je Benutzername (100, alle Adressen);
                   erfolgreiche Anmeldungen werden 45 s gemerkt (kein bcrypt je Anfrage; Passwortaenderung/Sperre wirken sofort); Bereinigung entfernt keine
                   aktiven Sperren mehr. Security-Header (X-Frame-Options, nosniff, Referrer-Policy, CSP, HSTS bei TLS). Schluesselpruefung auch fuer S3
                   (wie Ordner-Speicher). Gemeinsame Sperrliste fuer Kalender-Abos, Webhooks und KI (CGNAT 100.64/10, 0/8, 192.0.0/24, 198.18/15, NAT64,
                   Reserviert); Feed-Fehlertext ohne Netzwerkdetails; Webhooks folgen keinen Weiterleitungen mehr (307/308), Discord-Kuerzung an UTF-8-Grenze.
                   CalDAV/Abo: Wiederholungen feiner als taeglich werden abgelehnt (PUT) bzw. beim Abo-Import entfernt. Chat: kein Senden auf geschlossenen
                   Kanal mehr (Race beim Trennen). Export: CSV-Zellen mit = + - @ bekommen ein ' (Formel-Injektion), xlsx schreibt nur Formeln aus der
                   Calc-Funktionsliste, alles andere als Text. Gruppe "users" kann nicht angelegt werden. Benutzer loeschen: Gruppen-Admin-Eintraege
                   werden entfernt; neuer Assistent mit Vorschau (Anzahl Dateien/Dokumente/Kalendereintraege), Passwort und Snapshot (oder ausdruecklich
                   ohne) loescht Dateien, Dokumente, Kalender und Freigaben des Namens; Nachrichten/Aufgaben in Gruppen bleiben. Protokoll: users/_delete-log.json.

2026-10-01  0.13.1 Snapshot-Erkennung unter Windows (OpenZFS on Windows): Laufwerksbuchstabe -> Datentraegerbezeichnung = Pool, Ordner/Junctions darunter = Datasets
                   (D:\data -> winpool/data, laengster passender Dataset-Pfad). Neu: CS_SNAPSHOT_DATASET legt das Dataset fest. Fix: doppelte Element-ID
                   in den KI-Einstellungen (Modus-Auswahl ueberschrieb die Meldungsanzeige).

2026-10-01  0.13.0 Assistent fuer globale Aktionen: Vorlagenwahl + neue Vorlage "Chat-Auswertung" (nur globale Admins; Mobbing-Vorfaelle, Loeschanforderungen).
                   Suche ueber alle oder gewaehlte Gruppen/Kanaele nach Autor, Zeitraum und Suchbegriffen (alle/einer), mit Zusammenhang +-n Nachrichten.
                   Jede Aktion verlangt einen Anlass (Aktenzeichen) und wird ohne Nachrichteninhalt protokolliert (chat/_audit.json: Admin, Zeit, Filter,
                   Anzahl, Pruefsumme, Snapshot). Beweissicherung: ZIP mit messages.csv/json, Anhaengen und manifest.txt (SHA-256 je Datei); SHA-256 des
                   ZIP wird angezeigt (Header X-Content-SHA256) und protokolliert; Passwort noetig. Loeschen: Auswahl -> Vorschau mit Pruefsumme -> Passwort
                   -> Snapshot (oder ausdruecklich ohne) -> Nachrichten samt Anhaengen loeschen, wahlweise mit Hinweis "geloescht" (Autor/Zeit bleiben)
                   oder Eintrag ganz entfernen; Hinweis, dass ein Snapshot die Inhalte bis zu seiner Vernichtung enthaelt. Optional KI-Auswertung
                   (Einstellung "Globale Admins duerfen Chat-Vorfaelle mit KI auswerten", Standard aus, Bestaetigung je Auswertung, Server holt die
                   Nachrichten selbst, Nachrichten gelten als nicht vertrauenswuerdig; nur Anzahl/Anlass im Protokoll).

2026-10-01  0.12.2 Snapshot-Erkennung robuster: ohne Shell (kein Quoting-Problem unter Windows), Dataset notfalls ueber den laengsten passenden Mountpoint
                   (z.B. OpenZFS on Windows, wo "zfs list <pfad>" nicht geht). Testet mit Attrappe (snapshot_test.go).

2026-10-01  0.12.1 Jahrgangswechsel, Modus "Gruppe wird umbenannt" (Gruppen als Klassen): 5a heisst danach 6a und nimmt Mitglieder, Gruppenordner (Dateien),
                   Freigaben an die Gruppe, Gruppen-Admins und - einzeln waehlbar - Kalender, Chat und Aufgaben mit; darunter entsteht eine neue leere
                   Gruppe 5a (Einstellungen kopiert, Wiederholer wechseln dorthin), die oberste Stufe wird umbenannt (ehem-7a-2026) oder bleibt (dann sind
                   die Stufen darunter blockiert). Vorschau/Passwort/Pruefsumme/Sicherung wie beim Mitglieder-Wechsel; Rueckgaengig benennt in umgekehrter
                   Reihenfolge zurueck, solange die neuen Gruppen leer sind (sonst Hinweis auf den Snapshot). Snapshot vor jeder globalen Aktion:
                   bei Ordner-Speicher (CS_DIR) wird das ZFS-Dataset erkannt (zfs snapshot <dataset>@cs-team-<id>), sonst CS_SNAPSHOT_CMD (z.B. mit
                   cs-freeze4snap; {id} = Lauf-ID), CS_SNAPSHOT=off schaltet aus. Schlaegt der Snapshot fehl, wird nichts geaendert; ist keiner moeglich,
                   muss der Admin den Lauf ausdruecklich ohne Snapshot bestaetigen.

2026-10-01  0.12.0 Assistent fuer globale Aktionen (Vollbild): Gruppen > "Assistent: Jahrgangswechsel...". Verschiebt die Mitglieder jeder Gruppe mit Zahl
                   im Namen in die Folgegruppe (5a -> 6a, 9b -> 10b, 09 -> 10). Abgaenger (Gruppen ohne Folgegruppe): unveraendert lassen, in eine Gruppe
                   verschieben (wird angelegt), Konten sperren oder nur aus der Gruppe entfernen. Wiederholer: je Gruppe eine Merkliste, die der
                   Gruppen-Admin (Lehrer) selbst pflegt; in der Vorschau je Konto Haken "wechselt/bleibt", Gruppen einzeln abwaehlbar. Globale Admins
                   und Gruppen-Admins werden nie verschoben. Ablauf: Vorschau (aendert nichts) -> Ausfuehren nur mit erneuter Passworteingabe und Pruefsumme
                   der Vorschau (Datenstand geaendert = abgelehnt) -> Sicherung der betroffenen Konten -> Protokoll/Verlauf mit "Rueckgaengig".
                   Chats, Gruppenordner, Kalender und Aufgaben gehoeren zur Gruppe und werden nicht mitgenommen. Nur globale Admins.

2026-10-01  0.11.1 KI-Assistent: Dateien und Dokumente lesen und auswerten. Neuer Knopf im Widget waehlt bis zu 4 eigene oder mit dem Benutzer geteilte
                   Dateien (auch Gruppenordner) und Calc-/Text-Dokumente aus; die KI fasst zusammen, analysiert, rechnet oder uebersetzt. Unterstuetzt:
                   Text/CSV/JSON/Code, DOCX, XLSX, Calc, Text-Dokumente, Bilder (bei Bilderkennung); PDF noch nicht. Der Server liest die Auswahl mit den
                   Rechten des Fragenden ueber die normalen Routen (fremde Dateien werden abgelehnt), max. 2 MB je Datei, 40000 Zeichen je Datei und
                   80000 gesamt (sonst gekuerzt). Der Inhalt gilt fuer die KI als nicht vertrauenswuerdig (keine Anweisungen aus Dateien). Admin-Schalter
                   in Einstellungen > KI-Assistent. Weiterhin nur lesend; Schreibaktionen (Text/Calc erzeugen, Aufgabenplanung, Semesterwechsel) folgen
                   spaeter, Freigabe je Gruppe durch den Gruppen-Admin (Lehrer).

2026-10-01  0.11.0 KI-Assistent: Chat-Widget (Knopf "KI" unten rechts) fuer alle Benutzer. Anbieter, Modell und API-Schluessel stehen zentral in
                   Einstellungen > "KI-Assistent (fuer alle)": Anthropic, OpenAI-kompatibel (OpenAI, OpenRouter, eigene Server) oder Ollama; Endpunkt im
                   lokalen Netz nur nach ausdruecklicher Freigabe. Sechs Status-Knoepfe (Aufgaben, Termine, Dateien, Chat, Gruppen, System/Mein Konto):
                   der Server holt die Daten mit den Rechten des Fragenden ueber die normalen Routen - Benutzer sehen nur eigene Daten, Gruppen-Admins
                   ihre Gruppen, globale Admins alles. Daneben normaler KI-Chat in der eingestellten Sprache; Bilder per Datei, Drag & Drop oder
                   Einfuegen (verkleinert) bei Modellen mit Bilderkennung. Nur lesend: die KI aendert nichts. Antworten werden nur als Text
                   dargestellt, der Schluessel verlaesst den Server nie, Protokoll ohne Inhalte, Limit pro Benutzer und Minute.

2026-10-01  0.10.4 Sicherheit (siehe AUDIT.md): CSRF-Schutz (schreibende Anfragen nur von gleichem Ursprung; DAV-/CalDAV-Apps und curl ohne Origin-Header
                   funktionieren weiter), WebDAV liefert Dateien nur noch als Download (nosniff + CSP sandbox; kein Stored-XSS mehr), Gruppen-Admins
                   duerfen nur Konten verwalten/hinzufuegen, die ausschliesslich in von ihnen verwalteten Gruppen sind (kein Konto-Uebernahme per
                   Passwort-Reset), HTTP-Server mit Timeouts (Slowloris), Warnung bei HTTP ohne TLS, HSTS bei TLS, Webhook-Fehlermeldungen ohne URL,
                   SMTP-Passwort wird bei neuem Server/Benutzer verworfen, Calc-CSV-Export speicherschonend (Spalten bis ZZ), CalDAV-Body max. 1 MB,
                   Aufgabe: Faelligkeitsmeldung nach Datumsaenderung wieder moeglich.
                   Aenderung im Verhalten: Gruppen-Admins koennen bestehende Benutzer, die in anderen Gruppen sind oder nur in alluser, nicht mehr selbst
                   in ihre Gruppe holen (globaler Admin, oder neue Konten per Anlegen/CSV-Import).

2026-10-01  0.10.3 Oeffentliches GitHub-Repository (BSD 2-Clause), Release-Build fuer 8 Varianten (build-all.ps1), cs-team version zeigt die Versionsnummer,
                   englische README (README.de.md = ausfuehrliche deutsche Beschreibung), Handbuch als PDF (de/en) auf napp-it.org.

2026-10-01  0.10.2 Kopfzeile: Titel und Benutzername brechen nicht mehr um, Menuepunkte etwas enger (alle Menues inkl. Einstellungen passen bei 1280 px).

2026-10-01  0.10.1 Einstellungen als sichtbarer Menuepunkt "Einstellungen" in der Navigation (nur globale Admins; Mail/SMTP, oeffentliche Adresse/DynDNS,
                   Webhooks). Der Klick auf den Titel "cs-team" funktioniert weiterhin.

2026-10-01  0.10.0 Handy-Layout: auf schmalen Bildschirmen (bis 760 px) wird erst die Liste, nach Antippen der Inhalt gezeigt, mit Zurueck-Pfeil
                   links oben. Navigation seitlich wischbar, Eingabefelder 16 px (kein Auto-Zoom), Chat-Kanaele als Streifen, Aktionsknoepfe
                   (Reaktion, Bearbeiten, Loeschen) auf Touch-Geraeten immer sichtbar. Desktop unveraendert. Calc per Touch eingeschraenkt
                   (Eingabe ueber die Formelzeile).

2026-10-01  0.9.3  Einstellungen in cs-team selbst (Klick auf den Titel "cs-team", nur globale Admins): Mail (SMTP: Server, Port, Verschluesselung, Benutzer,
                   Passwort, Absender, Testmail an mich), oeffentliche Adresse (DynDNS; Links zu Aufgaben in Benachrichtigungen, Aufruf /#task/<id>) und
                   "Webhooks in private Netze erlauben". Gespeichert im Speicher (settings.json), wirkt sofort ohne Neustart, das Passwort wird nie
                   zurueckgegeben. Umgebungsvariablen CS_SMTP_*/CS_CHAT_ALLOW_PRIVATE bleiben als Vorgabe. Das csweb-gui-Menue 17_cs-team setzt nur noch
                   Startparameter (SMTP-Felder dort wieder entfernt). Import/Export der Benutzer: Spalten 4 und 5 = E-Mail und Chat-Adresse
                   (leer = unveraendert).

2026-09-30  0.9.2  Benutzer anlegen: Felder E-Mail-Adresse und Chat-Adresse (optional). Benutzer bearbeiten: die beiden Felder sehen und aendern auch
                   Gruppen-Admins fuer die Mitglieder ihrer Gruppen (bisher nur globale Admins).

2026-09-30  0.9.1  Erster Login mit Startpasswort: der Dialog fragt nur noch "neues Passwort" + "wiederholen" (kein zweites Mal das Startpasswort);
                   das neue Passwort muss sich vom Startpasswort unterscheiden. Die Passwort-Aendern-Funktion im Konto bleibt unveraendert.

2026-09-30  0.9.0  Aufgaben (Ticketsystem light): neues Hauptmenue. Aufgabe mit Titel, Beschreibung, Prioritaet, Faelligkeit, Meilensteinen
                   (Text + Datum, abhakbar), Auftraggeber, optional Gruppe, Bearbeiter (leer = "Bitte bearbeiten": jedes Gruppenmitglied
                   kann uebernehmen), Beteiligten und Verweisen auf andere Aufgaben. Status Offen -> In Arbeit -> Erledigt -> Abgenommen
                   (Auftraggeber/Gruppen-Admin nimmt ab oder oeffnet wieder). Verlauf wie im Chat: Kommentare (Hintergrund je Rolle:
                   Auftraggeber blau, Bearbeiter gruen, andere grau; bearbeiten/loeschen) und Systemzeilen. Wiederholung (taeglich, woechentlich,
                   monatlich, jaehrlich): nach der Abnahme entsteht genau eine Folgeaufgabe, Faelligkeit und Meilensteine ruecken weiter.
                   Filter: Meine, Von mir, Bitte bearbeiten, Faellig in 7 Tagen, Ueberfaellig, Alle offenen, Abgeschlossen. Live per WebSocket,
                   Badge im Menue und Desktop-Hinweis bei Neuigkeiten. E-Mail/Webhook (wie Nachricht) bei Zuweisung, Uebernahme, Erledigt,
                   Abnahme, Kommentar und wenn Aufgabe/Meilenstein faellig wird (stuendliche Pruefung). Gruppen-Einstellung "Aufgaben anlegen":
                   jedes Mitglied (Standard) / nur Admins / aus; Aufgaben ohne Gruppe darf jeder anlegen (nur Beteiligte und Admins sehen sie).
                   Nicht enthalten: Unteraufgaben, Zeiterfassung, Anhaenge an Aufgaben.

2026-09-30  0.8.0  Chat und Nachricht: zwei neue Hauptmenues. Chat (Discord-artig): ein Chat je Gruppe des Benutzers, Kanaele
                   (#allgemein immer, weitere je nach Gruppeneinstellung "Kanaele anlegen": nur Admins / jedes Mitglied / niemand),
                   Live per WebSocket, Anhaenge (Upload, Drag&Drop, Bild einfuegen; Bilder inline), @Name-Hervorhebung, Reaktionen
                   (6 Emojis), Bearbeiten/Loeschen (Autor, Gruppen-Admin), ungelesen-Markierung, Desktop-Benachrichtigung, Links
                   klickbar, aeltere Nachrichten nachladen (max. 1000 je Kanal). Nachricht: Text an alle Mitglieder einer Gruppe per
                   E-Mail (eingebauter SMTP-Client), an die externe Chat-Adresse jedes Mitglieds (Webhook, Slack/Discord/Telegram/
                   ntfy) und/oder in den Gruppen-Chat; Versandprotokoll. Benutzer-Einstellungen: E-Mail und Chat-Adresse.
                   Gruppen-Einstellungen: Chat = Mitglieder schreiben / nur Admins schreiben / aus; Nachricht senden = nur Admins /
                   jedes Mitglied / aus (Standard: nur Admins). Server: CS_SMTP_HOST/PORT/USER/PASS/FROM/TLS, CS_CHAT_MAX_MB (10),
                   CS_CHAT_ALLOW_PRIVATE=1 (Webhooks in private Netze, sonst gesperrt). Grenzen: Rate-Limit Chat 20/10 s, Nachricht
                   6/min.

2026-09-30  0.7.0  Calc als "Excel light": Zellen markieren (Maus ziehen, Shift+Klick/Pfeile, Spalten-/Zeilenkopf, Strg+A), Formelzeile mit
                   Zellname, Statuszeile mit Summe/Mittelwert/Anzahl/Min/Max der Markierung, Sigma-Knopf (Summe einfuegen).
                   Bedienung wie Tabellenkalkulation: tippen startet die Eingabe, F2/Doppelklick bearbeitet, Enter/Tab bewegen,
                   Entf leert, Strg+C/X/V (Bezuege verschieben sich), Strg+D/R ausfuellen, Strg+B/I/U. Formate wirken auf die
                   ganze Markierung. Neuer Rechenkern: + - * / ^ & % Vergleiche, Klammern, Text, $A$1, Bereiche, Funktionen
                   SUMME/SUM, MITTELWERT/AVERAGE, MIN, MAX, ANZAHL/COUNT, ANZAHL2/COUNTA, WENN/IF, RUNDEN/ROUND, ABS, WURZEL/SQRT,
                   PRODUKT/PRODUCT, UND/AND, ODER/OR, NICHT/NOT (; oder , als Trenner), Fehler #DIV/0! #WERT! #NAME? #BEZUG! #ZIRK!.
                   xlsx-Export schreibt Formeln als Formeln mit englischen Funktionsnamen (Import liest sie zurueck). Tabelle jetzt
                   26 Spalten x 100 Zeilen.

2026-09-30  0.6.7  Text: Zeichenformate (auch Hintergrund-/Textfarbe) wirken jetzt auch ohne Markierung: der Cursor-Stand merkt sich das
                   Format fuer den naechsten getippten Text (fett/kursiv/unterstrichen leuchten auf), bis der Cursor bewegt wird.
                   Absatzformate und Formate beherrschen intern auch Bereiche ueber mehrere Absaetze (Browser erlaubt dort
                   allerdings keine Markierung ueber Absatzgrenzen).

2026-09-30  0.6.6  Farbauswahl (Text/Calc): der Farbknopf wendet die gewaehlte Farbe immer an (auch bei unveraenderter Standardfarbe,
                   z.B. gelb), die Farbwahl wirkt sofort. Files: Haken "oeffentlicher Link" wirkt sofort (kein Speichern noetig),
                   Link mit Kopieren-Knopf, Klick markiert den Link.

2026-09-30  0.6.5  Text: Zeichenformate (fett, kursiv, unterstrichen, Groesse, Textfarbe, Hintergrund) gelten jetzt nur fuer den
                   markierten Text; Aufzaehlung, Nummerierung (neu) und Einrueckung sind Absatzformate, Liste/Nummer pro Zeile.
                   Shift+Enter = neue Zeile im Absatz, Enter = neuer Absatz (Text hinter dem Cursor wandert mit; Enter in
                   leerer Listenzeile beendet die Liste; Backspace am Absatzanfang verbindet mit dem vorigen). Einfuegen nur als
                   Text. http(s)://-Adressen werden zu Links (Klick oeffnet neues Fenster). Calc unveraendert (Format je Zelle).
                   Felder: Item.r = Zeichenformat-Laeufe. Die in 0.6.3 gesetzten Zeichenformate ganzer Text-Absaetze entfallen.

2026-09-30  0.6.4  Organisationen (Schule, Abteilung, Vertrieb ...): neuer Menuepunkt fuer globale Admins (anlegen/loeschen).
                   Eine Gruppe kann mehreren Organisationen angehoeren (Kommaliste beim Anlegen/Bearbeiten), ohne Angabe
                   gehoert sie zu "all" (nicht loeschbar). Organisationen vergeben keine Rechte. API: GET/POST /api/units,
                   DELETE /api/units/{name}; Gruppen: units in GET/POST /api/groups und POST /api/groups/{name}.

2026-09-30  0.6.3  Text/Calc: Info links ("Alle mit Bearbeitungsrecht koennen aendern; der Absatz/die Zelle, in der gerade jemand
                   schreibt, ist fuer andere nur lesbar"). Weiche Sperre pro Absatz/Zelle (30 s, Heartbeat, frei beim
                   Verlassen/Trennen). Formatierung pro Absatz/Zelle: fett, kursiv, unterstrichen, Groesse, Textfarbe,
                   Hintergrundfarbe, Einrueckung, Aufzaehlung. Nicht in docx/rtf/xlsx/csv-Export enthalten.

2026-09-30  0.6.2  Standardgruppe heisst jetzt "alluser" (frueher "users"; wird beim Start samt Mitgliedschaften umbenannt, alte
                   Freigaben an "users" gelten weiter), ist nicht loeschbar; ihre Gruppen-Admins sind die globalen Admins.
                   "Admin" heisst in der Oberflaeche "Globaler Admin" (Gruppen-Admin davon getrennt). Kopfzeile zeigt die Rolle
                   (Benutzer / Gruppen-Admin / Globaler Admin) und einen Abmelden-Knopf (GET /logout, Basic-Auth-Reset).
                   Freigabe-Panel (Files/Calc/Text): Hilfetext zu lesen/schreiben und zum oeffentlichen Link (Zufallstoken).

2026-09-30  0.6.1  Gruppe anlegen/bearbeiten: sichtbare Haken statt Auswahllisten (lesen/aendern je Bereich, Gruppenordner,
                   Gruppenkalender); "Team / Abteilung" und "Klasse" fuellen die Haken nur vor. Gruppen-Admins schon beim
                   Anlegen (werden automatisch Mitglied). API: POST /api/groups akzeptiert zusaetzlich cal ("", ro, rw) und
                   admins; unbekannter Admin legt keine halbe Gruppe an.

2026-09-30  0.6.0  Mehrsprachige Oberflaeche: de (Quelltext), en, fr, es, it, ru, cn (wie csweb-gui) sowie tr und ar (arabisch mit
                   Rechts-nach-links-Layout). Auswahl oben rechts, wird im Benutzerkonto gespeichert (sonst Browsersprache,
                   dann CS_LANG). Weitere Sprachen: Datei <code>.json in CS_LANGDIR legen, erscheint automatisch in der
                   Auswahl; Vorlage web/lang/_template.json (Schluessel = deutscher Text) kann eine KI uebersetzen.
                   CS_LANGDIR-Dateien ueberschreiben/ergaenzen die eingebauten Texte. API: GET /lang/index.json,
                   GET /lang/<code>.json, POST /api/me/lang.

2026-09-30  0.5.3  HTTPS direkt in cs-team: CS_TLS_CERT=<pem> (+ CS_TLS_KEY=<pem>, optional wenn Key in derselben Datei),
                   Zertifikat wird bei Dateiaenderung ohne Neustart neu geladen. Ohne CS_TLS_CERT bleibt es HTTP.

2026-09-30  0.5.2  Files: Unterordner (virtuell, beliebig tief) in Web-UI (Brotkrumen, + Ordner, Ordner hochladen, Umbenennen/
                   Verschieben, Ordner loeschen) und WebDAV (MKCOL, MOVE/COPY, PROPFIND tief). Meta-Cache fuer viele Dateien.
                   Kalender: Internet-Kalender per ICS-URL abonnieren (nur lesen, 30 min Cache, keine privaten Adressen,
                   CS_ICS_PRIVATE=1 erlaubt sie), Ressourcen-Kalender (Raum/Geraet) lehnen Ueberschneidungen ab (Web + CalDAV).
                   API: Freigabe jetzt POST /api/filesshare/<owner>/<pfad>, neu /api/filesdir, /api/filesmove.

2026-09-30  0.5.1  Startpasswoerter: vom Admin/Import vergebene Passwoerter muss der Benutzer beim ersten Login aendern
                   (Klartext nur in der Importdatei, sofort gehasht). CSV: einheitliches Format name;passwort;gruppen
                   fuer Import und Export, ?update=1 aktualisiert vorhandene Benutzer (Klassenwechsel per Export/Import),
                   leeres Passwort = unveraendert. Rechte pro Gruppe und Bereich: keine / lesen / aendern.
                   Gruppen-Vorlagen "team" und "klasse" (Bereiche, Gruppenordner, Gruppenkalender in einem Schritt).
                   UI: [x] pro Zeile mit Rueckfrage, "+ add" am Listenende, Gruppe beim Anlegen von Text/Calc waehlbar,
                   Listen "Meine" / "Freigegeben".

2026-09-30  0.5.0  Gruppen: jeder Benutzer in mindestens einer Gruppe (mehrere moeglich, Rechte = Vereinigung),
                   Bereiche pro Gruppe (Kalender/Calc/Text/Files), globale Admins + Gruppen-Admins (Mitglieder,
                   Passwoerter, Benutzer in eigener Gruppe anlegen), CSV-Import/-Export von Benutzern und Gruppen
                   (Excel-tauglich), Freigaben ueber Gruppen ("g:<gruppe>"), Gruppenordner in Files
                   (/webdav/groups/<gruppe>/, ro/rw), Kalender global/Gruppe/persoenlich mit Ueberlagerung
                   (Thunderbird-Stil UI: Tag/Woche/Monat/Agenda, Checkboxen, Farben; CalDAV zeigt alle),
                   Files im Drive-Stil (Orte, Liste/Raster, Detailfeld), JS-Filter in allen Listen.
                   Speicher wahlweise S3-Bucket oder lokaler Ordner/ZFS (CS_DIR=<pfad>, Daten in <pfad>/.csteam),
                   Konfigdatei per -c / CS_CONF. Tests: Store (Mem/FS), Gruppen, Gruppen-Admin, Import/Export,
                   Gruppenordner, Kalender-Bereiche (auch mit CS_TEST_FS=1 gegen den Ordner-Speicher).

2026-09-30  0.4.0  Export/Import Text und Calc <-> Files (package conv/office): .txt .rtf .docx .cstext bzw.
                   .csv .xlsx .cscalc; eigenes Format .cstext/.cscalc (JSON). UI: Export-Leiste im Dokument,
                   "Aus Files importieren" beim Anlegen. Nur Standardbibliothek. Tests: Roundtrips, Excel-Stil xlsx
                   (sharedStrings), Rechte beim Import, kaputte/falsche Dateien.

2026-09-30  0.3.0  Files: flache Ablage pro Benutzer, Teilen (Benutzer / Team "*" / öffentlicher Link),
                   WebDAV unter /webdav/ (eigene Dateien + shared/<besitzer>/), Streaming-Upload (CS_MAX_UPLOAD_MB).
                   Kalender-JSON-API (/api/cal) für die UI. Neue Oberfläche: Hauptnavigation oben
                   (Benutzer, Kalender, Calc, Text, Files), links add/sel/del, Inhalt rechts.
                   Team-Stufe ("*") auch für Calc/Text. Tests: Files, WebDAV, Kalender-API, Team, UI (Browser-Smoke-Test).
2026-09-30  0.1.0  Grundgerüst: store (S3/Mem, ETag-Update), auth (Basic, bcrypt),
                   CalDAV-Backend (go-webdav), Calc/Text als LWW-Map über WebSocket, Web-UI, Tests.

2026-09-30  0.2.0  Benutzerverwaltung: users.json mit Rollen, Admin-API + UI, Passwort ändern, sperren,
                   löschen (letzter Admin geschützt), Fehlversuch-Sperre, Altformat-kompatibel, Tests.
