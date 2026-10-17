package chat

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"

	"cs-team/auth"
)

const (
	maxExportAtts     = 5000
	maxExportAttBytes = int64(2) << 30
)

type exportAtt struct{ g, c, id, name string }

const maxExportMsgs = 10000 // neueste Nachrichten in der Datenauskunft (weitere: Hinweis "truncated")

type exportMsg struct {
	Group   string `json:"group"`
	Channel string `json:"channel"`
	Time    string `json:"time"`
	Text    string `json:"text,omitempty"`
	File    string `json:"file,omitempty"` // Name des Anhangs (der Inhalt ist nicht enthalten)
	Edited  bool   `json:"edited,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// exportVote: Teilnahme an einer Umfrage. Benannt: die gewaehlten Optionen; anonym nur "hat teilgenommen" (die Wahl wird nicht gespeichert).
type exportVote struct {
	Group        string   `json:"group"`
	Channel      string   `json:"channel"`
	Time         string   `json:"time"` // Zeit der Umfrage
	Question     string   `json:"question"`
	Anonymous    bool     `json:"anonymous,omitempty"`
	Participated bool     `json:"participated,omitempty"` // anonym: hat teilgenommen
	Choice       []string `json:"choice,omitempty"`       // benannt: gewaehlte Optionen
}

// ExportUser schreibt chat.json: die eigenen Nachrichten des Benutzers mit Gruppe, Kanal und Zeit (Datenauskunft Art. 15/20).
// Gruppen-Chat ist der einzige Chat in cs-team; Nachrichten anderer Personen sind nicht enthalten.
func (s *Svc) ExportUser(ctx context.Context, user string, zw *zip.Writer) error {
	type row struct {
		id int64
		m  exportMsg
	}
	var rows []row
	var votes []exportVote
	var atts []exportAtt
	for _, k := range s.channelKeys(ctx) {
		c := s.ch(ctx, k[0], k[1])
		c.mu.Lock()
		for _, m := range c.msgs {
			if p := m.Poll; p != nil && !m.Del && p.voted(user) {
				v := exportVote{Group: k[0], Channel: k[1], Time: time.UnixMicro(m.ID).UTC().Format(time.RFC3339), Question: p.Q, Anonymous: p.Anon}
				if p.Anon {
					v.Participated = true
				} else {
					v.Choice = p.choices(user)
				}
				votes = append(votes, v)
			}
			if m.By != user {
				continue
			}
			e := exportMsg{Group: k[0], Channel: k[1], Time: time.UnixMicro(m.ID).UTC().Format(time.RFC3339), Text: m.T, Edited: m.Ed, Deleted: m.Del}
			if m.Att != nil {
				e.File = m.Att.Name
			}
			rows = append(rows, row{m.ID, e})
			if m.Att != nil && !m.Del {
				atts = append(atts, exportAtt{k[0], k[1], m.Att.ID, m.Att.Name})
			}
		}
		c.mu.Unlock()
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	trunc := len(rows) > maxExportMsgs
	if trunc {
		rows = rows[len(rows)-maxExportMsgs:]
	}
	out := make([]exportMsg, len(rows))
	for i, r := range rows {
		out[i] = r.m
	}
	w, err := zw.Create("chat.json")
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(map[string]any{"count": len(out), "truncated": trunc, "messages": out, "votes": votes}, "", " ")
	if _, err = w.Write(b); err != nil {
		return err
	}
	var total int64
	var skipped []string
	for n, a := range atts { // eigene Anhaenge: chat-anhaenge/<gruppe>/<kanal>/<kennung>-<name>
		if n >= maxExportAtts || total >= maxExportAttBytes {
			skipped = append(skipped, a.g+"/"+a.c+"/"+a.name)
			continue
		}
		rc, err := s.St.GetStream(ctx, fileKey(a.g, a.c, a.id))
		if err != nil {
			skipped = append(skipped, a.g+"/"+a.c+"/"+a.name)
			continue
		}
		aw, err := zw.CreateHeader(&zip.FileHeader{Name: "chat-anhaenge/" + auth.ZipSafe(a.g+"/"+a.c+"/"+a.id+"-"+a.name), Method: zip.Deflate})
		if err == nil {
			var c int64
			c, err = io.Copy(aw, rc)
			total += c
		}
		rc.Close()
		if err != nil {
			return err
		}
	}
	if len(skipped) > 0 {
		sw, err := zw.Create("chat-anhaenge/_nicht-enthalten.txt")
		if err != nil {
			return err
		}
		_, err = sw.Write([]byte("Nicht exportiert (nicht mehr vorhanden oder Grenze erreicht):\r\n" + strings.Join(skipped, "\r\n") + "\r\n"))
		return err
	}
	return nil
}
