cs-ldapclient - log in to a computer with cs-team (LDAPS) accounts
==================================================================

1. Edit ldap.cfg (server, reader account, groups). Keep it on the stick: it holds the reader password.
2. Start as administrator/root:
   Windows : cs-ldapclient-windows.exe   (right click - run as administrator)
   Linux   : sh start-linux.sh           (works on FAT sticks, copies the program to /tmp)
   FreeBSD : sh start-freebsd.sh
   macOS   : not supported yet.
3. Menu: install / uninstall / show+change settings / test.
   Non-interactive: start-linux.sh install -y   (needs cert_sha256 in ldap.cfg)

Every cs-team user may log in on every computer. The cs-team groups only decide the local rights
(e.g. lehrer = administrators, klasse5a = users). After logout the local profile/home is deleted:
store documents in shared local folders or on SMB shares.
Keep a local administrator account as the way back.
