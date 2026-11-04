#!/bin/sh
# start-osx.command - starts cs-ldapclient from a USB stick (no execute bit needed): sh start-osx.command
# Copies the program to /tmp, makes it executable and runs it as root (sudo asks for the administrator password).
# Needs a sign-in at the Mac screen (the certificate approval is not possible over ssh).
D=$(cd "$(dirname "$0")" && pwd)
if [ "$(uname -m)" = "arm64" ]; then B="$D/cs-ldapclient-osx-arm64"; else B="$D/cs-ldapclient-osx"; fi
[ -f "$B" ] || { echo "$B not found"; exit 1; }
T=$(mktemp /tmp/cs-ldapclient.XXXXXX) || exit 1
cp "$B" "$T" && chmod +x "$T" || exit 1
sudo "$T" -cfg "$D/ldap.cfg" "$@"
R=$?
rm -f "$T"
exit $R
