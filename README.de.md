# cs-team

Kleine, in sich geschlossene Zusammenarbeits-Plattform ("Nextcloud light") als **ein Go-Programm** mit eingebauter Weboberfläche -
ohne Datenbank, ohne Webserver-Stack. Kalender, Tabellen, Texte, Dateien, Aufgaben und Chat für Teams, Schulen und Vereine,
im Browser am PC und am Handy. Speicher: ein Ordner (z. B. ZFS-Dataset) oder ein S3-Bucket.

cs-team hat drei Aufgaben:

- **Zusammenarbeits-Server** - Kalender (CalDAV), Calc/Text mit gemeinsamer Live-Bearbeitung, Dateien (WebDAV), Aufgaben,
  Chat mit Videochat, Nachrichten, KI-Assistent; 13 Sprachen.
- **Zentrales Verzeichnis** - Benutzer, Passwörter und Gruppen mit fester uid/gid für das ganze Netz: eingebauter, nur lesender
  LDAPS-Server, Client-Installer `cs-ldapclient`, lokale OS-/SMB-Konten auf dem cs-team-Rechner, AD/LDAP-Anmeldung mit Spiegelkonten.
- **Hochverfügbarkeit** - Failover mit repliziertem ZFS-Dataset (Master / nur lesender Standby / Übernahme), Dienst-IP,
  Übernahme der SMB-Konten und -Passwörter.

Teil der Werkzeugfamilie von [napp-it 4ai (Client-Server-Edition)](https://napp-it.org) ([cs-tools](https://www.napp-it.org/cs-tools.html));
lässt sich in der napp-it CS Web-GUI verwalten, läuft aber auch eigenständig.

**Handbuch (PDF, Version 0.72):** [Deutsch](https://www.napp-it.org/pdf/cs-team_de.pdf) · [English](https://www.napp-it.org/pdf/cs-team_en.pdf)
· English README: [README.md](README.md) · Änderungen: [CHANGELOG.md](CHANGELOG.md)

## Installation

**Mit napp-it CS:** cs-team unter *About > Download cs tools* herunterladen, unter *System > Services > cs-team* einrichten und
starten (Speicher, Port, HTTPS, Admin, SMB-Benutzer, HA, LDAP; enable = Autostart).

**Eigenständig (jedes Betriebssystem, ohne napp-it, kein ZFS nötig):**

1. Programm für das Betriebssystem unter [Releases](https://github.com/guenther-alka/cs-team/releases) in einen eigenen Ordner laden
   (z. B. `C:\cs-team` oder `/opt/cs-team`), Name `cs-team` bzw. `cs-team.exe`.
2. Starten (Doppelklick unter Windows, `./cs-team` im Terminal). Ein kurzes Menü fragt die Einstellungen ab, Enter übernimmt die
   Vorgabe: Daten-Ordner (`./`), PEM-Ordner (`./pem`, fehlt das Zertifikat, wird ein selbst signiertes angelegt), Port (`9004`),
   Webcraft-Ordner, *Sync local users for SMB* und das Admin-Startpasswort.
3. `https://<rechner>:9004` öffnen und als `admin` anmelden (Startpasswort beim ersten Anmelden ändern).

Die Einstellungen stehen in `team.cfg` neben dem Programm; `cs-team setup` öffnet das Menü erneut. Snapshots und Sicherung macht
der Betreiber selbst. Startskripte, Dienst, alle Optionen: [docs/HOWTO-standalone.md](docs/HOWTO-standalone.md) und Handbuch Kapitel 3.

Mit Umgebungsvariablen statt Menü:

```
export CS_DIR=/tank/team CS_LISTEN=:9004                   # Daten in /tank/team/.csteam
export CS_ADMIN_USER=admin CS_ADMIN_PASS=bitte-aendern-1   # nur beim ersten Start
./cs-team
```

S3 statt Ordner: `S3_ENDPOINT=... S3_KEY=... S3_SECRET=... S3_BUCKET=cs-team`. Demo im RAM: `CS_MEM=1 ./cs-team`.
Weitere Befehle: `cs-team adduser NAME PASS [admin]`, `cs-team sysadmin NAME`, `cs-team osaccounts status`, `cs-team version`.

## Funktionen

| Bereich | Inhalt |
|---------|--------|
| Benutzer / Gruppen | Rollen Sysadmin, globaler Admin, Gruppen-Admin, Benutzer, Gäste mit Ablauf; Gruppen schalten Bereiche frei; CSV- und JSON-Import/-Export; Initialpasswörter; Zwei-Faktor (TOTP) und App-Passwörter; Anmeldeformular, OIDC (Google, Microsoft, generisch), AD/LDAP-Anmeldung |
| Kalender | CalDAV (Thunderbird, iOS, DAVx5); persönliche, Gruppen-, Organisations- und globale Kalender, Ressourcen, Abos; Serien mit Zeitzonen, Erinnerungen, Einladungen (iMIP), .ics-Import/-Export, Plan-Kalender und Jahresansicht |
| Calc / Text | gemeinsame Live-Bearbeitung, Formeln, Kommentare/Notizen; Import/Export csv, xlsx, txt, rtf, docx, pdf; Änderungsliste, Schreibschutz, frühere Stände aus ZFS-Snapshots; Dateien zusammenfügen, KI-Zusammenfassung und KI-Recherche |
| Dateien | Freigaben (Benutzer, Gruppen, Team, öffentlicher Link mit Ablauf), Gruppenordner, WebDAV mit Sperren, Kontingent, Papierkorb, Versionen aus ZFS-Snapshots, nur lesender Ordner `share` |
| Aktuell / Suche | Termine, fällige Aufgaben, Aushänge; globale Suche (Strg+K) mit den eigenen Rechten |
| Aufgaben | Ticketsystem light: Bearbeiter, Meilensteine, Board, Wiederholung, Klassenaufgaben |
| Chat / Nachricht | Gruppen-Chat mit Kanälen, Threads, 1:1-Nachrichten, Umfragen; Nachricht per Gruppen-Chat, E-Mail (zwei SMTP-Server) und Webhook |
| Videochat | externe Server (Jitsi, MiroTalk) oder eingebauter WebRTC-Raum (bis 6 Teilnehmer, Bildschirm teilen) |
| KI-Assistent | Anthropic, OpenAI-kompatibel oder Ollama; sieht nur, was der Benutzer sehen darf, schreibt nichts ohne Bestätigung |

Ziel: rund 80 % der meistgenutzten Funktionen von Microsoft Teams / Office 365 für Schule und Kleinbetrieb bei einem Bruchteil der
Komplexität - eine Datei, dateibasiert, im eigenen Haus, DSGVO-freundlich (kein Tracking, Datenexport und Löschung, Aufbewahrungsfristen,
Hinweise; Handbuch Kapitel 14). KISS gewinnt, wenn sich Ziele widersprechen.

## Verzeichnis, SMB-Konten und Hochverfügbarkeit

- **LDAPS-Server:** `CS_LDAP=:636` liefert Benutzer (`uid=<name>,ou=people,<base>`, posixAccount) und exportierte Gruppen (posixGroup)
  nur lesend; Anmeldungen prüfen das cs-team-Passwort. Clients: Linux/FreeBSD mit sssd, illumos/Solaris, macOS (`CS_LDAP_ANON=1`),
  Windows (pGina), Geräte. Handbuch Kapitel 17.
- **cs-ldapclient:** Konsolen-Installer für Windows, Linux, FreeBSD und macOS (auch als `cs-ldapclient.zip` für den USB-Stick);
  jeder cs-team-Benutzer darf sich an jedem Client anmelden, Gruppen bestimmen die lokalen Rechte. Handbuch Kapitel 18.
- **Spiegelkonten (AD/LDAP):** ein Verzeichnisbenutzer meldet sich mit `paula@domain` oder `paula` an; das Konto heißt überall
  `paula`, das Verzeichnis ist nur ein Merkmal. Alte Konten `name@realm` werden beim Start umbenannt (vorher Snapshot, Daten ziehen
  mit). Ist das Verzeichnis nicht erreichbar oder abgeschaltet, melden sich Spiegelkonten bis zum Fristende aus dem Zwischenspeicher an;
  *Einstellungen > Anmeldung* wandelt sie in lokale Konten um, die ihr Passwort behalten.
- **Lokale Benutzer für SMB** (`CS_OSACCOUNTS=1`, Setup-Frage *Sync local users for SMB*, Vorgabe ja): Mitglieder exportierter Gruppen
  bekommen Betriebssystem-Konten auf dem cs-team-Rechner (illumos/OmniOS und Solaris 11 mit der cs-team-uid/gid und dem Kernel-SMB-Server,
  Linux/FreeBSD mit Samba, Windows). cs-team speichert keinen NT-Hash; es setzt das SMB-/OS-Passwort, wenn es das Passwort im Klartext
  sieht (Anmeldung, Passwortwechsel). Gesperrte Benutzer werden gesperrt, nie gelöscht. Systemnamen (root, administrator, guest ...)
  werden nie angelegt. Handbuch Kapitel 19.
- **Failover:** der Datenordner liegt in einem ZFS-Dataset, das ein eigener Replikationsjob auf einen zweiten Rechner kopiert;
  `CS_FAILOVER=master|standby|takeover`, HA-Gruppe und Dienst-IP unter *Einstellungen > Hochverfügbarkeit*. Beim Hochstufen übernimmt
  der neue Master die OS-Konten: illumos/Solaris mit Rechner-SID und SMB-Passwörtern (Rechte sofort gültig), Windows schreibt die
  ACL der Freigaben auf die neuen SIDs um, Linux/FreeBSD behalten uid/gid; sonst gilt das Passwort ab der nächsten Anmeldung. Der
  Standby verändert das Betriebssystem nie. Handbuch Kapitel 20, [docs/HA_SPEC.md](docs/HA_SPEC.md).

Im Labor getestet (Nov. 2026): OmniOS-Failover mit SMB von Linux vor und nach der Übernahme, Windows-11-Hochstufen mit SID-Umschreibung
und Passwort-Sync, Konten unter FreeBSD 15.1 und Debian 12, Clients mit pGina und sssd. Nicht getestet: Solaris 11, echtes AD.

## Konfiguration

Die meisten Einstellungen (Mail, öffentliche Adresse, Webhooks, Videochat, KI, Kontingent, Anmeldung, HA-Gruppe) macht man in der
Oberfläche (*Einstellungen*, globale Admins). Startparameter als Umgebungsvariablen oder `KEY=VALUE`-Zeilen in `team.cfg` / `-c datei`:

| Variable | Bedeutung | Standard |
|----------|-----------|----------|
| `CS_DIR` oder `S3_*` | Speicher: Ordner (Daten in `<ordner>/.csteam`) oder S3-Bucket; ein Prozess je Speicher | - |
| `CS_LISTEN` | Adresse/Port | `:8080` |
| `CS_TLS_CERT` / `CS_TLS_KEY` | HTTPS-Zertifikat / Schlüssel (PEM, bei Änderung neu geladen) | HTTP |
| `CS_ADMIN_USER` / `CS_ADMIN_PASS` | erster Admin eines leeren Speichers = Sysadmin | - |
| `CS_LOGIN_FORM` | `0` = Basic Auth im Browser statt Anmeldeformular | Formular |
| `CS_TRUST_PROXY` / `CS_MAX_FAILS_IP` | hinter HTTPS-Proxy / Fehlversuche je Adresse bis zur Sperre | `0` / `60` |
| `CS_IDENTITY_*` | Vorgaben AD/LDAP-Anmeldung (`URL`, `REALM`, `BASE`, `BIND_DN`, `CACHE_DAYS` ...) | lokal |
| `CS_OIDC*` | OIDC-Anbieter (auch in der Oberfläche) | - |
| `CS_LDAP` / `CS_LDAP_BASE` / `CS_LDAP_READER` / `CS_LDAP_ANON` | eingebauter LDAPS-Server | aus |
| `CS_OSACCOUNTS` / `CS_OSACCOUNTS_SHARES` | lokale Benutzer für SMB / Windows-Freigaben für die SID-Umschreibung | aus |
| `CS_FAILOVER` / `CS_HA_NAME` | `master`, `standby`, `takeover` / eigener Name in der HA-Gruppe | aus / automatisch |
| `CS_WEBCRAFT` / `CS_SHARE` | Ordner der Webcraft-Apps / nur lesender Dokumentordner | automatisch |
| `CS_LANG` / `CS_LANGDIR` | Standardsprache / eigene Sprachdateien | `de` |
| `CS_MAX_UPLOAD_MB` / `CS_QUOTA_MB` / `CS_TRASH_DAYS` | Upload-Grenze / Standard-Kontingent / Papierkorb-Tage | 100 / 0 / 30 |

Vollständige Liste: Handbuch Kapitel 3.

## Clients

CalDAV `https://host:9004/dav/` · WebDAV `https://host:9004/webdav/` (rclone, WinSCP, Cyberduck, Finder, Windows-Netzlaufwerk mit
HTTPS) · REST `/api/...`. Android: Firefox als Browser, DAVx5 für Kalender (Handbuch Kapitel 9.1).
Nicht direkt in `.csteam` oder den S3-Bucket schreiben; WebDAV oder die Oberfläche verwenden.

## Build

```
go vet ./... && go test ./... && go build -o cs-team .
powershell -NoProfile -File build-all.ps1 -Test     # Release-Builds nach dist\
```

Release-Builds: Windows, Linux (amd64, arm64), macOS (amd64, arm64), FreeBSD, illumos/OmniOS, Solaris, dazu `cs-ldapclient`.
Abhängigkeiten: minio-go (S3), emersion/go-webdav + go-ical (CalDAV), coder/websocket, x/crypto, x/term.

## Lizenz

BSD 2-Clause - siehe [LICENSE](LICENSE). Copyright (c) 2026 Guenther Alka / napp-it.org
