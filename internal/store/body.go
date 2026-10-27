package store

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Opener reopens the captured raw body (it may be read twice when decoding fails).
type Opener func() (io.ReadCloser, error)

// PutBody stores a body for flow/side. raw is the body exactly as it crossed
// the wire; encoding is its Content-Encoding. The decoded form is stored so it
// can be read and searched; if decoding fails the raw bytes are kept instead.
func (s *Store) PutBody(ctx context.Context, flowID, side, encoding string, rawSize int64, open Opener) (BodyInfo, error) {
	info := BodyInfo{FlowID: flowID, Side: side, Encoding: strings.ToLower(strings.TrimSpace(encoding)), RawSize: rawSize}
	path := filepath.Join(s.bodyDir, flowID+"-"+side)

	inline, onDisk, size, truncated, err := s.writeDecoded(open, info.Encoding, path)
	if err != nil {
		info.DecodeError = err.Error()
		inline, onDisk, size, truncated, err = s.writeDecoded(open, "", path)
		if err != nil {
			return info, err
		}
	}
	info.DecodedSize, info.Truncated, info.OnDisk = size, truncated, onDisk

	filePath := ""
	if onDisk {
		filePath = path
		inline = nil
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO bodies(flow_id, side, encoding, inline, file_path, raw_size, decoded_size, truncated, decode_error)
		VALUES(?,?,?,?,?,?,?,?,?)`, flowID, side, info.Encoding, inline, filePath, info.RawSize, size, truncated, info.DecodeError)
	if err != nil && onDisk {
		_ = os.Remove(path)
	}
	return info, err
}

// writeDecoded decodes into memory, spilling to path beyond the inline limit.
func (s *Store) writeDecoded(open Opener, encoding, path string) (inline []byte, onDisk bool, size int64, truncated bool, err error) {
	rc, err := open()
	if err != nil {
		return nil, false, 0, false, err
	}
	defer rc.Close()
	r, closeDec, err := decoder(rc, encoding)
	if err != nil {
		return nil, false, 0, false, err
	}
	defer closeDec()

	w := &spillWriter{limit: s.opts.InlineBodyLimit, path: path}
	n, err := io.Copy(w, io.LimitReader(r, s.opts.MaxBodySize+1))
	if cerr := w.close(); err == nil {
		err = cerr
	}
	if err != nil {
		w.remove()
		return nil, false, 0, false, err
	}
	if n > s.opts.MaxBodySize {
		truncated, n = true, s.opts.MaxBodySize
		if w.file != nil {
			_ = os.Truncate(path, n)
		} else {
			w.buf.Truncate(int(n))
		}
	}
	return w.buf.Bytes(), w.file != nil, n, truncated, nil
}

// decoder applies Content-Encoding in reverse order ("gzip, br" → br then gzip).
func decoder(r io.Reader, encoding string) (io.Reader, func(), error) {
	closers := []func(){}
	closeAll := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	if encoding == "" || encoding == "identity" {
		return r, closeAll, nil
	}
	parts := strings.Split(encoding, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		switch enc := strings.TrimSpace(parts[i]); enc {
		case "", "identity":
		case "gzip", "x-gzip":
			gz, err := gzip.NewReader(r)
			if err != nil {
				closeAll()
				return nil, nil, fmt.Errorf("gzip: %w", err)
			}
			closers = append(closers, func() { gz.Close() })
			r = gz
		case "deflate":
			// Servers send either zlib-wrapped (correct) or raw deflate.
			br := bufioPeek(r)
			if zr, err := zlib.NewReader(br); err == nil {
				closers = append(closers, func() { zr.Close() })
				r = zr
			} else {
				fr := flate.NewReader(br.replay())
				closers = append(closers, func() { fr.Close() })
				r = fr
			}
		case "br":
			r = brotli.NewReader(r)
		case "zstd":
			zr, err := zstd.NewReader(r)
			if err != nil {
				closeAll()
				return nil, nil, fmt.Errorf("zstd: %w", err)
			}
			closers = append(closers, zr.Close)
			r = zr
		default:
			closeAll()
			return nil, nil, fmt.Errorf("unsupported content-encoding %q", enc)
		}
	}
	return r, closeAll, nil
}

// peekReader keeps the first bytes so a failed zlib probe can be replayed.
type peekReader struct {
	r    io.Reader
	head bytes.Buffer
	pos  int
	tee  bool
}

func bufioPeek(r io.Reader) *peekReader { return &peekReader{r: r, tee: true} }

func (p *peekReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if p.tee && n > 0 && p.head.Len() < 64 {
		p.head.Write(b[:n])
	}
	return n, err
}

func (p *peekReader) replay() io.Reader {
	p.tee = false
	return io.MultiReader(bytes.NewReader(p.head.Bytes()), p.r)
}

type spillWriter struct {
	limit int64
	path  string
	buf   bytes.Buffer
	file  *os.File
}

func (w *spillWriter) Write(p []byte) (int, error) {
	if w.file == nil && int64(w.buf.Len()+len(p)) <= w.limit {
		return w.buf.Write(p)
	}
	if w.file == nil {
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, err
		}
		if _, err := f.Write(w.buf.Bytes()); err != nil {
			f.Close()
			return 0, err
		}
		w.buf.Reset()
		w.file = f
	}
	return w.file.Write(p)
}

func (w *spillWriter) close() error {
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}

func (w *spillWriter) remove() {
	if w.file != nil {
		_ = os.Remove(w.path)
	}
}

// BodyInfoFor returns metadata of a stored body.
func (s *Store) BodyInfoFor(ctx context.Context, flowID, side string) (BodyInfo, error) {
	info, _, _, err := s.bodyRow(ctx, flowID, side)
	return info, err
}

func (s *Store) bodyRow(ctx context.Context, flowID, side string) (BodyInfo, []byte, string, error) {
	info := BodyInfo{FlowID: flowID, Side: side}
	var inline []byte
	var path string
	err := s.db.QueryRowContext(ctx, `SELECT encoding, inline, file_path, raw_size, decoded_size, truncated, decode_error
		FROM bodies WHERE flow_id=? AND side=?`, flowID, side).
		Scan(&info.Encoding, &inline, &path, &info.RawSize, &info.DecodedSize, &info.Truncated, &info.DecodeError)
	if errors.Is(err, sql.ErrNoRows) {
		return info, nil, "", ErrNotFound
	}
	info.OnDisk = path != ""
	return info, inline, path, err
}

// OpenBody streams the stored (decoded) body.
func (s *Store) OpenBody(ctx context.Context, flowID, side string) (io.ReadCloser, BodyInfo, error) {
	info, inline, path, err := s.bodyRow(ctx, flowID, side)
	if err != nil {
		return nil, info, err
	}
	if path != "" {
		f, err := os.Open(path)
		return f, info, err
	}
	return io.NopCloser(bytes.NewReader(inline)), info, nil
}

// GetBody reads at most max bytes (≤0 = inline limit); truncated reports the cut.
func (s *Store) GetBody(ctx context.Context, flowID, side string, max int64) ([]byte, bool, BodyInfo, error) {
	if max <= 0 {
		max = s.opts.InlineBodyLimit
	}
	rc, info, err := s.OpenBody(ctx, flowID, side)
	if err != nil {
		return nil, false, info, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, max+1))
	if err != nil {
		return nil, false, info, err
	}
	if int64(len(b)) > max {
		return b[:max], true, info, nil
	}
	return b, info.Truncated, info, nil
}

// Decode undoes a Content-Encoding ("gzip", "br", "gzip, br"…) on an in-memory body.
func Decode(b []byte, encoding string) ([]byte, error) {
	r, done, err := decoder(bytes.NewReader(b), encoding)
	if err != nil {
		return nil, err
	}
	defer done()
	return io.ReadAll(io.LimitReader(r, 64<<20))
}
