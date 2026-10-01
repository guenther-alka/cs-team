# cs-team

A small, self-contained collaboration platform ("Nextcloud light") as a **single Go binary** with a built-in
web interface - no database, no web server stack. Calendars, spreadsheets, text, files, tasks and chat for
teams, schools and clubs. Works in the desktop and phone browser.

Part of the [napp-it 4ai (client-server edition)](https://napp-it.org) tooling family
(alongside [cs-tools](https://www.napp-it.org/cs-tools_en.html)). It can be started and stopped from the
napp-it CS web GUI (menu **System > Services > 17_cs-team**), but runs fine on its own.

**Run it standalone (CLI, config file, service):** [docs/HOWTO-standalone.md](docs/HOWTO-standalone.md)

**Manual (PDF):** [English](https://www.napp-it.org/pdf/cs-team_en.pdf) · [Deutsch](https://www.napp-it.org/pdf/cs-team_de.pdf)
· Detailed German notes: [README.de.md](README.de.md)

## Features

| Menu | What it does |
|------|--------------|
| User / Groups / Organizations | users, roles (global admin, group admin, user), groups that enable areas, CSV import/export |
| Calendar | CalDAV (Thunderbird, iOS, DAVx5): personal, global, group, resource (no double booking), internet subscriptions; recurring events with time zones, edit "only this / all events of the series" |
| Calc / Text | multi-user live editing (per cell / per paragraph), formulas, import/export csv, xlsx, txt, rtf, docx |
| Files | storage with sharing (users, groups, team, public link), group folders, WebDAV, resumable/seekable downloads (Range, ETag/304), optional quota per user and group folder, trash (restore, 30 days by default), WebDAV file locks (LOCK/UNLOCK) |
| Tasks | ticket system light: requester, assignee, milestones, history, repetition, notifications |
| Chat / Message | group chat with channels (attachments, reactions, @mentions); broadcast by e-mail, webhook (Slack, Discord, Telegram, ntfy) and chat |
| Video chat | in the chat: fixed room per group or ad-hoc rooms on external servers (Jitsi, MiroTalk, own URL), plus a **built-in WebRTC room** (browser only, up to 6 participants, peer-to-peer mesh, STUN/TURN configurable) |
| AI assistant | optional widget (Anthropic, OpenAI-compatible, Ollama): answers questions about your tasks, appointments, files and chat with your own rights, reads chosen files (text, DOCX, XLSX, PDF, images) and proposes new documents; nothing is written without your confirmation |
| Settings | mail server (SMTP), public address (DynDNS), webhooks, video chat servers, AI provider, file quota - effective immediately |

Also included: user deletion wizard with preview and snapshot, renaming a group (class) with its folders and shares,
rights changes that take effect at once on open documents, CSV user/group import and export, phone layout.
Security: bcrypt passwords with lockout, SSRF block list for webhooks, calendar feeds and AI endpoints, chat addresses
(webhook URLs hold access keys) visible only to the user and global admins, security headers, no formula injection in exports.

UI languages: de, en, fr, es, it, ru, cn, tr, ar (right-to-left); more can be added without rebuilding
(`CS_LANGDIR`).

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

Basic Auth is used - run it with HTTPS (`CS_TLS_CERT`, `CS_TLS_KEY`) or behind an HTTPS proxy
(`CS_TRUST_PROXY=1`). Only one process per data folder.

| Variable | Meaning | Default |
|----------|---------|---------|
| `CS_LISTEN` | address/port | `:8080` |
| `CS_DIR` or `S3_*` | storage (folder or S3 bucket) | - |
| `CS_ADMIN_USER` / `CS_ADMIN_PASS` | first admin (only if no user exists) | - |
| `CS_TLS_CERT` / `CS_TLS_KEY` | HTTPS certificate / key (PEM) | HTTP |
| `CS_TRUST_PROXY` | `1` = evaluate X-Forwarded-For | `0` |
| `CS_MAX_UPLOAD_MB` / `CS_CHAT_MAX_MB` | upload limits | 100 / 10 |
| `CS_QUOTA_MB` | default file quota per user and group folder in MB (better set in *Settings*) | 0 (unlimited) |
| `CS_TRASH_DAYS` | days deleted files stay in the trash (better set in *Settings*; 0 = no trash) | 30 |
| `CS_LANG` / `CS_LANGDIR` | default language / own language files | `de` |
| `CS_CONF` or `-c file` | configuration file (`KEY=VALUE`) | - |

Mail (SMTP), public address, webhooks, video chat (STUN/TURN), AI provider and the file quota are configured inside cs-team (menu *Settings*, global admins only).
The built-in video chat needs HTTPS or localhost (browser rule for camera and microphone).

## Clients

CalDAV `https://host:9004/dav/` · WebDAV `https://host:9004/webdav/` (rclone, WinSCP, Cyberduck, macOS Finder;
Windows network drive needs HTTPS; DAV class 2 with LOCK/UNLOCK, so Explorer and Office can write) · REST `/api/...`.

## Build

```
go vet ./... && go test ./... && go build -o cs-team .
powershell -NoProfile -File build-all.ps1 -Test     # 8 static release builds into dist\
```

Release builds: Windows, Linux (amd64, arm64), macOS (amd64, arm64), FreeBSD, illumos/OmniOS, Solaris.
Dependencies: minio-go (S3), emersion/go-webdav + go-ical (CalDAV), coder/websocket, x/crypto (bcrypt).

## License

BSD 2-Clause - see [LICENSE](LICENSE). Copyright (c) 2026 Guenther Alka / napp-it.org
