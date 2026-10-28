package api

import (
	"errors"
	"net/http"

	"github.com/hakanor/proxypilot/internal/diff"
	"github.com/hakanor/proxypilot/internal/store"
)

// diffBodyMax caps how much of each body is compared.
const diffBodyMax = 1 << 20

// handleDiff is GET /diff?a=<id>&b=<id>: what changed from flow a to flow b.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("a") == "" || q.Get("b") == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "a and b (flow ids) are required")
		return
	}
	redact := wantRedact(r)
	var flows [2]*store.Flow
	var bodies [4][]byte // reqA, respA, reqB, respB
	cut := false
	for i, id := range []string{q.Get("a"), q.Get("b")} {
		f, err := s.deps.Store.Get(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "flow "+id+" not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		for j, side := range []string{store.SideReq, store.SideResp} {
			data, truncated, _, err := s.deps.Store.GetBody(r.Context(), f.ID, side, diffBodyMax)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
			if redact {
				ct := f.ReqContentType
				if side == store.SideResp {
					ct = f.RespContentType
				}
				data = s.redactor().Body(data, ct)
			}
			bodies[i*2+j], cut = data, cut || truncated
		}
		if redact {
			f = s.redactor().Flow(f)
		}
		flows[i] = f
	}
	writeJSON(w, http.StatusOK, diff.Flows(flows[0], flows[1], diff.Bodies{
		ReqA: bodies[0], RespA: bodies[1], ReqB: bodies[2], RespB: bodies[3], Truncated: cut,
	}))
}
