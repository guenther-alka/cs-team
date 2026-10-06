package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// Mover: Speicher, die ein Objekt ohne Umweg über den Server umbenennen können.
type Mover interface {
	Move(ctx context.Context, from, to string) error
}

// Move verschiebt ein Objekt (Ziel wird überschrieben). Ohne eigene Unterstützung: kopieren, dann Quelle löschen.
func Move(ctx context.Context, s Store, from, to string) error {
	if m, ok := s.(Mover); ok {
		return m.Move(ctx, from, to)
	}
	rc, err := s.GetStream(ctx, from)
	if err != nil {
		return err
	}
	err = s.PutStream(ctx, to, rc, -1, "")
	rc.Close()
	if err != nil {
		return err
	}
	return s.Delete(ctx, from)
}

func (s *FS) Move(_ context.Context, from, to string) error {
	src, err := s.path(from)
	if err != nil {
		return err
	}
	dst, err := s.path(to)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fi, err := os.Stat(src); err != nil || fi.IsDir() {
		return ErrNotFound
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return sanitize(err)
	}
	for i := 0; i < 10; i++ { // Windows: kurzzeitig gesperrt, solange die Datei gelesen wird
		if err = os.Rename(src, dst); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		return sanitize(err)
	}
	for d := filepath.Dir(src); d != s.root && strings.HasPrefix(d, s.root); d = filepath.Dir(d) {
		if os.Remove(d) != nil { // nur leere Ordner
			break
		}
	}
	return nil
}

func (s *Mem) Move(_ context.Context, from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.m[from]
	if !ok {
		return ErrNotFound
	}
	s.n++
	o.etag, o.mod = "e"+itoa(s.n), time.Now()
	s.m[to] = o
	delete(s.m, from)
	return nil
}

func (s *S3) Move(ctx context.Context, from, to string) error {
	if !ValidKey(from) || !ValidKey(to) {
		return ErrNotFound
	}
	if _, err := s.c.CopyObject(ctx, minio.CopyDestOptions{Bucket: s.bucket, Object: to}, minio.CopySrcOptions{Bucket: s.bucket, Object: from}); err != nil {
		// große Objekte oder Server ohne Server-seitiges Kopieren: über den Server kopieren
		o, gerr := s.GetStream(ctx, from)
		if gerr != nil {
			return gerr
		}
		defer o.Close()
		if perr := s.PutStream(ctx, to, o, -1, ""); perr != nil {
			return mapErr(perr)
		}
	}
	return s.Delete(ctx, from)
}
