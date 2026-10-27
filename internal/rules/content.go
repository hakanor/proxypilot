package rules

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// MaxContent caps what the editor reads or writes for a Map Local response.
const MaxContent = 10 << 20

// Content is the response a Map Local rule serves, as the Rules editor shows it.
type Content struct {
	Source   string `json:"source"` // file | inline | base64 | dir
	Path     string `json:"path,omitempty"`
	Content  string `json:"content"`
	Size     int    `json:"size"`
	Editable bool   `json:"editable"`
	// Reason says why it can't be edited (folder rules, binary or huge files).
	Reason string `json:"reason,omitempty"`
}

var ErrNotMapLocal = errors.New("rule is not a map_local rule")

func (e *Engine) mapLocal(id string) (*Rule, *MapLocal, error) {
	r, err := e.Get(id)
	if err != nil {
		return nil, nil, err
	}
	if r.Type != TypeMapLocal {
		return nil, nil, ErrNotMapLocal
	}
	m, err := parseMapLocal(r.Spec)
	if err != nil {
		return nil, nil, err
	}
	return r, m, nil
}

// MapLocalContent reads the response a Map Local rule serves.
func (e *Engine) MapLocalContent(id string) (*Content, error) {
	_, m, err := e.mapLocal(id)
	if err != nil {
		return nil, err
	}
	switch {
	case m.Dir != "":
		return &Content{Source: "dir", Path: m.Dir, Reason: "folder rules map many files; edit them in the folder"}, nil
	case m.Body != nil:
		return &Content{Source: "inline", Content: *m.Body, Size: len(*m.Body), Editable: true}, nil
	case m.BodyBase64 != "":
		b, _ := base64.StdEncoding.DecodeString(m.BodyBase64)
		return text(&Content{Source: "base64"}, b), nil
	}
	c := &Content{Source: "file", Path: m.File}
	st, err := os.Stat(expandHome(m.File))
	if err != nil {
		c.Reason = err.Error()
		return c, nil
	}
	if st.Size() > MaxContent {
		c.Size, c.Reason = int(st.Size()), "file is larger than 10 MB"
		return c, nil
	}
	b, err := os.ReadFile(expandHome(m.File))
	if err != nil {
		c.Reason = err.Error()
		return c, nil
	}
	return text(c, b), nil
}

func text(c *Content, b []byte) *Content {
	c.Size = len(b)
	if !utf8.Valid(b) {
		c.Reason = "binary content"
		return c
	}
	c.Content, c.Editable = string(b), true
	return c
}

// SetMapLocalContent saves new response text: into the file for file rules,
// into the rule itself for inline (and base64) rules.
func (e *Engine) SetMapLocalContent(id, content string) (*Content, error) {
	if len(content) > MaxContent {
		return nil, invalid("content is larger than 10 MB")
	}
	r, m, err := e.mapLocal(id)
	if err != nil {
		return nil, err
	}
	switch {
	case m.Dir != "":
		return nil, invalid("folder rules can't be edited here")
	case m.File != "":
		p := expandHome(m.File)
		tmp, err := os.CreateTemp(filepath.Dir(p), ".pp-*")
		if err != nil {
			return nil, err
		}
		_, werr := tmp.WriteString(content)
		cerr := tmp.Close()
		if werr == nil {
			werr = cerr
		}
		if werr == nil {
			_ = os.Chmod(tmp.Name(), 0o644)
			werr = os.Rename(tmp.Name(), p)
		}
		if werr != nil {
			os.Remove(tmp.Name())
			return nil, werr
		}
	default:
		m.Body, m.BodyBase64 = &content, ""
		spec, _ := json.Marshal(m)
		next := *r
		next.Spec = spec
		if _, err := e.Update(id, next); err != nil {
			return nil, err
		}
	}
	return e.MapLocalContent(id)
}
