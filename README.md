# cs-team

A small, self-contained collaboration platform ("Nextcloud light") as a **single Go binary** with a built-in
web interface - no database, no web server stack. Calendars, spreadsheets, text, files, tasks and chat for
teams, schools and clubs. Works in the desktop and phone browser.

Part of the [napp-it 4ai (client-server edition)](https://napp-it.org) tooling family
(alongside [cs-tools](https://www.napp-it.org/cs-tools_en.html)). It can be started and stopped from the
napp-it CS web GUI (menu **System > Services > 17_cs-team**), but runs fine on its own.

**Run it standalone (CLI, config file, service):** [docs/HOWTO-standalone.md](docs/HOWTO-standalone.md)

**Manual (PDF, version 0.62):** [English](https://www.napp-it.org/pdf/cs-team_en.pdf) · [Deutsch](https://www.napp-it.org/pdf/cs-team_de.pdf)
· Detailed German notes: [README.de.md](README.de.md)

## Goal

cs-team aims at roughly 80% of the most used Microsoft Teams / Office 365 functions for a school or a small organisation - deliberately without
a built-in mail server, video-conferencing infrastructure, telephony and full docx/xlsx compatibility - at far less complexity than Teams, O365 or
Nextcloud (target: under 10%, a qualitative estimate): one file, copy and run, file based, no database, in-house and GDPR friendly, easy to extend and translate.
KISS wins whenever goals conflict. Honest AI assessment (handbook chapter 17, version 0.61, estimates): about 60-70% of the daily-used functions are covered;
since then added: global search, 1:1 messages and threads, mention notifications (e-mail, ntfy), guests, comments in documents and sign-in with OIDC; still open: native web push (ntfy covers the phone) and full docx/xlsx fidelity.

## Features

| Menu | What it does |
|------|--------------|
| User / Groups / Organizations | users, roles (global admin, group admin, user), guest accounts with expiry date (always in the group `guests`, no admin rights, no AI),  groups that enable areas, CSV import/export, universal JSON export/import of all users and groups (backup, migration, optionally with password hashes and 2FA secrets; handbook chapter 15), two-factor login (TOTP) with recovery codes and app passwords for WebDAV/CalDAV (local accounts; admins can be required to use it); **one role model**: *sysadmin* (startup admin, always global admin, the only one who creates and edits further global admins), global admin, group admin, user, with the fixed system groups `globaladmins` (membership = admin right) and `groupadmins` (computed from the group admin lists); **initial passwords** (30 days, shown once, forced change) with a user list per group (upload/download, optional delivery by e-mail) and *Reconfigure*; optional **login form** and **sign-in/linking with OIDC** (Google, Microsoft, generic); user page with login type, two-factor state, *last seen* and the role in one sentence; editable group description (comment) |
| Calendar | CalDAV (Thunderbird, iOS, DAVx5): personal, group, organization and global calendars, each with its own release (not released / entitled read / entitled write), plus internet subscriptions (read-only) and resources (no double booking); recurring events with time zones, edit "only this / this and following / all events of the series", mouse control (click a day for a new event, Shift+click for a multi-day range, drag & drop, Ctrl+X/C/V), reminders (VALARM), attendees with e-mail invitation (iMIP), .ics import/export per calendar, subscription status |
| Calc / Text | multi-user live editing (per cell / per paragraph), formulas, undo/redo, number formats, hour grid in the calendar, more functions (SUMIF, VLOOKUP, TEXT ...), sort, freeze, column width, alignment, import/export csv, xlsx, txt, rtf, docx, pdf export (with formats: bold, italic, underline, strikethrough, colour, size, background, lists, indent, comments/notes as Word comments and Excel notes, cell formats; xlsx first sheet, dates as text, docx tables as tab-separated rows; no images, charts, macros; pdf uses the standard font Helvetica, Latin text only); strikethrough cells count as empty in formulas; keyboard Ctrl+Shift+X (strikethrough), Shift+F2 (comment/note); Text > + add: merge several PDFs / Word / text files / texts into one new cs-text or PDF (no AI), and AI functions (summarize, key points, tasks and appointments) on a selection with the result saved as cs-text or PDF; **AI research** (Text > + add): question + level (short overview / short presentation / analytical paper), sources from the provider's web search, else from cs-team (Wikipedia, OpenAlex and, if set up by the admin, Brave Search or SearXNG), result with framework, fact check verdict, source rating and "how to check it yourself", translation into any UI language, saved as cs-text or PDF; Calc: strikethrough cells (count as empty), cell notes, PDF export |
| Files | storage with sharing (users, groups, team, public link), group folders, WebDAV, resumable/seekable downloads (Range, ETag/304), optional quota per user and group folder, trash (restore, 30 days by default), public links with expiry (7 days by default, global cap), file versions from ZFS snapshots (browse, download, restore), WebDAV file locks (LOCK/UNLOCK) |
| Today / Search | start page with today's events, due tasks and news, plus notices (admins publish to everyone or to a group, group admins to their groups; optional expiry date; unread counter); global search (Ctrl+K) over tasks, events, documents, files and chat, each with the user's own rights |
| Tasks | ticket system light: requester, assignee, milestones (= checklist), labels, board view (drag cards between open / doing / done), history, repetition, notifications, class tasks (distribute to all group members as private copies, hand-in and acceptance) |
| Chat / Message | group chat with channels (attachments, reactions, @mentions, replies/threads, private 1:1 messages switchable per group - group admin with member or members among themselves, nobody else reads them - polls - named or anonymous, multiple choice, end time); broadcast by e-mail, webhook (Slack, Discord, Telegram, ntfy) and chat: admins of a group choose group chat / e-mail / external chat address, members (if the group allows sending) write to the group chat only |
| Video chat | in the chat: fixed room per group or ad-hoc rooms on external servers (Jitsi, MiroTalk, own URL), plus a **built-in WebRTC room** (browser only, up to 6 participants, peer-to-peer mesh, STUN/TURN configurable, screen sharing) |
| AI assistant | optional widget (Anthropic, OpenAI-compatible, Ollama): answers questions about your tasks, appointments, files and chat with your own rights, reads chosen files (text, DOCX, XLSX, PDF, images) and proposes new documents; nothing is written without your confirmation |
| Settings | mail server (SMTP), public address (DynDNS), webhooks, video chat servers, AI provider, file quota - effective immediately |

Also included: user deletion wizard with preview and snapshot, renaming a group (class) with its folders and shares,
rights changes that take effect at once on open documents, CSV and JSON user/group import and export (`GET /api/users/exportjson`, `POST /api/users/importjson`), phone layout.
Security: bcrypt passwords with lockout, SSRF block list for webhooks, calendar feeds and AI endpoints, chat addresses
(webhook URLs hold access keys) visible only to the user and global admins, security headers, no formula injection in exports.

UI languages: de, en, fr, es, it, ru, cn, tr, ar (right-to-left), uk, pl, el, ja; more can be added without rebuilding
(`CS_LANGDIR`).

## Privacy / GDPR

cs-team keeps all data on your own server (folder/ZFS dataset or S3); no tracking, no external fonts or scripts; a session cookie is set only by the login form (default; `CS_LOGIN_FORM=0` turns it off).
- **Access / portability (Art. 15, 20):** "Export my data (ZIP)" on the account page (account without password hash, own files, calendars, tasks, chat); admins can export for a user.
- **Erasure (Art. 17):** user deletion with preview, snapshot and the option to replace the name in chat, tasks and events with "gelöschter Benutzer" (anonymize, on by default).
- **Retention:** per group "Chat retention (days)", global "Closed tasks: days until automatic deletion", trash days; each run is logged (`retention: ...`).
- **Notice before first use** of the AI assistant and the external video chat (provider/server, data sent, voluntary); acknowledgement per user, reset when the provider or server changes; optional additional privacy text in Settings.
- **Logs:** failed logins, lockouts, audit lines (who changed users/groups/settings) and errors, never passwords or contents; they contain user names and IP addresses, so rotate them externally (30-90 days).
- The handbook chapter 11 "Privacy (GDPR)" adds a TOM checklist, templates for schools (notice, record of processing activities, consent) and a 72-hour data breach procedure. It is technical help, not legal advice.

## Quick start

```
# data in a folder (e.g. a ZFS dataset) - stored in /tank/team/.csteam
export CS_DIR=/tank/team CS_LISTEN=:9004
export CS_ADMIN_USER=admin CS_ADMIN_PASS=change-me-123     # first start only
./cs-team
```

Or with an S3 bucket (RustFS): `S3_ENDPOINT=127.0.0.1:9000 S3_KEY=... S3_SECRET=... S3_BUCKET=cs-team S3_TLS=0`.
Demo without storage: `CS_MEM=1 ./cs-team`. Set or reset a user: `./cs-team adduser anna secret [admin]`.
Version: `./cs-team version`.

A login form with session cookie is the default (`CS_LOGIN_FORM=0` = browser Basic Auth) - run it with HTTPS (`CS_TLS_CERT`, `CS_TLS_KEY`) or behind an HTTPS proxy
(`CS_TRUST_PROXY=1`). Only one process per data folder.

| Variable | Meaning | Default |
|----------|---------|---------|
| `CS_LISTEN` | address/port | `:8080` |
| `CS_DIR` or `S3_*` | storage (folder or S3 bucket) | - |
| `CS_ADMIN_USER` / `CS_ADMIN_PASS` | first admin (only if no user exists); this account becomes the **sysadmin**: local, always admin, cannot be deleted, disabled or demoted (change with `cs-team sysadmin NAME`) | - |
| `CS_TLS_CERT` / `CS_TLS_KEY` | HTTPS certificate / key (PEM) | HTTP |
| `CS_TRUST_PROXY` | `1` = evaluate X-Forwarded-For | `0` |
| `CS_MAX_FAILS_IP` | failed sign-ins per address before it is locked (signed-in users exempt) | `60` |
| `CS_IDENTITY_*` | directory/LDAP sign-in defaults (`MODE`, `REALM`, `URL`, `BASE`, `BIND_DN`, `BIND_PW`, `STARTTLS`, `ADMIT_GROUPS`, `LOCAL_GROUP`, `ALLOW_LOCAL`, `CACHE_DAYS`); UI settings override | local |
| `CS_LOGIN_FORM` | `0` = browser login window (Basic) instead of the login form with session cookie; the form is the default and required for OIDC (WebDAV/CalDAV stay on Basic / app password) | form |
| `CS_OIDC` / `CS_OIDC_BASE` | OIDC providers, e.g. `google,ms,keycloak`; BASE = public **https** address of this server (return address `BASE/oidc/callback`) | - |
| `CS_OIDC_<ID>_CLIENT_ID` / `_CLIENT_SECRET` / `_ISSUER` / `_NAME` / `_SCOPE` | the application registered at the provider; Microsoft only with a fixed tenant id in `CS_OIDC_MS_TENANT` | - |
| `CS_MAX_UPLOAD_MB` / `CS_CHAT_MAX_MB` | upload limits | 100 / 10 |
| `CS_QUOTA_MB` | default file quota per user and group folder in MB (better set in *Settings*) | 0 (unlimited) |
| `CS_TRASH_DAYS` | days deleted files stay in the trash (better set in *Settings*; 0 = no trash) | 30 |
| `CS_LANG` / `CS_LANGDIR` | default language / own language files | `de` |
| `CS_CONF` or `-c file` | configuration file (`KEY=VALUE`) | - |

Mail (SMTP), public address, webhooks, video chat (STUN/TURN), AI provider and the file quota are configured inside cs-team (menu *Settings*, global admins only).
The built-in video chat needs HTTPS or localhost (browser rule for camera and microphone).

## Sign-in and roles

- **Login:** by default a login form with a session cookie (`CS_LOGIN_FORM=0` = browser Basic Auth); WebDAV/CalDAV stay on Basic or app passwords. Every user chooses a local password or OIDC. Account types: **local**, **directory** (LDAP/AD, `name@realm`) and **OIDC**; local names such as `anna@school.org` are allowed, the directory realm stays reserved. **The login type follows the account name** (no `@` = local with password or OIDC, `name@realm` = directory; refused while none is configured). **Forgot password** on the login page: link by mail (1 h, single use, in the account language, default English) for active local accounts with a mail address, excluding admins, guests, directory and 2FA accounts; then a new password or OIDC, logged. **OIDC for Google and Microsoft** can be entered under Settings (public https address required, secret write-only, overrides `CS_OIDC_*`).
- **Initial passwords:** the admin imports a user list into a group (`name[, mail[, password]]`, header `org: my.school.org` appends the domain), generates initial passwords (valid 30 days, displayed once, only the hash is stored, forced change at first login) and may deliver them by e-mail. *Reconfigure* issues a new one and removes the OIDC link, two-factor and app passwords.
- **OIDC:** the user logs in with the initial password once and links the account to a provider himself (authorization code + PKCE, own id_token check, identity = issuer + `sub`, never the mail address). Afterwards only OIDC applies (password *or* OIDC). The sysadmin and directory accounts never use OIDC (emergency access stays local); global admins may link, and only the sysadmin resets a linked admin.
- **Roles:** the *sysadmin* (`CS_ADMIN_USER`, or `cs-team sysadmin NAME`) is the startup admin and always global admin; only he creates and edits further global admins. Global admins manage everything else including group admins and only view other admins. System groups `globaladmins` and `groupadmins` cannot be deleted or renamed. Two-factor stays optional (mandatory for admins by a setting).
- **Message:** admins of a group may send by group chat, e-mail and external chat address, members (if the group allows it) only to the group chat.

## Clients

CalDAV `https://host:9004/dav/` · WebDAV `https://host:9004/webdav/` (rclone, WinSCP, Cyberduck, macOS Finder;
Windows network drive needs HTTPS; DAV class 2 with LOCK/UNLOCK, so Explorer and Office can write) · REST `/api/...`.

A CalDAV client sees exactly the calendars the user may see (own, group, organization, global and subscriptions);
calendars that are not released (`off`) are missing and answer 404 on direct access. Writing is allowed only where the
release says so (`rw`, or as owner/manager); subscriptions are always read-only (403). Owners may always change their own
calendars (the release only governs other people); if the user's *Calendar* area is read-only (group template `klasse`),
their own calendars are locked as well (403). New calendars are created in the UI (calendar bar → **+ add**); via CalDAV a
calendar is created with `MKCOL` and a `<c:calendar/>` resource type, `MKCALENDAR` is answered with 405.
Group calendars come with the group (checkbox *Create group calendar*, templates `team`/`klasse`) or later in the group
settings (global admins): `cal` = `""` (none / removal, only while the calendar is empty), `off`, `ro`, `rw`.
`GET /api/groups` reports `cal` per group, `POST /api/groups/<group>` sets it.
A single calendar as a file: **Export (.ics)** in the calendar bar = `GET /api/cal/<id>/export.ics` (also for read-only
calendars and subscriptions), back into a writable calendar via **Import (.ics)** = `POST /api/cal/<id>/import`
(matches by UID, repeated import creates no duplicates).

## Build

```
go vet ./... && go test ./... && go build -o cs-team .
powershell -NoProfile -File build-all.ps1 -Test     # 8 static release builds into dist\
```

Release builds: Windows, Linux (amd64, arm64), macOS (amd64, arm64), FreeBSD, illumos/OmniOS, Solaris.
Dependencies: minio-go (S3), emersion/go-webdav + go-ical (CalDAV), coder/websocket, x/crypto (bcrypt).

## License

BSD 2-Clause - see [LICENSE](LICENSE). Copyright (c) 2026 Guenther Alka / napp-it.org
