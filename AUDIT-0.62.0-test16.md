# Funktions- und Sicherheitsaudit cs-team 0.62.0-test16

Stand: 2026-10-17. Methode: statische Durchsicht (145 Routen, Muster-Suche), `govulncheck`, und zwei Live-Läufe gegen eine
getrennte Testinstanz (`CS_MEM=1`, 127.0.0.1:9104, eigener Admin) mit Node-Skripten (`audit_live.js` 175 Prüfungen,
`audit_live2.js` 90 Prüfungen, `audit_live3.js` Nachtests). Die produktive Instanz (Port 9004) wurde nur per TLS-Handshake
berührt. Alle Skripte liegen in `C:\opt\tmp\cs-team\`.

## Gesamturteil

Keine kritische Lücke gefunden. Rechtetrennung (Gruppen, Gruppen-Admin, DAV, Papierkorb, Suche, Aushänge, WebSocket) hielt in
allen Live-Tests. Handlungsbedarf besteht bei der Go-Version (bekannte Lücken in der Standardbibliothek), bei der
CSP (`unsafe-inline`) und bei einigen Härtungen mit geringem Aufwand.

## Befunde nach Schwere

| Nr | Schwere | Befund | Empfehlung | Status |
|----|---------|--------|------------|--------|
| A-1 | hoch | `govulncheck`: 32 erreichbare Lücken in der Go-Standardbibliothek (net/http HTTP/2, crypto/tls, crypto/x509, net/url, net/mail, os u. a.) plus `golang.org/x/net` < v0.60.0. Behoben ab Go 1.26.9 bzw. x/net v0.60.0 (gebaut wird derzeit mit Go 1.26.0 aus `C:\opt\go`). Betroffen sind u. a. der Server selbst (`main.go` ListenAndServe), LDAP (`auth/ldap.go`), WebDAV-Range (`files/webdav.go`), Webhooks (`chat/message.go`). | Mit aktuellem Go 1.26.x (mind. 1.26.9) neu bauen, `go get golang.org/x/net@latest && go mod tidy`, danach `govulncheck ./...` wiederholen. Build-Skript auf feste Mindestversion prüfen lassen (`go.mod`: `toolchain go1.26.9`). | offen |
| A-2 | mittel | CSP enthält `script-src 'unsafe-inline'`, weil die Oberfläche ein Skript in `index.html` trägt. Ein XSS-Fehler wäre dadurch nicht durch die CSP abgefangen. | Skript und Stil in eigene Dateien auslagern und mit Nonce/Hash arbeiten; bis dahin ist die konsequente Ausgabe-Maskierung (bisher kein Fund) die einzige Barriere. | offen (größerer Umbau) |
| A-3 | mittel | API-Antworten ohne `Cache-Control: no-store`; Browser oder Proxy dürfen Daten (Chat, Konten) zwischenspeichern. | Header für `/api/`, `/dav/`, `/webdav/` gesetzt (`secHeaders`, Datei-Downloads dürfen überschreiben). | behoben in test17 |
| A-4 | mittel | Anmeldesperre nach Fehlversuchen kann zur gezielten Aussperrung eines bekannten Kontos genutzt werden (Sperre je Konto). | Sperre zusätzlich je Quell-IP gewichten, Admin-Konten nur mit Verzögerung statt Sperre, Hinweis im Handbuch. | offen |
| A-5 | niedrig | Aushang-Endpunkt liefert `text/plain` statt `application/json`. | `Content-Type` setzen; kein Sicherheitsrisiko dank `nosniff`. | offen |
| A-6 | niedrig | Chat-Adressen (Webhook-Ziele) werden erst beim Senden geprüft, nicht beim Speichern. SSRF-Sperre greift, aber Fehler fällt spät auf. | Beim Speichern validieren. | offen |
| A-7 | niedrig | CSV-Export: Zellen mit `=`, `+`, `@` werden mit vorangestelltem `'` neutralisiert (Test S-Z6 bestanden). | – | ok |
| A-8 | info | Passwort-Hashes stehen nicht im JSON-Export der Benutzer (Test F-U3). | – | ok |
| A-9 | info | TLS produktiv: TLS 1.3, selbstsigniertes Zertifikat (cs-proxy, gültig bis 2036). Minimum TLS 1.2 im Code. | Für Internetbetrieb Zertifikat einer anerkannten CA verwenden. | Hinweis |

## Live-Ergebnisse im Überblick

- **Rechte:** Gruppen-Admin kann weder Admin-Flag setzen noch Konten mit Admin-Flag anlegen, Gruppenbereiche erweitern, fremde Gruppen
  verwalten oder Servereinstellungen ändern. Sperren/Gruppenentzug wirken sofort (Anmelde-Cache wird geleert).
- **WebSocket:** ohne Anmeldung 401, mit fremdem `Origin` abgewiesen (kein CSWSH), Dokument-WebSocket nur für Berechtigte.
- **WebDAV/CalDAV:** Pfad-Umgehung (`..`, `%2e%2e`) ohne Wirkung, fremde Kalender nicht lesbar, schreibbar oder löschbar,
  XXE-Versuch folgenlos, extreme Serientermine ohne Hänger.
- **Importe:** Zip-Bombe (220 MB entpackt aus 224 kB) wird abgewiesen, Server lebt weiter; kaputte DOCX/XLSX/PDF liefern 4xx.
- **Eingaben:** Header-Injektion über Dateinamen (400), 100-kB-Suchtext, Regex-Bomben, 200-kB-Titel und 100000-fach
  verschachteltes JSON: kein 500, kein Absturz. Log-Injektion über Benutzernamen mit Zeilenumbruch: Serverlog bleibt sauber.
- **Log:** Admin-Passwort und Basic-Header stehen nicht im Serverlog, keine Panics. Speicherbedarf nach Lauf 176 MB.
- **Funktion:** Aufgaben, Suche (fremde Aufgaben unsichtbar), Papierkorb (nur eigener Bereich), Aushänge (Fremde dürfen nicht löschen),
  Benutzerimport, Jahrgangswechsel-Vorschau, Rundnachrichten-Gruppenliste: ohne Fehler.

## Fehlalarme (Testfehler, kein Produktfehler)

`S-V5` (bob schrieb 201 nach `/webdav/alice/`: es ist bobs eigener Unterordner, alice sieht ihn nicht), `S-V8` (fehlender
Content-Type im Test), `S-X9` (lange URL ohne Login liefert 401 vor dem URL-Check), `F-N1/N2/N4` (Aushang-Datum darf höchstens 5 Jahre
in der Zukunft liegen), `F-M2/M3` (Nachrichtenformat im Test falsch), `F-T2` und `F-P1/P2` (Feldnamen im Test falsch). Die zugehörigen
Rechteprüfungen wurden in `audit_live3.js` mit korrekten Eingaben wiederholt und bestanden.

## Nicht geprüft

Last- und Dauertests, Reverse-Proxy-Setups, LDAP/OIDC-Anbindung gegen ein echtes Verzeichnis, KI-Anbieter, Windows-Dienstintegration
in csweb-gui, Browser-Oberfläche (XSS-Prüfung per Hand in Chrome empfohlen: Chat-Nachricht mit `<img onerror>` und Dateinamen mit HTML).

## Reihenfolge

1. A-1 (Go und x/net aktualisieren, `govulncheck` wiederholen) vor jedem Release.
2. A-3 ist umgesetzt, mit test17 ausrollen.
3. A-4, A-5, A-6 in einem kleinen Sammel-Release.
4. A-2 als eigenes Vorhaben (UI-Skript auslagern, CSP verschärfen).
