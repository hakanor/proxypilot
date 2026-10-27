// Package tui is `proxypilot tui`: a terminal UI over the control API for SSH sessions
// and terminal-only setups. Live list on top, the selected flow's detail below.
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/breakpoints"
	"github.com/hakanor/proxypilot/internal/client"
	"github.com/hakanor/proxypilot/internal/rules"
	"github.com/hakanor/proxypilot/internal/store"
)

// keep is how many flows the list holds (newest).
const keep = 2000

// Run starts the TUI and blocks until the user quits.
func Run(ctx context.Context, c *client.Client) error {
	m := newModel(ctx, c)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	go m.stream(p)
	_, err := p.Run()
	return err
}

type model struct {
	ctx context.Context
	c   *client.Client

	w, h    int
	status  api.StatusInfo
	flows   []*store.Flow // oldest first
	index   map[string]int
	pending []*store.Flow // arrived since the last render tick (render throttling)
	sel     int           // index into visible()
	top     int           // first visible row

	filter    string
	filtering bool

	detail     bool
	detailFor  string
	detailText string
	scroll     int

	help   bool
	notice string
	err    string
}

func newModel(ctx context.Context, c *client.Client) *model {
	return &model{ctx: ctx, c: c, index: map[string]int{}}
}

// Messages.
type (
	tickMsg     struct{}
	statusMsg   api.StatusInfo
	initialMsg  []*store.Flow
	flowMsg     *store.Flow
	detailMsg   struct{ id, text string }
	noticeMsg   string
	errMsg      error
	editDoneMsg struct {
		p    *breakpoints.Paused
		path string
		err  error
	}
)

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.loadInitial, m.loadStatus, tick())
}

// tick drives rendering of streamed flows at most 10 times a second, so a burst of
// traffic doesn't make the screen flicker.
func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) loadInitial() tea.Msg {
	p, err := m.c.ListFlows(m.ctx, url.Values{"limit": {"500"}})
	if err != nil {
		return errMsg(err)
	}
	fs := p.Flows
	for i, j := 0, len(fs)-1; i < j; i, j = i+1, j-1 { // API is newest first
		fs[i], fs[j] = fs[j], fs[i]
	}
	return initialMsg(fs)
}

func (m *model) loadStatus() tea.Msg {
	st, err := m.c.Status(m.ctx)
	if err != nil {
		return errMsg(err)
	}
	return statusMsg(st)
}

// stream forwards live flow events into the program until the context ends.
func (m *model) stream(p *tea.Program) {
	for m.ctx.Err() == nil {
		_ = m.c.Stream(m.ctx, url.Values{"events": {"flow.*"}}, func(e client.StreamEvent) bool {
			if r, ok := e.Flow(); ok {
				p.Send(flowMsg(rowToFlow(r)))
			}
			return m.ctx.Err() == nil
		})
		select {
		case <-m.ctx.Done():
		case <-time.After(2 * time.Second): // reconnect
		}
	}
}

func (m *model) visible() []*store.Flow {
	if m.filter == "" {
		return m.flows
	}
	q := strings.ToLower(m.filter)
	var out []*store.Flow
	for _, f := range m.flows {
		if strings.Contains(strings.ToLower(f.Method+" "+f.URL+" "+fmt.Sprint(f.Status)), q) {
			out = append(out, f)
		}
	}
	return out
}

func (m *model) selected() *store.Flow {
	v := m.visible()
	if m.sel >= 0 && m.sel < len(v) {
		return v[m.sel]
	}
	return nil
}

func (m *model) upsert(f *store.Flow) {
	if i, ok := m.index[f.ID]; ok {
		m.flows[i] = f
		return
	}
	m.flows = append(m.flows, f)
	m.index[f.ID] = len(m.flows) - 1
	if len(m.flows) > keep {
		drop := len(m.flows) - keep
		m.flows = m.flows[drop:]
		m.index = make(map[string]int, len(m.flows))
		for i, x := range m.flows {
			m.index[x.ID] = i
		}
		m.sel = max(0, m.sel-drop)
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case initialMsg:
		for _, f := range msg {
			m.upsert(f)
		}
		m.sel = max(0, len(m.visible())-1)
	case flowMsg:
		m.pending = append(m.pending, msg)
	case tickMsg:
		if len(m.pending) > 0 {
			follow := m.sel >= len(m.visible())-1
			for _, f := range m.pending {
				m.upsert(f)
			}
			m.pending = m.pending[:0]
			if follow {
				m.sel = max(0, len(m.visible())-1)
			}
		}
		return m, tick()
	case statusMsg:
		m.status = api.StatusInfo(msg)
		m.err = ""
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return m.loadStatus() })
	case detailMsg:
		if msg.id == m.detailFor {
			m.detailText = msg.text
		}
	case noticeMsg:
		m.notice = string(msg)
	case errMsg:
		m.err = msg.Error()
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return m.loadStatus() })
	case execMsg:
		return m, msg.cmd
	case editDoneMsg:
		return m, m.finishEdit(msg)
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *model) key(k tea.KeyMsg) tea.Cmd {
	if m.filtering {
		switch k.Type {
		case tea.KeyEnter, tea.KeyEsc:
			m.filtering = false
		case tea.KeyBackspace:
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			m.filter += k.String()
		}
		m.sel = max(0, len(m.visible())-1)
		return nil
	}
	if m.help {
		m.help = false
		return nil
	}
	m.notice = ""
	n := len(m.visible())
	switch k.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "?":
		m.help = true
	case "/":
		m.filtering = true
	case "esc":
		if m.detail {
			m.detail = false
		} else {
			m.filter = ""
		}
	case "up", "k":
		if m.detail && m.scroll > 0 {
			m.scroll--
		} else {
			m.sel = max(0, m.sel-1)
		}
	case "down", "j":
		m.sel = min(n-1, m.sel+1)
	case "pgup":
		m.sel = max(0, m.sel-m.listHeight())
	case "pgdown":
		m.sel = min(n-1, m.sel+m.listHeight())
	case "g", "home":
		m.sel = 0
	case "G", "end":
		m.sel = n - 1
	case "J":
		m.scroll++
	case "K":
		m.scroll = max(0, m.scroll-1)
	case "enter":
		m.detail = !m.detail
	case "r":
		if f := m.selected(); f != nil {
			return m.repeat(f)
		}
	case "b":
		if f := m.selected(); f != nil {
			return m.breakpoint(f)
		}
	case "c":
		if f := m.selected(); f != nil {
			return m.copyCurl(f)
		}
	case "x":
		return m.clear
	case "p":
		return m.editPaused
	}
	if m.detail {
		return m.loadDetail()
	}
	return nil
}

func (m *model) loadDetail() tea.Cmd {
	f := m.selected()
	if f == nil || f.ID == m.detailFor && m.detailText != "" {
		return nil
	}
	m.detailFor, m.detailText, m.scroll = f.ID, "loading…", 0
	id := f.ID
	return func() tea.Msg {
		d, err := m.c.GetFlow(m.ctx, id)
		if err != nil {
			return detailMsg{id, err.Error()}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s  %s  %s\n", d.Method, d.URL, statusText(&d.Flow), d.Protocol)
		fmt.Fprintf(&b, "client %s · %s · total %s\n\n", d.ClientAddr, d.CreatedAt.Local().Format("15:04:05"), ms(d.Timing.Total))
		b.WriteString("REQUEST HEADERS\n")
		for _, h := range d.ReqHeaders {
			fmt.Fprintf(&b, "  %s: %s\n", h.Name, h.Value)
		}
		b.WriteString("\nRESPONSE HEADERS\n")
		for _, h := range d.RespHeaders {
			fmt.Fprintf(&b, "  %s: %s\n", h.Name, h.Value)
		}
		if body, err := m.c.GetBody(m.ctx, id, "resp", 16<<10); err == nil && body.Data != "" {
			b.WriteString("\nRESPONSE BODY\n")
			b.WriteString(pretty(body.Data))
			if body.Truncated {
				b.WriteString("\n… (truncated, see the web UI or `proxypilot show --body`)")
			}
		}
		return detailMsg{id, b.String()}
	}
}

func (m *model) repeat(f *store.Flow) tea.Cmd {
	return func() tea.Msg {
		s, err := m.c.Repeat(m.ctx, f.ID, api.RepeatRequest{})
		if err != nil {
			return noticeMsg("repeat: " + err.Error())
		}
		if len(s.Results) > 0 {
			r := s.Results[0]
			if r.Error != "" {
				return noticeMsg("repeated: " + r.Error)
			}
			return noticeMsg(fmt.Sprintf("repeated: %d in %s", r.Status, ms(float64(r.Ms))))
		}
		return noticeMsg("repeated")
	}
}

func (m *model) breakpoint(f *store.Flow) tea.Cmd {
	pattern := strings.SplitN(f.URL, "?", 2)[0] + "*"
	return func() tea.Msg {
		_, err := m.c.AddRule(m.ctx, api.RuleInput{Rule: rules.Rule{
			Name: f.Method + " " + f.Host + f.Path, Type: rules.TypeBreakpoint, Enabled: true, Owner: rules.OwnerUser,
			Phase: "request", Match: rules.Matcher{URL: pattern, Methods: []string{f.Method}},
		}})
		if err != nil {
			return noticeMsg("breakpoint: " + err.Error())
		}
		return noticeMsg("breakpoint on " + pattern + " — press p when a request pauses")
	}
}

func (m *model) copyCurl(f *store.Flow) tea.Cmd {
	return func() tea.Msg {
		s, _, err := m.c.Curl(m.ctx, f.ID)
		if err != nil {
			return noticeMsg("curl: " + err.Error())
		}
		if err := copyToClipboard(s); err != nil {
			return noticeMsg("no clipboard (" + err.Error() + "): " + s)
		}
		return noticeMsg("cURL copied (secrets masked)")
	}
}

func (m *model) clear() tea.Msg {
	if err := m.c.Clear(m.ctx); err != nil {
		return noticeMsg("clear: " + err.Error())
	}
	return initialMsg(nil)
}

// editPaused opens the oldest paused flow in $EDITOR as JSON. Saving resumes it with the
// edits; leaving it unchanged continues it as is; emptying the file aborts it.
func (m *model) editPaused() tea.Msg {
	list, err := m.c.ListBreakpoints(m.ctx)
	if err != nil {
		return noticeMsg("breakpoints: " + err.Error())
	}
	var p *breakpoints.Paused
	for _, x := range list {
		if x.Owner == rules.OwnerUser {
			p = x
			break
		}
	}
	if p == nil {
		return noticeMsg("no paused request")
	}
	var v any = p.Request
	if p.Phase == "response" && p.Response != nil {
		v = p.Response
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	f, err := os.CreateTemp("", "proxypilot-*.json")
	if err != nil {
		return noticeMsg(err.Error())
	}
	f.Write(b)
	f.Close()
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], f.Name())...)
	return execMsg{tea.ExecProcess(cmd, func(err error) tea.Msg { return editDoneMsg{p, f.Name(), err} })}
}

// execMsg carries a tea.Cmd out of a plain message func.
type execMsg struct{ cmd tea.Cmd }

func (m *model) finishEdit(e editDoneMsg) tea.Cmd {
	return func() tea.Msg {
		defer os.Remove(e.path)
		if e.err != nil {
			return noticeMsg("editor: " + e.err.Error())
		}
		b, err := os.ReadFile(e.path)
		if err != nil {
			return noticeMsg(err.Error())
		}
		res := breakpoints.Resolution{Action: "modify"}
		var orig []byte
		if e.p.Phase == "response" && e.p.Response != nil {
			orig, _ = json.MarshalIndent(e.p.Response, "", "  ")
		} else {
			orig, _ = json.MarshalIndent(e.p.Request, "", "  ")
		}
		switch {
		case len(bytes.TrimSpace(b)) == 0:
			res.Action = "abort"
		case bytes.Equal(bytes.TrimSpace(b), bytes.TrimSpace(orig)):
			res.Action = "continue"
		case e.p.Phase == "response":
			res.Response = &breakpoints.Response{}
			if err := json.Unmarshal(b, res.Response); err != nil {
				return noticeMsg("not valid JSON, request still paused: " + err.Error())
			}
		default:
			res.Request = &breakpoints.Request{}
			if err := json.Unmarshal(b, res.Request); err != nil {
				return noticeMsg("not valid JSON, request still paused: " + err.Error())
			}
		}
		if err := m.c.ResumeBreakpoint(m.ctx, e.p.ID, res); err != nil {
			return noticeMsg("resume: " + err.Error())
		}
		return noticeMsg("breakpoint: " + res.Action)
	}
}

// ---- view ----

var (
	stBar    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236")).Padding(0, 1)
	stHead   = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)
	stSel    = lipgloss.NewStyle().Background(lipgloss.Color("22")).Foreground(lipgloss.Color("255"))
	stDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	stOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	stWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	stErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	stAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	stBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
)

func (m *model) listHeight() int {
	h := m.h - 3 // status bar, header, footer
	if m.detail {
		h = max(3, h/2)
	}
	return max(1, h)
}

func (m *model) View() string {
	if m.w == 0 {
		return "starting…"
	}
	if m.help {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(m.statusBar() + "\n")

	// Columns adapt to the width; the URL takes what is left.
	v := m.visible()
	lh := m.listHeight()
	if m.sel >= len(v) {
		m.sel = max(0, len(v)-1)
	}
	if m.top > max(0, len(v)-lh) { // the list got shorter (filter, clear): fill the window
		m.top = max(0, len(v)-lh)
	}
	if m.sel < m.top {
		m.top = m.sel
	}
	if m.sel >= m.top+lh {
		m.top = m.sel - lh + 1
	}
	wTime, wMethod, wStatus, wDur, wSize := 8, 7, 6, 8, 8
	if m.w < 90 {
		wTime, wSize = 0, 0
	}
	wURL := max(10, m.w-wTime-wMethod-wStatus-wDur-wSize-6)
	head := fmt.Sprintf("%s%-*s %-*s %-*s %*s", col(wTime, "TIME"), wMethod, "METHOD", wStatus, "STATUS", wURL, "URL", wDur, "DURATION")
	if wSize > 0 {
		head += fmt.Sprintf(" %*s", wSize, "SIZE")
	}
	b.WriteString(stHead.Render(fit(head, m.w)) + "\n")
	for i := m.top; i < min(len(v), m.top+lh); i++ {
		f := v[i]
		line := fmt.Sprintf("%s%-*s %s %-*s %*s", col(wTime, f.CreatedAt.Local().Format("15:04:05")), wMethod, fit(f.Method, wMethod),
			pad(statusStyled(f, i == m.sel), wStatus), wURL, fit(f.URL, wURL), wDur, ms(f.Timing.Total))
		if wSize > 0 {
			line += fmt.Sprintf(" %*s", wSize, size(f.RespSize))
		}
		if i == m.sel {
			line = stSel.Render(fit(line, m.w))
		}
		b.WriteString(line + "\n")
	}
	for i := min(len(v), m.top+lh) - m.top; i < lh; i++ {
		b.WriteString("\n")
	}
	if m.detail {
		b.WriteString(stBorder.Render(strings.Repeat("─", m.w)) + "\n")
		lines := strings.Split(m.detailText, "\n")
		dh := m.h - lh - 4
		m.scroll = min(m.scroll, max(0, len(lines)-dh))
		for i := m.scroll; i < min(len(lines), m.scroll+dh); i++ {
			b.WriteString(fit(lines[i], m.w) + "\n")
		}
		for i := min(len(lines), m.scroll+dh) - m.scroll; i < dh; i++ {
			b.WriteString("\n")
		}
	}
	b.WriteString(m.footer())
	return b.String()
}

func (m *model) statusBar() string {
	s := m.status
	parts := []string{stAccent.Render("ProxyPilot")}
	if m.err != "" {
		parts = append(parts, stErr.Render("not connected: "+m.err))
	} else {
		parts = append(parts, "proxy "+s.ProxyAddr, fmt.Sprintf("%d flows", s.Flows), fmt.Sprintf("%d rules", s.ActiveRules))
		if s.PendingBreakpoints > 0 {
			parts = append(parts, stWarn.Render(fmt.Sprintf("%d paused (p)", s.PendingBreakpoints)))
		}
		if s.Throttle != "" {
			parts = append(parts, stWarn.Render("throttle "+s.Throttle))
		}
		if s.NoCaching {
			parts = append(parts, "no caching")
		}
		if !s.Recording {
			parts = append(parts, stWarn.Render("recording paused"))
		}
	}
	return stBar.Width(m.w).Render(fit(strings.Join(parts, "  ·  "), m.w-2))
}

func (m *model) footer() string {
	switch {
	case m.filtering:
		return "/" + m.filter + "█"
	case m.notice != "":
		return stOK.Render(fit(m.notice, m.w))
	case m.filter != "":
		return stDim.Render(fit(fmt.Sprintf("filter: %s (%d)  esc clears · ? help", m.filter, len(m.visible())), m.w))
	}
	return stDim.Render(fit("enter detail · / filter · r repeat · b breakpoint · p edit paused · c cURL · x clear · ? help · q quit", m.w))
}

func (m *model) helpView() string {
	keys := [][2]string{
		{"↑/k ↓/j", "move (in detail: ↑/k scrolls when not at top)"},
		{"pgup pgdown g G", "page, first, last"},
		{"enter", "show / hide the detail pane"},
		{"J K", "scroll the detail pane"},
		{"/", "filter by method, URL or status; esc clears"},
		{"r", "repeat the selected request"},
		{"b", "add a request breakpoint for the selected URL"},
		{"p", "edit the oldest paused request in $EDITOR (save = send edits, unchanged = continue, empty = abort)"},
		{"c", "copy as cURL (secrets masked)"},
		{"x", "clear all captured traffic"},
		{"q", "quit"},
	}
	var b strings.Builder
	b.WriteString(stAccent.Render("ProxyPilot TUI — keys") + "\n\n")
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("  %-18s %s\n", k[0], fit(k[1], max(10, m.w-22))))
	}
	b.WriteString("\n" + stDim.Render("any key to close"))
	return b.String()
}

// ---- helpers ----

func statusText(f *store.Flow) string {
	switch {
	case f.Status > 0:
		return fmt.Sprint(f.Status)
	case f.ErrorKind != "":
		return "ERR"
	case f.State == store.StatePending:
		return "…"
	}
	return f.State
}

func statusStyled(f *store.Flow, selected bool) string {
	t := statusText(f)
	if selected {
		return t
	}
	switch {
	case f.ErrorKind != "" || f.Status >= 500:
		return stErr.Render(t)
	case f.Status >= 400:
		return stWarn.Render(t)
	case f.Status > 0:
		return stOK.Render(t)
	}
	return stDim.Render(t)
}

func col(w int, s string) string {
	if w == 0 {
		return ""
	}
	return fmt.Sprintf("%-*s ", w, fit(s, w))
}

// pad right-pads a possibly styled string to w visible cells.
func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// fit cuts s to w visible cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w-1 {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// ms formats a duration given in milliseconds.
func ms(v float64) string {
	switch {
	case v <= 0:
		return "—"
	case v < 1000:
		return fmt.Sprintf("%.0f ms", v)
	}
	return fmt.Sprintf("%.1f s", v/1000)
}

// rowToFlow keeps what the list shows from a streamed row.
func rowToFlow(r api.FlowRow) *store.Flow {
	f := &store.Flow{ID: r.ID, Method: r.Method, Scheme: r.Scheme, Host: r.Host, Path: r.Path, URL: r.URL, Status: r.Status,
		State: r.State, RespSize: r.RespSize, ErrorKind: r.ErrorKind, CreatedAt: r.CreatedAt}
	f.Timing.Total = r.TotalMs
	return f
}

func size(n int64) string {
	switch {
	case n <= 0:
		return "—"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

func pretty(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			return string(b)
		}
	}
	return s
}

func copyToClipboard(s string) error {
	for _, c := range [][]string{{"pbcopy"}, {"wl-copy"}, {"xclip", "-selection", "clipboard"}} {
		if _, err := exec.LookPath(c[0]); err == nil {
			cmd := exec.Command(c[0], c[1:]...)
			cmd.Stdin = strings.NewReader(s)
			return cmd.Run()
		}
	}
	return fmt.Errorf("pbcopy / wl-copy / xclip not found")
}
