package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// Hinweis vor der ersten Nutzung von KI-Assistent ("ai") und externem Videochat ("video"): die Oberfläche zeigt, wohin welche
// Daten gehen, und der Benutzer bestätigt. Die Bestätigung steht im Konto (Account.Ack) zusammen mit einem Hash der Adresse;
// ändert der Admin den KI-Anbieter oder den Videoserver, gilt sie nicht mehr und der Hinweis erscheint erneut.

var (
	// AckAddr: wirksame Adresse je Hinweis ("" = nicht eingerichtet); von main gesetzt (KI: Anbieter + Rechner, Video: Rechnernamen).
	AckAddr func(key string) string
	// PrivacyNote: zusätzlicher Text des Admins (Einstellungen), wird an die Hinweise angehängt.
	PrivacyNote func() string
)

var ackKeys = []string{"ai", "video"}

func ackHash(addr string) string {
	h := sha256.Sum256([]byte(addr))
	return hex.EncodeToString(h[:8])
}

func ackAddr(key string) string {
	if AckAddr == nil {
		return ""
	}
	return AckAddr(key)
}

func privacyNote() string {
	if PrivacyNote == nil {
		return ""
	}
	return PrivacyNote()
}

// ackState: je Hinweis, ob der Benutzer ihn für die jetzige Adresse bestätigt hat (nicht eingerichtet = false).
func ackState(u Account) map[string]bool {
	out := map[string]bool{}
	for _, k := range ackKeys {
		addr := ackAddr(k)
		out[k] = addr != "" && u.Ack[k] == ackHash(addr)
	}
	return out
}

// ackAddrs: die Adressen, die der Hinweis nennt (nur eingerichtete).
func ackAddrs() map[string]string {
	out := map[string]string{}
	for _, k := range ackKeys {
		if a := ackAddr(k); a != "" {
			out[k] = a
		}
	}
	return out
}

func (a *Auth) ackRoutes(mux *http.ServeMux) {
	// POST /api/me/ack {key:"ai"|"video", addr:"<die angezeigte Adresse>"}: Hinweis bestätigen. Hat sich die Adresse inzwischen
	// geändert (addr stimmt nicht mehr), wird abgelehnt (409), damit nichts bestätigt wird, was nicht angezeigt war.
	mux.Handle("POST /api/me/ack", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Key, Addr string }
		if !body(w, r, &in) {
			return
		}
		if !contains(ackKeys, in.Key) {
			http.Error(w, "bad key", http.StatusBadRequest)
			return
		}
		cur := ackAddr(in.Key)
		if cur == "" {
			http.Error(w, "not configured", http.StatusBadRequest)
			return
		}
		if in.Addr != cur {
			http.Error(w, "address changed, please read the notice again", http.StatusConflict)
			return
		}
		me := User(r.Context())
		if err := a.mutate(r.Context(), func(m map[string]Account) error {
			u, ok := m[me]
			if !ok {
				return ErrNoUser
			}
			ack := map[string]string{}
			for k, v := range u.Ack {
				ack[k] = v
			}
			ack[in.Key] = ackHash(cur)
			u.Ack = ack
			m[me] = u
			return nil
		}); err != nil {
			fail_(w, err)
		}
	})))
}
