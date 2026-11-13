package main

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

// standalone.set neben dem Programm: keine napp-it-Uebernahme, team.cfg und webcraft neben dem Programm.
func TestStandaloneSet(t *testing.T) {
	f := filepath.Join(exeDir(), "standalone.set")
	if standalone() {
		t.Fatalf("%s exists before the test", f)
	}
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f)
	if !standalone() || napp() {
		t.Fatal("standalone.set not detected")
	}
	if cfgPath() != exeCfg() {
		t.Fatalf("cfgPath %s, want %s", cfgPath(), exeCfg())
	}
	os.Unsetenv("CS_WEBCRAFT")
	w := filepath.Join(exeDir(), "webcraft")
	if err := os.Mkdir(w, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(w)
	if got := webcraftDir(); got != w {
		t.Fatalf("webcraftDir %s, want %s", got, w)
	}
}

// relPaths: ./ ab dem Basisordner, absolute Pfade und off bleiben.
func TestRelPaths(t *testing.T) {
	base := t.TempDir()
	abs := filepath.Join(base, "abs", "data")
	m := map[string]string{"CS_DIR": "./", "CS_TLS_CERT": "./pem/cert.pem", "CS_WEBCRAFT": "off", "CS_SHARE": abs, "CS_LISTEN": ":9004"}
	relPaths(m, base)
	if m["CS_DIR"] != base || m["CS_TLS_CERT"] != filepath.Join(base, "pem", "cert.pem") {
		t.Fatalf("relative: %v", m)
	}
	if m["CS_WEBCRAFT"] != "off" || m["CS_SHARE"] != abs || m["CS_LISTEN"] != ":9004" {
		t.Fatalf("unchanged: %v", m)
	}
}

// genCert: legt cert.pem/key.pem an, die zusammen passen; vorhandene bleiben unveraendert.
func TestGenCert(t *testing.T) {
	d := filepath.Join(t.TempDir(), "pem")
	cert, key := filepath.Join(d, "cert.pem"), filepath.Join(d, "key.pem")
	made, err := genCert(cert, key)
	if err != nil || !made {
		t.Fatalf("made=%v err=%v", made, err)
	}
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		t.Fatal(err)
	}
	b1, _ := os.ReadFile(cert)
	if made, err = genCert(cert, key); err != nil || made {
		t.Fatalf("second call: made=%v err=%v", made, err)
	}
	if b2, _ := os.ReadFile(cert); string(b1) != string(b2) {
		t.Fatal("existing certificate was replaced")
	}
}
