# cs-team

A small, self-contained collaboration platform ("Nextcloud light") as a **single Go binary** with a built-in web interface -
no database, no web server stack. Calendars, spreadsheets, text, files, tasks and chat for teams, schools and clubs,
in the desktop and phone browser. Storage: a folder (e.g. a ZFS dataset) or an S3 bucket.

cs-team has three roles:

- **Collaboration server** - calendar (CalDAV), Calc/Text with live multi-user editing, files (WebDAV), tasks, chat with
  video chat, messages, AI assistant; 13 UI languages.
- **Central directory** - users, passwords and groups with fixed uid/gid for the whole network: built-in read-only LDAPS
  server, client installer `cs-ldapclient`, local OS/SMB accounts on the cs-team host, AD/LDAP sign-in with mirror accounts.
- **High availability** - failover with a replicated ZFS dataset (master / read-only standby / takeover), service IP,
  takeover of the SMB accounts and passwords.

Part of the [napp-it 4ai (client-server edition)](https://napp-it.org) tool family ([cs-tools](https://www.napp-it.org/cs-tools_en.html));
it can be managed in the napp-it CS web GUI, but runs fine on its own.

**Manual (PDF, version 0.72):** [English](https://www.napp-it.org/pdf/cs-team_en.pdf) · [Deutsch](https://www.napp-it.org/pdf/cs-team_de.pdf)
· German README: [README.de.md](README.de.md) · Changes: [CHANGELOG.md](CHANGELOG.md)

## Installation

**With napp-it CS:** download cs-team in *About > Download cs tools*, configure and start it in *System > Services > cs-team*
(storage, port, HTTPS, admin, SMB users, HA, LDAP; enable = autostart).

**Standalone (any OS, no napp-it, no ZFS needed):**

1. Download the program for your OS from [Releases](https://github.com/guenther-alka/cs-team/releases) into a folder of its own
   (e.g. `C:\cs-team` or `/opt/cs-team`) and name it `cs-team` or `cs-team.exe`.
2. Start it (double-click on Windows, `./cs-team` in a terminal). A short menu asks for the settings, Enter keeps the default:
   data folder (`./`), PEM folder (`./pem`, a self-signed certificate is created if missing), port (`9004`), webcraft folder,
   *Sync local users for SMB* and the admin start password.
3. Open `https://<host>:9004` and sign in as `admin` (change the start password at the first sign-in).

The settings go to `team.cfg` next to the program; `cs-team setup` opens the menu again. Snapshots and backups are up to you.
Start scripts, service, all options: [docs/HOWTO-standalone.md](docs/HOWTO-standalone.md) and manual chapter 3.

With environment variables instead of the menu:

```
export CS_DIR=/tank/team CS_LISTEN=:9004                   # data in /tank/team/.csteam
export CS_ADMIN_USER=admin CS_ADMIN_PASS=change-me-123     # first start only
./cs-team
```

S3 instead of a folder: `S3_ENDPOINT=... S3_KEY=... S3_SECRET=... S3_BUCKET=cs-team`. Demo in RAM: `CS_MEM=1 ./cs-team`.
Other commands: `cs-team adduser NAME PASS [admin]`, `cs-team sysadmin NAME`, `cs-team osaccounts status`, `cs-team version`.

## Features

| Area | What it does |
|------|--------------|
| Users / groups | roles sysadmin, global admin, group admin, user, guests with expiry; groups enable areas; CSV and JSON import/export; initial passwords; two-factor (TOTP) and app passwords; login form, OIDC (Google, Microsoft, generic), AD/LDAP sign-in |
| Calendar | CalDAV (Thunderbird, iOS, DAVx5); personal, group, organization and global calendars, resources, subscriptions; series with time zones, reminders, invitations (iMIP), .ics import/export, plan calendars and year view |
| Calc / Text | live multi-user editing, formulas, comments/notes; import/export csv, xlsx, txt, rtf, docx, pdf; change list, write protection, earlier states from ZFS snapshots; merge files, AI summaries and AI research |
| Files | sharing (users, groups, team, public link with expiry), group folders, WebDAV with locks, quota, trash, versions from ZFS snapshots, read-only folder `share` |
| Today / search | events, due tasks, notices; global search (Ctrl+K) with the user's own rights |
| Tasks | ticket system light: assignee, milestones, board view, repetition, class tasks |
| Chat / message | group chat with channels, threads, 1:1 messages, polls; messages by group chat, e-mail (two SMTP servers) and webhook |
| Video chat | external servers (Jitsi, MiroTalk) or built-in WebRTC room (up to 6 participants, screen sharing) |
| AI assistant | Anthropic, OpenAI-compatible or Ollama; sees only what the user may see, writes nothing without confirmation |

Goal: about 80% of the most used Microsoft Teams / Office 365 functions for a school or a small organisation at a small fraction of
the complexity - one file, file based, in-house, GDPR friendly (no tracking, data export and erasure, retention, notices; manual chapter 14).
KISS wins whenever goals conflict.

## Directory, SMB accounts and high availability

- **LDAPS server:** `CS_LDAP=:636` serves users (`uid=<name>,ou=people,<base>`, posixAccount) and exported groups (posixGroup)
  read-only; binds check the cs-team password. Clients: Linux/FreeBSD sssd, illumos/Solaris, macOS (`CS_LDAP_ANON=1`), Windows
  (pGina), devices. Manual chapter 17.
- **cs-ldapclient:** console installer for Windows, Linux, FreeBSD and macOS (also as `cs-ldapclient.zip` for a USB stick);
  every cs-team user may sign in on every client, groups decide the local rights. Manual chapter 18.
- **Mirror accounts (AD/LDAP):** a directory user signs in as `paula@domain` or `paula`; the account is `paula` everywhere, the
  directory is only a marker. Old `name@realm` accounts are renamed at start (snapshot first, data moves along). With the
  directory unreachable or switched off, mirror accounts sign in from the cache until it expires; *Settings > Sign-in* converts
  them into local accounts that keep their password.
- **Local users for SMB** (`CS_OSACCOUNTS=1`, setup option *Sync local users for SMB*, default yes): members of exported groups
  get OS accounts on the cs-team host (illumos/OmniOS and Solaris 11 with the cs-team uid/gid and the kernel SMB server,
  Linux/FreeBSD with Samba, Windows). cs-team stores no NT hash; it sets the SMB/OS password whenever it sees the password in clear
  text (sign-in, password change). Disabled users are locked, never deleted. System names (root, administrator, guest ...) are
  never created. Manual chapter 19.
- **Failover:** the data folder lives in a ZFS dataset that your own replication job copies to a second host;
  `CS_FAILOVER=master|standby|takeover`, HA group and service IP in *Settings > High availability*. On promotion the new master
  takes over the OS accounts: illumos/Solaris with machine SID and SMB passwords (rights valid at once), Windows rewrites the
  share ACLs to the new SIDs, Linux/FreeBSD keep uid/gid; otherwise the password applies from the user's next sign-in. The standby
  never changes the OS. Manual chapter 20, [docs/HA_SPEC.md](docs/HA_SPEC.md).

Lab-tested (Nov 2026): OmniOS failover with SMB from Linux before and after takeover, Windows 11 promotion with SID rewrite and
password sync, FreeBSD 15.1 and Debian 12 accounts, pGina and sssd clients. Not tested: Solaris 11, a real AD.

## Configuration

Most settings (mail, public address, webhooks, video chat, AI, quota, sign-in, HA group) are made in the UI (*Settings*, global admins).
Start parameters as environment variables or `KEY=VALUE` lines in `team.cfg` / `-c file`:

| Variable | Meaning | Default |
|----------|---------|---------|
| `CS_DIR` or `S3_*` | storage: folder (data in `<dir>/.csteam`) or S3 bucket; one process per storage | - |
| `CS_LISTEN` | address/port | `:8080` |
| `CS_TLS_CERT` / `CS_TLS_KEY` | HTTPS certificate / key (PEM, reloaded on change) | HTTP |
| `CS_ADMIN_USER` / `CS_ADMIN_PASS` | first admin of an empty storage = sysadmin | - |
| `CS_LOGIN_FORM` | `0` = browser Basic Auth instead of the login form | form |
| `CS_TRUST_PROXY` / `CS_MAX_FAILS_IP` | behind an HTTPS proxy / failed sign-ins per address before lock | `0` / `60` |
| `CS_IDENTITY_*` | AD/LDAP sign-in defaults (`URL`, `REALM`, `BASE`, `BIND_DN`, `CACHE_DAYS` ...) | local |
| `CS_OIDC*` | OIDC providers (also in the UI) | - |
| `CS_LDAP` / `CS_LDAP_BASE` / `CS_LDAP_READER` / `CS_LDAP_ANON` | built-in LDAPS server | off |
| `CS_OSACCOUNTS` / `CS_OSACCOUNTS_SHARES` | local users for SMB / Windows share paths for the SID rewrite | off |
| `CS_FAILOVER` / `CS_HA_NAME` | `master`, `standby`, `takeover` / own name in the HA group | off / auto |
| `CS_WEBCRAFT` / `CS_SHARE` | folder of the webcraft apps / read-only document folder | auto |
| `CS_LANG` / `CS_LANGDIR` | default language / own language files | `de` |
| `CS_MAX_UPLOAD_MB` / `CS_QUOTA_MB` / `CS_TRASH_DAYS` | upload limit / default quota / trash days | 100 / 0 / 30 |

Full list: manual chapter 3.

## Clients

CalDAV `https://host:9004/dav/` · WebDAV `https://host:9004/webdav/` (rclone, WinSCP, Cyberduck, Finder, Windows network drive
with HTTPS) · REST `/api/...`. Android: Firefox as browser, DAVx5 for calendars (manual chapter 9.1).
Do not write directly into `.csteam` or the S3 bucket; use WebDAV or the UI.

## Build

```
go vet ./... && go test ./... && go build -o cs-team .
powershell -NoProfile -File build-all.ps1 -Test     # release builds into dist\
```

Release builds: Windows, Linux (amd64, arm64), macOS (amd64, arm64), FreeBSD, illumos/OmniOS, Solaris, plus `cs-ldapclient`.
Dependencies: minio-go (S3), emersion/go-webdav + go-ical (CalDAV), coder/websocket, x/crypto, x/term.

## License

BSD 2-Clause - see [LICENSE](LICENSE). Copyright (c) 2026 Guenther Alka / napp-it.org
