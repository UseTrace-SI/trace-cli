package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

const (
	// maxLine is the largest message the observer parses. Larger messages are
	// still forwarded; they are only not recorded.
	maxLine = 32 << 20
	// maxContext bounds the response, reasoning and plan text kept per turn.
	maxContext  = 64 << 10
	maxToolName = 200
)

// Method names from the ACP v1 schema (schema/v1/meta.json).
const (
	methodNewSession    = "session/new"
	methodLoadSession   = "session/load"
	methodResumeSession = "session/resume"
	methodCloseSession  = "session/close"
	methodPrompt        = "session/prompt"
	methodUpdate        = "session/update"
	methodWriteFile     = "fs/write_text_file"
	methodReadFile      = "fs/read_text_file"
)

// message is the JSON-RPC 2.0 envelope. Only the members Trace reads are decoded.
type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	URI      string `json:"uri"`
	Resource *struct {
		URI string `json:"uri"`
	} `json:"resource"`
}

type toolCallContent struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

type location struct {
	Path string `json:"path"`
}

type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
	ToolCallID    string          `json:"toolCallId"`
	Title         *string         `json:"title"`
	Name          *string         `json:"name"`
	Kind          *string         `json:"kind"`
	Status        *string         `json:"status"`
	Locations     []location      `json:"locations"`
	Entries       []planEntry     `json:"entries"`
}

type planEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
}

type pending struct {
	method    string
	sessionID string
	cwd       string
}

type toolState struct {
	title, name, kind, status string
	paths                     []string
	diff                      bool
	recorded                  bool
}

type turn struct {
	response, thinking, plan strings.Builder
	tools                    map[string]*toolState
	order                    []string
}

type session struct {
	cwd  string
	turn *turn
	// promptID is the open session/prompt request, keyed like pending.
	promptID string
}

type lineBuffer struct {
	data     []byte
	skipping bool
}

// observer turns the two message streams into Trace events. It runs on one
// goroutine; nothing in it is shared with the forwarding path.
type observer struct {
	agent    string
	now      func() time.Time
	logf     func(string, ...any)
	emit     func(event.Event)
	lines    [2]lineBuffer
	requests [2]map[string]pending
	sessions map[string]*session
}

func newObserver(agent string, now func() time.Time, logf func(string, ...any)) *observer {
	return &observer{
		agent:    agent,
		now:      now,
		logf:     logf,
		requests: [2]map[string]pending{{}, {}},
		sessions: map[string]*session{},
	}
}

func (o *observer) run(queue <-chan chunk, done <-chan struct{}, emit func(event.Event)) {
	o.emit = emit
	for {
		select {
		case c := <-queue:
			o.feed(c)
		case <-done:
			for {
				select {
				case c := <-queue:
					o.feed(c)
				default:
					o.finish()
					return
				}
			}
		}
	}
}

// feed splits a chunk into newline-delimited messages (ACP stdio transport).
func (o *observer) feed(c chunk) {
	lb := &o.lines[c.dir]
	if c.gap {
		o.logf("observer fell behind; skipping to the next message")
		lb.data = lb.data[:0]
		lb.skipping = true
	}
	data := c.data
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			if !lb.skipping {
				if len(lb.data)+len(data) > maxLine {
					o.logf("message larger than %d bytes not recorded", maxLine)
					lb.data = lb.data[:0]
					lb.skipping = true
				} else {
					lb.data = append(lb.data, data...)
				}
			}
			return
		}
		if lb.skipping {
			lb.skipping = false
		} else {
			line := data[:i]
			if len(lb.data) > 0 {
				if len(lb.data)+i > maxLine {
					o.logf("message larger than %d bytes not recorded", maxLine)
					line = nil
				} else {
					lb.data = append(lb.data, line...)
					line = lb.data
				}
			}
			if line != nil {
				o.line(c.dir, line)
			}
			lb.data = lb.data[:0]
		}
		data = data[i+1:]
	}
}

func (o *observer) line(dir Direction, line []byte) {
	defer func() {
		if r := recover(); r != nil {
			o.logf("observing message: panic: %v", r)
		}
	}()
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	if line[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(line, &batch); err != nil {
			o.logf("unparsable message from %s: %v", dirName(dir), err)
			return
		}
		for _, m := range batch {
			o.message(dir, m)
		}
		return
	}
	o.message(dir, line)
}

func dirName(dir Direction) string {
	if dir == FromClient {
		return "editor"
	}
	return "agent"
}

func (o *observer) message(dir Direction, raw []byte) {
	var m message
	if err := json.Unmarshal(raw, &m); err != nil {
		o.logf("unparsable message from %s: %v", dirName(dir), err)
		return
	}
	id := string(bytes.TrimSpace(m.ID))
	if id == "null" {
		id = ""
	}
	switch {
	case m.Method != "" && id != "":
		o.request(dir, id, m.Method, m.Params)
	case m.Method != "":
		o.notification(dir, m.Method, m.Params)
	case id != "":
		// A response answers a request sent in the other direction.
		other := FromAgent
		if dir == FromAgent {
			other = FromClient
		}
		p, ok := o.requests[other][id]
		if !ok {
			return
		}
		delete(o.requests[other], id)
		o.response(id, p, m.Result, len(m.Error) > 0 && string(m.Error) != "null")
	}
}

func (o *observer) request(dir Direction, id, method string, params json.RawMessage) {
	var p struct {
		SessionID string         `json:"sessionId"`
		Cwd       string         `json:"cwd"`
		Prompt    []contentBlock `json:"prompt"`
		Path      string         `json:"path"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			o.logf("unparsable %s params: %v", method, err)
			return
		}
	}
	switch method {
	case methodNewSession, methodLoadSession, methodResumeSession:
		if dir == FromClient {
			o.requests[dir][id] = pending{method: method, sessionID: p.SessionID, cwd: p.Cwd}
		}
	case methodPrompt:
		if dir != FromClient {
			return
		}
		s := o.sessions[p.SessionID]
		if s == nil {
			o.logf("prompt for unknown session")
			return
		}
		o.endTurn(p.SessionID, s, "")
		o.requests[dir][id] = pending{method: method, sessionID: p.SessionID}
		s.turn = &turn{tools: map[string]*toolState{}}
		s.promptID = id
		o.send(event.Event{Kind: event.TurnStart, AgentSessionID: p.SessionID, Worktree: s.cwd, Prompt: promptText(p.Prompt)})
	case methodCloseSession:
		if s := o.sessions[p.SessionID]; dir == FromClient && s != nil {
			o.endTurn(p.SessionID, s, "")
			o.send(event.Event{Kind: event.SessionEnd, AgentSessionID: p.SessionID, Worktree: s.cwd})
			delete(o.sessions, p.SessionID)
		}
	case methodWriteFile:
		if s := o.sessions[p.SessionID]; dir == FromAgent && s != nil && s.turn != nil && p.Path != "" {
			o.send(event.Event{Kind: event.ToolEdit, AgentSessionID: p.SessionID, Worktree: s.cwd, Paths: []string{p.Path}})
		}
	case methodReadFile:
		if s := o.sessions[p.SessionID]; dir == FromAgent && s != nil && s.turn != nil && p.Path != "" {
			o.send(event.Event{Kind: event.FileRead, AgentSessionID: p.SessionID, Worktree: s.cwd, Paths: []string{p.Path}})
		}
	}
}

func (o *observer) response(id string, p pending, result json.RawMessage, failed bool) {
	switch p.method {
	case methodNewSession, methodLoadSession, methodResumeSession:
		if failed {
			return
		}
		sessionID := p.sessionID
		if p.method == methodNewSession {
			var r struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(result, &r); err != nil || r.SessionID == "" {
				o.logf("session/new response without a session id")
				return
			}
			sessionID = r.SessionID
		}
		if sessionID == "" || p.cwd == "" {
			return
		}
		if _, ok := o.sessions[sessionID]; !ok {
			o.sessions[sessionID] = &session{cwd: p.cwd}
			o.send(event.Event{Kind: event.SessionStart, AgentSessionID: sessionID, Worktree: p.cwd})
		}
	case methodPrompt:
		s := o.sessions[p.sessionID]
		if s == nil || s.promptID != id {
			return
		}
		var r struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(result, &r)
		if failed {
			r.StopReason = "error"
		}
		o.endTurn(p.sessionID, s, r.StopReason)
	}
}

func (o *observer) notification(dir Direction, method string, params json.RawMessage) {
	if method == methodUpdate && dir == FromAgent {
		var n struct {
			SessionID string        `json:"sessionId"`
			Update    sessionUpdate `json:"update"`
		}
		if err := json.Unmarshal(params, &n); err != nil {
			o.logf("unparsable session/update: %v", err)
			return
		}
		s := o.sessions[n.SessionID]
		if s == nil || s.turn == nil {
			return
		}
		o.update(n.SessionID, s, n.Update)
	}
}

func (o *observer) update(sessionID string, s *session, u sessionUpdate) {
	t := s.turn
	switch u.SessionUpdate {
	case "agent_message_chunk":
		appendBounded(&t.response, chunkText(u.Content))
	case "agent_thought_chunk":
		appendBounded(&t.thinking, chunkText(u.Content))
	case "plan":
		t.plan.Reset()
		for _, e := range u.Entries {
			appendBounded(&t.plan, fmt.Sprintf("- [%s] %s\n", e.Status, e.Content))
		}
	case "tool_call", "tool_call_update":
		if u.ToolCallID == "" {
			return
		}
		tool := t.tools[u.ToolCallID]
		if tool == nil {
			tool = &toolState{}
			t.tools[u.ToolCallID] = tool
			t.order = append(t.order, u.ToolCallID)
		}
		if u.Title != nil {
			tool.title = *u.Title
		}
		if u.Name != nil {
			tool.name = *u.Name
		}
		if u.Kind != nil {
			tool.kind = *u.Kind
		}
		if u.Status != nil {
			tool.status = *u.Status
		}
		for _, l := range u.Locations {
			tool.addPath(l.Path)
		}
		var content []toolCallContent
		if len(u.Content) > 0 && json.Unmarshal(u.Content, &content) == nil {
			for _, c := range content {
				if c.Type == "diff" {
					tool.diff = true
					tool.addPath(c.Path)
				}
			}
		}
		if tool.status == "completed" || tool.status == "failed" {
			o.recordTool(sessionID, s, tool)
		}
	}
}

func (t *toolState) addPath(p string) {
	if p == "" {
		return
	}
	for _, have := range t.paths {
		if have == p {
			return
		}
	}
	t.paths = append(t.paths, p)
}

func (t *toolState) label() string {
	name := t.name
	if name == "" {
		name = t.title
	}
	if name == "" {
		name = t.kind
	}
	return textutil.TruncateBytes(name, maxToolName)
}

// recordTool records a finished tool call once. Edits, and commands whose
// effects are unknown, become tool_edit events (no paths asks the engine to
// scan the worktree); reads become file_read events.
func (o *observer) recordTool(sessionID string, s *session, tool *toolState) {
	if tool.recorded {
		return
	}
	tool.recorded = true
	base := event.Event{AgentSessionID: sessionID, Worktree: s.cwd, ToolName: tool.label()}
	switch {
	case tool.diff || tool.kind == "edit" || tool.kind == "delete" || tool.kind == "move":
		if tool.status == "failed" && !tool.diff {
			return
		}
		base.Kind, base.Paths = event.ToolEdit, tool.paths
	case tool.kind == "execute" || tool.kind == "other" || tool.kind == "":
		if tool.status == "failed" {
			return
		}
		base.Kind = event.ToolEdit
	case tool.kind == "read":
		if len(tool.paths) == 0 {
			return
		}
		base.Kind, base.Paths, base.ToolName = event.FileRead, tool.paths, ""
	default:
		return
	}
	o.send(base)
}

func (o *observer) endTurn(sessionID string, s *session, stopReason string) {
	t := s.turn
	if t == nil {
		return
	}
	s.turn, s.promptID = nil, ""
	for _, id := range t.order {
		if tool := t.tools[id]; tool.diff {
			o.recordTool(sessionID, s, tool)
		}
	}
	base := event.Event{AgentSessionID: sessionID, Worktree: s.cwd}
	if text := t.thinking.String(); strings.TrimSpace(text) != "" {
		ev := base
		ev.Kind, ev.ContextType, ev.Text = event.TurnContext, "thinking", text
		o.send(ev)
	}
	if text := t.plan.String(); text != "" {
		ev := base
		ev.Kind, ev.ContextType, ev.Text = event.TurnContext, "plan", "Plan:\n"+text
		o.send(ev)
	}
	response := t.response.String()
	if stopReason != "" && stopReason != "end_turn" {
		if response != "" {
			response += "\n"
		}
		response += "(stop reason: " + stopReason + ")"
	}
	if strings.TrimSpace(response) != "" {
		ev := base
		ev.Kind, ev.ContextType, ev.Text = event.TurnContext, "response", response
		o.send(ev)
	}
	ev := base
	ev.Kind = event.TurnEnd
	o.send(ev)
}

// finish closes open turns and sessions when the agent exits.
func (o *observer) finish() {
	for id, s := range o.sessions {
		o.endTurn(id, s, "")
		o.send(event.Event{Kind: event.SessionEnd, AgentSessionID: id, Worktree: s.cwd})
	}
}

func (o *observer) send(ev event.Event) {
	ev.Agent = o.agent
	ev.At = o.now().UnixMilli()
	o.emit(ev)
}

func appendBounded(b *strings.Builder, s string) {
	if remaining := maxContext - b.Len(); remaining > 0 {
		b.WriteString(textutil.TruncateBytes(s, remaining))
	}
}

func chunkText(raw json.RawMessage) string {
	var c contentBlock
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil || c.Type != "text" {
		return ""
	}
	return c.Text
}

// promptText keeps the user's text and names attached resources by URI;
// embedded resource contents are not recorded.
func promptText(blocks []contentBlock) string {
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "resource_link":
			parts = append(parts, "@"+b.URI)
		case "resource":
			if b.Resource != nil && b.Resource.URI != "" {
				parts = append(parts, "@"+b.Resource.URI)
			}
		}
	}
	return textutil.TruncateBytes(strings.Join(parts, "\n"), maxContext)
}
