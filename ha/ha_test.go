package ha

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cs-team/store"
)

func TestParseMode(t *testing.T) {
	for in, want := range map[string]string{"": "", "master": "master", "STANDBY": "standby", "takeover": "master"} {
		m, _, err := ParseMode(in)
		if err != nil || m != want {
			t.Fatalf("%q: %q %v", in, m, err)
		}
	}
	if _, take, _ := ParseMode("takeover"); !take {
		t.Fatal("takeover flag")
	}
	if _, _, err := ParseMode("x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadOnlyStore(t *testing.T) {
	ctx := context.Background()
	m := store.NewMem()
	m.Put(ctx, "a", []byte("1"), "")
	ro := store.ReadOnly(m)
	if b, _, err := ro.Get(ctx, "a"); err != nil || string(b) != "1" {
		t.Fatal("read", err)
	}
	if _, err := ro.Put(ctx, "a", []byte("2"), ""); !errors.Is(err, store.ErrReadOnly) {
		t.Fatal("put", err)
	}
	if err := ro.Delete(ctx, "a"); !errors.Is(err, store.ErrReadOnly) {
		t.Fatal("delete", err)
	}
}

func TestMasterEpochAndTakeover(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	a := New(st, "master", "A", false, false)
	if err := a.Start(ctx, false); err != nil || a.Status().Epoch != 1 {
		t.Fatal("first start", err, a.Status())
	}
	// Neustart desselben Hosts: Epoche bleibt
	a2 := New(st, "master", "A", false, false)
	if err := a2.Start(ctx, false); err != nil || a2.Status().Epoch != 1 {
		t.Fatal("restart", err, a2.Status())
	}
	// anderer Host ohne takeover: abgelehnt
	b := New(st, "master", "B", false, false)
	if err := b.Start(ctx, false); err == nil {
		t.Fatal("B must not start without takeover")
	}
	// mit takeover: Epoche 2
	b = New(st, "master", "B", false, false)
	if err := b.Start(ctx, true); err != nil || b.Status().Epoch != 2 {
		t.Fatal("takeover", err, b.Status())
	}
}

func TestOtherMasterBlocksStart(t *testing.T) {
	ctx := context.Background()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"B","mode":"master","epoch":1}`))
	}))
	defer other.Close()
	st := store.NewMem()
	a := New(st, "master", "A", false, false)
	a.Mode = "master"
	if err := a.Start(ctx, false); err != nil { // Gruppe noch leer
		t.Fatal(err)
	}
	if err := a.SaveGroup(ctx, Group{Members: []Member{{Name: "A", Addr: "x:1"}, {Name: "B", Addr: other.URL}}}); err != nil {
		t.Fatal(err)
	}
	a = New(st, "master", "A", false, false)
	if err := a.Start(ctx, false); err == nil || !strings.Contains(err.Error(), "bereits Master") {
		t.Fatal("expected running-master refusal, got", err)
	}
	a = New(st, "master", "A", true, false) // CS_HA_FORCE
	if err := a.Start(ctx, false); err != nil {
		t.Fatal("force", err)
	}
}

func TestTakeoverNeedsOffOrAssume(t *testing.T) {
	ctx := context.Background()
	st := store.NewMem()
	a := New(st, "master", "A", false, false)
	a.Start(ctx, false)
	a.SaveGroup(ctx, Group{Members: []Member{{Name: "A", Addr: "127.0.0.1:1"}, {Name: "B", Addr: "127.0.0.1:1"}}})
	b := New(st, "master", "B", false, false)
	if err := b.Start(ctx, true); err == nil {
		t.Fatal("unreachable A without ASSUME_OFF must refuse")
	}
	b = New(st, "master", "B", false, true)
	if err := b.Start(ctx, true); err != nil || b.Status().Epoch != 2 {
		t.Fatal(err, b.Status())
	}
}

func TestGuardStandby(t *testing.T) {
	s := New(store.ReadOnly(store.NewMem()), "standby", "S", false, false)
	h := s.Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	for m, want := range map[string]int{"GET": 200, "PROPFIND": 200, "POST": 503, "PUT": 503, "DELETE": 503} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/x", nil))
		if rec.Code != want {
			t.Fatalf("%s: %d", m, rec.Code)
		}
	}
	if err := s.SaveGroup(context.Background(), Group{}); !errors.Is(err, ErrNotMaster) {
		t.Fatal("standby must not save group")
	}
}
