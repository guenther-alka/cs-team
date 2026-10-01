package auth

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cs-team/store"
)

// hangStore: Get der Benutzerdatei hängt, bis der Kontext endet (S3 antwortet nicht).
type hangStore struct {
	store.Store
	hang  atomic.Bool
	calls atomic.Int32
}

func (h *hangStore) Get(ctx context.Context, key string) ([]byte, string, error) {
	if h.hang.Load() && key == usersKey {
		h.calls.Add(1)
		<-ctx.Done()
		return nil, "", ctx.Err()
	}
	return h.Store.Get(ctx, key)
}

// S-11: ein hängender Speicher blockiert weder Anfragen mit gültigem Zwischenspeicher noch solche mit altem Stand.
func TestRefreshHang(t *testing.T) {
	old := loadTimeout
	loadTimeout = 300 * time.Millisecond
	defer func() { loadTimeout = old }()
	hs := &hangStore{Store: store.NewMem()}
	a := New(hs)
	ctx := context.Background()
	if err := a.Bootstrap(ctx, "admin", "adminadmin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.get(ctx, "admin"); !ok {
		t.Fatal("admin fehlt")
	}
	hs.hang.Store(true)

	// gültiger Zwischenspeicher: kein Speicherzugriff
	t0 := time.Now()
	if _, ok := a.get(ctx, "admin"); !ok || time.Since(t0) > 100*time.Millisecond || hs.calls.Load() != 0 {
		t.Fatal("frischer Zwischenspeicher darf nicht laden:", time.Since(t0), hs.calls.Load())
	}

	// abgelaufen + Speicher hängt: ein Lader wartet höchstens loadTimeout, alle anderen benutzen den alten Stand
	a.mu.Lock()
	a.load = time.Now().Add(-time.Minute)
	a.mu.Unlock()
	var wg sync.WaitGroup
	var slow atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := time.Now()
			if _, ok := a.get(ctx, "admin"); !ok {
				t.Error("alter Stand muss gelten")
			}
			if time.Since(s) > 250*time.Millisecond {
				slow.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if n := slow.Load(); n > 1 {
		t.Fatal("mehr als ein Aufrufer wurde blockiert:", n)
	}
	if n := hs.calls.Load(); n != 1 {
		t.Fatal("genau ein Ladeversuch erwartet:", n)
	}
	// nach dem Fehlschlag kurze Pause: keine neue Wartezeit
	t0 = time.Now()
	if _, ok := a.get(ctx, "admin"); !ok || time.Since(t0) > 100*time.Millisecond {
		t.Fatal("nach Fehler kein erneutes Warten:", time.Since(t0))
	}

	// Speicher wieder da: nach der Pause wird neu geladen
	hs.hang.Store(false)
	a.mu.Lock()
	a.retry = time.Time{}
	a.mu.Unlock()
	if err := a.SetPassword(ctx, "admin", "neues-passwort1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.verify(ctx, "admin", "neues-passwort1"); !ok {
		t.Fatal("neues Passwort muss sofort gelten (Änderung macht den Zwischenspeicher ungültig)")
	}
}

// Änderung während eines Ladevorgangs: das alte Ergebnis darf den neuen Stand nicht überschreiben.
func TestRefreshInvalidateRace(t *testing.T) {
	a := New(store.NewMem())
	ctx := context.Background()
	a.Bootstrap(ctx, "admin", "adminadmin")
	var wg sync.WaitGroup
	var ok atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); a.get(ctx, "admin") }()
		go func(i int) {
			defer wg.Done()
			if a.AddUser(ctx, "u"+string(rune('a'+i)), "passwort-123", false, nil) == nil {
				ok.Add(1)
			}
		}(i)
	}
	wg.Wait()
	a.invalidate()
	a.refresh(ctx)
	a.mu.Lock()
	n := len(a.users)
	a.mu.Unlock()
	if n != 1+int(ok.Load()) {
		t.Fatal("alle Benutzer müssen nach dem Neuladen da sein:", n)
	}
}
