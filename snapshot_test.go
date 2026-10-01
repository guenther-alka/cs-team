package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestZFSDataset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	d := t.TempDir()
	fake := filepath.Join(d, "zfs")
	os.WriteFile(fake, []byte(`#!/bin/sh
case "$*" in
"list -H -o name /data/cs/sub") exit 1;;
"list -H -o name,mountpoint -t filesystem") printf 'pool\t/\npool/data\t/data\npool/data/cs\t/data/cs\npool/other\t/other\npool/x\tnone\n';;
"list -H -o name /direct") echo pool/direct;;
*) exit 1;;
esac
`), 0o755)
	if got := zfsDataset(fake, "/data/cs/sub"); got != "pool/data/cs" {
		t.Fatalf("Mountpoint-Suche: %q", got)
	}
	if got := zfsDataset(fake, "/direct"); got != "pool/direct" {
		t.Fatalf("direkt: %q", got)
	}
	if got := zfsDataset(fake, "/nirgends/x"); got != "pool" {
		t.Fatalf("Wurzel: %q", got)
	}
}
