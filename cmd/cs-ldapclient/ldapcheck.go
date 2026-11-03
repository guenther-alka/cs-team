package main

// Minimal LDAPv3 simple bind over TLS (no library): used to test the server, the reader account and a test user.

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

func berLen(n int) []byte {
	if n < 128 {
		return []byte{byte(n)}
	}
	if n < 256 {
		return []byte{0x81, byte(n)}
	}
	return []byte{0x82, byte(n >> 8), byte(n)}
}

func tlv(tag byte, content []byte) []byte {
	out := append([]byte{tag}, berLen(len(content))...)
	return append(out, content...)
}

// bindRequest builds the LDAPMessage of a simple bind (message id 1).
func bindRequest(dn, pw string) []byte {
	req := tlv(0x02, []byte{3})                 // version 3
	req = append(req, tlv(0x04, []byte(dn))...) // name
	req = append(req, tlv(0x80, []byte(pw))...) // simple auth
	msg := tlv(0x02, []byte{1})
	msg = append(msg, tlv(0x60, req)...)
	return tlv(0x30, msg)
}

func readTLV(r io.Reader) (tag byte, val []byte, err error) {
	h := make([]byte, 2)
	if _, err = io.ReadFull(r, h); err != nil {
		return
	}
	tag = h[0]
	n := int(h[1])
	if n&0x80 != 0 {
		k := n & 0x7f
		if k == 0 || k > 3 {
			return 0, nil, errors.New("bad BER length")
		}
		lb := make([]byte, k)
		if _, err = io.ReadFull(r, lb); err != nil {
			return
		}
		n = 0
		for _, b := range lb {
			n = n<<8 | int(b)
		}
	}
	if n > 1<<20 {
		return 0, nil, errors.New("BER too long")
	}
	val = make([]byte, n)
	_, err = io.ReadFull(r, val)
	return
}

// bindResult returns the LDAP resultCode of a BindResponse inside an LDAPMessage value.
func bindResult(msg []byte) (int, error) {
	// msg = INTEGER id, [APPLICATION 1] { ENUMERATED resultCode, ... }
	if len(msg) < 4 || msg[0] != 0x02 {
		return 0, errors.New("bad LDAP response")
	}
	i := 2 + int(msg[1])
	if i >= len(msg) || msg[i] != 0x61 {
		return 0, errors.New("no BindResponse")
	}
	i++
	if msg[i]&0x80 != 0 {
		i += 1 + int(msg[i]&0x7f)
	} else {
		i++
	}
	if i+2 >= len(msg) || msg[i] != 0x0a {
		return 0, errors.New("no resultCode")
	}
	return int(msg[i+2]), nil
}

// ldapBind does a simple bind over LDAPS and returns the LDAP resultCode (0 = success, 49 = wrong password).
// pool nil = do not verify (only for fetching the certificate).
func ldapBind(host string, port int, pool *x509.CertPool, dn, pw string) (int, error) {
	cfg := &tls.Config{RootCAs: pool, ServerName: host}
	if pool == nil {
		cfg.InsecureSkipVerify = true
	}
	d := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), cfg)
	if err != nil {
		return -1, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err = conn.Write(bindRequest(dn, pw)); err != nil {
		return -1, err
	}
	tag, val, err := readTLV(conn)
	if err != nil {
		return -1, err
	}
	if tag != 0x30 {
		return -1, errors.New("not an LDAP message")
	}
	return bindResult(val)
}

// fetchCert connects without verification and returns the server certificate (PEM) and its SHA-256 fingerprint.
func fetchCert(host string, port int) (pemBytes []byte, fp string, err error) {
	d := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(host, strconv.Itoa(port)), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return nil, "", err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, "", errors.New("the server sent no certificate")
	}
	c := certs[0]
	sum := sha256.Sum256(c.Raw)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}), hex.EncodeToString(sum[:]), nil
}

func fingerprintOfPEM(p []byte) (string, error) {
	b, _ := pem.Decode(p)
	if b == nil {
		return "", errors.New("no PEM certificate")
	}
	sum := sha256.Sum256(b.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

func poolFromPEM(p []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(p) {
		return nil, errors.New("no PEM certificate")
	}
	return pool, nil
}

// prettyFP formats a hex fingerprint as AA:BB:CC...
func prettyFP(h string) string {
	out := make([]byte, 0, len(h)*3/2)
	for i := 0; i+1 < len(h); i += 2 {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, h[i], h[i+1])
	}
	return string(out)
}

func bindMessage(code int, err error) string {
	switch {
	case err != nil:
		return err.Error()
	case code == 0:
		return "ok"
	case code == 49:
		return "wrong user or password (49)"
	default:
		return fmt.Sprintf("LDAP result %d", code)
	}
}
