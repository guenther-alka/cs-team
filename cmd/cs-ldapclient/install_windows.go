//go:build windows

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed apply.ps1
var applyScript string

const pginaDir = `C:\Program Files\pGina.fork`
const pginaCP = `{d0befefb-3d2c-44da-bbad-3b2d04557246}`

func pginaInstalled() bool {
	_, err := os.Stat(filepath.Join(pginaDir, "pGina.Configuration.exe"))
	return err == nil
}

func cpRegistered() bool {
	_, err := runCmd("reg", "query", `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\Credential Providers\`+pginaCP)
	return err == nil
}

// ---- policies (set by us, removed by uninstall) ----

type policy struct {
	key, name string
	val       string
	on        func(Config) bool
}

var policies = []policy{
	{`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, "dontdisplaylastusername", "1", func(c Config) bool { return c.HideUsers }},
	{`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, "DontDisplayLockedUserId", "3", func(c Config) bool { return c.HideUsers }},
	{`HKLM\SOFTWARE\Policies\Microsoft\Windows\OOBE`, "DisablePrivacyExperience", "1", func(c Config) bool { return c.ShortSetup }},
	{`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`, "EnableFirstLogonAnimation", "0", func(c Config) bool { return c.ShortSetup }},
	{`HKLM\SOFTWARE\Policies\Microsoft\Windows\CloudContent`, "DisableWindowsConsumerFeatures", "1", func(c Config) bool { return c.ShortSetup }},
}

func applyPolicies(c Config) {
	for _, p := range policies {
		if p.on(c) {
			if out, err := runCmd("reg", "add", p.key, "/v", p.name, "/t", "REG_DWORD", "/d", p.val, "/f"); err != nil {
				fmt.Println("  reg add", p.name, ":", out)
			}
		} else {
			runCmd("reg", "delete", p.key, "/v", p.name, "/f") // switched off in ldap.cfg: take it back
		}
	}
}

func removePolicies() {
	for _, p := range policies {
		runCmd("reg", "delete", p.key, "/v", p.name, "/f")
	}
}

// ---- installer ----

func findInstaller(c Config) (string, error) {
	dir := exeDir()
	if c.Installer != "" {
		p := c.Installer
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "pGinaSetup*.exe")); len(m) > 0 {
		return m[len(m)-1], nil
	}
	if c.DownloadURL == "" {
		return "", fmt.Errorf("no pGina installer found next to the program and no download_url")
	}
	fmt.Println(tr("Lade pGina: ", "Downloading pGina: ") + c.DownloadURL)
	cl := &http.Client{Timeout: 5 * time.Minute}
	resp, err := cl.Get(c.DownloadURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	// save next to the program (USB stick: found there next time), else in temp
	p := filepath.Join(dir, filepath.Base(c.DownloadURL))
	f, err := os.Create(p)
	if err != nil {
		p = filepath.Join(os.TempDir(), filepath.Base(c.DownloadURL))
		if f, err = os.Create(p); err != nil {
			return "", err
		}
	}
	_, err = io.Copy(f, resp.Body)
	f.Close()
	return p, err
}

// ---- install ----

func doInstall(o options, c *Config, cfgPath string) bool {
	if !isAdmin() {
		fmt.Println(tr("Bitte als Administrator starten (Rechtsklick > Als Administrator ausführen).", "Please start as administrator (right click > Run as administrator)."))
		return false
	}
	if p := c.Problems(); len(p) > 0 {
		if o.yes {
			fmt.Println(tr("In der Konfiguration fehlt: ", "Missing in the settings: ") + strings.Join(p, ", "))
			return false
		}
		wizard(c)
		if err := saveConfig(cfgPath, *c); err != nil {
			fmt.Println(err)
		}
	}
	rules, err := c.GroupRules()
	if err != nil {
		fmt.Println(err)
		return false
	}
	certPath := filepath.Join(filepath.Dir(cfgPath), c.CertFile)
	pemb, ok := ensureCert(c, o, certPath)
	if !ok {
		return false
	}
	saveConfig(cfgPath, *c)
	if !checkServer(*c, pemb) {
		if o.yes || !askYN(tr("Der Server antwortet nicht richtig. Trotzdem installieren", "The server does not answer correctly. Install anyway"), false) {
			return false
		}
	} else if !o.yes {
		askTestUser(*c, pemb)
	}
	if !o.yes {
		fmt.Println(tr("\nVorher sicherstellen: Es gibt ein lokales Administratorkonto MIT Passwort. Das ist der Rückweg,\nwenn der Server nicht erreichbar ist (Anmeldebildschirm > \"Anderer Benutzer\").", "\nMake sure first: there is a local administrator account WITH a password. It is the way back if the\nserver is not reachable (sign-in screen > \"Other user\")."))
		out, _ := runCmd("net", "localgroup", "Administrators")
		if out == "" {
			out, _ = runCmd("net", "localgroup", "Administratoren")
		}
		fmt.Println(out)
		if !askYN(tr("Fortfahren", "Continue"), false) {
			return false
		}
	}
	// pGina counts as installed only with its sign-in tile registered (a half-removed pGina is installed again)
	fresh := !pginaInstalled() || !cpRegistered()
	if fresh {
		inst, err := findInstaller(*c)
		if err != nil {
			fmt.Println(err)
			return false
		}
		fmt.Println(tr("Installiere pGina ...", "Installing pGina ..."))
		if out, err := runCmd(inst, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"); err != nil {
			fmt.Println("pGina setup:", err, out)
			return false
		}
		if !pginaInstalled() {
			fmt.Println(tr("pGina wurde nicht installiert.", "pGina was not installed."))
			return false
		}
		if !cpRegistered() {
			// typical right after an uninstall: Windows still holds the old sign-in DLL
			fmt.Println(tr("Die pGina-Anmeldekachel konnte nicht registriert werden (Windows hält noch die alte Datei).\nBitte Windows neu starten und \"Install\" noch einmal ausführen.", "The pGina sign-in tile could not be registered (Windows still holds the old file).\nPlease restart Windows and run \"Install\" again."))
			return false
		}
	}
	// certificate next to pGina (pGina reads the file from there)
	certDst := filepath.Join(pginaDir, "cs-team-cert.pem")
	if err := os.WriteFile(certDst, pemb, 0o644); err != nil {
		fmt.Println(err)
		return false
	}
	type rule struct{ Group, Local string }
	var rs []rule
	for _, r := range rules {
		rs = append(rs, rule{r.Group, r.Local})
	}
	js, _ := json.Marshal(map[string]any{
		"Server": c.Server, "Port": c.Port, "Base": c.Base, "ReaderDN": c.ReaderDN(), "ReaderPassword": c.ReaderPassword,
		"CertPath": certDst, "Rules": rs, "RemoveProfiles": c.RemoveProfiles,
	})
	tmp, _ := os.MkdirTemp("", "cs-ldapclient")
	defer os.RemoveAll(tmp)
	jp, sp := filepath.Join(tmp, "s.json"), filepath.Join(tmp, "apply.ps1")
	os.WriteFile(jp, append([]byte{0xEF, 0xBB, 0xBF}, js...), 0o600)
	os.WriteFile(sp, append([]byte{0xEF, 0xBB, 0xBF}, []byte(strings.TrimPrefix(applyScript, "\ufeff"))...), 0o600)
	fmt.Println(tr("Schreibe die pGina-Einstellungen ...", "Writing the pGina settings ..."))
	out, err := runCmd("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", sp, "-Json", jp)
	fmt.Println(out)
	if err != nil {
		fmt.Println(tr("Fehler beim Schreiben der Einstellungen: ", "Error writing the settings: ") + err.Error())
		return false
	}
	applyPolicies(*c)
	fmt.Println(tr("\nFertig. ", "\nDone. "))
	if fresh {
		fmt.Println(tr("Windows muss einmal neu gestartet werden, damit die pGina-Anmeldekachel erscheint.", "Windows must be restarted once so that the pGina sign-in tile appears."))
		if !o.yes && askYN(tr("Jetzt neu starten", "Restart now"), false) {
			runCmd("shutdown", "/r", "/t", "10", "/c", "cs-ldapclient")
		}
	} else {
		fmt.Println(tr("Die Einstellungen gelten ab der nächsten Anmeldung.", "The settings apply from the next sign-in."))
	}
	return true
}

// ---- uninstall ----

func doUninstall(o options) bool {
	if !isAdmin() {
		fmt.Println(tr("Bitte als Administrator starten.", "Please start as administrator."))
		return false
	}
	if !o.yes && !askYN(tr("pGina und die Richtlinien entfernen (die Anmeldung läuft danach wieder nur über lokale Konten)", "Remove pGina and the policies (sign-in then works with local accounts only)"), false) {
		return false
	}
	runCmd("net", "stop", "pgina")
	// the live uninstaller is the highest uninsNNN.exe that has its .dat (older ones stay after a re-install)
	un := ""
	if m, _ := filepath.Glob(filepath.Join(pginaDir, "unins*.exe")); len(m) > 0 {
		for _, e := range m {
			if _, err := os.Stat(strings.TrimSuffix(e, ".exe") + ".dat"); err == nil && e > un {
				un = e
			}
		}
	}
	if un != "" {
		fmt.Println(tr("Deinstalliere pGina ...", "Uninstalling pGina ..."))
		if out, err := runCmd(un, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"); err != nil {
			fmt.Println(err, out)
		}
		for i := 0; i < 20 && pginaInstalled(); i++ {
			time.Sleep(time.Second)
		}
	}
	runCmd("reg", "delete", `HKLM\SOFTWARE\pGina3`, "/f")
	runCmd("reg", "delete", `HKLM\SOFTWARE\pGina3.fork`, "/f")
	for _, k := range []string{`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\Credential Providers\` + pginaCP,
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\Credential Provider Filters\` + pginaCP} {
		runCmd("reg", "delete", k, "/f")
	}
	removePolicies()
	fmt.Println(tr("Entfernt. Bitte Windows neu starten (auch vor einer erneuten Installation).", "Removed. Please restart Windows (also before installing again)."))
	fmt.Println(tr("Konten, die pGina angelegt hat (Beschreibung \"pGina created\"), bleiben bestehen: ", "Accounts created by pGina (description \"pGina created\") stay: ") +
		tr("mit \"net user\" ansehen und bei Bedarf löschen.", "see them with \"net user\" and delete them if needed."))
	if !o.yes && askYN(tr("Jetzt neu starten", "Restart now"), false) {
		runCmd("shutdown", "/r", "/t", "10", "/c", "cs-ldapclient")
	}
	return true
}

func stateText() string {
	state := tr("nicht installiert", "not installed")
	if pginaInstalled() {
		state = tr("installiert", "installed")
		if out, _ := runCmd("sc", "query", "pgina"); strings.Contains(out, "RUNNING") {
			state += ", " + tr("Dienst läuft", "service running")
		}
	}
	return "pGina: " + state
}
