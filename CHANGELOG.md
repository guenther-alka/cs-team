cs-team changelog (newest first)

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
