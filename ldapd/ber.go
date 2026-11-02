package ldapd

import (
	"bufio"
	"errors"
	"io"
)

// Minimales BER fuer LDAP v3 (RFC 4511): nur Tags unter 31, Laengen bis 4 Byte, keine unbestimmte Laenge.

const maxMsg = 1 << 20 // groesste angenommene Nachricht (1 MB)

type tlv struct {
	tag byte
	val []byte
}

var errBER = errors.New("ldap: bad BER")

// parse liest ein TLV vom Anfang von b und gibt den Rest zurueck.
func parse(b []byte) (t tlv, rest []byte, err error) {
	if len(b) < 2 || b[0]&0x1f == 0x1f {
		return t, nil, errBER
	}
	t.tag = b[0]
	l, n := int(b[1]), 2
	if l >= 0x80 {
		k := l & 0x7f
		if k == 0 || k > 4 || len(b) < 2+k {
			return t, nil, errBER
		}
		l = 0
		for _, c := range b[2 : 2+k] {
			l = l<<8 | int(c)
		}
		n = 2 + k
	}
	if l < 0 || l > len(b)-n {
		return t, nil, errBER
	}
	t.val = b[n : n+l]
	return t, b[n+l:], nil
}

// items zerlegt den Inhalt einer SEQUENCE/SET in TLVs.
func items(b []byte) ([]tlv, error) {
	var out []tlv
	for len(b) > 0 {
		t, rest, err := parse(b)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		b = rest
	}
	return out, nil
}

func asInt(b []byte) int {
	n := 0
	if len(b) > 0 && b[0]&0x80 != 0 {
		n = -1
	}
	for _, c := range b {
		n = n<<8 | int(c)
	}
	return n
}

// readMsg liest eine vollstaendige LDAPMessage (SEQUENCE) von der Verbindung.
func readMsg(r *bufio.Reader) ([]byte, error) {
	tag, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if tag != 0x30 {
		return nil, errBER
	}
	lb, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	l := int(lb)
	if lb >= 0x80 {
		k := int(lb & 0x7f)
		if k == 0 || k > 4 {
			return nil, errBER
		}
		l = 0
		for i := 0; i < k; i++ {
			c, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			l = l<<8 | int(c)
		}
	}
	if l < 0 || l > maxMsg {
		return nil, errBER
	}
	buf := make([]byte, l)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func enc(tag byte, v []byte) []byte {
	l := len(v)
	var h []byte
	switch {
	case l < 0x80:
		h = []byte{tag, byte(l)}
	case l < 0x100:
		h = []byte{tag, 0x81, byte(l)}
	case l < 0x10000:
		h = []byte{tag, 0x82, byte(l >> 8), byte(l)}
	default:
		h = []byte{tag, 0x83, byte(l >> 16), byte(l >> 8), byte(l)}
	}
	return append(h, v...)
}

func encInt(tag byte, n int) []byte {
	b := []byte{byte(n)}
	for v := n >> 8; v != 0 && v != -1; v >>= 8 {
		b = append([]byte{byte(v)}, b...)
	}
	if n >= 0 && b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return enc(tag, b)
}

func encStr(tag byte, s string) []byte { return enc(tag, []byte(s)) }

func cat(parts ...[]byte) []byte {
	var o []byte
	for _, p := range parts {
		o = append(o, p...)
	}
	return o
}
