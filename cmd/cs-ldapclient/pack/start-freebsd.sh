#!/bin/sh
# start-freebsd.sh - starts cs-ldapclient from a USB stick (also FAT sticks without execute bit).
# Copies the program to /tmp, makes it executable and runs it as root with ldap.cfg from this folder.
D=$(cd "$(dirname "$0")" && pwd)
B="$D/cs-ldapclient-freebsd"
[ -f "$B" ] || { echo "cs-ldapclient-freebsd not found in $D"; exit 1; }
T=$(mktemp /tmp/cs-ldapclient.XXXXXX) || exit 1
cp "$B" "$T" && chmod +x "$T" || exit 1
if [ "$(id -u)" -eq 0 ]; then "$T" -cfg "$D/ldap.cfg" "$@"
else su root -c "$T -cfg $D/ldap.cfg $*"; fi
R=$?
rm -f "$T"
exit $R
