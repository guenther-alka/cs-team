cs-team changelog (newest first)

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
