package files

// Papierkorb: Löschen verschiebt eine Datei in den Papierkorb des Besitzers (Benutzer oder Gruppenordner). Der Eintrag belegt weiter
// Kontingent und wird nach TrashDays Tagen (Einstellung, 0 = Papierkorb aus) oder bei Platzmangel (älteste zuerst) endgültig gelöscht.
//
// Ablage: dieselben Schlüssel wie Dateien, aber unter dem reservierten Namen ".trash/<id>" (ValidName lässt ihn nie zu); die Metadaten
// tragen Trash{Name, By, At}. Freigaben und öffentlicher Link gehen beim Löschen verloren. Zeigt s.List/under/WebDAV nie an.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cs-team/store"
)

const trashDir = ".trash"

type TrashInfo struct {
	Name string `json:"name"` // ursprünglicher Pfad
	By   string `json:"by"`
	At   int64  `json:"at"` // unix s
}

type TrashItem struct {
	ID      string `json:"id"`
	Owner   string `json:"owner"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	By      string `json:"by"`
	At      int64  `json:"at"`
	Expires int64  `json:"expires,omitempty"` // unix s; 0 = ohne Ablauf
}

func isTrash(m *Meta) bool { return m.Trash != nil }

func (s *Svc) trashDays() int {
	if s.TrashDays == nil {
		return 0
	}
	return s.TrashDays()
}

func newTrashID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func trashName(id string) string { return trashDir + "/" + id }

func validTrashID(id string) bool {
	if len(id) != 12 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// trashRemove: Datei in den Papierkorb legen (Rechte hat der Aufrufer schon geprüft).
func (s *Svc) trashRemove(ctx context.Context, actor string, m *Meta) error {
	id := newTrashID()
	tn := trashName(id)
	tm := &Meta{Name: tn, Owner: m.Owner, Size: m.Size, Type: m.Type, Mod: m.Mod, Trash: &TrashInfo{Name: m.Name, By: actor, At: time.Now().Unix()}}
	if err := s.saveMeta(ctx, tm); err != nil { // erst die Metadaten, dann der Inhalt: ein Abbruch hinterlässt höchstens einen leeren Eintrag
		return err
	}
	if err := store.Move(ctx, s.St, dataKey(m.Owner, m.Name), dataKey(m.Owner, tn)); err != nil {
		s.St.Delete(ctx, metaKey(m.Owner, tn))
		s.invalidate()
		return err
	}
	if m.Token != "" {
		s.St.Delete(ctx, tokKey(m.Token))
	}
	s.St.Delete(ctx, metaKey(m.Owner, m.Name))
	s.invalidate()
	return nil
}

func (s *Svc) trashMeta(ctx context.Context, owner, id string) (*Meta, error) {
	if !validTrashID(id) {
		return nil, ErrNotFound
	}
	b, _, err := s.St.Get(ctx, metaKey(owner, trashName(id)))
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var m Meta
	if json.Unmarshal(b, &m) != nil || m.Trash == nil {
		return nil, ErrNotFound
	}
	return &m, nil
}

// Trash: Einträge im Papierkorb, die user verwalten darf (eigene und die der Gruppenordner mit Schreibrecht), neueste zuerst.
func (s *Svc) Trash(ctx context.Context, user string) ([]TrashItem, error) {
	all, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	days := int64(s.trashDays())
	out := []TrashItem{}
	for i := range all {
		m := &all[i]
		if !isTrash(m) || !canManage(m, user) {
			continue
		}
		it := TrashItem{ID: strings.TrimPrefix(m.Name, trashDir+"/"), Owner: m.Owner, Name: m.Trash.Name, Size: m.Size, By: m.Trash.By, At: m.Trash.At}
		if days > 0 {
			it.Expires = m.Trash.At + days*86400
		}
		out = append(out, it)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].At > out[b].At })
	return out, nil
}

// TrashUsed: vom Papierkorb belegte Bytes eines Besitzers.
func (s *Svc) TrashUsed(ctx context.Context, owner string) int64 {
	all, _ := s.all(ctx)
	var n int64
	for _, m := range all {
		if m.Owner == owner && m.Trash != nil {
			n += m.Size
		}
	}
	return n
}

func (s *Svc) dropTrash(ctx context.Context, m *Meta) {
	s.St.Delete(ctx, dataKey(m.Owner, m.Name))
	s.St.Delete(ctx, metaKey(m.Owner, m.Name))
}

// DeleteTrash löscht einen Eintrag endgültig.
func (s *Svc) DeleteTrash(ctx context.Context, actor, owner, id string) error {
	m, err := s.trashMeta(ctx, owner, id)
	if err != nil {
		return err
	}
	if !canManage(m, actor) {
		return ErrNotFound
	}
	s.dropTrash(ctx, m)
	s.invalidate()
	return nil
}

// EmptyTrash leert den Papierkorb des Besitzers (nur Einträge, die actor verwalten darf).
func (s *Svc) EmptyTrash(ctx context.Context, actor, owner string) (int, error) {
	items, err := s.Trash(ctx, actor)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if it.Owner != owner {
			continue
		}
		if err := s.DeleteTrash(ctx, actor, owner, it.ID); err == nil {
			n++
		}
	}
	return n, nil
}

// altName: freier Name "a (2).txt" für eine Wiederherstellung, wenn der ursprüngliche belegt ist.
func (s *Svc) freeName(ctx context.Context, owner, name string) (string, error) {
	ok := func(n string) bool {
		if _, err := s.Meta(ctx, owner, n); err == nil {
			return false
		}
		return s.treeConflict(ctx, owner, n) == nil
	}
	if ok(name) {
		return name, nil
	}
	dir, base := Dir(name), Base(name)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; i < 1000; i++ {
		n := stem + " (" + itoa(i) + ")" + ext
		if dir != "" {
			n = dir + "/" + n
		}
		if ValidName(n) && ok(n) {
			return n, nil
		}
	}
	return "", ErrExists
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// Restore stellt einen Eintrag wieder her (unter dem alten Namen, sonst "name (2).ext") und liefert den Namen.
func (s *Svc) Restore(ctx context.Context, actor, owner, id string) (string, error) {
	m, err := s.trashMeta(ctx, owner, id)
	if err != nil {
		return "", err
	}
	if !canManage(m, actor) {
		return "", ErrNotFound
	}
	name, err := s.freeName(ctx, owner, m.Trash.Name)
	if err != nil {
		return "", err
	}
	if err := store.Move(ctx, s.St, dataKey(owner, m.Name), dataKey(owner, name)); err != nil {
		return "", err
	}
	nm := &Meta{Name: name, Owner: owner, Size: m.Size, Type: contentType(name), Mod: m.Mod}
	if err := s.saveMeta(ctx, nm); err != nil {
		return "", err
	}
	s.St.Delete(ctx, metaKey(owner, m.Name))
	s.invalidate()
	return name, nil
}

// PurgeTrash: abgelaufene Einträge endgültig löschen (days <= 0: Papierkorb ist aus, alles löschen). Liefert die Anzahl.
func (s *Svc) PurgeTrash(ctx context.Context) int {
	days := int64(s.trashDays())
	now := time.Now().Unix()
	all, err := s.all(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for i := range all {
		m := all[i]
		if isTrash(&m) && (days <= 0 || m.Trash.At+days*86400 <= now) {
			s.dropTrash(ctx, &m)
			n++
		}
	}
	if n > 0 {
		s.invalidate()
	}
	return n
}

// RunTrash löscht abgelaufene Einträge beim Start und danach stündlich, bis ctx endet.
func (s *Svc) RunTrash(ctx context.Context) {
	tk := time.NewTicker(time.Hour)
	defer tk.Stop()
	for {
		s.PurgeTrash(ctx)
		select {
		case <-tk.C:
		case <-ctx.Done():
			return
		}
	}
}

// evictTrash: macht Platz für einen Upload, indem die ältesten Papierkorb-Einträge des Besitzers gelöscht werden, bis mindestens need Bytes frei
// geworden sind (need <= 0: der ganze Papierkorb). Liefert die freigegebenen Bytes.
func (s *Svc) evictTrash(ctx context.Context, owner string, need int64) int64 {
	all, err := s.all(ctx)
	if err != nil {
		return 0
	}
	var mine []Meta
	for _, m := range all {
		if m.Owner == owner && isTrash(&m) {
			mine = append(mine, m)
		}
	}
	sort.Slice(mine, func(a, b int) bool { return mine[a].Trash.At < mine[b].Trash.At })
	var freed int64
	for i := range mine {
		if need > 0 && freed >= need {
			break
		}
		s.dropTrash(ctx, &mine[i])
		freed += mine[i].Size
	}
	if freed > 0 || len(mine) > 0 {
		s.invalidate()
	}
	return freed
}
