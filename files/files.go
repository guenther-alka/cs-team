// Package files: Dateiablage pro Benutzer bzw. Gruppenordner mit Unterordnern, Teilen und öffentlichem Link.
//
// Unterordner sind virtuell: der Dateiname ist ein Pfad ("Mathe/Aufgaben/a.pdf"); der Speicher bleibt flach
// (im Schlüssel steht statt "/" das Zeichen 0x1f). Ein leerer Ordner besteht aus der Marker-Datei "<ordner>/.folder".
//
// Bucket-Layout:
//
//	files/<owner>/<name>            Inhalt
//	filesmeta/<owner>/<name>.json   Metadaten (Größe, Typ, Freigaben, Token)
//	filestok/<token>                "<owner>/<name>" (öffentlicher Link)
//
// Schreibreihenfolge: erst Inhalt, dann Metadaten. Ein Abbruch dazwischen hinterlässt höchstens
// ein unsichtbares Objekt ohne Metadaten bzw. eine alte Größe; kein kaputter Eintrag in der Liste.
package files

import (
	"runtime"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cs-team/auth"
	"cs-team/store"
)

var (
	ErrNotFound = errors.New("not found")
	ErrDenied   = errors.New("denied")
	ErrTooLarge = errors.New("file too large")
	ErrBadName  = errors.New("bad file name")
	ErrExists   = errors.New("exists")
	ErrQuota    = errors.New("storage quota exceeded")
)

type Meta struct {
	Name  string     `json:"name"`
	Owner string     `json:"owner"`
	Size  int64      `json:"size"`
	Type  string     `json:"type"`
	Mod   int64      `json:"mod"` // unix nano
	Read  []string   `json:"read,omitempty"`
	Write []string   `json:"write,omitempty"`
	Token string     `json:"token,omitempty"`
	Exp   int64      `json:"exp,omitempty"` // Ablauf des öffentlichen Links (unix s, 0 = unbegrenzt; 0.60)
	Trash *TrashInfo `json:"trash,omitempty"` // gesetzt: Eintrag im Papierkorb (siehe trash.go)
}

func (m *Meta) ETag() string { return fmt.Sprintf("%d-%d", m.Mod, m.Size) }

// GroupOf: Besitzer "@<gruppe>" = Gruppenordner (Benutzernamen beginnen nie mit "@").
func GroupOf(owner string) (string, bool) {
	if strings.HasPrefix(owner, "@") && len(owner) > 1 {
		return owner[1:], true
	}
	return "", false
}

func (m *Meta) Level(user string) (read, write bool) {
	read, write = m.level(user)
	return read, write && auth.WriteArea(user, "files")
}

func (m *Meta) level(user string) (read, write bool) {
	if g, ok := GroupOf(m.Owner); ok {
		return auth.FolderAccess(user, g)
	}
	if user == m.Owner {
		return true, true
	}
	if auth.Allowed(m.Write, user) { // "*" = alle, "g:<gruppe>" = Gruppe, sonst Benutzername
		return true, true
	}
	return auth.Allowed(m.Read, user), false
}

type Svc struct {
	St  store.Store
	Max int64 // maximale Dateigröße in Bytes
	// Quota: Kontingent je Besitzer (Benutzer oder Gruppenordner) in Bytes; nil oder 0 = unbegrenzt (F8)
	Quota func() int64
	// TrashDays: Aufbewahrung gelöschter Dateien in Tagen; nil oder 0 = kein Papierkorb (sofort löschen)
	TrashDays func() int
	// PubMax: längste Gültigkeit öffentlicher Links in Tagen (globale Einstellung); nil oder 0 = unbegrenzt erlaubt (0.60)
	PubMax func() int

	locks  lockTable // WebDAV-Sperren (davlock.go)
	cmu    sync.Mutex
	cache  []Meta
	cload  time.Time
	cgen   int              // zählt invalidate(); verwirft veraltete Ladeergebnisse
	lmu    sync.Mutex       // ein Lader gleichzeitig (all)
	flight map[string]int64 // laufende Uploads je Besitzer (reservierte Bytes), unter cmu
}

const (
	cacheTTL    = 20 * time.Second
	loadWorkers = 16 // parallele Lesezugriffe auf die Metadaten (F9)
)

func (s *Svc) invalidate() { s.cmu.Lock(); s.cload = time.Time{}; s.cgen++; s.cmu.Unlock() }

// all: alle Metadaten (kurz gecacht; Schreibzugriffe dieses Prozesses invalidieren sofort). Geladen wird ohne Sperre, parallel und
// von höchstens einem Aufrufer gleichzeitig; ist der Zwischenspeicher nur abgelaufen, benutzen die anderen den alten Stand (F9).
func (s *Svc) all(ctx context.Context) ([]Meta, error) {
	s.cmu.Lock()
	c, fresh, have := s.cache, !s.cload.IsZero() && time.Since(s.cload) < cacheTTL, !s.cload.IsZero()
	s.cmu.Unlock()
	if fresh {
		return c, nil
	}
	if !s.lmu.TryLock() {
		if have {
			return c, nil
		}
		s.lmu.Lock()
	}
	defer s.lmu.Unlock()
	s.cmu.Lock()
	c, fresh, gen := s.cache, !s.cload.IsZero() && time.Since(s.cload) < cacheTTL, s.cgen
	s.cmu.Unlock()
	if fresh {
		return c, nil
	}
	infos, err := s.St.List(ctx, "filesmeta/")
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, i := range infos {
		if strings.HasSuffix(i.Key, ".json") {
			keys = append(keys, i.Key)
		}
	}
	res := make([]*Meta, len(keys))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < loadWorkers && w < len(keys); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				b, _, err := s.St.Get(ctx, keys[i])
				if err != nil {
					continue
				}
				var m Meta
				if json.Unmarshal(b, &m) != nil || m.Name == "" {
					continue
				}
				res[i] = &m
			}
		}()
	}
	for i := range keys {
		next <- i
	}
	close(next)
	wg.Wait()
	out := make([]Meta, 0, len(keys))
	for _, m := range res {
		if m != nil {
			out = append(out, *m)
		}
	}
	s.cmu.Lock()
	if s.cgen == gen { // währenddessen geschrieben: Ergebnis nicht übernehmen, der nächste Aufruf lädt neu
		s.cache, s.cload = out, time.Now()
	}
	s.cmu.Unlock()
	return out, nil
}

// Usage: belegter Speicher eines Besitzers in Bytes (Summe der Dateigrößen).
func (s *Svc) Usage(ctx context.Context, owner string) (int64, error) {
	all, err := s.all(ctx)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, m := range all {
		if m.Owner == owner {
			n += m.Size
		}
	}
	return n, nil
}

// reserve: prüft das Kontingent des Besitzers und reserviert Platz für einen Upload (Größe size, -1 = unbekannt).
// Liefert den erlaubten Höchstwert in Bytes (-1 = unbegrenzt) und die Freigabe der Reservierung.
func (s *Svc) reserve(ctx context.Context, owner string, size, replaced int64) (room int64, release func(), err error) {
	var q int64
	if s.Quota != nil {
		q = s.Quota()
	}
	if q <= 0 {
		return -1, func() {}, nil
	}
	var used int64
	for try := 0; ; try++ {
		s.cmu.Lock()
		gen := s.cgen
		s.cmu.Unlock()
		if used, err = s.Usage(ctx, owner); err != nil {
			return 0, nil, err
		}
		s.cmu.Lock()
		if s.cgen == gen || try >= 5 {
			break // unter cmu weiter: kein Schreibzugriff zwischen Messung und Reservierung (sonst würde ein fertiger Upload doppelt fehlen)
		}
		s.cmu.Unlock()
	}
	defer s.cmu.Unlock()
	room = q - used + replaced - s.flight[owner] // eine ersetzte Datei zählt nicht doppelt
	if room < 0 || (size >= 0 && size > room) {
		return 0, nil, ErrQuota
	}
	need := size
	if need < 0 {
		need = room
		if need > s.Max {
			need = s.Max
		}
	}
	if s.flight == nil {
		s.flight = map[string]int64{}
	}
	s.flight[owner] += need
	return room, func() {
		s.cmu.Lock()
		s.flight[owner] -= need
		if s.flight[owner] <= 0 {
			delete(s.flight, owner)
		}
		s.cmu.Unlock()
	}, nil
}

const (
	sepKey  = "\x1f" // im Schlüssel statt "/"
	Marker  = ".folder"
	maxName = 255
)

func keyName(name string) string { return strings.ReplaceAll(name, "/", sepKey) }

func dataKey(owner, name string) string { return "files/" + owner + "/" + keyName(name) }
func metaKey(owner, name string) string { return "filesmeta/" + owner + "/" + keyName(name) + ".json" }
func tokKey(tok string) string          { return "filestok/" + tok }

// ValidName: Pfad aus 1..10 Segmenten getrennt durch "/"; erstes Segment nie "shared"/"groups" (WebDAV-Wurzel).
func ValidName(n string) bool {
	if n == "" || len(n) > maxName || !utf8.ValidString(n) {
		return false
	}
	segs := strings.Split(n, "/")
	if len(segs) > 10 || segs[0] == "shared" || segs[0] == "groups" || segs[0] == trashDir {
		return false
	}
	for _, sg := range segs {
		if sg == "" || sg == "." || sg == ".." || len(sg) > 150 {
			return false
		}
		for _, r := range sg {
			if r < 0x20 || r == 0x7f || r == '\\' {
				return false
			}
		}
		if runtime.GOOS == "windows" && winBad(sg) {
			return false
		}
	}
	return true
}

// winBad: siehe store.WinBad (nur auf Windows-Servern geprüft, s. ValidName).
func winBad(sg string) bool { return store.WinBad(sg) }

// Dir/Base eines Namens.
func Dir(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i]
	}
	return ""
}
func Base(name string) string { return name[strings.LastIndex(name, "/")+1:] }

func contentType(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}

// InlineOK: nur diese Typen werden im Browser angezeigt, alles andere wird zum Download erzwungen
// (HTML/SVG/JS dürfen nie aus unserem Origin ausgeführt werden).
func InlineOK(ctype string) bool {
	t, _, _ := mime.ParseMediaType(ctype)
	switch t {
	case "application/pdf", "image/png", "image/jpeg", "image/gif", "image/webp", "text/plain":
		return true
	}
	return false
}

func (s *Svc) Meta(ctx context.Context, owner, name string) (*Meta, error) {
	if !ValidName(name) {
		return nil, ErrNotFound
	}
	b, _, err := s.St.Get(ctx, metaKey(owner, name))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Svc) saveMeta(ctx context.Context, m *Meta) error {
	b, _ := json.Marshal(m)
	_, err := s.St.Put(ctx, metaKey(m.Owner, m.Name), b, "")
	s.invalidate()
	return err
}

// List: eigene Dateien und die mit user geteilten (inkl. lesbarer Gruppenordner).
func (s *Svc) List(ctx context.Context, user string) (own, shared []Meta, err error) {
	all, err := s.all(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range all {
		if isTrash(&m) {
			continue
		}
		if m.Owner == user {
			own = append(own, m)
		} else if r, _ := m.Level(user); r {
			shared = append(shared, m)
		}
	}
	byName := func(l []Meta) {
		sort.Slice(l, func(a, b int) bool {
			if l[a].Owner != l[b].Owner {
				return l[a].Owner < l[b].Owner
			}
			return strings.ToLower(l[a].Name) < strings.ToLower(l[b].Name)
		})
	}
	byName(own)
	byName(shared)
	return
}

type limitReader struct {
	r    io.Reader
	left int64
	n    int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.left {
		return n, ErrTooLarge
	}
	return n, err
}

// Put: legt an oder ersetzt owner/name. actor braucht Schreibrecht (Owner oder Write-Liste).
func (s *Svc) Put(ctx context.Context, actor, owner, name string, r io.Reader, size int64) (*Meta, error) {
	if !ValidName(name) {
		return nil, ErrBadName
	}
	if !auth.WriteArea(actor, "files") {
		return nil, ErrDenied
	}
	old, err := s.Meta(ctx, owner, name)
	switch {
	case errors.Is(err, ErrNotFound):
		if g, isG := GroupOf(owner); isG {
			if _, w := auth.FolderAccess(actor, g); !w {
				return nil, ErrDenied
			}
		} else if actor != owner {
			return nil, ErrDenied // neue Dateien legt nur der Owner an
		}
		old = &Meta{Name: name, Owner: owner}
	case err != nil:
		return nil, err
	default:
		if _, w := old.Level(actor); !w {
			return nil, ErrDenied
		}
	}
	if s.lockedByOther(owner, name, actor) {
		return nil, ErrLocked
	}
	if size > s.Max {
		return nil, ErrTooLarge
	}
	if err := s.treeConflict(ctx, owner, name); err != nil {
		return nil, err
	}
	room, release, err := s.reserve(ctx, owner, size, old.Size)
	if errors.Is(err, ErrQuota) && s.evictTrash(ctx, owner, size) > 0 { // Platzmangel: zuerst den Papierkorb leeren (älteste zuerst)
		room, release, err = s.reserve(ctx, owner, size, old.Size)
	}
	if err != nil {
		return nil, err
	}
	defer release()
	left := s.Max
	if room >= 0 && room < left {
		left = room
	}
	lr := &limitReader{r: r, left: left}
	ct := contentType(name)
	if err := s.St.PutStream(ctx, dataKey(owner, name), lr, size, ct); err != nil {
		if errors.Is(err, ErrTooLarge) || lr.n > left {
			if room >= 0 && room < s.Max {
				return nil, ErrQuota
			}
			return nil, ErrTooLarge
		}
		return nil, err
	}
	old.Size, old.Type = lr.n, ct
	old.Mod = time.Now().UnixNano()
	return old, s.saveMeta(ctx, old)
}

// treeConflict: kein Ordner darf wie eine Datei heißen und umgekehrt (auch im lokalen Ordner-Speicher nötig).
func (s *Svc) treeConflict(ctx context.Context, owner, name string) error {
	segs := strings.Split(name, "/")
	for i := 1; i < len(segs); i++ {
		if _, err := s.Meta(ctx, owner, strings.Join(segs[:i], "/")); err == nil {
			return ErrExists // "a" ist eine Datei, "a/b" soll angelegt werden
		}
	}
	if infos, _ := s.St.List(ctx, "filesmeta/"+owner+"/"+keyName(name)+sepKey); len(infos) > 0 {
		return ErrExists // "a" soll Datei werden, "a/..." existiert als Ordner
	}
	return nil
}

func (s *Svc) Open(ctx context.Context, actor, owner, name string) (*Meta, io.ReadCloser, error) {
	m, err := s.Meta(ctx, owner, name)
	if err != nil {
		return nil, nil, err
	}
	if rd, _ := m.Level(actor); !rd {
		return nil, nil, ErrNotFound // nichts über Existenz verraten
	}
	rc, err := s.St.GetStream(ctx, dataKey(owner, name))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, ErrNotFound
	}
	return m, rc, err
}

// Remove löscht eine Datei: in den Papierkorb (wenn eingeschaltet, nicht für Ordner-Marker und leere Dateien), sonst endgültig.
func (s *Svc) Remove(ctx context.Context, actor, owner, name string) error {
	m, err := s.Meta(ctx, owner, name)
	if err != nil {
		return err
	}
	if !canManage(m, actor) {
		return ErrDenied
	}
	if s.lockedByOther(owner, name, actor) {
		return ErrLocked
	}
	if s.trashDays() > 0 && Base(m.Name) != Marker && m.Size > 0 {
		return s.trashRemove(ctx, actor, m)
	}
	return s.removeHard(ctx, m)
}

// removeHard: endgültig (auch beim Verschieben/Umbenennen, wo kein Papierkorb-Eintrag entstehen soll).
func (s *Svc) removeHard(ctx context.Context, m *Meta) error {
	if m.Token != "" {
		s.St.Delete(ctx, tokKey(m.Token))
	}
	s.St.Delete(ctx, metaKey(m.Owner, m.Name))
	err := s.St.Delete(ctx, dataKey(m.Owner, m.Name))
	s.invalidate()
	return err
}

// Mkdir: legt einen (leeren) Ordner an.
func (s *Svc) Mkdir(ctx context.Context, actor, owner, dir string) error {
	dir = strings.Trim(dir, "/")
	if !ValidName(dir + "/" + Marker) {
		return ErrBadName
	}
	_, err := s.Put(ctx, actor, owner, dir+"/"+Marker, strings.NewReader(""), 0)
	return err
}

// under: alle Dateien (inkl. Marker) unterhalb von dir.
func (s *Svc) under(ctx context.Context, owner, dir string) []Meta {
	all, _ := s.all(ctx)
	var out []Meta
	for _, m := range all {
		if m.Owner == owner && !isTrash(&m) && strings.HasPrefix(m.Name, dir+"/") {
			out = append(out, m)
		}
	}
	return out
}

// RemoveDir: löscht einen Ordner mit allem darin (jede Datei prüft die Rechte).
func (s *Svc) RemoveDir(ctx context.Context, actor, owner, dir string) error {
	l := s.under(ctx, owner, dir)
	if len(l) == 0 {
		return ErrNotFound
	}
	for _, m := range l {
		if err := s.Remove(ctx, actor, owner, m.Name); err != nil {
			return err
		}
	}
	return nil
}

// MoveTo verschiebt/kopiert eine Datei oder (dir=true) einen ganzen Ordner innerhalb desselben Besitzers.
func (s *Svc) MoveTo(ctx context.Context, actor, owner, from, to string, dir, move, overwrite bool) error {
	if !ValidName(to) || !ValidName(from) {
		return ErrBadName
	}
	if from == to {
		return nil
	}
	if dir && (to == from || strings.HasPrefix(to, from+"/")) {
		return ErrBadName // Ordner nicht in sich selbst
	}
	list := []Meta{}
	if dir {
		list = s.under(ctx, owner, from)
		if len(list) == 0 {
			return ErrNotFound
		}
	} else {
		m, err := s.Meta(ctx, owner, from)
		if err != nil {
			return err
		}
		list = append(list, *m)
	}
	for _, m := range list {
		dst := to
		if dir {
			dst = to + strings.TrimPrefix(m.Name, from)
		}
		if _, err := s.Meta(ctx, owner, dst); err == nil && !overwrite {
			return ErrExists
		}
	}
	for _, m := range list {
		dst := to
		if dir {
			dst = to + strings.TrimPrefix(m.Name, from)
		}
		_, rc, err := s.Open(ctx, actor, owner, m.Name)
		if err != nil {
			return err
		}
		_, err = s.Put(ctx, actor, owner, dst, rc, m.Size)
		rc.Close()
		if err != nil {
			return err
		}
		if move {
			if m.Token != "" {
				s.moveLink(ctx, owner, m.Name, dst)
			}
			if err := s.removeFrom(ctx, actor, owner, m.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// moveLink: der öffentliche Link einer umbenannten/verschobenen Datei bleibt gültig (Token zeigt auf den neuen Namen).
func (s *Svc) moveLink(ctx context.Context, owner, from, to string) {
	o, e1 := s.Meta(ctx, owner, from)
	n, e2 := s.Meta(ctx, owner, to)
	if e1 != nil || e2 != nil || o.Token == "" {
		return
	}
	if n.Token != "" {
		s.St.Delete(ctx, tokKey(n.Token)) // überschriebenes Ziel hatte einen eigenen Link
	}
	if _, err := s.St.Put(ctx, tokKey(o.Token), []byte(owner+"/"+to), ""); err != nil {
		return
	}
	n.Token, n.Exp, o.Token, o.Exp = o.Token, o.Exp, "", 0
	s.saveMeta(ctx, n)
	s.saveMeta(ctx, o)
}

// Share: nur der Owner. public=true erzeugt einen Token (falls keiner da), false widerruft ihn.
// days (0.60): Gültigkeit des öffentlichen Links in Tagen (0 = unbegrenzt, begrenzt durch PubMax); nil lässt einen vorhandenen
// Link unverändert, ein neuer Link ohne Angabe bekommt die Standardgültigkeit unbegrenzt bzw. PubMax.
func (s *Svc) Share(ctx context.Context, actor, owner, name string, read, write []string, public bool, days *int) (*Meta, error) {
	m, err := s.Meta(ctx, owner, name)
	if err != nil {
		return nil, err
	}
	if !canManage(m, actor) {
		return nil, ErrDenied
	}
	if _, isG := GroupOf(m.Owner); isG {
		read, write = nil, nil // Gruppenordner: Rechte kommen aus der Gruppe; nur der öffentliche Link ist einstellbar
	}
	m.Read, m.Write = clean(read, m.Owner), clean(write, m.Owner)
	switch {
	case public && m.Token == "":
		b := make([]byte, 16)
		rand.Read(b)
		m.Token = hex.EncodeToString(b)
		if _, err := s.St.Put(ctx, tokKey(m.Token), []byte(owner+"/"+name), ""); err != nil {
			return nil, err
		}
		m.Exp = s.expiry(days)
		logLink("created", actor, m)
	case public && days != nil: // Gültigkeit ändern / verlängern: der Link bleibt, die Frist beginnt neu
		m.Exp = s.expiry(days)
		logLink("changed", actor, m)
	case !public && m.Token != "":
		s.St.Delete(ctx, tokKey(m.Token))
		m.Token, m.Exp = "", 0
		logLink("revoked", actor, m)
	}
	return m, s.saveMeta(ctx, m)
}

// logLink: Protokollzeile für öffentliche Links (nie der Token selbst).
func logLink(what, actor string, m *Meta) {
	exp := "unlimited"
	if m.Exp > 0 {
		exp = time.Unix(m.Exp, 0).UTC().Format("2006-01-02")
	}
	if what == "revoked" {
		exp = "-"
	}
	log.Printf("audit: public link %s file=%q/%q by=%q expires=%s", what, m.Owner, m.Name, actor, exp)
}

// expiry: Ablaufzeitpunkt (unix s) für eine Gültigkeit in Tagen; 0 = unbegrenzt. PubMax begrenzt: ist eine Obergrenze gesetzt,
// gibt es kein "unbegrenzt" und nichts darüber.
func (s *Svc) expiry(days *int) int64 {
	d := 0
	if days != nil {
		d = *days
	}
	if d < 0 {
		d = 0
	}
	if d > 3650 {
		d = 3650
	}
	if mx := s.pubMax(); mx > 0 && (d == 0 || d > mx) {
		d = mx
	}
	if d == 0 {
		return 0
	}
	return time.Now().Add(time.Duration(d) * 24 * time.Hour).Unix()
}

func (s *Svc) pubMax() int {
	if s.PubMax == nil {
		return 0
	}
	return s.PubMax()
}

// canManage: Löschen/Freigeben: Besitzer; im Gruppenordner jeder mit Schreibrecht.
func canManage(m *Meta, actor string) bool {
	if _, isG := GroupOf(m.Owner); isG {
		_, w := m.Level(actor)
		return w
	}
	return actor == m.Owner
}

func clean(l []string, owner string) []string {
	seen := map[string]bool{owner: true}
	var out []string
	for _, u := range l {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// Public: Datei über Token (ohne Login, nur lesen).
func (s *Svc) Public(ctx context.Context, tok string) (*Meta, io.ReadCloser, error) {
	if len(tok) != 32 || strings.Trim(tok, "0123456789abcdef") != "" {
		return nil, nil, ErrNotFound
	}
	b, _, err := s.St.Get(ctx, tokKey(tok))
	if err != nil {
		return nil, nil, ErrNotFound
	}
	owner, name, ok := strings.Cut(string(b), "/")
	if !ok {
		return nil, nil, ErrNotFound
	}
	m, err := s.Meta(ctx, owner, name)
	if err != nil || m.Token != tok {
		return nil, nil, ErrNotFound
	}
	if m.Exp > 0 && time.Now().Unix() >= m.Exp { // abgelaufen: wie ein unbekannter Token (der Ablauf wird nicht verraten)
		return nil, nil, ErrNotFound
	}
	rc, err := s.St.GetStream(ctx, dataKey(owner, name))
	if err != nil {
		return nil, nil, ErrNotFound
	}
	return m, rc, nil
}

// Copy kopiert oder verschiebt (move) eine eigene Datei auf einen neuen eigenen Namen (WebDAV COPY/MOVE).
func (s *Svc) Copy(ctx context.Context, user, from, to string, move, overwrite bool) (created bool, err error) {
	if !ValidName(to) {
		return false, ErrBadName
	}
	if from == to {
		return false, nil
	}
	src, rc, err := s.Open(ctx, user, user, from)
	if err != nil {
		return false, err
	}
	defer rc.Close()
	_, e := s.Meta(ctx, user, to)
	exists := e == nil
	if exists && !overwrite {
		return false, ErrExists
	}
	if _, err = s.Put(ctx, user, user, to, rc, src.Size); err != nil {
		return false, err
	}
	if move {
		if err = s.removeFrom(ctx, user, user, from); err != nil {
			return false, err
		}
	}
	return !exists, nil
}

// removeFrom: Quelle nach Verschieben/Umbenennen endgültig entfernen (Rechte wie bei Remove, aber nie in den Papierkorb).
func (s *Svc) removeFrom(ctx context.Context, actor, owner, name string) error {
	m, err := s.Meta(ctx, owner, name)
	if err != nil {
		return err
	}
	if !canManage(m, actor) {
		return ErrDenied
	}
	if s.lockedByOther(owner, name, actor) {
		return ErrLocked
	}
	return s.removeHard(ctx, m)
}
