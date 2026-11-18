package osacct

import (
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4" //nolint:staticcheck // NT-Hash ist per Definition MD4(UTF-16LE)
)

// NTHash: NT-Hash eines Passworts (MD4 ueber UTF-16LE), gross geschrieben wie in /var/smb/smbpasswd.
func NTHash(pass string) string {
	u := utf16.Encode([]rune(pass))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	h := md4.New()
	h.Write(b)
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}

// SMBHash: NT-Hash so, wie illumos ihn in /var/smb/smbpasswd schreibt: je Byte erst das niedrige, dann das hohe Halbbyte
// (libsmb bintohex), z.B. "1234567." -> NT 04473131... -> Datei 40741313... (am echten OmniOS-Eintrag geprueft).
func SMBHash(pass string) string {
	nt := NTHash(pass)
	b := []byte(nt)
	for i := 0; i+1 < len(b); i += 2 {
		b[i], b[i+1] = b[i+1], b[i]
	}
	return string(b)
}

// smbpasswd (illumos): eine Zeile je Konto, "name:uid:LM-Hash:NT-Hash[:weitere Felder]"; LM bleibt leer.

// SMBSet setzt den NT-Hash eines Kontos in einer smbpasswd-Datei (weitere Felder bleiben erhalten) oder haengt eine Zeile an.
func SMBSet(content, name string, uid int, nt string) string {
	lines, found := smbLines(content), false
	for i, l := range lines {
		f := strings.Split(l, ":")
		if len(f) >= 4 && f[0] == name {
			f[1], f[3] = strconv.Itoa(uid), nt
			lines[i] = strings.Join(f, ":")
			found = true
		}
	}
	if !found {
		lines = append(lines, name+":"+strconv.Itoa(uid)+"::"+nt)
	}
	return strings.Join(lines, "\n") + "\n"
}

// SMBGet liefert die Zeilen der genannten Konten.
func SMBGet(content string, names []string) map[string]string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string]string{}
	for _, l := range smbLines(content) {
		if n, _, ok := strings.Cut(l, ":"); ok && want[n] {
			out[n] = l
		}
	}
	return out
}

// SMBMerge ersetzt bzw. ergaenzt die Zeilen aus add (Name -> Zeile); nur Zeilen, deren uid zu uids passt.
func SMBMerge(content string, add map[string]string, uids map[string]int) (string, []string) {
	lines := smbLines(content)
	var done []string
	for n, l := range add {
		f := strings.Split(l, ":")
		if len(f) < 4 || f[0] != n || f[1] != strconv.Itoa(uids[n]) {
			continue
		}
		found := false
		for i, x := range lines {
			if xn, _, _ := strings.Cut(x, ":"); xn == n {
				lines[i], found = l, true
			}
		}
		if !found {
			lines = append(lines, l)
		}
		done = append(done, n)
	}
	return strings.Join(lines, "\n") + "\n", done
}

func smbLines(content string) []string {
	var out []string
	for _, l := range strings.Split(content, "\n") {
		if l = strings.TrimRight(l, "\r"); strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
