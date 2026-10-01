package files

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cs-team/store"
)

// slowStore verzögert Get und zählt Listen und gleichzeitige Zugriffe.
type slowStore struct {
	store.Store
	delay        time.Duration
	lists        int32
	cur, maxConc int32
}

func (s *slowStore) List(ctx context.Context, p string) ([]store.Info, error) {
	atomic.AddInt32(&s.lists, 1)
	return s.Store.List(ctx, p)
}

func (s *slowStore) Get(ctx context.Context, k string) ([]byte, string, error) {
	n := atomic.AddInt32(&s.cur, 1)
	for {
		m := atomic.LoadInt32(&s.maxConc)
		if n <= m || atomic.CompareAndSwapInt32(&s.maxConc, m, n) {
			break
		}
	}
	time.Sleep(s.delay)
	defer atomic.AddInt32(&s.cur, -1)
	return s.Store.Get(ctx, k)
}

// F9: die Metadaten werden parallel geladen und von höchstens einem Aufrufer gleichzeitig.
func TestAllParallelSingleflight(t *testing.T) {
	mem := store.NewMem()
	for i := 0; i < 48; i++ {
		b, _ := json.Marshal(Meta{Name: fmt.Sprintf("f%d", i), Owner: "u", Size: 10})
		mem.Put(context.Background(), fmt.Sprintf("filesmeta/%d.json", i), b, "")
	}
	ss := &slowStore{Store: mem, delay: 20 * time.Millisecond}
	s := &Svc{St: ss}
	ctx := context.Background()

	t0 := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := s.all(ctx)
			if err != nil || len(m) != 48 {
				t.Error("all:", len(m), err)
			}
		}()
	}
	wg.Wait()
	if d := time.Since(t0); d > 700*time.Millisecond { // sequentiell wären es 960 ms
		t.Error("zu langsam (nicht parallel?):", d)
	}
	if ss.maxConc < 2 || ss.maxConc > loadWorkers {
		t.Error("gleichzeitige Zugriffe:", ss.maxConc)
	}
	if n := atomic.LoadInt32(&ss.lists); n != 1 {
		t.Error("zehn Aufrufer müssen sich einen Ladevorgang teilen, Ladevorgänge:", n)
	}
	// abgelaufener Zwischenspeicher: nur ein Aufrufer lädt, die anderen bekommen sofort den alten Stand
	s.cmu.Lock()
	s.cload = time.Now().Add(-2 * cacheTTL)
	s.cmu.Unlock()
	done := make(chan struct{})
	go func() { s.all(ctx); close(done) }()
	time.Sleep(60 * time.Millisecond)
	t1 := time.Now()
	m, _ := s.all(ctx)
	if len(m) != 48 || time.Since(t1) > 30*time.Millisecond {
		t.Error("alter Stand erwartet, ohne Warten:", len(m), time.Since(t1))
	}
	<-done
}

// Ein Schreibzugriff während des Ladens: das veraltete Ergebnis wird nicht übernommen.
func TestAllInvalidateDuringLoad(t *testing.T) {
	mem := store.NewMem()
	b, _ := json.Marshal(Meta{Name: "a", Owner: "u", Size: 1})
	mem.Put(context.Background(), "filesmeta/a.json", b, "")
	ss := &slowStore{Store: mem, delay: 100 * time.Millisecond}
	s := &Svc{St: ss}
	ctx := context.Background()
	done := make(chan struct{})
	go func() { s.all(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	s.cmu.Lock()
	s.cgen++ // wie invalidate()
	s.cmu.Unlock()
	<-done
	s.cmu.Lock()
	fresh := !s.cload.IsZero()
	s.cmu.Unlock()
	if fresh {
		t.Error("veraltetes Ergebnis wurde in den Zwischenspeicher übernommen")
	}
}
