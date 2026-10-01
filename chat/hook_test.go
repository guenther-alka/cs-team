package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHookErrorHidesURL(t *testing.T) { // C-01: Token im Webhook-Pfad darf nie in der Fehlermeldung stehen
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, u := range []string{"http://10.1.2.3/hooks/GEHEIM-TOKEN", "http://127.0.0.1:1/hooks/GEHEIM-TOKEN"} {
		err := hook(ctx, safeClient(false), u, "s", "t")
		if err == nil {
			t.Fatal("privates Ziel haette abgelehnt werden muessen:", u)
		}
		if strings.Contains(err.Error(), "GEHEIM") || strings.Contains(err.Error(), "10.1.2.3") || strings.Contains(err.Error(), "127.0.0.1") {
			t.Fatal("URL im Fehler:", err)
		}
	}
}
