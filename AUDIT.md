# cs-team 0.10.3 – Funktions- und Security-Audit

Stand: 01.10.2026. **Umgesetzt in 0.10.4:** S-01, S-02 (K-01, C-02, F3), C-03, F1, S-05, C-01, F2, K-04, A-01 (alle mit Tests). Die übrigen Punkte sind offen. Methode: statische Code-Analyse aller Go-Pakete (auth, store, files, office, conv, doc, cal, tasks, chat, main) und der UI-Menüs in `web/index.html`, vier parallele Prüfungen. Es wurde nichts ausgeführt und nichts geändert. Die Punkte S-01, S-02, S-05, F1 wurden nach der Prüfung nochmals direkt im Code bestätigt. Alle anderen Funde stammen aus der Code-Lektüre mit Zeilenangaben und sind nicht per Exploit nachgestellt (K-03 ist ausdrücklich aus der Bibliothek abgeleitet).

Schweregrade: kritisch / hoch / mittel / niedrig / info.

## 1. Gesamtbild

Die Grundlagen sind solide: bcrypt mit Dummy-Vergleich beim Login (keine Nutzer-Enumeration am Login), Lockout nach Fehlversuchen, Rechteprüfung pro Objekt in allen Diensten (IDOR-Tests negativ), Pfad-Traversal durch `ValidName`/`FS.path` abgefangen, SSRF-Schutz per `Dialer.Control` (auch bei Redirects und DNS-Rebinding), kein `InsecureSkipVerify`, SMTP mit TLS ≥ 1.2 und CRLF-Schutz, konsequentes `esc()` im Frontend, WebSocket mit Origin-Check und Größenlimits, Chat-Anhänge mit `nosniff` + CSP-sandbox.

Die Schwächen konzentrieren sich auf vier Themen:

1. **Basic Auth ohne CSRF-Schutz** (betrifft alle schreibenden Menüs).
2. **Rechte-Logik bei Gruppen-Admins** (Kontoübernahme möglich).
3. **Stored XSS über WebDAV** (Account-Übernahme).
4. **Ressourcen-DoS** (kein Timeout am Server, Speicher-/CPU-Grenzen fehlen).

## 2. Top-10 Maßnahmen (Reihenfolge der Umsetzung)

| # | Maßnahme | Behebt |
|---|---|---|
| 1 | In `Wrap` bei POST/PUT/PATCH/DELETE `Origin`/`Sec-Fetch-Site` prüfen (same-origin) und `Content-Type: application/json` erzwingen | S-02, K-01, C-02, F3 |
| 2 | WebDAV-GET: `Content-Disposition: attachment`, `nosniff`, `CSP: sandbox; default-src 'none'` | F1 |
| 3 | `manages()` und `SetMembers` einschränken: Gruppen-Admin darf nur Nutzer verwalten, deren **alle** Gruppen er verwaltet; Hinzufügen nur von Nutzern ohne fremde Gruppen oder per Bestätigung | S-01 |
| 4 | `http.Server` mit `ReadHeaderTimeout`, `IdleTimeout`, `MaxHeaderBytes`; Warnung bei HTTP ohne TLS; HSTS bei TLS | S-05, C-04 |
| 5 | Webhook-Fehlermeldungen ohne URL (generisch) | C-01 |
| 6 | Calc-Gitter: Zeilen/Spalten begrenzen, `ToCSV` streamen | F2 |
| 7 | Rate-Limit je IP und je Benutzer, `X-Forwarded-For` korrekt auswerten (letzter Eintrag des eigenen Proxys), Auth-Cache gegen bcrypt-DoS | S-03, S-04 |
| 8 | Benutzer löschen: aus `Group.Admins` entfernen, Namen-Wiederverwendung absichern; Gruppenname `users` sperren | S-07, S-08 |
| 9 | Security-Header-Middleware (X-Frame-Options, CSP, nosniff, Referrer-Policy) | S-06 |
| 10 | RRULE-Limits (Frequenz < täglich ablehnen, Zeitraum begrenzen) und DAV-Body-Limit | K-03, K-04 |

## 3. Sicherheitsbefunde

### Übergreifend (Auth, Server, Store)

**S-01 | kritisch | `auth/groups.go:666-672`, `SetMembers` (:675ff) – Gruppen-Admin kann fremde Konten übernehmen**
`POST /api/groups/{name}/members` prüft nur `CanManage(Gruppe)`; wer hinzugefügt wird, ist nicht eingeschränkt. `manages()` erlaubt den Passwort-Reset für jeden Nicht-Admin, der irgendeine Gruppe mit dem Aufrufer teilt. Ablauf: Gruppen-Admin von G fügt Opfer V (Mitglied der vertraulichen Gruppe H) zu G hinzu, setzt dessen Passwort, meldet sich als V an und sieht alles aus H (Dateien, Chats, Kalender). Gleiches gegen Gruppen-Admins anderer Gruppen.
Fix: `manages()` nur erlauben, wenn sämtliche Gruppen des Ziels in `AdminOf(ctx)` liegen; Hinzufügen nur von Nutzern, die in keiner nicht verwalteten Gruppe sind, oder per Einladung, die der Nutzer bestätigt.

**S-02 (= K-01, C-02, F3) | hoch | `auth/auth.go:340ff`, `auth/http.go body()` – CSRF auf alle Zustandsänderungen**
Browser senden gecachte Basic-Credentials auch cross-site; JSON-Decoder prüfen den Content-Type nicht, `<form enctype="text/plain">` braucht keinen Preflight. Über eine fremde Webseite lassen sich Nutzer anlegen, Termine/Aufgaben/Dateien anlegen oder überschreiben, Dateien verschieben, Freigaben setzen und – bei Admin-Opfern – die SMTP-Einstellungen ändern (Mailserver auf Angreifer setzen, gespeichertes Passwort bleibt erhalten und wird dorthin gesendet, C-03).
Fix: in `Wrap` bei Methode ≠ GET/HEAD/OPTIONS `Sec-Fetch-Site ∈ {same-origin, none}` oder `Origin == Host` verlangen; JSON-Routen nur mit `Content-Type: application/json`. Frontend `api()` (index.html:250) entsprechend.

**S-03 | hoch | `auth/auth.go:287-292` – XFF-Spoofing hebelt Sperre aus**
Mit `CS_TRUST_PROXY=1` wird das erste Element von `X-Forwarded-For` genommen, das der Client frei setzt.
Fix: letztes Element (vom eigenen Proxy angehängt) bzw. `X-Real-IP`; nur von `TrustedCIDR` akzeptieren.

**S-04 | hoch | `auth/auth.go:307-326, 348` – Brute-Force-Schutz lückenhaft, bcrypt pro Request**
Schlüssel ist Benutzer+IP, also Passwort-Spraying über viele Namen unlimitiert. Jede Anfrage (auch WebDAV/API) kostet ein bcrypt (~50–80 ms): CPU-DoS ohne Login. Die Bereinigung bei > 10000 Schlüsseln löscht auch aktive Sperren.
Fix: zusätzlich Zähler je IP und je Benutzer global; erfolgreiche Auth 30–60 s cachen; Bereinigung nur abgelaufener Einträge.

**S-05 | hoch | `main.go:129-133` – Klartext-HTTP als Standard, keine Server-Timeouts**
Ohne `CS_TLS_CERT` laufen Basic-Credentials im Klartext auf allen Interfaces; `http.ListenAndServe`/`http.Server` ohne `ReadHeaderTimeout`, `IdleTimeout`, `MaxHeaderBytes` (Slowloris; betrifft auch C-04, K-04).
Fix: `http.Server{ReadHeaderTimeout: 10s, IdleTimeout: 120s}` in beiden Zweigen; beim Start bei fehlendem TLS deutlich warnen; bei TLS HSTS senden.

**S-06 | mittel | `main.go:159-200` – keine Security-Header für die UI**
Kein X-Frame-Options, keine CSP, kein nosniff/Referrer-Policy (Clickjacking).
Fix: Middleware: `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `CSP default-src 'self'; script-src 'self' 'unsafe-inline'`.

**S-07 | mittel | `auth/http.go:229-247` – Benutzer löschen räumt Verweise nicht auf**
`DeleteUser` entfernt den Namen nicht aus `Group.Admins`; wer den Namen neu anlegt, erbt die Gruppenadmin-Rechte. Ein Gruppen-Admin kann so einen gelöschten Admin-Namen einer anderen Gruppe neu anlegen und wird dort Admin. Dateien, Dokumente und Freigaben des alten Kontos bleiben bestehen und gehen an den neuen Namensträger.
Fix: Admins/Freigaben beim Löschen bereinigen; Namen gelöschter Nutzer zeitweise sperren oder Reste mit Bestätigung löschen.

**S-08 | mittel | `auth/groups.go:94-113, 314` – Gruppe „users“ erbt Legacy-`alluser`**
`Allowed` behandelt `g:users` als alle Benutzer; `validName` erlaubt eine neue Gruppe „users“.
Fix: Name in `SetGroup` ablehnen oder Legacy-Mapping nach Migration entfernen.

**S-09 | mittel | `auth/http.go:120-137, 169`, `contact.go:375-387` – Webhook-URLs für Gruppen-Admins sichtbar und überschreibbar – BEHOBEN 0.13.9**
Chat-Webhook-URLs (Slack/ntfy/Telegram enthalten Tokens) gehen an Gruppen-Admins; diese können sie auch überschreiben.
Fix: nur Besitzer/globaler Admin; Gruppen-Admins sehen `chatSet: bool`.

**S-10 | mittel | `store/store.go:114-127` (S3) vs. `store/fs.go:78-90` – uneinheitliche Key-Validierung**
FS lehnt `..`, leere Segmente, `\`, `\0` ab; S3 gibt Keys ungeprüft weiter.
Fix: gemeinsame `validKey()` im `store`-Wrapper für beide Backends.

**S-11 | mittel | `auth/auth.go:99-119` – `refresh` hält `a.mu` während S3-Zugriffen, Hilfsfunktionen ohne Timeout – BEHOBEN 0.13.9**
Ein hängender S3-Zugriff blockiert alle Anfragen.
Fix: ohne Lock laden und tauschen; 5-s-Kontext.

**S-12 | niedrig | `auth/http.go:30, 109-116` – interne Fehlertexte in 500-Antworten; `store.ErrConflict` wird zu 500**
Fix: generischer Text, Details ins Log; `ErrConflict` → 409.

**S-13 | niedrig – Benutzer-Enumeration für Gruppen-Admins** (`409 user exists`, `no such user: <name>`). Login selbst ist sauber.

**S-14 | niedrig | `auth/auth.go:160-172`, `main.go:107,114` – Bootstrap und Passwortrichtlinie**
Kein Default-Passwort im Code; der erste Admin (`CS_ADMIN_PASS`) wird aber ohne Änderungspflicht angelegt, CLI `adduser <name> <pw>` legt das Passwort in Shell-History und `ps` ab, bcrypt-Kosten 10, nur Längenprüfung 8–72.
Fix: `Must=true` beim Bootstrap, Passwort per stdin/Env, Kosten 12, einfache Denyliste.

**S-15 | niedrig | `store/fs.go:31, 152`** – Dateien 0600 (gut), Verzeichnisse 0755; liegengebliebene Temp-Dateien. Fix: 0700.

**S-16 | niedrig | `store/fs.go:188-246`** – Race `PutStream` vs. `Delete`; `Get` liest unbegrenzt per `ReadAll`.

**S-17 | info** – „Muss Passwort ändern“ blockiert DAV-Clients mit unverständlichem 403.

**S-18 | info** – Kein Audit-Log (Fehlversuche, Passwort-Resets, Rechteänderungen). CSV-Export ohne Formel-Schutz (praktisch kaum ausnutzbar).

### Dateien, Calc, Text

**F1 | kritisch | `files/webdav.go:26, 64`, `files.go:169` – Stored XSS über WebDAV-GET**
Der WebDAV-Handler liefert `GET` mit dem aus der Endung abgeleiteten Content-Type (`text/html`, `image/svg+xml`) ohne `Content-Disposition`, `nosniff`, CSP. (Die REST-Route `serve()` in `files/http.go:118-126` erzwingt dagegen Download und nosniff.) Ein Nutzer legt `x.html` in einem geteilten Ordner ab; das Opfer öffnet `/webdav/shared/<anna>/x.html` im Browser, das Skript läuft im App-Origin mit den gecachten Basic-Credentials: Kontoübernahme bis zum Admin.
Fix: Wrapper um den DAV-Handler, der für GET/HEAD `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff`, `CSP: sandbox; default-src 'none'` setzt.

**F2 | hoch | `conv/calc.go:68-76`, `doc/lww.go:78` – Server-OOM durch `ToCSV`**
`ValidKey` erlaubt `[A-Z]{1,3}[0-9]{1,5}`; ein Schreiber setzt `A1` und `ZZZ99999`, der Export (CSV/XLSX) legt ein Gitter 99999 × 18278 an (~29 GB) und beendet den ganzen Prozess.
Fix: zeilenweise streamen, Zeilen/Spalten serverseitig begrenzen.

**F3 →** siehe S-02.

**F4 | mittel | `doc/http.go:141/152`, `hub.go:41` – Rechteentzug wirkt nicht auf offene WebSockets – BEHOBEN 0.13.9**
Schreibrecht wird nur beim Verbinden vergeben; nach `share` (Entzug) oder Kontosperre bleibt die Verbindung lese-/schreibfähig.
Fix: nach `share`/`remove` Clients neu bewerten und trennen.

**F5 | mittel | `doc/http.go:120-132`, `hub.go:225-252` – gelöschtes Dokument lebt weiter** (Clients, Timer, `persist` schreibt Snapshot ohne Meta neu).

**F6 | mittel | `doc/hub.go:13-15, 71, 99, 142` – Dokumentgröße/Hub-Speicher unbegrenzt**
Bis 200000 Items × 64 KB ≈ 12,8 GB je Dokument; `h.docs` wird nie bereinigt.

**F7 | mittel | `conv/calc.go:68, 157-158` – Formel-Injektion im Export**
CSV schreibt `=…`/`+`/`-`/`@` roh; XLSX schreibt echte `<f>`-Formeln (auch `HYPERLINK`, `WEBSERVICE`).
Fix: CSV mit `'` voranstellen; XLSX-Formeln nur aus Whitelist.

**F8 | mittel | `files/files.go:278`, `main.go:174` – BEHOBEN 0.13.9** – kein Kontingent (nur Größe je Datei, Standard 100 MB).

**F9 | mittel | `files/files.go:97-124`, `doc/http.go:38-55` – BEHOBEN 0.13.9** – `Svc.all()` lädt alle Metadaten seriell unter Mutex (alle 20 s bei Cache-Miss).

**F10 | mittel | `conv/text.go:23/130`, `office.go:28/225`** – Zip-Import: 32 MB gezippt, je Teil bis 64 MB entpackt; parallele Importe → GBs.

**F11 | niedrig | `files/http.go:118-128`** – keine CSP bei `/api/files` und `/pub`; `/pub` ohne `Cache-Control: no-store`, öffentlicher Link ohne Ablauf, Passwort oder Rate-Limit.

**F12 | niedrig | `files/files.go:327-334, 367-378, 437-444`** – 403/404 verrät Existenz fremder Dateien/Ordner (`Open` ist korrekt).

**F13 | niedrig | `doc/lww.go:78`, `conv/calc.go:23`** – `A01` und `A1` sind verschiedene Schlüssel, im Export doppelte Zellreferenzen (Excel meldet Reparatur). Fix: `reCell` ohne führende Null.

**F14 | niedrig | `files/files.go:253-294`, `doc/hub.go:101`** – Races: `Put` liest Meta vor dem Schreiben, `saveMeta` ohne ETag (Freigabe kann zurückkehren); `Level()` in `ws` ohne Lock.

**F15 | niedrig** – WebDAV `PROPFIND` ohne Depth = infinity listet den ganzen Baum. Fix: Depth erzwingen.

**F16/F17 | info** – UTF-8-Kürzung des Dateinamens (`office.go:99`), `toFiles` meldet `ErrExists` als 500, `importDoc` liefert bei `ErrNotFound` leere ID.

### Kalender und Aufgaben

**K-02 | mittel | `cal/sub.go:50-60`** – SSRF-Blocklist unvollständig: nicht gesperrt `100.64.0.0/10` (CGNAT/Cloud-Metadaten), `0.0.0.0/8`, `192.0.0.0/24`, `198.18.0.0/15`, NAT64; Fehlertext `"feed: "+err` (api.go:161) verrät Resolver-Details. Gleiches Muster bei Webhooks (C-10).

**K-03 | mittel | `cal/caldav.go:324-330`** (aus go-webdav abgeleitet, nicht ausgeführt) – `RRULE:FREQ=SECONDLY` ohne COUNT/UNTIL bei großem Zeitraum im `REPORT` bindet CPU; Quellen: CalDAV-PUT jedes Schreibers oder Abo-Feed (trifft alle Leser eines globalen Kalenders).
Fix: beim PUT/Abo-Import Frequenzen < DAILY ablehnen, Zeitraum begrenzen.

**K-04 | mittel | `cal/caldav.go:332-367, 394-396`** – kein Größenlimit bei CalDAV-PUT/REPORT, keine Inhaltsvalidierung; Ressourcen-Kalender lädt bei jedem Speichern alle Objekte.

**K-05 | mittel | `cal/resource.go:32-60` – BEHOBEN 0.13.9** – Race bei Ressourcen-Doppelbuchung (`conflict()` und `Put` nicht atomar); RRULE/RECURRENCE-ID ignoriert; Meldung in Server-Zeitzone.

**K-06 | mittel | `cal/sub.go:96-168`** – Abo-Refresh ohne Serialisierung: jeder Leser kann parallele Fetches (bis 20 s) und Rewrites auslösen; bei Teilfehler halb ersetzte Termine. Fix: singleflight + Hintergrund-Goroutine.

**K-07 | niedrig | `cal/api.go:246-251`** – ganztägig mit `end == start` erlaubt.

**K-08 | niedrig | `cal/api.go:99-104, 124, 233`** – keine Längen-/Formatprüfung; Gruppenkalender für nicht existierende Gruppe (`@beliebig`) anlegbar.

**K-09 | niedrig | `cal/caldav.go:292`** – GET per Präfix-Match (`ab` liefert `abc.ics`); DELETE des Kalenders „default“ per DAV leert ihn (Schutz nur in der Web-API).

**A-01 | mittel | `tasks/tasks.go:439`** – Fälligkeits-Benachrichtigung wird nach Datumsänderung nicht neu ausgelöst (`DueNt` nicht zurückgesetzt; bei Meilensteinen korrekt).

**A-02 | niedrig | `tasks/tasks.go:343-367`** – WebSocket sendet `del`-Nachrichten mit Aufgaben-IDs an alle verbundenen Nutzer, auch wenn sie die Aufgabe nicht sehen dürfen (ID-/Aktivitätsleck).

**A-03 | niedrig | `tasks/tasks.go:338-341, 613`** – `save()` ignoriert Speicherfehler; Speichern unter globalem Mutex.

**A-04 | niedrig | `tasks/tasks.go:507-525`** – Status `open` mit gesetztem Bearbeiter ist inkonsistent.

**A-05/A-06/A-07 | niedrig** – doppelte Meilenstein-IDs möglich; keine Gesamtgrenzen (Log bis 500 × 4000 Zeichen, jede Änderung schreibt die ganze Datei); Beteiligte (`Watch`) bekommen keine Mails, `Notify` ohne Rate-Limit (`Tick` in Serverzeit).

**I-01 | info** – Globale Admins sehen und ändern alle Aufgaben, auch persönliche. Bitte dokumentieren.

### Chat, Nachricht, Einstellungen

**C-01 | hoch | `chat/message.go:341-343`** – `HookFailed` enthält `err.Error()` mit vollständiger Ziel-URL (`*url.Error`); der Sender sieht fremde Webhook-URLs samt Geheimnis im Pfad (Slack-Webhook, Telegram-Token, ntfy-Topic).
Fix: generische Fehler (Statuscode), URL nie ausgeben.

**C-03 | mittel | `chat/settings.go:129-130`** – Bei Host-/Benutzerwechsel bleibt das alte SMTP-Passwort erhalten (Wirkung hinter S-02 / kompromittiertem Admin). Fix: Passwort löschen oder Neueingabe verlangen.

**C-04 | mittel | `chat/message.go:320-347, 441`** – Massenversand ohne Obergrenze (6 Sendungen/min/Nutzer, aber je an die ganze Gruppe, z. B. `alluser`); Webhooks sequentiell je bis 12 s im HTTP-Request; `Mailer.Notify` ohne Limit.
Fix: Empfängerzahl und Tageskontingent, Hintergrundversand/Worker-Pool.

**C-05 | mittel | `chat/chat.go:184-193, 412, 484-517`** – Upload-Flut: bis 20 × 10 MB je 10 s, `io.ReadAll` in den RAM, Rate-Limit erst nach dem Speichern, keine Quota (rechnerisch ~200 GB je Gruppe).

**C-06 | mittel | `chat/chat.go:199-222, 581-588`** – Race „send on closed channel“: `broadcast` gibt den Lock vor `c.out <- b` frei, Disconnect schließt `c.out` → Panic im Sender-Request (Nachricht schon gespeichert, Rest des Broadcasts entfällt).
Fix: `done`-Channel/`sync.Once` statt `close(c.out)`.

**C-07 | niedrig | `chat/chat.go:269-272, 307`** – Edit/Reaktion verlangt nur Leserecht und ist nicht rate-limitiert; jede Änderung schreibt den ganzen Kanal.

**C-08/C-09 | niedrig** – `St.Put`/`St.Delete` Fehler ignoriert; Race in `RemoveChannel` (alter Kanal wird neu geschrieben); gelöschte Gruppen lassen Chat-/Dateidaten zurück.

**C-10/C-11 | niedrig** – SSRF-Blocklist (siehe K-02); `msg[:1900]` schneidet UTF-8; Redirects 307/308 leiten den Nachrichtentext an das Ziel weiter.

**C-12 | info | `chat/settings.go:63-89`** – Env-Werte lassen sich in der UI nicht leeren; `Private` als `bool` (fehlendes Feld setzt „privat erlauben“ stillschweigend auf false); SMTP-Passwort im Klartext in `settings.json` (0600); Testmail-Fehler gibt rohe SMTP-Antwort zurück. DynDNS gibt es im Code nicht, nur das Feld „Öffentliche Adresse“ für Links.

**C-13/C-14 | info** – WebSocket ohne Ping/Idle-Timeout und ohne Verbindungslimit je Nutzer; Mitgliedschaftsänderungen erst nach Reconnect; Versandprotokoll global auf 200 Einträge (eine aktive Gruppe verdrängt alle anderen), Chat ohne zeitliche Retention.

## 4. Routen und Rollen (Kurzfassung)

Alle Routen laufen über `auth.Wrap` (Basic Auth, Lockout). Die Objekt-Autorisierung ist in allen geprüften Diensten vorhanden; es gab keinen IDOR-Fund. Die Schwäche liegt in der **Logik** von `manages`/`SetMembers` (S-01), nicht in fehlenden Prüfungen.

| Bereich | Rolle | Anmerkung |
|---|---|---|
| Konto (`/api/me*`) | angemeldet | Passwortwechsel verlangt altes Passwort (außer „Muss ändern“) |
| Benutzer/Gruppen/Organisationen/Einheiten (Schreiben) | globaler Admin; Gruppen-Admin eingeschränkt | siehe S-01, S-07 |
| Einstellungen (`/api/settings*`) | nur globaler Admin | Passwort wird nicht zurückgegeben (`passSet`) |
| Dateien/WebDAV | Bereich `files` + Dateirechte | `/pub/{token}` ohne Login (128-Bit-Token) |
| Calc/Text (`/api/docs`, `/ws/{id}`) | Bereich `calc`/`text` + Level | Rechte beim Verbinden und laufend neu bewertet (F4, 0.13.9) |
| Kalender/CalDAV | Bereich `cal`; Pfad-User == Login | Abos nur lesbar |
| Aufgaben | angemeldet + `canSee`/`isOwner` je Objekt | Admin sieht alles |
| Chat/Nachricht | Gruppenmitgliedschaft; Senden nur Gruppen-Admin (Standard) | 6 Sendungen/min |

## 5. Funktion je Menü: Lücken und Verbesserungsvorschläge

**Benutzer / Konto.** Konto (Passwort, Mail, Chat-URL, CSV-Export), Anlegen einzeln und per CSV, Detail (Gruppen, Admin-Flag, sperren, Passwort, löschen).
Lücken: Nach Passwortänderung zeigt `location.reload()` mit alten Basic-Credentials einen 401-Dialog; kein echtes Login/Logout (Basic-Auth-Trick, in Firefox/Safari unterschiedlich); Löschen entfernt Dateien/Dokumente nicht, der Hinweis „Admin kann sie übernehmen“ hat keine UI; keine Suche/Paginierung (MAXSHOW 300), kein Umbenennen.
Vorschläge: Login-Formular mit Session-Cookie (HttpOnly, SameSite=Strict) – behebt zugleich CSRF grundsätzlich und das Logout-Problem; „letzter Login“, Anzeige gesperrter IPs mit „Sperre aufheben“; Übernahme-Funktion beim Löschen.

**Gruppen.** Anlegen mit Vorlagen (Team/Klasse), Rechteraster je Bereich, Gruppenordner/-kalender, Chat-/Nachrichten-/Aufgaben-Modi, Admins, Organisationen, Mitgliederliste als Textarea.
Lücken: Anlegen nicht atomar (halb konfigurierte Gruppe bei Fehler, TOCTOU bei `exists`); Löschen entfernt Gruppenordner/-kalender/Chat nicht (neue gleichnamige Gruppe sieht alte Daten); ein unbekannter Name in der Mitgliederliste bricht alles mit technischer Meldung ab.
Vorschläge: Mitglieder per Suchfeld/Checkliste, Meldung pro Name, Gruppe umbenennen/kopieren, Vorschau „diese Gruppe sieht: …“.

**Organisationen.** Rein ordnend (Zuordnung von Gruppen), nur Admin. Keine Berechtigungswirkung, daher schwer verständlich; Zuordnung nur in der Gruppenansicht, Umbenennen fehlt.
Vorschläge: Zuordnung direkt auf der Organisationsseite, Filter in Gruppen-/Benutzerlisten nach Organisation.

**Kalender.** Monats-/Wochen-/Tages-/Agenda-Ansicht, Anlegen/Löschen von Terminen, Kalender persönlich/global/Gruppe/Ressource/Abo, CalDAV unter `/dav/`.
Größter funktionaler Mangel: **Serientermine** werden nur am Startdatum angezeigt (`apiEvents` expandiert `RRULE` nicht, nur erster VEVENT; keine `EXDATE`/`RECURRENCE-ID`) – Termine aus Thunderbird/iOS erscheinen falsch. Außerdem: Windows-/unbekannte TZID werden stillschweigend übersprungen, Floating-Zeiten als UTC gelesen; kein Bearbeiten von Terminen, keine Teilnehmer, keine Erinnerungen (VALARM); VTODO in CalDAV freigegeben, in der UI nicht sichtbar; Abo: URL nicht änderbar, Route `refresh` ohne Button, Fehler unsichtbar, kein „Stand“; `load()` holt alle Termine ohne Zeitraum und lädt Objekte einzeln (N × Get); Ressourcenmeldungen englisch; kein ICS-Import/-Export, kein Umbenennen/Farbwahl.
Vorschläge: RRULE-Expansion mit Zeitraumparameter `?from=&to=`, TZ-Fallback, Terminbearbeitung, Erinnerungen, Abo-Status.

**Calc.** Live-Tabelle mit weichen Zellsperren, Formatierung, 14 Funktionen (SUM, AVERAGE, MIN, MAX, COUNT, COUNTA, PRODUCT, ROUND, ABS, SQRT, AND, OR, NOT, IF), Export CSV/XLSX/cscalc, Import.
Lücken: UI zeigt nur A–Z × 100 Zeilen, Server kennt bis ZZZ99999 (unsichtbare Daten); CSV-Import auf 20000 Zeilen begrenzt; Dezimalkomma: UI akzeptiert `1,5`, XLSX-Export nur `1.5` (wird in Excel Text); keine Text-/Datumsfunktionen, kein SUMIF/VLOOKUP; Zellformate gehen im Export verloren.
Vorschläge: sichtbaren Bereich dynamisch erweitern, Dezimalkomma im Export normalisieren, Funktionsumfang erweitern, F2/F7 beheben.

**Text.** Live-Absätze mit Sperre je Absatz, Listen, Einrückung, Zeichenformate, Links; Export TXT/RTF/DOCX/cstext, Import TXT/DOCX/cstext.
Lücken: `FromDOCX` zählt `w:tab` in den Absatzeigenschaften als Tab im Text; Tabellen, Kopf-/Fußzeilen, Listen gehen beim Import verloren; der Export schreibt keine Formatierung (Fett, Farben, Listen fehlen in DOCX/RTF); „→ Files“ überschreibt gleichnamige Dateien ohne Rückfrage; kein RTF-Import.
Vorschläge: Formatierung im DOCX-Export (mindestens b/i/u, Listen), `w:tab` nur innerhalb von `w:r`, Überschreib-Bestätigung.

**Dateien.** Eigene Ablage, „Für mich freigegeben“, Gruppenordner, Liste/Raster, Suche, Upload (Dialog, Drag&Drop, Ordner), Teilen per Benutzer/Gruppe/alle, öffentlicher Link, WebDAV.
Lücken: Umbenennen/Verschieben legt die Datei neu an – **Freigaben und öffentlicher Link gehen verloren**, ohne Hinweis; bei Ordnern Teilzustand bei Fehlern; `serve()` ohne Range/304 (Videos nicht spulbar); WebDAV ohne `LOCK` (Word/Excel/Finder mounten oft nur lesend); kein Papierkorb, keine Versionierung, kein Kontingent, keine Mehrfachauswahl; Umbenennen per `prompt()`; Fehlermeldung bei gesperrten Namen `shared`/`groups` nur „bad file name“.
Vorschläge: Metadaten beim Verschieben übernehmen, Range/304, Papierkorb + Quota, Ablauf/Passwort für öffentliche Links.

**Aufgaben.** Ticketsystem mit Filtern, Status open → doing → done → closed, Meilensteine, Wiederholung, Verweise, Kommentarverlauf, Echtzeit, Desktop-Benachrichtigungen.
Lücken: A-01, A-04, A-07; kein Papierkorb, keine Anhänge/Suche; Wiederholung entsteht erst nach „Abnehmen“ (`closed`) und bricht nach 2000 Schritten ab; Bearbeiter muss „Peer“ sein – löst sich die Beziehung, scheitert jedes Speichern mit „bad assignee“; Serverfehler englisch; „gelesen“ nur im Browser-localStorage.
Vorschläge: Server-seitiger Lesezustand, Beteiligte benachrichtigen, deutsche Fehlermeldungen mit Feldbezug.

**Chat.** Kanäle je Gruppe, Anhänge, Bearbeiten, Löschen, Reaktionen, @Mentions, Desktop-Benachrichtigung, Nachladen.
Lücken: keine Suche, keine Direktnachrichten, keine Threads, keine Bearbeitungshistorie, keine Link-Vorschau; Ungelesen-Zustand nur im Browser; Mentions lösen keine Mail/Webhook aus.
Vorschläge: Reaktion/Edit an Schreibrecht koppeln (C-07), Quota je Gruppe (C-05), Server-seitiger Lesezustand.

**Nachricht.** Versand an eine Gruppe per Mail, Webhook (Slack, Discord, Telegram, ntfy/generisch) und/oder Chat; Protokoll.
Lücken: Protokoll ohne Empfänger und Fehlertext; bei Fehler bricht der Rest der Mail-Batches ab; kein Entwurf, keine Empfängervorschau, keine Abbestellung; fester Betreff-Präfix „[Gruppe]“.
Vorschläge: Hintergrundversand mit Status, Fehlerprotokoll ohne URL (C-01), Vorabzählung „x Mails, y Webhooks“.

**Einstellungen (Admin).** SMTP (Host, Port, Verschlüsselung, Benutzer, Passwort, Absender), öffentliche Adresse, „Webhooks in privaten Netzen erlauben“, Testmail.
Lücken: kein Schalter zum Abschalten der Mail solange `CS_SMTP_*` gesetzt ist; Testmail nur an die eigene Adresse; keine Anzeige, ob ein Wert aus Env oder UI stammt; der Schalter „private Netze“ ohne SSRF-Warnhinweis; Upload-Größe und Rate-Limits nicht einstellbar; kein DynDNS (nur Adresse für Links).
Vorschläge: Sicherheitsabschnitt (Mindestpasswortlänge, Sperrdauer, Anzeige „TLS aktiv: ja/nein“, Bootstrap-Status), Host-Änderung nur mit Passwortbestätigung (C-03).

## 6. Empfohlene Reihenfolge

1. **Sofort (Release 0.10.4):** CSRF-Schutz in `Wrap` (S-02), WebDAV-Header (F1), `manages`/`SetMembers` (S-01), Server-Timeouts + TLS-Warnung (S-05), Webhook-Fehlertext (C-01), Calc-Gitterlimit (F2).
2. **Danach (0.11):** Rate-Limit/XFF/bcrypt-Cache (S-03/S-04), Benutzer-Löschen und „users“-Gruppe (S-07/S-08), Security-Header (S-06), RRULE- und Body-Limits (K-03/K-04), Race in `broadcast` (C-06), Key-Validierung (S-10), SSRF-Blocklist (K-02/C-10), Formel-Export (F7).
3. **Funktional:** RRULE-Anzeige und Zeitzonen-Fallback im Kalender, Terminbearbeitung, Metadaten beim Verschieben von Dateien, DOCX-Formatierung, Login-Formular mit Session-Cookie, Quota, Papierkorb.

Nicht geprüft: Laufzeitverhalten (keine Exploits oder Lasttests), Go-Abhängigkeiten auf bekannte CVEs (`govulncheck` empfohlen), die Windows-Dienstintegration in csweb-gui, Reverse-Proxy-Setups.
