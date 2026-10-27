package proxy

import (
	"bytes"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

// Body captures a copy of a streamed body: in memory up to Limit bytes, then
// spilled to a temp file. The proxied stream itself is never buffered.
type Body struct {
	Limit int64

	mu   sync.Mutex
	buf  bytes.Buffer
	file *os.File
	size int64
	dir  string
	err  error
}

func newBody(limit int64, tmpDir string) *Body {
	return &Body{Limit: limit, dir: tmpDir}
}

func (b *Body) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.size += int64(len(p))
	if b.err != nil {
		return len(p), nil // capture failure must not break proxying
	}
	if b.file == nil && int64(b.buf.Len()+len(p)) <= b.Limit {
		b.buf.Write(p)
		return len(p), nil
	}
	if b.file == nil {
		f, err := os.CreateTemp(b.dir, "proxypilot-body-*")
		if err != nil {
			b.err = err
			return len(p), nil
		}
		if _, err := f.Write(b.buf.Bytes()); err != nil {
			b.err = err
		}
		b.buf.Reset()
		b.file = f
	}
	if _, err := b.file.Write(p); err != nil {
		b.err = err
	}
	return len(p), nil
}

// Size is the total number of bytes seen.
func (b *Body) Size() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

// InMemory returns the buffered bytes when the body did not spill.
func (b *Body) InMemory() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file != nil {
		return nil, false
	}
	return b.buf.Bytes(), true
}

// SpillPath is the temp file holding the body, if it spilled. The store moves
// or copies it; Close removes it.
func (b *Body) SpillPath() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file == nil {
		return ""
	}
	return b.file.Name()
}

// Open returns a reader over the captured bytes.
func (b *Body) Open() (io.ReadCloser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file == nil {
		return io.NopCloser(bytes.NewReader(append([]byte(nil), b.buf.Bytes()...))), nil
	}
	return os.Open(b.file.Name())
}

func (b *Body) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// Close releases the temp file.
func (b *Body) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file == nil {
		return nil
	}
	name := b.file.Name()
	b.file.Close()
	b.file = nil
	return os.Remove(name)
}

// teeBody streams r while copying into capture and counting bytes.
type teeBody struct {
	r       io.ReadCloser
	capture io.Writer
	n       *int64
	onEOF   func()
	once    sync.Once
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		atomic.AddInt64(t.n, int64(n))
		_, _ = t.capture.Write(p[:n])
	}
	if err == io.EOF && t.onEOF != nil {
		t.once.Do(t.onEOF)
	}
	return n, err
}

func (t *teeBody) Close() error { return t.r.Close() }
