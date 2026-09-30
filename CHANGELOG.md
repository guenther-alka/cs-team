cs-team changelog (newest first)

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
