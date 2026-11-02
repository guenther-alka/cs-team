package store

import (
	"context"
	"errors"
	"io"
)

// ErrReadOnly: der Speicher ist schreibgeschuetzt (Standby, docs/HA_SPEC.md Abschnitt 5).
var ErrReadOnly = errors.New("standby: read only")

// ReadOnly: Hülle um einen Speicher, die jedes Schreiben, Loeschen und Verschieben ablehnt. Lesen bleibt unveraendert.
// Ein Standby liest die replizierte Kopie und darf sie nie veraendern, auch nicht durch Hintergrundauftraege.
func ReadOnly(s Store) Store {
	ro := roStore{s}
	if _, ok := s.(pathStore); ok {
		return roPath{ro}
	}
	return ro
}

type roStore struct{ Store }

func (roStore) Put(context.Context, string, []byte, string) (string, error) { return "", ErrReadOnly }
func (roStore) PutStream(context.Context, string, io.Reader, int64, string) error {
	return ErrReadOnly
}
func (roStore) Delete(context.Context, string) error { return ErrReadOnly }

// roPath: gibt Root/Path des unterlegten Dateispeichers weiter (Lesen aus Snapshots, siehe snapver.go).
type roPath struct{ roStore }

func (r roPath) Root() string { return r.Store.(pathStore).Root() }
func (r roPath) Path(key string) (string, error) {
	return r.Store.(pathStore).Path(key)
}
