package main

// webcraft.go -- cs-team liefert die Webcraft-Apps (WordCraft, GridCraft, PDFCraft ...) unter /webcraft/<app>/ selbst aus,
// damit cs-team auch ohne cs-proxy laeuft (0.70.0). Uebernommen aus cs-proxy webcraft.go:
//   - "/webcraft/app" -> 302 "/webcraft/app/", "/webcraft/app/" = index.html (die Apps laden alles relativ)
//   - jeder Dateityp mit passendem Content-Type (application/wasm ist fuer das Streaming-Kompilieren noetig)
//   - gzip fuer wasm/js/html/css/json: einmal je Dateiversion komprimiert und im Speicher gehalten
//   - Range, ETag, 304 ueber http.ServeContent; Dateien mit Hash im Namen "immutable", sonst "no-cache" (ETag)
//   - /webcraft/index.json: JSON-Liste der installierten Apps (Unterordner mit index.html)
// Ordner: CS_WEBCRAFT (off = aus), sonst napp-it <napp-it>/_my/wwwroot/webcraft, sonst webcraft neben dem Programm.
// Hinter cs-proxy beantwortet weiterhin cs-proxy /webcraft/ (andere Adresse/Port).

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// webcraftDir: Ordner mit den Apps (je App ein Unterordner); "" = aus.
func webcraftDir() string {
	if p, set := os.LookupEnv("CS_WEBCRAFT"); set && p != "" {
		if p == "off" {
			return ""
		}
		return p
	}
	dirs := []string{filepath.Join(exeDir(), "webcraft")}
	if !standalone() { // napp-it: _my/wwwroot/webcraft zuerst
		dirs = append([]string{filepath.Join(nappDir(), "_my", "wwwroot", "webcraft")}, dirs...)
	}
	for _, p := range dirs {
		if dirExists(p) {
			return p
		}
	}
	return ""
}

// hashedName: Datei- oder Ordnername mit Inhalts-Hash (trunk/wasm-bindgen), z. B. -32ffbcd5056039fd.js
var hashedName = regexp.MustCompile(`[-_/][0-9a-f]{16}([_./]|$)`)

var gzExt = map[string]bool{
	".wasm": true, ".js": true, ".mjs": true, ".html": true, ".htm": true,
	".css": true, ".json": true, ".svg": true, ".txt": true, ".md": true,
	".map": true, ".xml": true, ".webmanifest": true,
}

type gzEntry struct {
	data    []byte
	size    int64
	modTime time.Time
}

var (
	gzMu    sync.Mutex
	gzCache = map[string]*gzEntry{}
)

// gzFor: gzip-Form einer Datei, einmal je Groesse+Zeitstempel komprimiert (Sperre waehrend des Komprimierens,
// damit gleichzeitige erste Abrufe nicht doppelt komprimieren).
func gzFor(full string, fi os.FileInfo) *gzEntry {
	gzMu.Lock()
	defer gzMu.Unlock()
	if e, ok := gzCache[full]; ok && e.size == fi.Size() && e.modTime.Equal(fi.ModTime()) {
		return e
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		return nil
	}
	if err := gw.Close(); err != nil {
		return nil
	}
	e := &gzEntry{data: buf.Bytes(), size: fi.Size(), modTime: fi.ModTime()}
	gzCache[full] = e
	return e
}

func webcraftType(ext string) string {
	switch ext {
	case ".wasm":
		return "application/wasm"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".webmanifest":
		return "application/manifest+json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// insideRoot: der aufgeloeste Pfad (Symlinks verfolgt) muss im aufgeloesten Ordner bleiben.
func insideRoot(root, full string) bool {
	res, err := filepath.EvalSymlinks(full)
	if err != nil {
		return false
	}
	if r, e := filepath.EvalSymlinks(root); e == nil {
		root = r
	}
	rel, err := filepath.Rel(root, res)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// webcraftList: /webcraft/index.json = installierte Apps (Unterordner mit index.html), alphabetisch.
func webcraftList(w http.ResponseWriter, root string) {
	keys := []string{}
	ents, _ := os.ReadDir(root)
	for _, e := range ents {
		n := e.Name()
		if strings.HasPrefix(n, ".") || !insideRoot(root, filepath.Join(root, n)) {
			continue
		}
		if st, err := os.Stat(filepath.Join(root, n, "index.html")); err != nil || st.IsDir() {
			continue
		}
		keys = append(keys, n)
	}
	sort.Strings(keys)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(keys)
}

// webcraftHandler: /webcraft/... aus root (oeffentlich, nur statische Dateien, nur GET/HEAD).
func webcraftHandler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		up := r.URL.Path
		cu := pathpkg.Clean(up) // loest ".." auf - muss unter /webcraft bleiben
		if !strings.HasPrefix(cu, "/webcraft/") {
			http.NotFound(w, r)
			return
		}
		if cu == "/webcraft/index.json" {
			webcraftList(w, root)
			return
		}
		rel := strings.TrimPrefix(cu, "/webcraft/")
		full := filepath.Join(root, filepath.FromSlash(rel))
		fi, err := os.Stat(full)
		if err != nil || !insideRoot(root, full) {
			http.NotFound(w, r)
			return
		}
		if fi.IsDir() {
			if !strings.HasSuffix(up, "/") {
				loc := cu + "/"
				if r.URL.RawQuery != "" {
					loc += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, loc, http.StatusFound)
				return
			}
			full = filepath.Join(full, "index.html")
			cu = pathpkg.Join(cu, "index.html")
			if fi, err = os.Stat(full); err != nil || fi.IsDir() || !insideRoot(root, full) {
				http.NotFound(w, r)
				return
			}
		}
		ext := strings.ToLower(filepath.Ext(full))
		h := w.Header()
		h.Set("Content-Type", webcraftType(ext))
		h.Add("Vary", "Accept-Encoding")
		if hashedName.MatchString(cu) {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		etag := fmt.Sprintf("%x-%x", fi.Size(), fi.ModTime().UnixNano())
		if gzExt[ext] && fi.Size() >= 1024 && fi.Size() <= 64<<20 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			if e := gzFor(full, fi); e != nil {
				h.Set("Content-Encoding", "gzip")
				h.Set("ETag", `"`+etag+`-gz"`)
				http.ServeContent(w, r, "", e.modTime, bytes.NewReader(e.data))
				return
			}
		}
		f, err := os.Open(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		h.Set("ETag", `"`+etag+`"`)
		http.ServeContent(w, r, "", fi.ModTime(), f)
	})
}
