// Package store persists flows in SQLite (WAL) with large bodies on disk.
// Every query is bounded: listings always carry a LIMIT.
package store

import "time"

const (
	DefaultLimit = 50
	MaxLimit     = 500
)

// Flow states.
const (
	StatePending   = "pending"
	StateCompleted = "completed"
	StateFailed    = "failed"
	StateTunneled  = "tunneled"
	StatePaused    = "paused"
)

// Sources.
const (
	SourceProxy    = "proxy"
	SourceCDP      = "cdp"
	SourceComposer = "composer"
	SourceImport   = "import"
)

// Flow is the stored form of one exchange. It is independent of the proxy
// package so imports (HAR, CDP) can create flows too.
type Flow struct {
	ID              string            `json:"id"`
	SessionID       string            `json:"session_id"`
	Source          string            `json:"source"`
	ClientAddr      string            `json:"client_addr"`
	ClientLabel     string            `json:"client_label,omitempty"`
	Method          string            `json:"method"`
	Scheme          string            `json:"scheme"`
	Host            string            `json:"host"`
	Port            int               `json:"port"`
	Path            string            `json:"path"`
	Query           string            `json:"query,omitempty"`
	URL             string            `json:"url"`
	Status          int               `json:"status,omitempty"`
	ReqContentType  string            `json:"req_content_type,omitempty"`
	RespContentType string            `json:"resp_content_type,omitempty"`
	ReqSize         int64             `json:"req_size"`   // decoded body bytes
	RespSize        int64             `json:"resp_size"`  // decoded body bytes
	BytesUp         int64             `json:"bytes_up"`   // on the wire, client → upstream
	BytesDown       int64             `json:"bytes_down"` // on the wire, upstream → client
	Protocol        string            `json:"protocol,omitempty"`
	State           string            `json:"state"`
	ErrorKind       string            `json:"error_kind,omitempty"`
	ErrorMsg        string            `json:"error_msg,omitempty"`
	Timing          Timing            `json:"timing"`
	Tags            map[string]string `json:"tags,omitempty"`
	MatchedRules    []string          `json:"matched_rules,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`

	// Filled by Get only.
	ReqHeaders  []Header `json:"req_headers,omitempty"`
	RespHeaders []Header `json:"resp_headers,omitempty"`
}

// Timing in milliseconds.
type Timing struct {
	DNS     float64 `json:"dns_ms"`
	Connect float64 `json:"connect_ms"`
	TLS     float64 `json:"tls_ms"`
	TTFB    float64 `json:"ttfb_ms"`
	Total   float64 `json:"total_ms"`
	Reused  bool    `json:"reused,omitempty"`
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Sides of a flow.
const (
	SideReq  = "req"
	SideResp = "resp"
)

// BodyInfo describes a stored body without its bytes.
type BodyInfo struct {
	FlowID      string `json:"flow_id"`
	Side        string `json:"side"`
	Encoding    string `json:"encoding,omitempty"` // original Content-Encoding
	RawSize     int64  `json:"raw_size"`
	DecodedSize int64  `json:"decoded_size"`
	Truncated   bool   `json:"truncated"`
	OnDisk      bool   `json:"on_disk"`
	DecodeError string `json:"decode_error,omitempty"`
}

type Session struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
}
