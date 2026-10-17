package auth

// Passwort vergessen (0.62): Anmeldeseite -> E-Mail mit Link -> Link oeffnen -> neues Passwort ODER Anbieter (OIDC) -> Protokolleintrag.
//
//  1. POST /api/pwforgot {name}: Anmeldename oder Mailadresse. Die Antwort ist immer dieselbe (keine Auskunft, ob es das Konto gibt);
//     die Mail geht im Hintergrund hinaus. Je IP hoechstens resetMax Anfragen, je Konto eine Mail pro resetGap.
//  2. Die Mail kommt nur in der eingestellten Sprache des Kontos (Vorgabe en) und enthaelt BASE/#reset=name~token. Der Token steht nur als Abdruck im Konto (Reset/ResetExp), gilt eine Stunde, einmal.
//     Er steht hinter # und wird erst per POST eingeloest: Mail-Scanner, Proxys und Referer sehen ihn nicht bzw. verbrauchen ihn nicht.
//  3. POST /api/pwreset {name, token}: das Konto bekommt ein unbekanntes Zufallspasswort mit Aenderungspflicht (wie beim Erstlogin),
//     die Sitzung wird eroeffnet - die Oberflaeche zeigt "Passwort waehlen oder mit Anbieter verknuepfen". Alte Sitzungen enden.
// Ausgenommen: Admins, Sysadmin, Gaeste, Verzeichniskonten (eligible), Konten ohne Mailadresse und Konten mit Zwei-Faktor-Anmeldung
// (sonst umginge die Mail den zweiten Faktor: dort setzt ein Admin neu).

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// PublicURL: oeffentliche Adresse fuer den Link (von main.go gesetzt; sonst CS_OIDC_BASE).
var PublicURL func() string

const (
	resetTTL = time.Hour
	resetGap = 5 * time.Minute
	resetMax = 10 // Anfragen je IP, danach gesperrt (wie Fehlanmeldungen)
)

var errResetSkip = errors.New("no reset")

func resetBase() string {
	if PublicURL != nil {
		if b := strings.TrimRight(strings.TrimSpace(PublicURL()), "/"); b != "" {
			return b
		}
	}
	return oidcBase()
}

// resetEnabled: Formular aktiv, Mailversand eingerichtet und eine Adresse fuer den Link bekannt.
func resetEnabled() bool {
	return LoginForm && SendMail != nil && MailOK != nil && MailOK() && resetBase() != ""
}

func resetOK(u Account) bool {
	return eligible(u) && !u.Disabled && u.Mail != "" && u.TOTP == ""
}

func resetSum(tok string) string {
	h := sha256.Sum256([]byte("cs-reset\x00" + tok))
	return hex.EncodeToString(h[:])
}

func resetValid(u Account, tok string) bool {
	if u.Reset == "" || tok == "" || len(tok) > 100 {
		return false
	}
	exp, err := time.Parse(time.RFC3339, u.ResetExp)
	return err == nil && time.Now().Before(exp) && subtle.ConstantTimeCompare([]byte(u.Reset), []byte(resetSum(tok))) == 1
}

// findForgot: Konto zum Namen oder zur Mailadresse (bei mehreren Konten mit derselben Adresse: keins).
func (a *Auth) findForgot(ctx context.Context, q string) (string, Account, bool) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" || len(q) > 200 {
		return "", Account{}, false
	}
	a.refresh(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	if u, ok := a.users[q]; ok {
		return q, u, true
	}
	hit := ""
	for n, u := range a.users {
		if u.Mail != "" && strings.EqualFold(u.Mail, q) {
			if hit != "" {
				return "", Account{}, false
			}
			hit = n
		}
	}
	if hit == "" {
		return "", Account{}, false
	}
	return hit, a.users[hit], true
}

// resetTexts: Betreff, Titel, "Konto", "Link"-Zeile, Erklaerung, Hinweis zum Ignorieren - je Sprache der Oberflaeche (Konto.Lang, sonst en).
var resetTexts = map[string][6]string{
	"de": {"cs-team: Link zum Zurücksetzen des Passworts", "Passwort zurücksetzen bei cs-team", "Konto", "Link (gilt eine Stunde, nur einmal):", "Mit dem Link vergeben Sie ein neues Passwort oder verknüpfen Ihr Konto mit einem Anbieter.", "Falls Sie den Passwort-Reset-Link nicht angefordert haben, können Sie diese Mail ignorieren: Ihr Passwort bleibt unverändert."},
	"en": {"cs-team: password reset link", "Password reset for cs-team", "Account", "Link (valid for one hour, single use):", "The link lets you choose a new password or link your account to a provider.", "If you did not request the password reset link, you can ignore this email: your password stays as it is."},
	"es": {"cs-team: enlace para restablecer la contraseña", "Restablecer la contraseña en cs-team", "Cuenta", "Enlace (válido durante una hora, un solo uso):", "Con el enlace elige una nueva contraseña o vincula su cuenta a un proveedor.", "Si no ha solicitado el enlace de restablecimiento, puede ignorar este correo: su contraseña no cambia."},
	"fr": {"cs-team : lien de réinitialisation du mot de passe", "Réinitialisation du mot de passe pour cs-team", "Compte", "Lien (valable une heure, usage unique) :", "Avec ce lien, vous choisissez un nouveau mot de passe ou liez votre compte à un fournisseur.", "Si vous n'avez pas demandé ce lien de réinitialisation, vous pouvez ignorer cet e-mail : votre mot de passe reste inchangé."},
	"it": {"cs-team: link per reimpostare la password", "Reimpostazione della password per cs-team", "Account", "Link (valido un'ora, monouso):", "Con il link scegli una nuova password o colleghi il tuo account a un provider.", "Se non hai richiesto il link di reimpostazione, puoi ignorare questa e-mail: la tua password resta invariata."},
	"pl": {"cs-team: link do zresetowania hasła", "Resetowanie hasła w cs-team", "Konto", "Link (ważny godzinę, jednorazowy):", "Za pomocą linku wybierzesz nowe hasło lub połączysz konto z dostawcą.", "Jeśli nie prosiłeś(-aś) o link do resetu hasła, zignoruj tę wiadomość: Twoje hasło pozostaje bez zmian."},
	"ru": {"cs-team: ссылка для сброса пароля", "Сброс пароля в cs-team", "Учётная запись", "Ссылка (действует один час, одноразовая):", "По ссылке вы выберете новый пароль или привяжете учётную запись к провайдеру.", "Если вы не запрашивали ссылку для сброса пароля, просто проигнорируйте это письмо: ваш пароль останется прежним."},
	"tr": {"cs-team: parola sıfırlama bağlantısı", "cs-team için parola sıfırlama", "Hesap", "Bağlantı (bir saat geçerli, tek kullanımlık):", "Bağlantı ile yeni bir parola seçer veya hesabınızı bir sağlayıcıya bağlarsınız.", "Parola sıfırlama bağlantısını siz istemediyseniz bu e-postayı yok sayabilirsiniz: parolanız değişmez."},
	"uk": {"cs-team: посилання для скидання пароля", "Скидання пароля в cs-team", "Обліковий запис", "Посилання (дійсне одну годину, одноразове):", "За посиланням ви оберете новий пароль або прив'яжете обліковий запис до провайдера.", "Якщо ви не запитували посилання для скидання пароля, просто проігноруйте цей лист: ваш пароль залишиться без змін."},
	"ja": {"cs-team: パスワード再設定リンク", "cs-team のパスワード再設定", "アカウント", "リンク（有効期間1時間、1回のみ）：", "このリンクから、新しいパスワードを設定するか、アカウントをプロバイダーに連携できます。", "パスワード再設定リンクをご請求でない場合は、このメールを無視してください。パスワードは変更されません。"},
	"cn": {"cs-team：重置密码链接", "cs-team 密码重置", "账户", "链接（有效期一小时，仅限一次）：", "通过该链接，您可以设置新密码，或将账户关联到身份提供商。", "如果您没有申请重置密码链接，请忽略此邮件：您的密码保持不变。"},
	"el": {"cs-team: σύνδεσμος επαναφοράς κωδικού", "Επαναφορά κωδικού στο cs-team", "Λογαριασμός", "Σύνδεσμος (ισχύει για μία ώρα, μία χρήση):", "Με τον σύνδεσμο επιλέγετε νέο κωδικό ή συνδέετε τον λογαριασμό σας με πάροχο.", "Αν δεν ζητήσατε τον σύνδεσμο επαναφοράς κωδικού, αγνοήστε αυτό το μήνυμα: ο κωδικός σας παραμένει ως έχει."},
	"ar": {"cs-team: رابط إعادة تعيين كلمة المرور", "إعادة تعيين كلمة المرور في cs-team", "الحساب", "الرابط (صالح لمدة ساعة، للاستخدام مرة واحدة):", "من خلال الرابط تختار كلمة مرور جديدة أو تربط حسابك بمزوّد.", "إذا لم تطلب رابط إعادة التعيين، فتجاهل هذه الرسالة: ستبقى كلمة مرورك كما هي."},
}

// resetMail: Mail nur in der eingestellten Sprache des Kontos (Vorgabe en).
func resetMail(lang, name, link string) (string, string) {
	t, ok := resetTexts[strings.ToLower(strings.TrimSpace(lang))]
	if !ok {
		t = resetTexts["en"]
	}
	return t[0], t[1] + "\n\n" + t[2] + ": " + name + "\n" + t[3] + "\n" + link + "\n\n" + t[4] + "\n" + t[5] + "\n"
}

// issueReset: Token anlegen und per Mail schicken (laeuft im Hintergrund).
func (a *Auth) issueReset(name, ip string) {
	ctx := context.Background()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	var mail, lang string
	err := a.mutate(ctx, func(m map[string]Account) error {
		u, ok := m[name]
		if !ok || !resetOK(u) {
			return errResetSkip
		}
		if exp, e := time.Parse(time.RFC3339, u.ResetExp); e == nil && time.Now().Before(exp.Add(-resetTTL).Add(resetGap)) {
			return errResetSkip // gerade erst eine Mail geschickt
		}
		u.Reset, u.ResetExp = resetSum(tok), time.Now().Add(resetTTL).UTC().Format(time.RFC3339)
		mail, lang = u.Mail, u.Lang
		m[name] = u
		return nil
	})
	if err != nil {
		return
	}
	subj, text := resetMail(lang, name, resetBase()+"/#reset="+name+"~"+tok)
	if err := SendMail(mail, subj, text); err != nil {
		log.Printf("auth: password reset mail for %q failed: %v", safeName(name), err)
		return
	}
	log.Printf("auth: password reset link sent user=%q ip=%s", safeName(name), ip)
}

func (a *Auth) resetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/pwforgot", func(w http.ResponseWriter, r *http.Request) {
		if !resetEnabled() {
			http.NotFound(w, r)
			return
		}
		if !sameOriginStrict(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		in := map[string]string{}
		if !body(w, r, &in) {
			return
		}
		ip := a.ip(r)
		key := "pwf|" + ip
		if a.locked(key) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		a.failedMax(key, resetMax)
		if name, u, ok := a.findForgot(r.Context(), in["name"]); ok && resetOK(u) {
			go a.issueReset(name, ip)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/pwreset", func(w http.ResponseWriter, r *http.Request) {
		if !LoginForm {
			http.NotFound(w, r)
			return
		}
		if !sameOriginStrict(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		in := map[string]string{}
		if !body(w, r, &in) {
			return
		}
		ip := a.ip(r)
		key := "pwu|" + ip
		if a.locked(key) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		name, tok := strings.ToLower(strings.TrimSpace(in["name"])), strings.TrimSpace(in["token"])
		var nu Account
		err := a.mutate(r.Context(), func(m map[string]Account) error {
			u, ok := m[name]
			if !ok || !resetOK(u) || !resetValid(u, tok) {
				return errResetSkip
			}
			h, err := hash(genInitPw() + genInitPw()) // unbekanntes Passwort: gewaehlt wird in der Oberflaeche
			if err != nil {
				return err
			}
			u.Hash, u.Must, u.Link = h, true, nil
			u.InitExp = time.Now().Add(resetTTL).UTC().Format(time.RFC3339)
			u.Reset, u.ResetExp = "", ""
			m[name] = u
			nu = u
			return nil
		})
		if err != nil {
			if errors.Is(err, errResetSkip) {
				a.failedMax(key, resetMax)
				log.Printf("auth: password reset link refused user=%q ip=%s", safeName(name), ip)
				http.Error(w, "link invalid or expired", http.StatusForbidden)
			} else {
				fail_(w, err)
			}
			return
		}
		a.dropWeb(name)
		a.webStart(w, r, name, nu, false)
		log.Printf("auth: password reset link used user=%q ip=%s - waiting for new password or provider", safeName(name), ip)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "must": true})
	})
}
