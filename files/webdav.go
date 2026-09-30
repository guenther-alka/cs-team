package files

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-webdav"

	"cs-team/auth"
)

// WebDAV-Sicht (Mount-URL https://host/webdav/), Ordner beliebig tief:
//
//	/webdav/<pfad>                       eigene Dateien (lesen/schreiben)
//	/webdav/shared/<besitzer>/<pfad>     mit mir geteilt (lesen; schreiben bei Write-Freigabe)
//	/webdav/groups/<gruppe>/<pfad>       Gruppenordner (lesen; schreiben je nach Gruppeneinstellung)
type davFS struct{ s *Svc }

func (s *Svc) WebDAV() http.Handler { return &webdav.Handler{FileSystem: &davFS{s}} }

const davPrefix = "/webdav"

func nf() error { return webdav.NewHTTPError(http.StatusNotFound, os.ErrNotExist) }

func dErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return nf()
	case errors.Is(err, ErrDenied):
		return webdav.NewHTTPError(http.StatusForbidden, err)
	case errors.Is(err, ErrTooLarge):
		return webdav.NewHTTPError(http.StatusRequestEntityTooLarge, err)
	case errors.Is(err, ErrBadName):
		return webdav.NewHTTPError(http.StatusBadRequest, err)
	case errors.Is(err, ErrExists):
		return webdav.NewHTTPError(http.StatusPreconditionFailed, os.ErrExist)
	}
	return err
}

// parts: Pfad relativ zu /webdav ohne Rand-Slashes -> Segmente
func parts(p string) []string {
	p = strings.Trim(path.Clean("/"+strings.TrimPrefix(p, davPrefix)), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func dirInfo(p string) webdav.FileInfo {
	return webdav.FileInfo{Path: p, IsDir: true, ModTime: time.Now()}
}

func fileInfo(m *Meta, p string) webdav.FileInfo {
	return webdav.FileInfo{Path: p, Size: m.Size, ModTime: time.Unix(0, m.Mod), MIMEType: m.Type, ETag: m.ETag()}
}

// loc: aufgelöster Pfad: Besitzer, Pfad im Besitzer-Baum ("" = Wurzel), DAV-Präfix dieser Wurzel.
// kind: "own" | "shared" (Wurzel /shared/<o>) | "groups" (Wurzel /groups/<g>) | "top" (/, /shared, /groups) | "" ungültig
type loc struct {
	kind, owner, sub, base string
}

func resolve(me string, seg []string) loc {
	switch {
	case len(seg) == 0:
		return loc{kind: "top", base: davPrefix}
	case seg[0] == "shared":
		if len(seg) == 1 {
			return loc{kind: "top", base: davPrefix + "/shared"}
		}
		if strings.HasPrefix(seg[1], "@") {
			return loc{}
		}
		return loc{"shared", seg[1], strings.Join(seg[2:], "/"), davPrefix + "/shared/" + seg[1]}
	case seg[0] == "groups":
		if len(seg) == 1 {
			return loc{kind: "top", base: davPrefix + "/groups"}
		}
		return loc{"groups", "@" + seg[1], strings.Join(seg[2:], "/"), davPrefix + "/groups/" + seg[1]}
	}
	return loc{"own", me, strings.Join(seg, "/"), davPrefix}
}

func (l loc) davPath(name string) string {
	if name == "" {
		return l.base
	}
	return l.base + "/" + name
}

// metasOf: die für den Benutzer sichtbaren Dateien des Besitzers.
func (d *davFS) metasOf(ctx context.Context, me, owner string) ([]Meta, error) {
	own, shared, err := d.s.List(ctx, me)
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, l := range [][]Meta{own, shared} {
		for _, m := range l {
			if m.Owner == owner {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// children: direkte Kinder von sub: Dateien (ohne Marker) und Ordnernamen.
func children(metas []Meta, sub string) (files []*Meta, dirs []string) {
	seen := map[string]bool{}
	for i := range metas {
		rel := metas[i].Name
		if sub != "" {
			if !strings.HasPrefix(rel, sub+"/") {
				continue
			}
			rel = rel[len(sub)+1:]
		}
		if k := strings.Index(rel, "/"); k >= 0 {
			if d := rel[:k]; !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		} else if rel != Marker {
			files = append(files, &metas[i])
		}
	}
	sort.Strings(dirs)
	return
}

func (d *davFS) Stat(ctx context.Context, name string) (*webdav.FileInfo, error) {
	me := auth.User(ctx)
	l := resolve(me, parts(name))
	switch l.kind {
	case "":
		return nil, nf()
	case "top":
		fi := dirInfo(l.base)
		return &fi, nil
	}
	if l.kind == "groups" && l.sub == "" {
		if r, _ := auth.FolderAccess(me, l.owner[1:]); r {
			fi := dirInfo(l.base)
			return &fi, nil
		}
		return nil, nf()
	}
	metas, err := d.metasOf(ctx, me, l.owner)
	if err != nil {
		return nil, err
	}
	if l.sub == "" { // /shared/<besitzer>
		if len(metas) > 0 {
			fi := dirInfo(l.base)
			return &fi, nil
		}
		return nil, nf()
	}
	for i := range metas {
		if metas[i].Name == l.sub {
			fi := fileInfo(&metas[i], l.davPath(l.sub))
			return &fi, nil
		}
	}
	for i := range metas {
		if strings.HasPrefix(metas[i].Name, l.sub+"/") {
			fi := dirInfo(l.davPath(l.sub))
			return &fi, nil
		}
	}
	return nil, nf()
}

func (d *davFS) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	me := auth.User(ctx)
	l := resolve(me, parts(name))
	if (l.kind != "own" && l.kind != "shared" && l.kind != "groups") || l.sub == "" {
		return nil, nf()
	}
	_, rc, err := d.s.Open(ctx, me, l.owner, l.sub)
	return rc, dErr(err)
}

func (d *davFS) ReadDir(ctx context.Context, name string, recursive bool) ([]webdav.FileInfo, error) {
	me := auth.User(ctx)
	l := resolve(me, parts(name))
	var out []webdav.FileInfo
	addTree := func(lc loc, metas []Meta, sub string) { // Kinder von sub, optional alle Nachfahren
		if !recursive {
			fs, ds := children(metas, sub)
			for _, m := range fs {
				out = append(out, fileInfo(m, lc.davPath(m.Name)))
			}
			for _, dn := range ds {
				p := dn
				if sub != "" {
					p = sub + "/" + dn
				}
				out = append(out, dirInfo(lc.davPath(p)))
			}
			return
		}
		seen := map[string]bool{}
		for i := range metas {
			n, rel := metas[i].Name, metas[i].Name
			if sub != "" {
				if !strings.HasPrefix(n, sub+"/") {
					continue
				}
				rel = n[len(sub)+1:]
			}
			segs := strings.Split(rel, "/")
			for k := 1; k < len(segs); k++ {
				full := strings.Join(segs[:k], "/")
				if sub != "" {
					full = sub + "/" + full
				}
				if !seen[full] {
					seen[full] = true
					out = append(out, dirInfo(lc.davPath(full)))
				}
			}
			if segs[len(segs)-1] != Marker {
				out = append(out, fileInfo(&metas[i], lc.davPath(n)))
			}
		}
	}
	switch l.kind {
	case "":
		return nil, nf()
	case "top":
		own, shared, err := d.s.List(ctx, me)
		if err != nil {
			return nil, err
		}
		switch l.base {
		case davPrefix:
			addTree(loc{"own", me, "", davPrefix}, own, "")
			out = append(out, dirInfo(davPrefix+"/shared"), dirInfo(davPrefix+"/groups"))
			if recursive {
				for _, o := range d.sharedOwners(shared) {
					out = append(out, dirInfo(davPrefix+"/shared/"+o))
					addTree(loc{"shared", o, "", davPrefix + "/shared/" + o}, filterOwner(shared, o), "")
				}
				for _, g := range auth.FolderGroups(me) {
					out = append(out, dirInfo(davPrefix+"/groups/"+g.Name))
					addTree(loc{"groups", "@" + g.Name, "", davPrefix + "/groups/" + g.Name}, filterOwner(shared, "@"+g.Name), "")
				}
			}
		case davPrefix + "/shared":
			for _, o := range d.sharedOwners(shared) {
				out = append(out, dirInfo(davPrefix+"/shared/"+o))
				if recursive {
					addTree(loc{"shared", o, "", davPrefix + "/shared/" + o}, filterOwner(shared, o), "")
				}
			}
		default: // /groups
			for _, g := range auth.FolderGroups(me) {
				out = append(out, dirInfo(davPrefix+"/groups/"+g.Name))
				if recursive {
					addTree(loc{"groups", "@" + g.Name, "", davPrefix + "/groups/" + g.Name}, filterOwner(shared, "@"+g.Name), "")
				}
			}
		}
	default:
		if l.kind == "groups" {
			if r, _ := auth.FolderAccess(me, l.owner[1:]); !r {
				return nil, nf()
			}
		}
		metas, err := d.metasOf(ctx, me, l.owner)
		if err != nil {
			return nil, err
		}
		if l.sub != "" {
			if fs, ds := children(metas, l.sub); len(fs) == 0 && len(ds) == 0 && !hasMarker(metas, l.sub) {
				return nil, nf()
			}
		} else if l.kind == "shared" && len(metas) == 0 {
			return nil, nf()
		}
		addTree(l, metas, l.sub)
	}
	return out, nil
}

func hasMarker(metas []Meta, dir string) bool {
	for i := range metas {
		if strings.HasPrefix(metas[i].Name, dir+"/") {
			return true
		}
	}
	return false
}

func filterOwner(l []Meta, o string) []Meta {
	var out []Meta
	for _, m := range l {
		if m.Owner == o {
			out = append(out, m)
		}
	}
	return out
}

func (d *davFS) sharedOwners(shared []Meta) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range shared {
		if _, g := GroupOf(m.Owner); !g && !seen[m.Owner] {
			seen[m.Owner] = true
			out = append(out, m.Owner)
		}
	}
	sort.Strings(out)
	return out
}

func (d *davFS) Create(ctx context.Context, name string, body io.ReadCloser, opts *webdav.CreateOptions) (*webdav.FileInfo, bool, error) {
	me := auth.User(ctx)
	seg := parts(name)
	l := resolve(me, seg)
	if (l.kind != "own" && l.kind != "shared" && l.kind != "groups") || l.sub == "" {
		return nil, false, webdav.NewHTTPError(http.StatusForbidden, errors.New("not a file path"))
	}
	old, err := d.s.Meta(ctx, l.owner, l.sub)
	exists := err == nil
	if opts != nil {
		if opts.IfNoneMatch.IsWildcard() && exists {
			return nil, false, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("exists"))
		}
		if opts.IfMatch.IsSet() {
			if !exists {
				return nil, false, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("missing"))
			}
			if ok, _ := opts.IfMatch.MatchETag(old.ETag()); !ok {
				return nil, false, webdav.NewHTTPError(http.StatusPreconditionFailed, errors.New("etag mismatch"))
			}
		}
	}
	m, err := d.s.Put(ctx, me, l.owner, l.sub, body, -1)
	if err != nil {
		return nil, false, dErr(err)
	}
	fi := fileInfo(m, l.davPath(l.sub))
	return &fi, !exists, nil
}

func (d *davFS) RemoveAll(ctx context.Context, name string, _ *webdav.RemoveAllOptions) error {
	me := auth.User(ctx)
	l := resolve(me, parts(name))
	if (l.kind != "own" && l.kind != "shared" && l.kind != "groups") || l.sub == "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("cannot delete here"))
	}
	if _, err := d.s.Meta(ctx, l.owner, l.sub); err == nil {
		return dErr(d.s.Remove(ctx, me, l.owner, l.sub))
	}
	return dErr(d.s.RemoveDir(ctx, me, l.owner, l.sub))
}

func (d *davFS) Mkdir(ctx context.Context, name string) error {
	me := auth.User(ctx)
	l := resolve(me, parts(name))
	if (l.kind != "own" && l.kind != "shared" && l.kind != "groups") || l.sub == "" {
		return webdav.NewHTTPError(http.StatusForbidden, errors.New("cannot create folder here"))
	}
	return dErr(d.s.Mkdir(ctx, me, l.owner, l.sub))
}

func (d *davFS) copyMove(ctx context.Context, name, dest string, move, overwrite bool) (bool, error) {
	me := auth.User(ctx)
	from, to := resolve(me, parts(name)), resolve(me, parts(dest))
	ok := func(l loc) bool { return (l.kind == "own" || l.kind == "groups") && l.sub != "" }
	if !ok(from) || !ok(to) || from.owner != to.owner {
		return false, webdav.NewHTTPError(http.StatusForbidden, errors.New("only inside one own or group folder"))
	}
	_, e := d.s.Meta(ctx, to.owner, to.sub)
	isDir := true
	if _, err := d.s.Meta(ctx, from.owner, from.sub); err == nil {
		isDir = false
	}
	err := d.s.MoveTo(ctx, me, from.owner, from.sub, to.sub, isDir, move, overwrite)
	return e != nil, dErr(err)
}

func (d *davFS) Copy(ctx context.Context, name, dest string, o *webdav.CopyOptions) (bool, error) {
	return d.copyMove(ctx, name, dest, false, o == nil || !o.NoOverwrite)
}

func (d *davFS) Move(ctx context.Context, name, dest string, o *webdav.MoveOptions) (bool, error) {
	return d.copyMove(ctx, name, dest, true, o == nil || !o.NoOverwrite)
}
