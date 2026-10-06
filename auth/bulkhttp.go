package auth

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func splitCSVParam(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// bulkRoutes: universeller JSON-Export/Import (0.61), siehe bulk.go.
func (a *Auth) bulkRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/users/exportjson", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) && len(AdminOf(r.Context())) == 0 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		q := r.URL.Query()
		d := a.ExportBulk(r.Context(), BulkExportOpts{
			PW:      q.Get("pw") == "1",
			Secrets: q.Get("secrets") == "1",
			Users:   splitCSVParam(q.Get("users")),
			Groups:  splitCSVParam(q.Get("groups")),
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="cs-team-users.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(d)
	})))

	mux.Handle("POST /api/users/importjson", a.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
		if err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		var d BulkDump
		if err := json.Unmarshal(raw, &d); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		q := r.URL.Query()
		res := a.ImportBulk(r.Context(), d, BulkImportOpts{
			Create: q.Get("create") == "1",
			Update: q.Get("update") == "1",
			GenPW:  q.Get("genpw") == "1",
		})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(res)
	})))
}
