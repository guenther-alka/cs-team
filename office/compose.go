package office

// Zusammenfuegen und Text als Dokument speichern (Menue Text). Ziel ist ein neues cs-text-Dokument (Mehrbenutzer) oder ein PDF in der Ablage.
//
//	POST /api/docs/merge     {"sources":[{"kind":"file","owner":"anna","name":"a.pdf"},{"kind":"doc","id":"..."}],"target":"text"|"pdf","name":"..","dir":".."}
//	POST /api/docs/fromtext  {"text":"..","target":"text"|"pdf","name":"..","dir":".."}      (z. B. die Zusammenfassung der KI)
//
// Quellen: Dateien .pdf .docx .txt .cstext aus der Ablage (eigene und freigegebene) und Text-Dokumente; es gelten die Rechte des Benutzers.
// Ziel PDF: PDFs werden direkt verbunden, andere Quellen zuerst als PDF gesetzt (ohne KI). Ziel Text: PDFs werden als Text uebernommen.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"cs-team/auth"
	"cs-team/conv"
	"cs-team/doc"
	"cs-team/files"
	"cs-team/store"
)

type srcRef struct {
	Kind  string `json:"kind"` // file | doc
	Owner string `json:"owner"`
	Name  string `json:"name"`
	ID    string `json:"id"`
}

type loaded struct {
	title string
	pdf   []byte
	paras []conv.Para
}

const maxSources = 20

func fail(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	switch {
	case errors.Is(err, errNotFound):
		code = http.StatusNotFound
	case errors.Is(err, errTooLarge), errors.Is(err, files.ErrTooLarge):
		code = http.StatusRequestEntityTooLarge
	case errors.Is(err, errForbidden):
		code = http.StatusForbidden
	}
	http.Error(w, err.Error(), code)
}

var (
	errNotFound  = errors.New("not found")
	errTooLarge  = errors.New("too large")
	errForbidden = errors.New("no permission")
)

// load liest eine Quelle mit den Rechten des Benutzers; budget zaehlt die Bytes aller Dateien herunter.
func (s *Svc) load(ctx context.Context, me string, src srcRef, budget *int64) (loaded, error) {
	switch src.Kind {
	case "doc":
		m, d, err := s.Hub.Snapshot(ctx, me, src.ID)
		if err != nil || !auth.Can(ctx, doc.Area(m.Type)) {
			return loaded{}, errNotFound
		}
		if m.Type != "text" {
			return loaded{}, errors.New("only text documents can be used: " + m.Name)
		}
		return loaded{title: m.Name, paras: richParas(d)}, nil
	case "file":
		if !auth.Can(ctx, "files") {
			return loaded{}, errForbidden
		}
		ext := strings.ToLower(path.Ext(src.Name))
		if ext != ".pdf" && ext != ".docx" && ext != ".txt" && ext != ".cstext" {
			return loaded{}, errors.New("file type not supported: " + files.Base(src.Name))
		}
		m, rc, err := s.Files.Open(ctx, me, src.Owner, src.Name)
		if err != nil {
			return loaded{}, errNotFound
		}
		defer rc.Close()
		if *budget -= m.Size; *budget < 0 {
			return loaded{}, errTooLarge
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxImport+1))
		if err != nil || len(b) > maxImport {
			return loaded{}, errTooLarge
		}
		l := loaded{title: strings.TrimSuffix(files.Base(src.Name), path.Ext(src.Name))}
		switch ext {
		case ".pdf":
			l.pdf = b
		case ".docx":
			l.paras, err = conv.FromDOCXRich(b)
		case ".txt":
			var p []string
			p, err = conv.FromTXT(b)
			for _, v := range p {
				l.paras = append(l.paras, conv.Para{V: v})
			}
		case ".cstext":
			l.paras, err = conv.FromCSTextRich(b)
		}
		if err != nil {
			return loaded{}, errors.New("cannot read " + files.Base(src.Name))
		}
		return l, nil
	}
	return loaded{}, errors.New("bad source")
}

// pdfParas: Text eines PDFs als Absaetze (Leerzeile trennt Absaetze, Zeilenumbrueche innerhalb werden zu Leerzeichen).
func pdfParas(b []byte, title string) ([]conv.Para, error) {
	txt, err := conv.FromPDF(b)
	switch {
	case errors.Is(err, conv.ErrPDFEncrypted):
		return nil, errors.New("pdf is encrypted: " + title)
	case errors.Is(err, conv.ErrPDFNoText):
		return nil, errors.New("pdf has no readable text (scanned?): " + title)
	case err != nil:
		return nil, errors.New("cannot read " + title)
	}
	var out []conv.Para
	for _, p := range strings.Split(txt, "\n\n") {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			out = append(out, conv.Para{V: p})
		}
	}
	return out, nil
}

// finish legt das Ergebnis an: target "text" = neues Dokument des Benutzers, "pdf" = Datei in seiner Ablage (dir = Ordner, optional).
func (s *Svc) finish(w http.ResponseWriter, r *http.Request, target, name, dir string, paras []conv.Para, pdf []byte) {
	ctx, me := r.Context(), auth.User(r.Context())
	name = strings.TrimSpace(name)
	switch target {
	case "text":
		if !auth.CanWrite(ctx, "text") {
			fail(w, errForbidden)
			return
		}
		if name == "" {
			name = "Text"
		}
		if len(name) > 100 {
			name = name[:100]
		}
		if len(paras) > conv.MaxParas {
			fail(w, errTooLarge)
			return
		}
		id, err := s.Hub.Create(ctx, me, name, "text", items(me, paras, nil))
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": id, "type": "text", "name": name})
	case "pdf":
		if !auth.Can(ctx, "files") {
			fail(w, errForbidden)
			return
		}
		if pdf == nil {
			pdf = conv.ToPDFRich(name, paras)
		}
		full := fileName(strings.TrimSuffix(name, ".pdf"), "pdf")
		if d := strings.Trim(dir, "/"); d != "" {
			full = d + "/" + full
		}
		if !files.ValidName(full) {
			fail(w, errors.New("bad name"))
			return
		}
		if _, err := s.Files.Put(ctx, me, me, full, bytes.NewReader(pdf), int64(len(pdf))); err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, files.ErrTooLarge) {
				code = http.StatusRequestEntityTooLarge
			} else if errors.Is(err, files.ErrDenied) || errors.Is(err, files.ErrBadName) {
				code = http.StatusBadRequest
			}
			http.Error(w, err.Error(), code)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"name": full})
	default:
		fail(w, errors.New("target: text or pdf"))
	}
}

// merge: mehrere Quellen zu einem Dokument oder PDF zusammenfuegen (ohne KI).
func (s *Svc) merge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Sources         []srcRef `json:"sources"`
		Target, Name, Dir string
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil || len(in.Sources) < 2 || len(in.Sources) > maxSources {
		http.Error(w, "2 to 20 sources required", http.StatusBadRequest)
		return
	}
	if in.Target != "text" && in.Target != "pdf" {
		fail(w, errors.New("target: text or pdf"))
		return
	}
	ctx, me := r.Context(), auth.User(r.Context())
	budget := int64(maxImport)
	var ls []loaded
	for _, src := range in.Sources {
		l, err := s.load(ctx, me, src, &budget)
		if err != nil {
			fail(w, err)
			return
		}
		ls = append(ls, l)
	}
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" {
		in.Name = ls[0].title + " (zusammengefuegt)"
	}
	if in.Target == "pdf" {
		parts := make([][]byte, len(ls))
		for i, l := range ls {
			if parts[i] = l.pdf; parts[i] == nil {
				parts[i] = conv.ToPDFRich(l.title, l.paras)
			}
		}
		out, err := conv.MergePDF(parts)
		if err != nil {
			fail(w, err)
			return
		}
		s.finish(w, r, "pdf", in.Name, in.Dir, nil, out)
		return
	}
	var paras []conv.Para
	for i, l := range ls {
		ps := l.paras
		if l.pdf != nil {
			var err error
			if ps, err = pdfParas(l.pdf, l.title); err != nil {
				fail(w, err)
				return
			}
		}
		if i > 0 {
			paras = append(paras, conv.Para{})
		}
		paras = append(paras, conv.BoldPara(l.title))
		paras = append(paras, ps...)
	}
	s.finish(w, r, "text", in.Name, in.Dir, paras, nil)
}

// fromText: Text (z. B. die Zusammenfassung der KI) als neues Dokument oder PDF speichern.
func (s *Svc) fromText(w http.ResponseWriter, r *http.Request) {
	var in struct{ Text, Target, Name, Dir string }
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&in) != nil || strings.TrimSpace(in.Text) == "" || len(in.Text) > 300000 {
		http.Error(w, "text required (max. 300000 characters)", http.StatusBadRequest)
		return
	}
	s.finish(w, r, in.Target, in.Name, in.Dir, conv.ParasFromMarkdown(in.Text), nil)
}
