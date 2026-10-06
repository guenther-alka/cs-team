package store

import (
	"errors"
	"io/fs"
	"log"
	"os"
)

// ErrStorage: allgemeine Fehlermeldung für Dateisystemfehler. Die Meldungen des Betriebssystems enthalten den
// Serverpfad ("mkdir C:\...") und gehören nicht in Antworten an Clients; sie stehen nur im Server-Log.
var ErrStorage = errors.New("storage error (details in the server log)")

// sanitize ersetzt Pfad-/Link-Fehler des Betriebssystems durch ErrStorage und protokolliert die Einzelheiten.
func sanitize(err error) error {
	var pe *fs.PathError
	var le *os.LinkError
	if errors.As(err, &pe) || errors.As(err, &le) {
		log.Printf("store: %v", err)
		return ErrStorage
	}
	return err
}
