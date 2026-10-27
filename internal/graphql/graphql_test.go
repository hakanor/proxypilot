package graphql

import "testing"

func TestDetect(t *testing.T) {
	cases := []struct {
		name, method, url, ct, body, want, typ string
	}{
		{"named query", "POST", "https://x/graphql", "application/json", `{"query":"query FeedQuery($p:Int){ feed(page:$p){ id } }","variables":{"p":2}}`, "FeedQuery", "query"},
		{"operationName wins", "POST", "https://x/graphql", "application/json", `{"operationName":"Like","query":"mutation L { like(id:1) }"}`, "Like", "mutation"},
		{"anonymous", "POST", "https://x/graphql", "application/json", `{"query":"{ me { id } }"}`, "anonymous", "query"},
		{"comment and fragment first", "POST", "https://x/graphql", "application/json", `{"query":"# hi\nfragment F on User { id }\nsubscription OnMsg { msg { ...F } }"}`, "OnMsg", "subscription"},
		{"application/graphql", "POST", "https://x/graphql", "application/graphql", `query Me { me { id } }`, "Me", "query"},
		{"GET", "GET", "https://x/graphql?query=query%20Search%7Bs%7D&operationName=Search", "", "", "Search", "query"},
		{"persisted", "POST", "https://x/graphql", "application/json", `{"operationName":"Feed","extensions":{"persistedQuery":{"version":1,"sha256Hash":"abc"}}}`, "Feed", "query"},
		{"batched", "POST", "https://x/graphql", "application/json", `[{"query":"query A{a}"},{"query":"mutation B{b}"}]`, "A,B", "query"},
	}
	for _, c := range cases {
		ops := Detect(c.method, c.url, c.ct, []byte(c.body))
		if len(ops) == 0 || Names(ops) != c.want || ops[0].Type != c.typ {
			t.Errorf("%s: %+v", c.name, ops)
		}
	}
	for _, body := range []string{`{"user":"x"}`, `not json`, `{"query":"not graphql"}`, ``} {
		if ops := Detect("POST", "https://x/api", "application/json", []byte(body)); ops != nil {
			t.Errorf("%q detected as GraphQL: %+v", body, ops)
		}
	}
}

func TestErrors(t *testing.T) {
	if e := Errors([]byte(`{"data":null,"errors":[{"message":"Not authorized"}]}`)); len(e) != 1 || e[0] != "Not authorized" {
		t.Fatal(e)
	}
	if e := Errors([]byte(`[{"data":{}},{"errors":[{"message":"a"},{"message":"b"}]}]`)); len(e) != 2 {
		t.Fatal(e)
	}
	if e := Errors([]byte(`{"data":{"me":{"id":1}}}`)); e != nil {
		t.Fatal(e)
	}
}
