package rules

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Different operations on one /graphql endpoint are told apart and mocked separately.
func TestGraphQLOperationMatching(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		io.WriteString(w, "server saw "+string(b))
	}))
	defer up.Close()
	e, _ := New("", nil, nil)
	c := proxyWith(t, e)
	for op, body := range map[string]string{"FeedQuery": `{"data":{"feed":[]}}`, "Like": `{"errors":[{"message":"rate limited"}]}`} {
		if _, err := e.Add(Rule{Type: TypeMapLocal, Enabled: true, Match: Matcher{URL: up.URL + "/graphql", GraphQLOperation: op}, Spec: body2(body)}, 0); err != nil {
			t.Fatal(err)
		}
	}
	post := func(payload string) string {
		resp, err := c.Post(up.URL+"/graphql", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if got := post(`{"query":"query FeedQuery { feed { id } }"}`); got != `{"data":{"feed":[]}}` {
		t.Fatalf("FeedQuery: %s", got)
	}
	if got := post(`{"operationName":"Like","query":"mutation Like { like(id:1) }"}`); got != `{"errors":[{"message":"rate limited"}]}` {
		t.Fatalf("Like: %s", got)
	}
	// Another operation on the same endpoint reaches the server with its body intact.
	other := `{"query":"query Profile { me { id } }"}`
	if got := post(other); got != "server saw "+other {
		t.Fatalf("Profile: %s", got)
	}
}

func body2(s string) []byte { return spec(map[string]any{"body": s}) }
