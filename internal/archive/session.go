package archive

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/hakanor/proxypilot/internal/store"
	"github.com/hakanor/proxypilot/internal/version"
)

// A session file (.proxypilot) is a zip:
//
//	manifest.json           {"format":"proxypilot-session","version":1,…}
//	flows.ndjson            one store.Flow per line (with headers)
//	bodies/<id>.req|.resp   decoded bodies
const sessionFormat = "proxypilot-session"

type manifest struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Creator   string    `json:"creator"`
	Flows     int       `json:"flows"`
	Redacted  bool      `json:"redacted"`
}

// SaveSession writes the selected flows and their bodies as a session file.
func SaveSession(ctx context.Context, st *store.Store, w io.Writer, o ExportOptions) (int, error) {
	ids, err := collect(ctx, st, o.Filter)
	if err != nil {
		return 0, err
	}
	z := zip.NewWriter(w)
	fw, err := z.Create("flows.ndjson")
	if err != nil {
		return 0, err
	}
	enc := json.NewEncoder(fw)
	type bodyRef struct {
		id, side string
		data     []byte
	}
	var bodies []bodyRef
	n := 0
	for i, id := range ids {
		f, err := st.Get(ctx, id)
		if err != nil {
			continue
		}
		if o.Redact != nil {
			f = o.Redact.Flow(f)
		}
		for _, side := range []string{store.SideReq, store.SideResp} {
			b, _, _, err := st.GetBody(ctx, id, side, maxSessionBody)
			if err != nil || len(b) == 0 {
				continue
			}
			if o.Redact != nil {
				ct := f.RespContentType
				if side == store.SideReq {
					ct = f.ReqContentType
				}
				b = o.Redact.Body(b, ct)
			}
			bodies = append(bodies, bodyRef{id, side, b})
		}
		if err := enc.Encode(f); err != nil {
			return n, err
		}
		n++
		if o.Progress != nil {
			o.Progress(i+1, len(ids))
		}
	}
	for _, b := range bodies {
		bw, err := z.Create("bodies/" + b.id + "." + b.side)
		if err != nil {
			return n, err
		}
		if _, err := bw.Write(b.data); err != nil {
			return n, err
		}
	}
	mw, err := z.Create("manifest.json")
	if err != nil {
		return n, err
	}
	_ = json.NewEncoder(mw).Encode(manifest{Format: sessionFormat, Version: 1, CreatedAt: time.Now().UTC(), Creator: "ProxyPilot " + version.Version, Flows: n, Redacted: o.Redact != nil})
	return n, z.Close()
}

// OpenSession imports a session file into a new session (source=import).
func OpenSession(ctx context.Context, st *store.Store, r io.ReaderAt, size int64) (ImportResult, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return ImportResult{}, fmt.Errorf("not a session file: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	mf, ok := files["manifest.json"]
	if !ok {
		return ImportResult{}, errors.New("not a session file: manifest.json missing")
	}
	var m manifest
	if err := readJSON(mf, &m); err != nil || m.Format != sessionFormat {
		return ImportResult{}, errors.New("not a ProxyPilot session file")
	}
	if m.Version > 1 {
		return ImportResult{}, fmt.Errorf("session file version %d is newer than this ProxyPilot", m.Version)
	}
	ff, ok := files["flows.ndjson"]
	if !ok {
		return ImportResult{}, errors.New("session file has no flows")
	}
	rc, err := ff.Open()
	if err != nil {
		return ImportResult{}, err
	}
	defer rc.Close()
	sess, err := st.AddSession(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{Session: sess.ID}
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		var f store.Flow
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			res.Skipped++
			continue
		}
		orig := f.ID
		if f.Tags == nil {
			f.Tags = map[string]string{}
		}
		f.Tags["original_id"], f.Tags["original_source"], f.Tags["imported_from"] = orig, f.Source, "session"
		f.ID, f.SessionID, f.Source = "", sess.ID, store.SourceImport
		if f.State == store.StatePending || f.State == store.StatePaused {
			f.State = store.StateFailed
		}
		if err := st.Insert(ctx, &f); err != nil {
			res.Skipped++
			continue
		}
		for _, side := range []string{store.SideReq, store.SideResp} {
			bf, ok := files["bodies/"+orig+"."+side]
			if !ok {
				continue
			}
			data, err := readAll(bf)
			if err != nil {
				continue
			}
			info, err := st.PutBody(ctx, f.ID, side, "", int64(len(data)), func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil })
			if err == nil {
				if side == store.SideReq {
					f.ReqSize = info.DecodedSize
				} else {
					f.RespSize = info.DecodedSize
				}
			}
		}
		_ = st.Update(ctx, &f)
		res.Imported++
	}
	return res, sc.Err()
}

func readJSON(f *zip.File, v any) error {
	b, err := readAll(f)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func readAll(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, maxSessionBody+1))
}

// maxSessionBody matches the store's default body cap.
const maxSessionBody = 64 << 20
