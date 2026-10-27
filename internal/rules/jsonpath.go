package rules

import (
	"fmt"
	"strconv"
	"strings"
)

// A small JSON path: $.a.b[0].c, $["odd key"], $.items[-1] (last element).
type pathSeg struct {
	key   string
	index int
	isIdx bool
}

func parsePath(p string) ([]pathSeg, error) {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("JSON path must start with $: %q", p)
	}
	var segs []pathSeg
	for i := 1; i < len(p); {
		switch p[i] {
		case '.':
			j := i + 1
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				j++
			}
			if j == i+1 {
				return nil, fmt.Errorf("empty key in %q", p)
			}
			segs = append(segs, pathSeg{key: p[i+1 : j]})
			i = j
		case '[':
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("unclosed [ in %q", p)
			}
			inner := p[i+1 : i+j]
			if q := strings.Trim(inner, `"'`); len(inner) >= 2 && (inner[0] == '"' || inner[0] == '\'') {
				segs = append(segs, pathSeg{key: q})
			} else {
				n, err := strconv.Atoi(inner)
				if err != nil {
					return nil, fmt.Errorf("bad index %q in %q", inner, p)
				}
				segs = append(segs, pathSeg{index: n, isIdx: true})
			}
			i += j + 1
		default:
			return nil, fmt.Errorf("unexpected %q at %d in %q", p[i], i, p)
		}
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("JSON path %q selects the whole document", p)
	}
	return segs, nil
}

func idx(n, length int) int {
	if n < 0 {
		return length + n
	}
	return n
}

// jsonSet sets the value at path, creating missing objects along the way.
func jsonSet(doc any, segs []pathSeg, val any) (any, error) {
	if len(segs) == 0 {
		return val, nil
	}
	s := segs[0]
	if s.isIdx {
		arr, ok := doc.([]any)
		if !ok {
			return nil, fmt.Errorf("[%d] on a non-array", s.index)
		}
		i := idx(s.index, len(arr))
		switch {
		case i == len(arr): // append
			arr = append(arr, nil)
		case i < 0 || i > len(arr):
			return nil, fmt.Errorf("index %d out of range (length %d)", s.index, len(arr))
		}
		v, err := jsonSet(arr[i], segs[1:], val)
		if err != nil {
			return nil, err
		}
		arr[i] = v
		return arr, nil
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		if doc != nil {
			return nil, fmt.Errorf(".%s on a non-object", s.key)
		}
		obj = map[string]any{}
	}
	v, err := jsonSet(obj[s.key], segs[1:], val)
	if err != nil {
		return nil, err
	}
	obj[s.key] = v
	return obj, nil
}

// jsonRemove deletes the value at path; a missing path is not an error.
func jsonRemove(doc any, segs []pathSeg) any {
	if len(segs) == 0 || doc == nil {
		return doc
	}
	s, last := segs[0], len(segs) == 1
	if s.isIdx {
		arr, ok := doc.([]any)
		if !ok {
			return doc
		}
		i := idx(s.index, len(arr))
		if i < 0 || i >= len(arr) {
			return doc
		}
		if last {
			return append(arr[:i:i], arr[i+1:]...)
		}
		arr[i] = jsonRemove(arr[i], segs[1:])
		return arr
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return doc
	}
	if last {
		delete(obj, s.key)
	} else if v, ok := obj[s.key]; ok {
		obj[s.key] = jsonRemove(v, segs[1:])
	}
	return obj
}
