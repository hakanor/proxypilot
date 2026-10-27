package proxy

// Chain calls several Hooks in order. For OnRequest/OnResponse the first
// non-Continue decision wins and later hooks are skipped, so put observers
// (the recorder) before deciders (rules, breakpoints).
type Chain []Hooks

func (c Chain) OnRequest(f *Flow) Decision {
	for _, h := range c {
		if d := h.OnRequest(f); d.Action != Continue {
			return d
		}
	}
	return Decision{}
}

func (c Chain) OnResponse(f *Flow) Decision {
	for _, h := range c {
		if d := h.OnResponse(f); d.Action != Continue {
			return d
		}
	}
	return Decision{}
}

func (c Chain) OnComplete(f *Flow) {
	for _, h := range c {
		h.OnComplete(f)
	}
}

func (c Chain) OnError(f *Flow, err error) {
	for _, h := range c {
		h.OnError(f, err)
	}
}
