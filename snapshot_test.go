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

func TestWinDataset(t *testing.T) {
	names := []string{"winpool", "winpool/2", "winpool/2/data", "winpool/data", "winpool/data/data", "winpool/test"}
	cases := []struct{ path, label, want string }{
		{`D:\data`, "winpool", "winpool/data"},
		{`D:\data\.csteam\x`, "winpool", "winpool/data"},
		{`d:\DATA\data\sub`, "winpool", "winpool/data/data"},
		{`D:\2\data`, "WinPool", "winpool/2/data"},
		{`D:\nirgends\x`, "winpool", "winpool"},
		{`D:\`, "winpool", "winpool"},
		{`E:\data`, "", "winpool/data"}, // genau ein Pool
		{`/unix/x`, "winpool", ""},
	}
	for _, c := range cases {
		if got := winDataset(c.path, c.label, names); got != c.want {
			t.Errorf("%s (%s): %q, want %q", c.path, c.label, got, c.want)
		}
	}
	if got := winDataset(`D:\data`, "x", append(names, "other")); got != "" {
		t.Errorf("mehrere Pools, Label passt nicht: %q", got)
	}
}
