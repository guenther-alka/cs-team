# Start cs-team as a standalone CLI app

cs-team is ONE static binary (no database, no web server, no installer). It runs without napp-it: you only need
the binary for your platform, a data folder (or an S3 bucket) and one command. This howto covers Linux, FreeBSD,
illumos/OmniOS and Windows.

## 1. Get the binary

Download the file for your platform from the GitHub release page (`windows`, `linux` amd64/arm64, `darwin`
amd64/arm64, `freebsd`, `illumos`; `checksums.sha256` is attached) or build it yourself:

```
go vet ./... && go test ./... && go build -o cs-team .          # current platform
powershell -NoProfile -File build-all.ps1 -Test                 # all platforms into dist\
```

Check it: `./cs-team version` prints the version and exits.

## 2. First start (folder storage, the simplest case)

```
mkdir -p /tank/team                                 # any local folder or ZFS dataset
export CS_DIR=/tank/team                            # data goes to /tank/team/.csteam
export CS_LISTEN=:9004                              # default is :8080
export CS_ADMIN_USER=admin CS_ADMIN_PASS=change-me-123   # first start only (only if no user exists yet)
./cs-team
```

Open `http://<host>:9004/` and log in as `admin`. Change the start password; stop with Ctrl+C.
Only ONE process per data folder.

Windows (PowerShell):

```
$env:CS_DIR='D:\team'; $env:CS_LISTEN=':9004'
$env:CS_ADMIN_USER='admin'; $env:CS_ADMIN_PASS='change-me-123'
.\cs-team.exe
```

Other storage choices:

```
# S3 bucket (e.g. RustFS)
S3_ENDPOINT=127.0.0.1:9000 S3_KEY=... S3_SECRET=... S3_BUCKET=cs-team S3_TLS=0 ./cs-team
# demo, everything in RAM, lost on exit
CS_MEM=1 ./cs-team
```

## 3. Commands

| Command | Meaning |
|---------|---------|
| `cs-team` | start the server (foreground, log to stdout/stderr) |
| `cs-team -c team.cfg` | start with a configuration file (see below) |
| `cs-team adduser anna secret [admin]` | create a user or reset the password (optional admin role); uses the same storage settings |
| `cs-team version` | print the version |

## 4. Configuration file instead of environment

`-c file` (or `CS_CONF=file`) reads `KEY=VALUE` lines; `#` starts a comment, quotes are removed. Variables that are
already set in the environment win over the file.

```
# team.cfg
CS_DIR=/tank/team
CS_LISTEN=:9004
CS_ADMIN_USER=admin
CS_ADMIN_PASS=change-me-123
CS_TLS_CERT=/etc/cs-team/cert.pem
CS_TRUST_PROXY=0
```

Start: `./cs-team -c team.cfg`. The first-admin variables can be removed after the first start.
Keep the file readable by the service user only (it may contain passwords).

## 5. Settings

| Variable | Meaning | Default |
|----------|---------|---------|
| `CS_LISTEN` | address:port | `:8080` |
| `CS_DIR` | data folder (data in `<dir>/.csteam`) | - |
| `S3_ENDPOINT` `S3_KEY` `S3_SECRET` `S3_BUCKET` `S3_TLS` | S3 storage instead of a folder | `127.0.0.1:9000`, bucket `cs-team` |
| `CS_MEM` | `1` = RAM only (demo/test) | `0` |
| `CS_ADMIN_USER` / `CS_ADMIN_PASS` | first admin, only if no user exists | - |
| `CS_TLS_CERT` / `CS_TLS_KEY` | HTTPS certificate / key (PEM; key may be in the same file); a changed PEM is reloaded without restart | HTTP |
| `CS_TRUST_PROXY` | `1` = evaluate `X-Forwarded-For` (only behind your own proxy) | `0` |
| `CS_MAX_UPLOAD_MB` / `CS_CHAT_MAX_MB` | upload limits | 100 / 10 |
| `CS_QUOTA_MB` | default file quota per user and group folder (better set in the UI) | 0 (unlimited) |
| `CS_LANG` / `CS_LANGDIR` | default UI language / folder with own language files | `de` |
| `CS_SMTP_HOST` `PORT` `TLS` `USER` `PASS` `FROM` | mail defaults (better set in the UI) | - |
| `CS_CHAT_ALLOW_PRIVATE` | `1` = allow webhooks to private networks by default | `0` |
| `CS_SNAPSHOT` / `CS_SNAPSHOT_CMD` / `CS_SNAPSHOT_DATASET` | snapshot before global admin actions: `off`, own command (`{id}` = run id), ZFS dataset override | automatic on ZFS |

Mail (SMTP), public address, webhooks, video chat (STUN/TURN), AI provider and quota are NOT start parameters:
they are set inside cs-team (global admin, menu *Settings*, effective immediately).

## 6. HTTPS and clients

Basic Auth is used, so run it with HTTPS: `CS_TLS_CERT=/path/cert.pem` (+ `CS_TLS_KEY`), or put an HTTPS proxy in front
and set `CS_TRUST_PROXY=1`. The built-in video chat (camera/microphone) needs HTTPS or `localhost`; the Windows network
drive (WebDAV) needs HTTPS.

Clients: CalDAV `https://host:9004/dav/` (Thunderbird, iOS, DAVx5), WebDAV `https://host:9004/webdav/` (rclone, WinSCP,
Cyberduck, macOS Finder), REST `/api/...`. Open the firewall port you chose.

## 7. Run as a service

Linux (systemd), file `/etc/systemd/system/cs-team.service`:

```
[Unit]
Description=cs-team
After=network.target

[Service]
User=csteam
ExecStart=/opt/cs-team/cs-team -c /etc/cs-team/team.cfg
Restart=on-failure
# bind to a port below 1024 only if needed:
# AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

```
useradd -r -s /usr/sbin/nologin csteam && chown -R csteam /tank/team
systemctl daemon-reload && systemctl enable --now cs-team
journalctl -u cs-team -f
```

FreeBSD: `rc.d` script or `daemon -r -u csteam -p /var/run/cs-team.pid /opt/cs-team/cs-team -c /etc/cs-team/team.cfg`.
illumos/OmniOS: an SMF manifest or `nohup`/`daemon` equivalent with the same command line.

Windows: Task Scheduler (task "at startup", run as a service account, action
`C:\cs-team\cs-team.exe -c C:\cs-team\team.cfg`, "start in" the same folder) or a service wrapper such as NSSM:
`nssm install cs-team C:\cs-team\cs-team.exe -c C:\cs-team\team.cfg`.

With napp-it CS web GUI you can instead use the menu *System > Services > 17_cs-team* (start, stop, enable).

## 8. Backup, update, stop

* Backup: stop cs-team or take a ZFS snapshot of the data folder (`.csteam`); with S3 use a bucket copy. Never write
  into `.csteam` or the bucket with other tools (metadata, rights and shares are kept separately); read-only access
  for backup is fine.
* Update: stop the process, replace the binary, start it again. Data and settings (`settings.json` in the storage) stay.
* Stop: Ctrl+C (foreground), `systemctl stop cs-team`, or end the process. Changes in open documents are written about 2 seconds after the last edit.
* Lost admin password: `./cs-team adduser admin newpassword admin` (same storage settings).

## 9. Quick checks

```
./cs-team version
curl -i http://127.0.0.1:9004/                     # 200 or 401 (login) = running
ss -ltnp | grep 9004                               # Linux: is it listening?
```

If the port is taken, change `CS_LISTEN`. If it refuses to start with a storage error, check the folder permissions
(the service user needs write access) and that no second cs-team uses the same folder.
