package graphql

import (
	"strings"

	"github.com/hakanor/proxypilot/internal/store"
)

// ErrorKind marks a 200 response whose GraphQL `errors` is not empty.
const ErrorKind = "graphql_error"

// Likely says whether a request is worth reading for GraphQL (cheap pre-check).
func Likely(f *store.Flow) bool {
	ct := strings.ToLower(f.ReqContentType)
	return strings.Contains(ct, "json") || strings.Contains(ct, "graphql") ||
		(f.Method == "GET" && (strings.Contains(f.Query, "query=") || strings.Contains(f.Query, "extensions=")))
}

// Annotate tags a recorded flow with its GraphQL operations and marks GraphQL
// errors. resp may be nil. It returns false when the request is not GraphQL.
func Annotate(f *store.Flow, req, resp []byte) bool {
	ops := Detect(f.Method, f.URL, f.ReqContentType, req)
	if len(ops) == 0 {
		return false
	}
	if f.Tags == nil {
		f.Tags = map[string]string{}
	}
	f.Tags["gql_op"] = Names(ops)
	f.Tags["gql_type"] = ops[0].Type
	if errs := Errors(resp); len(errs) > 0 && f.ErrorKind == "" {
		f.ErrorKind, f.ErrorMsg = ErrorKind, strings.Join(errs, "; ")
		if len(f.ErrorMsg) > 500 {
			f.ErrorMsg = f.ErrorMsg[:500] + "…"
		}
	}
	return true
}
