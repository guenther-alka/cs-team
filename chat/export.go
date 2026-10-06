package chat

import (
	"archive/zip"
	"context"
	"encoding/json"
	"sort"
	"time"
)

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
	_, err = w.Write(b)
	return err
}
