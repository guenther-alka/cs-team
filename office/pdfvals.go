package office

import (
	"encoding/json"
	"net/http"

	"cs-team/conv"
)

// pdfVals: fuer den PDF-Export einer Tabelle schickt der Browser per POST {"vals":{"A1":"1.234,50", ...}} die angezeigten Werte
// (Formeln berechnet, Zahlenformate angewendet), weil nur der Browser die Formeln auswertet. Die Rechte prueft der Export wie immer;
// die Werte werden nur gedruckt, nichts wird gespeichert. Ohne Body (GET) gibt das PDF die gespeicherten Werte bzw. Formeln aus.
func pdfVals(w http.ResponseWriter, r *http.Request) map[string]string {
	if r.Method != http.MethodPost || r.URL.Query().Get("format") != "pdf" {
		return nil
	}
	var in struct {
		Vals map[string]string `json:"vals"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&in) != nil || len(in.Vals) > conv.MaxCells {
		return nil
	}
	return in.Vals
}
