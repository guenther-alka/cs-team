package main

// ldap.cfg: plain "key = value" lines, lives next to the program (e.g. on a USB stick).
// Lines starting with # are comments. Unknown keys are kept out; missing keys use the defaults.

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Server         string // IP or name of the cs-team LDAP server
	Port           int    // LDAPS port (CS_LDAP), default 636
	Base           string // base DN, default dc=csteam,dc=local
	Reader         string // reader account (CS_LDAP_READER): uid or full DN
	ReaderPassword string // password of the reader account
	Groups         string // "klasse5a=Users;lehrer=Administrators" (cs-team group = local group)
	RemoveProfiles bool   // delete the local account and profile at logout
	ShortSetup     bool   // switch off the privacy questions / first-logon animation / suggestions
	HideUsers      bool   // logon screen: only "other user" and pGina
	CertFile       string // PEM file of the server certificate (next to the program)
	CertSHA256     string // fingerprint the certificate was accepted with
	Installer      string // pGina installer (file name next to the program), optional
	DownloadURL    string // where to download the installer if there is none
	Lang           string // de | en (empty = from Windows)
}

func defaultConfig() Config {
	return Config{
		Port:           636,
		Base:           "dc=csteam,dc=local",
		Groups:         "",
		RemoveProfiles: true,
		ShortSetup:     true,
		HideUsers:      true,
		CertFile:       "ldap.pem",
		DownloadURL:    "https://github.com/MutonUfoAI/pgina/releases/download/3.9.9.12/pGinaSetup-3.9.9.12.exe",
	}
}

func yes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "y", "yes", "j", "ja", "true", "on":
		return true
	}
	return false
}

func yn(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func parseConfig(text string) Config {
	c := defaultConfig()
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, ";") {
			continue
		}
		i := strings.IndexByte(l, '=')
		if i < 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(l[:i]))
		v := strings.TrimSpace(l[i+1:])
		c.set(k, v)
	}
	return c
}

// set sets one key; it reports false for an unknown key or a bad value.
func (c *Config) set(k, v string) bool {
	switch k {
	case "server":
		c.Server = v
	case "port":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
		c.Port = n
	case "base":
		c.Base = v
	case "reader":
		c.Reader = v
	case "reader_password":
		c.ReaderPassword = v
	case "groups":
		c.Groups = v
	case "remove_profiles":
		c.RemoveProfiles = yes(v)
	case "short_setup":
		c.ShortSetup = yes(v)
	case "hide_users":
		c.HideUsers = yes(v)
	case "cert_file":
		c.CertFile = v
	case "cert_sha256":
		c.CertSHA256 = strings.ToLower(strings.ReplaceAll(v, ":", ""))
	case "installer":
		c.Installer = v
	case "download_url":
		c.DownloadURL = v
	case "lang":
		c.Lang = v
	default:
		return false
	}
	return true
}

func (c Config) String() string {
	var b strings.Builder
	w := func(comment, k, v string) {
		if comment != "" {
			b.WriteString("# " + comment + "\n")
		}
		b.WriteString(k + " = " + v + "\n")
	}
	b.WriteString("# cs-ldapclient: settings (ldap.cfg). Keep this file next to cs-ldapclient.exe, e.g. on a USB stick.\n")
	b.WriteString("# It contains the password of the reader account: protect the stick.\n\n")
	w("IP or name of the cs-team LDAP server (LDAPS)", "server", c.Server)
	w("", "port", strconv.Itoa(c.Port))
	w("base DN of the cs-team directory", "base", c.Base)
	w("reader account (CS_LDAP_READER): uid or full DN; pGina needs it to read the groups", "reader", c.Reader)
	w("", "reader_password", c.ReaderPassword)
	w("cs-team group = local Windows group, separated by ; (Users, Administrators, or any local group)", "groups", c.Groups)
	w("delete the local account and profile at logout (yes/no)", "remove_profiles", yn(c.RemoveProfiles))
	w("short account creation: no privacy questions, no first-logon animation, no suggestions (yes/no)", "short_setup", yn(c.ShortSetup))
	w("logon screen: only \"other user\" and pGina (yes/no)", "hide_users", yn(c.HideUsers))
	w("certificate of the cs-team server (PEM, next to this file) and the fingerprint it was accepted with", "cert_file", c.CertFile)
	w("", "cert_sha256", c.CertSHA256)
	w("pGina installer next to this file (empty = pGinaSetup*.exe, else download)", "installer", c.Installer)
	w("", "download_url", c.DownloadURL)
	w("language of the program: de, en (empty = from Windows)", "lang", c.Lang)
	return b.String()
}

// Problems lists what is missing for an installation.
func (c Config) Problems() []string {
	var p []string
	if c.Server == "" {
		p = append(p, "server")
	}
	if c.Base == "" {
		p = append(p, "base")
	}
	if c.Reader == "" {
		p = append(p, "reader")
	}
	if c.ReaderPassword == "" {
		p = append(p, "reader_password")
	}
	return p
}

// ReaderDN returns the full DN of the reader account.
func (c Config) ReaderDN() string {
	if strings.Contains(c.Reader, "=") {
		return c.Reader
	}
	return "uid=" + c.Reader + ",ou=people," + c.Base
}

// GroupRule is one mapping cs-team group -> local group.
type GroupRule struct{ Group, Local string }

func (c Config) GroupRules() ([]GroupRule, error) {
	var out []GroupRule
	for _, part := range strings.Split(c.Groups, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.IndexByte(part, '=')
		if i <= 0 || i == len(part)-1 {
			return nil, fmt.Errorf("group rule %q: expected cs-team-group=local-group", part)
		}
		out = append(out, GroupRule{strings.TrimSpace(part[:i]), strings.TrimSpace(part[i+1:])})
	}
	return out, nil
}

func loadConfig(path string) (Config, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return defaultConfig(), false
	}
	return parseConfig(strings.TrimPrefix(string(b), "\ufeff")), true
}

func saveConfig(path string, c Config) error {
	return os.WriteFile(path, []byte(strings.ReplaceAll(c.String(), "\n", "\r\n")), 0o600)
}
