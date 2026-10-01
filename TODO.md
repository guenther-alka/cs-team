# TODO

- (nichts offen) Handbuch cs-team_de.pdf / cs-team_en.pdf am 2026-10-01 auf Stand 0.13 aktualisiert (Quellen: C:\opt\tmp\cs-team\man_src).
- Audit 0.11-Liste in 0.13.2 umgesetzt (S-03/04, S-06, S-07, S-08, S-10, K-02/C-10, K-03, C-06, F7). Mittel-Punkte S-09, S-11, K-05, F4, F8, F9 in 0.13.9 umgesetzt; A-04 (beabsichtigt), C-07, C-08/C-09, C-11, C-13/C-14 in 0.14.1. Offen aus AUDIT.md: C-12 (nur Dokumentation), WebDAV-Range/LOCK und die funktionalen Punkte (Quota-Anzeige je Gruppe/Admin-Uebersicht, Papierkorb).
- KI Stufe 2 erledigt fuer neue Dokumente (0.13.3); KI liest PDFs erledigt (0.13.5, Text-PDFs, kein OCR); offen: Aufgaben/Termine per KI vorschlagen, KI-Vorlagenentwuerfe, OCR fuer gescannte PDFs.
- Chat: Videochat. Umgesetzt: 0.13.7 drei externe Plaetze (Jitsi, MiroTalk, eigene URL mit {room}; fester Raum je Gruppe, Ad-hoc Gruppen-Admins, Ad-hoc alle Schreiber; Raumname per HMAC, Einladung 24 h); 0.13.8 Platz 4 eingebautes WebRTC (Mesh, bis 6, STUN/TURN in den Einstellungen). Offen: spaeter BigBlueButton (Ad-hoc durch Gruppen-Admin, API mit Geheimnis); Einladung optional per "Nachricht" (E-Mail/Webhook); Moderation/JWT bei Link-Anbietern; WebRTC: Bildschirm teilen, Sprecher-Hervorhebung, SFU fuer mehr als 6 (nur bei Bedarf).

- Kalender 0.14.0: Serientermine in der Anzeige, Zeitzonen-Fallback und Terminbearbeitung (nur dieser / alle) umgesetzt. Offen: Serien "dieser und folgende", Erinnerungen (VALARM), Teilnehmer, Abo-Status/Aktualisieren-Knopf, ICS-Import/-Export, VTIMEZONE fuer Apple/Outlook pruefen.
