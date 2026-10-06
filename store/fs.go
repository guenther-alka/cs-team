package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FS: Speicher in einem lokalen Ordner (z.B. ZFS-Dataset) statt S3. Ein Prozess je Ordner.
// Key "a/b/c" -> Datei <root>/a/b/c; jedes Pfadsegment wird dateisystemsicher kodiert (%XX).
// ETag = Änderungszeit(ns)-Größe; Put mit Bedingung ist über einen Prozess-Mutex atomar.
type FS struct {
	root string
	mu   sync.Mutex
}

const tmpSuffix = "~tmp"

func NewFS(root string) (*FS, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &FS{root: root}, nil
}

// Root und Path: Ordner des Speichers und Pfad eines Schlüssels darin (Dateiversionen aus ZFS-Snapshots lesen dieselben Pfade).
func (s *FS) Root() string { return s.root }

func (s *FS) Path(key string) (string, error) { return s.path(key) }

func safeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '.' || c == '-' || c == '_' || c == '@' || c == '+' || c == ',' || c == '=' || c == ' ' || c >= 0x80
}

func encSeg(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if safeByte(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	out := b.String()
	if n := len(out); n > 0 && (out[n-1] == '.' || out[n-1] == ' ') { // Windows: kein Punkt/Leerzeichen am Ende
		out = out[:n-1] + fmt.Sprintf("%%%02X", out[n-1])
	}
	if out == "." || out == ".." {
		out = "%2E" + out[1:]
	}
	return out
}

func decSeg(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+3 <= len(s) {
			var v byte
			if _, err := fmt.Sscanf(s[i+1:i+3], "%02X", &v); err == nil {
				b = append(b, v)
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return string(b)
}

func (s *FS) path(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.Contains(key, "\x00") {
		return "", ErrNotFound
	}
	parts := strings.Split(key, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", ErrNotFound
		}
		parts[i] = encSeg(p)
	}
	return filepath.Join(append([]string{s.root}, parts...)...), nil
}

func etagOf(fi os.FileInfo) string { return fmt.Sprintf("%x-%x", fi.ModTime().UnixNano(), fi.Size()) }

func mapFS(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	}
	return sanitize(err)
}

func (s *FS) Get(_ context.Context, key string) ([]byte, string, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, "", mapFS(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return nil, "", ErrNotFound
	}
	b, err := io.ReadAll(f)
	return b, etagOf(fi), sanitize(err)
}

// commit: tmp-Datei atomar an Ziel setzen; Änderungszeit strikt größer als die alte (eindeutiger ETag).
func (s *FS) commit(tmp, dst string) (string, error) {
	var old time.Time
	if fi, err := os.Stat(dst); err == nil {
		old = fi.ModTime()
	}
	mt := time.Now()
	if !mt.After(old) {
		mt = old.Add(time.Microsecond)
	}
	os.Chtimes(tmp, mt, mt)
	var err error
	for i := 0; i < 10; i++ { // Windows: Ersetzen scheitert kurz, solange die Datei gelesen wird
		if err = os.Rename(tmp, dst); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		os.Remove(tmp)
		return "", sanitize(err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return "", sanitize(err)
	}
	return etagOf(fi), nil
}

func (s *FS) tmpFile(dst string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, sanitize(err)
	}
	f, err := os.CreateTemp(filepath.Dir(dst), ".w*"+tmpSuffix)
	return f, sanitize(err)
}

func (s *FS) Put(_ context.Context, key string, data []byte, ifMatch string) (string, error) {
	dst, err := s.path(key)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fi, serr := os.Stat(dst)
	exists := serr == nil
	if (ifMatch == "*" && exists) || (ifMatch != "" && ifMatch != "*" && (!exists || etagOf(fi) != ifMatch)) {
		return "", ErrConflict
	}
	if exists && fi.IsDir() {
		return "", ErrConflict
	}
	f, err := s.tmpFile(dst)
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return "", sanitize(werr)
	}
	return s.commit(f.Name(), dst)
}

func (s *FS) PutStream(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	dst, err := s.path(key)
	if err != nil {
		return err
	}
	f, err := s.tmpFile(dst)
	if err != nil {
		return err
	}
	_, werr := io.Copy(f, r)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return sanitize(werr)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.commit(f.Name(), dst)
	return err // commit liefert bereits bereinigte Fehler
}

func (s *FS) GetStream(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, mapFS(err)
	}
	if fi, err := f.Stat(); err != nil || fi.IsDir() {
		f.Close()
		return nil, ErrNotFound
	}
	return f, nil
}

func (s *FS) Delete(_ context.Context, key string) error {
	p, err := s.path(key)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return nil
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return sanitize(err)
	}
	for d := filepath.Dir(p); d != s.root && strings.HasPrefix(d, s.root); d = filepath.Dir(d) {
		if os.Remove(d) != nil { // nur leere Ordner
			break
		}
	}
	return nil
}

func (s *FS) List(_ context.Context, prefix string) ([]Info, error) {
	dir := s.root
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		p, err := s.path(prefix[:i])
		if err != nil {
			return nil, nil
		}
		dir = p
	}
	var out []Info
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || strings.HasSuffix(d.Name(), tmpSuffix) {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return nil
		}
		segs := strings.Split(filepath.ToSlash(rel), "/")
		for i := range segs {
			segs[i] = decSeg(segs[i])
		}
		key := strings.Join(segs, "/")
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, Info{key, etagOf(fi), fi.Size(), fi.ModTime()})
		return nil
	})
	return out, sanitize(err)
}
