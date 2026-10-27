package store

import (
	"context"
	"time"
)

// Message is one WebSocket message of a flow (table ws_messages).
type Message struct {
	FlowID    string    `json:"flow_id"`
	Seq       int64     `json:"seq"`
	Dir       string    `json:"dir"` // "up" client → server, "down" server → client
	At        time.Time `json:"at"`
	Opcode    string    `json:"opcode"` // text | binary | close | ping | pong
	Size      int64     `json:"size"`   // full payload size (after permessage-deflate)
	Payload   []byte    `json:"payload,omitempty"`
	Truncated bool      `json:"truncated,omitempty"` // Payload holds only the first part
}

const messagesSchema = `
CREATE TABLE IF NOT EXISTS ws_messages (
	flow_id   TEXT NOT NULL,
	seq       INTEGER NOT NULL,
	dir       TEXT NOT NULL,
	at        INTEGER NOT NULL,
	opcode    TEXT NOT NULL,
	size      INTEGER NOT NULL,
	payload   BLOB,
	truncated INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (flow_id, seq)
);
`

// InsertMessage appends a message; Seq must be set by the caller (per flow, increasing).
func (s *Store) InsertMessage(ctx context.Context, m Message) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO ws_messages (flow_id, seq, dir, at, opcode, size, payload, truncated) VALUES (?,?,?,?,?,?,?,?)`,
		m.FlowID, m.Seq, m.Dir, m.At.UnixNano(), m.Opcode, m.Size, m.Payload, m.Truncated)
	return err
}

// Messages returns up to limit messages of a flow with seq > after, oldest first.
func (s *Store) Messages(ctx context.Context, flowID string, after int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, dir, at, opcode, size, payload, truncated FROM ws_messages WHERE flow_id=? AND seq>? ORDER BY seq LIMIT ?`,
		flowID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		m := Message{FlowID: flowID}
		var at int64
		if err := rows.Scan(&m.Seq, &m.Dir, &at, &m.Opcode, &m.Size, &m.Payload, &m.Truncated); err != nil {
			return nil, err
		}
		m.At = time.Unix(0, at)
		out = append(out, m)
	}
	return out, rows.Err()
}

// MessageCount is how many messages a flow has.
func (s *Store) MessageCount(ctx context.Context, flowID string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ws_messages WHERE flow_id=?`, flowID).Scan(&n)
	return n, err
}
