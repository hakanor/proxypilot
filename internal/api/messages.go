package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/hakanor/proxypilot/internal/store"
)

// WSMessage is one WebSocket message in GET /flows/{id}/messages: text payloads as
// "text", anything else as "base64".
type WSMessage struct {
	Seq       int64     `json:"seq"`
	Dir       string    `json:"dir"`
	At        time.Time `json:"at"`
	Opcode    string    `json:"opcode"`
	Size      int64     `json:"size"`
	Text      *string   `json:"text,omitempty"`
	Base64    string    `json:"base64,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
}

// MessagesPage is GET /flows/{id}/messages?after=<seq>&limit=<n>.
type MessagesPage struct {
	Messages []WSMessage `json:"messages"`
	// Next is the after= value for the following page (the last seq returned).
	Next  int64 `json:"next"`
	Total int64 `json:"total"`
}

func ToWSMessage(m store.Message) WSMessage {
	out := WSMessage{Seq: m.Seq, Dir: m.Dir, At: m.At, Opcode: m.Opcode, Size: m.Size, Truncated: m.Truncated}
	if len(m.Payload) > 0 {
		if m.Opcode != "binary" && utf8.Valid(m.Payload) {
			t := string(m.Payload)
			out.Text = &t
		} else {
			out.Base64 = base64.StdEncoding.EncodeToString(m.Payload)
		}
	}
	return out
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	ms, err := s.deps.Store.Messages(r.Context(), id, after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	total, _ := s.deps.Store.MessageCount(r.Context(), id)
	page := MessagesPage{Messages: make([]WSMessage, 0, len(ms)), Next: after, Total: total}
	for _, m := range ms {
		page.Messages = append(page.Messages, ToWSMessage(m))
		page.Next = m.Seq
	}
	writeJSON(w, http.StatusOK, page)
}
