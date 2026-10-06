# cs-team

Kleine und in sich geschlossene Zusammenarbeits-Plattform ("Nextcloud light"): Kalender (CalDAV), Calc, Text, Dateien (WebDAV),
Aufgaben, Chat mit Videochat, Nachrichten und KI-Assistent. Ein Go-Binary, Web-UI eingebettet, Speicher: Ordner/ZFS oder S3 (RustFS).

Funktionen im Überblick: Benutzer, Gruppen, Organisationen mit Rechten je Bereich; Kalender (persönlich, global, Gruppe, Ressource
ohne Doppelbuchung, Internet-Abo; Serientermine mit Zeitzonen, Bearbeiten "nur dieser / dieser und folgende / alle", Maussteuerung (Klick auf Tag = neuer Termin, Umschalt+Klick = Mehrtagesbereich, Ziehen, Strg+X/C/V), Erinnerungen, Teilnehmer mit Einladungsmail, .ics-Import/-Export); Calc/Text mit gemeinsamer Live-Bearbeitung; Dateien mit Freigaben, öffentlichem Link, Gruppenordnern
und Kontingent; Aufgaben; Chat; Videochat (extern oder eingebaut); Nachricht per E-Mail/Webhook/Chat; KI-Assistent; neun Sprachen.
Start als eigenständige Kommandozeilen-App (ohne napp-it): [docs/HOWTO-standalone.md](docs/HOWTO-standalone.md) (englisch).
Handbuch (PDF): <https://www.napp-it.org/pdf/cs-team_de.pdf> · English: <https://www.napp-it.org/pdf/cs-team_en.pdf>. Siehe auch README.md.

Abhängigkeiten: minio-go (S3), emersion/go-webdav + go-ical (CalDAV), coder/websocket, x/crypto (bcrypt).

## Start

    export S3_ENDPOINT=127.0.0.1:9000 S3_KEY=... S3_SECRET=... S3_BUCKET=cs-team S3_TLS=0
    export CS_ADMIN_USER=admin CS_ADMIN_PASS=geheim     # nur beim ersten Start (legt users.json an)
    ./cs-team                                           # :8080  (CS_LISTEN ändert das)
    ./cs-team adduser anna geheim2 [admin]              # Benutzer anlegen / Passwort setzen (optional als Admin)
    CS_MEM=1 ./cs-team                                  # Demo ohne RustFS (RAM)

Bitte hinter TLS-Proxy betreiben (Basic Auth).

## Benutzerverwaltung

`users/users.json`: `name -> {hash (bcrypt), admin, disabled, created}` (Altformat `name -> hash` wird gelesen).
Der Start mit `CS_ADMIN_USER`/`CS_ADMIN_PASS` legt den ersten Admin an, aber nur wenn noch kein Benutzer existiert.
Admins (Web-UI: "Benutzer"): anlegen, löschen (optional mit Kalendern), sperren, Admin-Rolle, Passwort zurücksetzen.
Jeder Benutzer: eigenes Passwort ändern ("Konto"). Der letzte aktive Admin ist geschützt.
Passwort 8..72 Bytes; triviale Passwörter (z. B. `password1`, `12345678`, `qwertzui`) werden abgelehnt. Startpasswörter
(`CS_ADMIN_PASS`, `adduser`, Passwort-Reset durch Admin) müssen beim ersten Login geändert werden.
5 Fehlversuche je Benutzer+IP sperren 5 Minuten (429). Zusätzlich sperrt eine Adresse nach `CS_MAX_FAILS_IP`
Fehlversuchen (Standard 60, wegen Schul-NAT/Proxy); bereits angemeldete Benutzer (Auth-Cache) sind von dieser
Adress-Sperre ausgenommen, Fehlerzähler verfallen nach 15 Minuten Ruhe. `CS_TRUST_PROXY=1` wertet
`X-Forwarded-For` aus (nur hinter eigenem Proxy). Anmelde-Voreinstellungen (Verzeichnis/LDAP): `CS_IDENTITY_MODE`
(local|dir|mixed), `_REALM`, `_REALM_DEFAULT`, `_URL`, `_BASE`, `_BIND_DN`, `_BIND_PW`, `_STARTTLS`, `_ADMIT_GROUPS`,
`_LOCAL_GROUP`, `_ALLOW_LOCAL`, `_CACHE_DAYS`; Einstellungen in der Oberfläche überschreiben sie. Gelöschte/gesperrte Benutzer verlieren den Zugriff sofort
für neue Anfragen; offene Calc/Text-Verbindungen verlieren Rechte spätestens nach 15 Sekunden (seit 0.13.9).
Dokumente eines gelöschten Benutzers bleiben; Admins können sie freigeben oder löschen.

REST: `GET /api/me`, `POST /api/me/password`, `GET /api/users`, `POST /api/users`,
`POST /api/users/<n>/password|flags`, `DELETE /api/users/<n>[?purge=1]`.

## Oberfläche

Oben die Hauptnavigation (Benutzer, Kalender, Calc, Text, Files), links je Bereich `add` / `sel` (Liste) / `del`,
rechts der Inhalt. Mess (Benachrichtigungen wie cs-send) folgt.

## Files und WebDAV

Flache Ablage pro Benutzer (`files/<owner>/<name>`, Metadaten unter `filesmeta/`). Upload wird gestreamt
(`CS_MAX_UPLOAD_MB`, Standard 100). Gleicher Name ersetzt die ganze Datei (kein Merge; für gemeinsames
Bearbeiten Calc/Text verwenden).
Teilen pro Datei: ausgewählte Benutzer (lesen / schreiben), **Team** (alle angemeldeten Benutzer, technisch `"*"`
in der Lese-/Schreibliste; gilt auch für Calc/Text) und optional ein öffentlicher Link `/pub/<token>` (nur lesen,
widerrufbar). PDF, Bilder und Text werden im Browser angezeigt, alles andere (HTML, SVG, ...) nur als Download. Downloads unterstützen Range (Teilabrufe, Spulen, Fortsetzen) und ETag/304.

WebDAV: `https://host/webdav/` (Basic Auth, dieselben Rechte wie im Browser). Root = eigene Dateien,
`shared/<besitzer>/` = mit mir geteilt. Keine Ordner (MKCOL = 403). COPY/MOVE nur innerhalb der eigenen Dateien.
Getestet mit dem Protokoll; im Windows-Explorer ist WebDAV wählerisch (HTTPS nötig) - rclone, WinSCP,
Cyberduck oder macOS-Finder funktionieren zuverlässiger. Seit 0.14.2 meldet der Server DAV-Klasse 2 (`LOCK`/`UNLOCK`): exklusive
Schreibsperre je Datei im Speicher des Prozesses (Standard 10 Minuten, höchstens 1 Stunde, Erneuerung per `If`-Token, lock-null legt eine
leere Datei an). Gesperrt ist die Datei für alle anderen Benutzer, im Browser wie per WebDAV (HTTP 423); der Sperrende darf auch ohne
Token schreiben. Nach einem Neustart sperren die Clients neu.

**Papierkorb (0.14.2).** Löschen (Browser, WebDAV, REST) verschiebt Dateien nach `files/<besitzer>/.trash/<id>` (Metadaten mit
Ursprungspfad, Löscher, Zeit); der Name `.trash` ist reserviert. Aufbewahrung: Einstellungen > Dateien "Papierkorb: Tage" (Standard 30,
Vorgabe `CS_TRASH_DAYS`, 0 = kein Papierkorb, sofort endgültig). Stündlicher Lauf entfernt abgelaufene Einträge. Der Papierkorb zählt zum
Kontingent; bei Platzmangel werden zuerst die ältesten Einträge endgültig gelöscht. Gruppenordner: Verwalter sehen und stellen wieder her.
REST: `GET /api/trash`, `POST /api/trash/<besitzer>/<id>/restore` (bei belegtem Namen neuer Name, Antwort `{"name":...}`),
`DELETE /api/trash/<besitzer>/<id>`, `DELETE /api/trash[?owner=@gruppe]` (leeren), `POST /api/settings/trash {"days":n}` (Admin).
Ein Gruppenordner lässt sich erst löschen, wenn sein Papierkorb leer ist.

REST: `GET/POST /api/files`, `GET/DELETE /api/files/<owner>/<name>`, `POST .../share`, `GET /pub/<token>`;
Kalender: `GET/POST /api/cal`, `PUT /api/cal/<id>` (Name, Beschreibung, Freigabe, Ressource, Abo-URL), `DELETE /api/cal/<id>`,
`GET/POST /api/cal/<id>/events`, `PUT/DELETE .../events/<file>`
(`scope=one|following|all` mit `rid`), `GET /api/cal/<id>/export.ics`, `POST /api/cal/<id>/import` (Body: .ics), `POST /api/cal/<id>/refresh`.

**Kalender (0.15.0).** Termine haben optional eine **Erinnerung** (`VALARM`, im Termin gespeichert, kein Mailversand durch den Server) und
**Teilnehmer** (`ATTENDEE`/`ORGANIZER`; Benutzername oder E-Mail-Adresse). Ist SMTP eingerichtet, gehen Einladung, Aktualisierung und Absage als
Mail mit .ics (iMIP, `METHOD:REQUEST`/`CANCEL`) hinaus, höchstens 100 Empfänger je Benutzer und Stunde; Zu-/Absagen werden nicht ausgewertet.
"Dieser und folgende" trennt die Serie (alte Serie endet mit `UNTIL`, neue Datei/UID ab dem gewählten Termin). Für Termine mit Zeitzone wird eine
`VTIMEZONE` eingebettet (iOS, macOS, Outlook). Abos zeigen letzten Abruf und Fehler und lassen sich sofort aktualisieren.
Admins sehen unter Einstellungen > Dateien die **Belegung** je Besitzer (`GET /api/filesusage`).

## Export / Import (Text und Calc <-> Files)

Nur Go-Standardbibliothek (package `conv`). Übernommen werden Text bzw. Zellwerte und Formeln, keine Formatierung
(keine Schriftarten, kein Fett, keine Zellformate, Diagramme oder Makros).

| Typ | Export | Import |
|-----|--------|--------|
| Text | `.txt` `.rtf` `.docx` `.cstext` | `.txt` `.docx` `.cstext` |
| Calc | `.csv` `.xlsx` `.cscalc` | `.csv` (`,` oder `;`, UTF-8/Latin-1) `.xlsx` (erstes Blatt) `.cscalc` |

`.cstext` / `.cscalc` sind das eigene Format (JSON, `"format":"cs-team/text"` bzw. `"cs-team/calc"`) und ermöglichen
die eindeutige Zuordnung in der Ablage. UI: im Dokument `Export: [Format] [Download] [-> Files]`, beim Anlegen
`Aus Files importieren`. REST: `GET /api/docs/<id>/export?format=`, `POST /api/docs/<id>/tofiles?format=`,
`POST /api/docs/import {"owner","file","name"}` (Quelle: eigene oder mit dem Benutzer geteilte Datei; das neue
Dokument gehört dem Benutzer).

## Gruppen, Rechte, Speicher (0.5.0)

- Jeder Benutzer gehoert mindestens einer Gruppe an (Standard `users`); Rechte = Vereinigung der Gruppen.
- Gruppe schaltet Bereiche frei: cal, calc, text, files. Globale Admins duerfen alles verwalten, Gruppen-Admins
  die Mitglieder ihrer Gruppe. Freigaben von Dokumenten/Dateien: `g:<gruppe>`, `*` (alle) oder Benutzername.
- Gruppenordner (Files): `folder` = `ro` (Mitglieder lesen, Gruppen-Admins schreiben) oder `rw`; WebDAV `/webdav/groups/<gruppe>/`.
- Kalender hierarchisch: persoenlich (`<benutzer>`), Gruppe (`@<gruppe>`), Organisation (`+<organisation>`) und global
  (`_global`), dazu Abos (ICS-URL, immer nur lesen). UI: von innen nach aussen sortiert, die gewaehlten Kalender liegen
  uebereinander. Verantwortlich: persoenlich der Benutzer, Gruppe deren Gruppen-Admins (sie sehen ihren Gruppenkalender
  auch ohne Mitgliedschaft), Organisation/global die globalen Admins. Freigabe je Kalender: `off` (nur Verantwortliche),
  `ro` (Berechtigte lesen) oder `rw` (Berechtigte tragen ein). Gruppenkalender entstehen beim Anlegen der Gruppe (Haken
  *Gruppenkalender anlegen*, Vorlagen `team`/`klasse`) oder spaeter in den Gruppen-Einstellungen des globalen Admins: `cal`
  = `""` (keiner bzw. entfernen - nur solange er leer ist), `off`, `ro`, `rw`. `GET /api/groups` nennt je Gruppe `cal`,
  `POST /api/groups/<gruppe>` setzt ihn (`GET/PUT /api/cal/<id>`, Kalender-Menue *Bearbeiten*, koennen dasselbe).
- CSV: `POST /api/users/import` (`name;passwort;gruppe1,gruppe2`), `GET /api/users/export`, `GET /api/groups/export`.
- Speicher: `S3_*` (RustFS/S3-Bucket) oder `CS_DIR=/pfad` (Ordner/ZFS-Dataset, Daten in `/pfad/.csteam`, ein Prozess je Ordner).
  Konfigdatei `-c datei` oder `CS_CONF` (KEY=VALUE, gesetzte Umgebungsvariablen gewinnen).

## Sprachen (0.6.0)
Die Oberfläche gibt es in de, en, fr, es, it, ru, cn, tr und ar (rechts-nach-links). Die Auswahl steht oben rechts und wird im
Benutzerkonto gespeichert; ohne Auswahl gilt die Browsersprache, sonst `CS_LANG` (Standard de).

Weitere Sprachen, z.B. für Schüler mit anderem Sprachhintergrund: `CS_LANGDIR=/pfad/lang` setzen und dort `<code>.json`
ablegen (z.B. `fa.json`, `uk.json`). Die Datei erscheint sofort in der Auswahl (Name aus `"_name"`). Aufbau: ein JSON-Objekt,
Schlüssel = deutscher Text, Wert = Übersetzung; fehlende Einträge fallen auf Deutsch zurück. Als Vorlage dient
`web/lang/_template.json`: Datei einer KI geben ("übersetze die Werte nach Ukrainisch, Platzhalter {0} {1} und Leerzeichen am
Rand behalten, `_name` ergänzen"). Dateien in `CS_LANGDIR` überschreiben gleichnamige eingebaute Texte, so lassen sich auch
einzelne Begriffe anpassen (z.B. "Gruppe" → "Klasse"). Rechts-nach-links-Schrift wird für ar, he, fa, ur automatisch gesetzt.

## Layout im Bucket

    users/users.json                     name -> bcrypt
    cal/<user>/<kalender>/_meta.json     Name, Beschreibung
    cal/<user>/<kalender>/<uid>.ics      ein Termin = ein Objekt
    doc/<id>/meta.json                   Name, Typ (sheet|text), Owner, Read[], Write[]
    doc/<id>/snapshot.json               {"items":{key:{v,pos,ts,by,del}}}
    files/<owner>/<name>                 Dateiinhalt
    filesmeta/<owner>/<name>.json        Größe, Typ, Freigaben, Token
    filestok/<token>                     "<owner>/<name>" (öffentlicher Link)

## CalDAV

URL: `https://host/dav/` (auch `/.well-known/caldav`). Thunderbird, DAVx5, iOS, macOS.
Ein Client sieht genau die Kalender, die der Benutzer sehen darf (eigene, Gruppen-, Organisations- und globale Kalender
sowie Abos); nicht freigegebene Kalender (`off`) fehlen in der Liste und sind per Direktzugriff 404. Geschrieben wird nur
dort, wo die Freigabe es erlaubt (`rw` oder als Verantwortlicher/Admin); Abos sind immer nur lesbar (403).
Schreiben mit `If-Match` / `If-None-Match: *` wird auf S3-ETags abgebildet (412 bei Konflikt).
Eigene Kalender darf der Besitzer immer ändern und löschen (die Freigabe regelt nur den Zugriff anderer); ist der Bereich
*Kalender* für den Benutzer nur lesend (Gruppen-Vorlage `klasse`), bleibt auch der eigene Kalender gesperrt (403).
Neue Kalender entstehen in der Oberfläche (Kalender-Leiste → **+ add**); per CalDAV legt `MKCOL` mit `<c:calendar/>`
einen Kalender an, `MKCALENDAR` beantwortet der WebDAV-Server mit 405.

**Einen Kalender als Datei** (für Programme ohne CalDAV oder zum Weitergeben): in der Kalender-Leiste **Exportieren (.ics)**
= `GET /api/cal/<id>/export.ics` (alle Termine des gewählten Kalenders, mit Zeitzone/`VTIMEZONE`, Dateiname aus dem
Kalendernamen; funktioniert auch für nur lesbare Kalender und Abos). Zurück in einen beschreibbaren Kalender:
**Importieren (.ics)** = `POST /api/cal/<id>/import` (nach UID, wiederholter Import erzeugt keine Dubletten).

## Calc und Text (LWW)

Beide sind dieselbe Map key -> Item. Der Server vergibt pro Änderung einen monotonen Zeitstempel,
der neuere gewinnt: bei Calc pro Zelle (`A1`), bei Text pro Absatz (Reihenfolge über `pos`, Fließkomma).
Löschen = Tombstone. Snapshot wird 2 s nach der letzten Änderung geschrieben (ETag-Update, bei
Konflikt LWW-Merge). WebSocket: `/ws/<id>`, Nachrichten `{"t":"set","k":"A1","v":"=A2*2"}` / `{"t":"del","k":..}`.
Formeln (+ - * / SUM(A1:B3)) rechnet der Browser.

REST: `GET/POST /api/docs`, `POST /api/docs/<id>/share {"read":[],"write":[]}`,
`GET /api/docs/<id>/export` (CSV / Text), `DELETE /api/docs/<id>`.

## Chat und Nachricht (0.8.0)

Hauptmenü **Chat**: je Gruppe ein Chat mit Kanälen (`#allgemein` immer; weitere anlegen je nach Gruppeneinstellung), Live per WebSocket,
Anhänge (max. `CS_CHAT_MAX_MB`, Standard 10), @Name, Reaktionen, Bearbeiten/Löschen, Ungelesen-Markierung. Hauptmenü **Nachricht**: Text
an alle Mitglieder einer Gruppe per E-Mail, externe Chat-Adresse (Webhook) und/oder Gruppen-Chat. Je Gruppe einstellbar: Chat (Mitglieder
schreiben / nur Admins schreiben / aus), Kanäle anlegen (nur Admins / jedes Mitglied / niemand), Nachricht senden (nur Admins / jedes
Mitglied / aus; Standard nur Admins). Benutzer pflegen E-Mail und Chat-Adresse im Konto (Admins auch bei anderen).

Einstellungen (Mail/SMTP, öffentliche Adresse, Webhooks in private Netze): Klick auf den Titel „cs-team“ (nur globale Admins), gespeichert in
`settings.json` im Speicher, wirksam ohne Neustart. Die Umgebungsvariablen `CS_SMTP_HOST`, `CS_SMTP_PORT` (587), `CS_SMTP_TLS` (`starttls`|`ssl`|`none`),
`CS_SMTP_USER`, `CS_SMTP_PASS`, `CS_SMTP_FROM` und `CS_CHAT_ALLOW_PRIVATE=1` gelten nur als Vorgabe, solange dort nichts gespeichert ist.
Webhooks in private Netze sind sonst gesperrt (SSRF-Schutz). Start-Parameter (Port, Speicher, HTTPS, Admin) bleiben im Dienst/Menü.
Webhook-Formate: Slack (`{"text"}`), Discord (`{"content"}`), Telegram (`text=`), sonst text/plain (z.B. ntfy).

## Handy (0.10.0)

Bis 760 px Breite zeigt cs-team Liste und Inhalt nacheinander (Pfeil links oben = zurueck). Ohne Installation im Browser nutzbar; Calc ist per Touch eingeschraenkt.

## Aufgaben (0.9.0)

Hauptmenü **Aufgaben**: Ticketsystem light. Eine Aufgabe hat Auftraggeber, optional eine Gruppe, einen Bearbeiter (leer = „Bitte bearbeiten“,
jedes Gruppenmitglied kann übernehmen), Beteiligte, Priorität, Fälligkeit, Meilensteine (Text + Datum) und einen Verlauf aus Kommentaren und
Systemzeilen. Status: Offen → In Arbeit → Erledigt → Abgenommen. Nur Auftraggeber, Gruppen-Admin und globale Admins ändern Stammdaten, nehmen ab
und löschen; der Bearbeiter setzt „In Arbeit“/„Erledigt“, hakt Meilensteine ab und kommentiert. **Wiederholung** (täglich/wöchentlich/monatlich/
jährlich, braucht ein Fälligkeitsdatum): mit der Abnahme entsteht genau eine Folgeaufgabe, Fälligkeit und Meilensteine rücken weiter (nie in die
Vergangenheit). E-Mail/Webhook (SMTP und Chat-Adresse wie bei Nachricht) bei Zuweisung, Übernahme, Erledigt, Abnahme, Kommentar und Fälligkeit
(stündliche Prüfung). Rechte: Gruppen-Einstellung „Aufgaben anlegen“ (jedes Mitglied/nur Admins/aus); Aufgaben ohne Gruppe darf jeder anlegen,
sichtbar für Auftraggeber, Bearbeiter, Beteiligte und Admins. Speicherung: `tasks/<id>.json`.

## Kontingent (0.13.9)

Einstellungen > Dateien: Kontingent je Benutzer und je Gruppenordner in MB (0 = unbegrenzt). Vorgabe aus `CS_QUOTA_MB`. Es gilt für Browser und
WebDAV (der Papierkorb zählt mit) und wirkt sofort; ist es voll, antwortet der Server mit HTTP 507 (`storage quota exceeded`). Eine ersetzte Datei zählt nicht doppelt,
parallele Uploads überschreiten das Kontingent nicht. Die Dateiansicht zeigt "belegt X von Y". REST: `POST /api/settings/quota {"mb":n}`
(Admin), `GET /api/settings` liefert `quotaMB`, `GET /api/files` liefert `quota` und `used`.

## Videochat (0.13.7 / 0.13.8)

Im Chat unter der Gruppenliste, Einrichtung unter Einstellungen. Plätze 1 bis 3 nutzen externe Server (Jitsi, MiroTalk oder eigene Adresse mit
`{room}`): fester Raum je Gruppe, Ad-hoc-Raum für Gruppen-Admins, Ad-hoc-Raum für alle Schreiber. Der Raumname ist ein HMAC (nicht erratbar),
die Einladung im Kanal gilt 24 Stunden. Platz 4 ist der **eingebaute Videochat (WebRTC)**: nur Browser, höchstens 6 Teilnehmer, Bild und Ton laufen
direkt zwischen den Browsern (Mesh), der Server vermittelt nur die Signalisierung über die Chat-WebSocket (`rtcjoin`, `rtcsig`, `rtcleave`) und
prüft Leserecht und Einladung. STUN-Server vorbelegt (änderbar), optional TURN (coturn `use-auth-secret`, zeitlich begrenzte Zugangsdaten).
Der Browser braucht HTTPS oder localhost.

## KI-Assistent (0.12 ff.)

Optionales Widget "KI" (Einstellungen: Anbieter Anthropic, OpenAI-kompatibel oder Ollama, Schlüssel nur auf dem Server). Die KI sieht nur,
was der Benutzer selbst sehen darf, schreibt nichts selbst und schlägt neue Dokumente (Text, Calc) nur vor; angelegt wird nach Bestätigung.
Gewählte Dateien (Text, CSV, DOCX, XLSX, PDF mit Text, Bilder) werden ausgewertet. Ein zweiter Anbieter springt bei Ausfall ein.

## Sicherheit (Auswahl)

Chat-/Webhook-Adressen enthalten Zugangsschlüssel und sind nur für den Benutzer und globale Admins sichtbar und änderbar (0.13.9).
Rechteentzug trennt offene Calc/Text-Verbindungen (sofort, spätestens nach 15 s). Ressourcen-Kalender buchen atomar (auch Serientermine).
SSRF-Sperrliste für Webhooks, Kalender-Abos und KI-Endpunkte; CSV-/XLSX-Export gegen Formel-Injektion; Anmelde-Sperre; Security-Header.
Prüfbericht: `AUDIT.md`.

## Grenzen / TODO

- Soft-Locks (Zelle/Absatz sperren) und Freigabe-Dialog in der UI fehlen noch.
- Kalender-Freigaben gelten je Kalender (`off` = nur Verantwortliche, `ro` = Berechtigte lesen, `rw` = Berechtigte schreiben);
  Rechte einzelner Personen (ACL je Benutzer) fehlen noch.
- Kalender *anlegen* per CalDAV nur mit `MKCOL` (`MKCALENDAR` → 405); in der Oberfläche geht es immer.
- ListCalendarObjects liest je Termin ein Objekt (N x Get); ab einigen tausend Terminen Index/Cache.
- Änderungen anderer Server-Instanzen werden beim Persist gemerged, aber nicht live gebroadcastet.
- Tombstones werden nicht kompaktiert. Gleichzeitiges Tippen im selben Absatz: letzter Schreiber gewinnt.
- RustFS: bedingte Writes (If-Match / If-None-Match) vor Produktivbetrieb gegen die eigene Version testen.

## Build

    go test ./... && go build -o cs-team .
    GOOS=illumos GOARCH=amd64 go build -o cs-team .     # OmniOS


## Dateizugriff von aussen
Per WebDAV: `http(s)://host:9004/webdav/` (rclone Typ webdav, WinSCP WebDAV, Finder, Windows Netzlaufwerk nur mit HTTPS).
Nicht direkt auf den Basisordner (`.csteam`, SMB) oder den S3-Bucket schreiben: dort liegt die interne Ablage
(Metadaten, Rechte, Freigaben getrennt). SMB/S3-Tools nur lesend fuer Backup/Snapshots.

## HTTPS
`CS_TLS_CERT=/pfad/cert.pem` (und `CS_TLS_KEY=/pfad/key.pem`, falls der Key nicht im selben PEM steht) schaltet HTTPS ein.
Erneuerte Zertifikate werden ohne Neustart uebernommen. Mit HTTPS funktioniert auch das Windows-Netzlaufwerk (WebDAV).
