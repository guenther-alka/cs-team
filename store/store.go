// Package store: dünne Schicht über S3/RustFS mit ETag-basiertem Locking.
package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("etag mismatch")
)

type Info struct {
	Key     string
	ETag    string
	Size    int64
	ModTime time.Time
}

type Store interface {
	Get(ctx context.Context, key string) ([]byte, string, error)
	// Put: ifMatch=="" -> unbedingt, "*" -> nur anlegen (If-None-Match), sonst If-Match ETag
	Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error)
	List(ctx context.Context, prefix string) ([]Info, error)
	Delete(ctx context.Context, key string) error
	// Streaming für große Objekte (Files). size<0 = unbekannt.
	PutStream(ctx context.Context, key string, r io.Reader, size int64, ctype string) error
	GetStream(ctx context.Context, key string) (io.ReadCloser, error)
}

// Update: Read-Modify-Write mit Retry bei ETag-Konflikt. fn bekommt nil, wenn das Objekt fehlt.
func Update(ctx context.Context, s Store, key string, fn func(cur []byte) ([]byte, error)) error {
	for i := 0; i < 6; i++ {
		cur, etag, err := s.Get(ctx, key)
		cond := etag
		if errors.Is(err, ErrNotFound) {
			cur, cond = nil, "*"
		} else if err != nil {
			return err
		}
		nu, err := fn(cur)
		if err != nil {
			return err
		}
		if _, err = s.Put(ctx, key, nu, cond); !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return ErrConflict
}

// ---------- S3 / RustFS ----------

type S3 struct {
	c      *minio.Client
	bucket string
}

func NewS3(endpoint, key, secret, bucket string, tls bool) (*S3, error) {
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(key, secret, ""), Secure: tls})
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if ok, err := c.BucketExists(ctx, bucket); err != nil {
		return nil, err
	} else if !ok {
		if err := c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, err
		}
	}
	return &S3{c, bucket}, nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	r := minio.ToErrorResponse(err)
	switch {
	case r.Code == "NoSuchKey" || r.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case r.Code == "PreconditionFailed" || r.StatusCode == http.StatusPreconditionFailed:
		return ErrConflict
	}
	return err
}

// ValidKey: gemeinsame Schlüsselprüfung für alle Backends (wie FS): nicht leer, kein führendes "/", kein "\\", kein NUL,
// keine leeren Segmente, kein "." und "..". Bei List darf das Präfix mit "/" enden oder leer sein.
func ValidKey(key string) bool { return validKey(key, false) }

func validKey(key string, prefix bool) bool {
	if prefix && key == "" {
		return true
	}
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.Contains(key, "\x00") {
		return false
	}
	parts := strings.Split(key, "/")
	for i, p := range parts {
		if p == "." || p == ".." || (p == "" && !(prefix && i == len(parts)-1)) {
			return false
		}
	}
	return true
}

func (s *S3) Get(ctx context.Context, key string) ([]byte, string, error) {
	if !ValidKey(key) {
		return nil, "", ErrNotFound
	}
	o, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", mapErr(err)
	}
	defer o.Close()
	st, err := o.Stat()
	if err != nil {
		return nil, "", mapErr(err)
	}
	b, err := io.ReadAll(o)
	return b, st.ETag, mapErr(err)
}

func (s *S3) Put(ctx context.Context, key string, data []byte, ifMatch string) (string, error) {
	if !ValidKey(key) {
		return "", ErrNotFound
	}
	opt := minio.PutObjectOptions{}
	switch ifMatch {
	case "":
	case "*":
		opt.SetMatchETagExcept("*")
	default:
		opt.SetMatchETag(ifMatch)
	}
	r, err := s.c.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), opt)
	return r.ETag, mapErr(err)
}

func (s *S3) List(ctx context.Context, prefix string) ([]Info, error) {
	var out []Info
	if !validKey(prefix, true) {
		return nil, nil
	}
	for o := range s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		out = append(out, Info{o.Key, o.ETag, o.Size, o.LastModified})
	}
	return out, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return ErrNotFound
	}
	return mapErr(s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}))
}

func (s *S3) PutStream(ctx context.Context, key string, r io.Reader, size int64, ctype string) error {
	if !ValidKey(key) {
		return ErrNotFound
	}
	_, err := s.c.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: ctype})
	return mapErr(err)
}

func (s *S3) GetStream(ctx context.Context, key string) (io.ReadCloser, error) {
	if !ValidKey(key) {
		return nil, ErrNotFound
	}
	o, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, mapErr(err)
	}
	if _, err := o.Stat(); err != nil { // GetObject ist lazy: Existenz hier prüfen
		o.Close()
		return nil, mapErr(err)
	}
	return o, nil
}

// ---------- Memory (Tests / Demo ohne RustFS) ----------

type Mem struct {
	mu sync.Mutex
	m  map[string]memObj
	n  int
}
type memObj struct {
	data []byte
	etag string
	mod  time.Time
}

func NewMem() *Mem { return &Mem{m: map[string]memObj{}} }

func (s *Mem) Get(_ context.Context, key string) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.m[key]
	if !ok {
		return nil, "", ErrNotFound
	}
	return append([]byte(nil), o.data...), o.etag, nil
}

func (s *Mem) Put(_ context.Context, key string, data []byte, ifMatch string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.m[key]
	if (ifMatch == "*" && ok) || (ifMatch != "" && ifMatch != "*" && (!ok || o.etag != ifMatch)) {
		return "", ErrConflict
	}
	s.n++
	e := "e" + strings.Repeat("0", 0) + itoa(s.n)
	s.m[key] = memObj{append([]byte(nil), data...), e, time.Now()}
	return e, nil
}

func (s *Mem) List(_ context.Context, prefix string) ([]Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Info
	for k, o := range s.m {
		if strings.HasPrefix(k, prefix) {
			out = append(out, Info{k, o.etag, int64(len(o.data)), o.mod})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *Mem) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func (s *Mem) PutStream(ctx context.Context, key string, r io.Reader, _ int64, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	_, err = s.Put(ctx, key, b, "")
	return err
}

func (s *Mem) GetStream(ctx context.Context, key string) (io.ReadCloser, error) {
	b, _, err := s.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
