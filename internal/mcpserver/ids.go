package mcpserver

import (
	"strings"
	"sync"
)

// Flow ids are 26-char ULIDs (~15 tokens each). Agents see the last 8 characters
// (the random part) instead; ids maps them back. Full ids are accepted too.
const shortLen = 8

type ids struct {
	mu   sync.Mutex
	full map[string]string
}

func (x *ids) short(id string) string {
	if len(id) <= shortLen {
		return id
	}
	s := id[len(id)-shortLen:]
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.full == nil {
		x.full = map[string]string{}
	}
	if old, ok := x.full[s]; ok && old != id {
		return id // collision: keep the full id
	}
	x.full[s] = id
	return s
}

func (x *ids) resolve(id string) (string, error) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "#")
	if len(id) != shortLen {
		return id, nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if f, ok := x.full[strings.ToUpper(id)]; ok {
		return f, nil
	}
	return "", errorf("unknown request id %q; take ids from list_requests or search_traffic", id)
}
