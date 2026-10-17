package main

import (
	"encoding/json"
	"net/http"

	"cs-team/auth"
	"cs-team/cal"
	"cs-team/chat"
	"cs-team/doc"
	"cs-team/files"
	"cs-team/hit"
	"cs-team/tasks"
)

// searchSem: hoechstens 4 Suchen gleichzeitig (die Suche liest Dokumente und Chat-Kanaele ohne Index).
var searchSem = make(chan struct{}, 4)

// searchRoutes: GET /api/search?q=...  -> {"tasks":[],"events":[],"docs":[],"files":[],"chat":[]}, je Bereich hoechstens 8 Treffer.
// Jeder Dienst prueft die Rechte des Benutzers selbst (Gruppenmitgliedschaft, Freigaben, Bereichsrechte).
func searchRoutes(mux *http.ServeMux, wrap func(http.Handler) http.Handler, ts *tasks.Svc, cs *chat.Svc, fs *files.Svc, hub *doc.Hub, cb *cal.Backend) {
	mux.Handle("GET /api/search", wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q, ok := hit.Query(r.URL.Query().Get("q"))
		if !ok {
			http.Error(w, "q: 2 to 80 characters", http.StatusBadRequest)
			return
		}
		select {
		case searchSem <- struct{}{}:
			defer func() { <-searchSem }()
		default:
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		ctx, me := r.Context(), auth.User(r.Context())
		const per = 8
		res := map[string][]hit.Hit{
			"tasks":  ts.Search(me, q, per),
			"chat":   cs.Search(ctx, me, q, per),
			"docs":   hub.Search(ctx, me, q, per),
			"events": nil,
			"files":  nil,
		}
		if auth.Can(ctx, "cal") {
			res["events"] = cb.Search(ctx, q, per)
		}
		if auth.Can(ctx, "files") {
			res["files"] = fs.Search(ctx, me, q, per)
		}
		for k, v := range res {
			if v == nil {
				res[k] = []hit.Hit{}
			}
		}
		json.NewEncoder(w).Encode(res)
	})))
}
