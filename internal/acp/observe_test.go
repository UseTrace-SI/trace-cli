package acp

import (
	"strings"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

func observe(t *testing.T, split int, msgs []struct {
	dir  Direction
	line string
}) []event.Event {
	t.Helper()
	var got []event.Event
	o := newObserver("acp:test", func() time.Time { return time.UnixMilli(1) }, func(string, ...any) {})
	o.emit = func(ev event.Event) { got = append(got, ev) }
	for _, m := range msgs {
		data := []byte(m.line + "\n")
		for len(data) > 0 {
			n := split
			if n <= 0 || n > len(data) {
				n = len(data)
			}
			o.feed(chunk{dir: m.dir, data: data[:n]})
			data = data[n:]
		}
	}
	o.finish()
	return got
}

func kinds(evs []event.Event) string {
	var k []string
	for _, ev := range evs {
		k = append(k, string(ev.Kind))
	}
	return strings.Join(k, ",")
}

func TestObserverTurns(t *testing.T) {
	type msg = struct {
		dir  Direction
		line string
	}
	setup := []msg{
		{FromClient, `{"jsonrpc":"2.0","id":"a","method":"session/new","params":{"cwd":"/w"}}`},
		{FromAgent, `{"jsonrpc":"2.0","id":"a","result":{"sessionId":"s"}}`},
	}
	tests := []struct {
		name  string
		msgs  []msg
		want  string
		check func(*testing.T, []event.Event)
	}{
		{"cancelled turn keeps stop reason", append(setup,
			msg{FromClient, `{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s","prompt":[{"type":"text","text":"hi"}]}}`},
			msg{FromClient, `{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"s"}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","id":2,"result":{"stopReason":"cancelled"}}`},
		), "session_start,turn_start,turn_context,turn_end,session_end", func(t *testing.T, evs []event.Event) {
			if evs[2].Text != "(stop reason: cancelled)" {
				t.Fatalf("text = %q", evs[2].Text)
			}
		}},
		{"error response ends turn", append(setup,
			msg{FromClient, `{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s","prompt":[]}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"boom"}}`},
		), "session_start,turn_start,turn_context,turn_end,session_end", nil},
		{"failed session/new records nothing", []msg{
			{FromClient, `{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/w"}}`},
			{FromAgent, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"auth required"}}`},
			{FromClient, `{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s","prompt":[]}}`},
		}, "", nil},
		{"agent request ids do not answer editor requests", append(setup,
			msg{FromClient, `{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s","prompt":[]}}`},
			msg{FromClient, `{"jsonrpc":"2.0","id":2,"result":{"stopReason":"end_turn"}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","id":2,"result":{"stopReason":"end_turn"}}`},
		), "session_start,turn_start,turn_end,session_end", nil},
		{"session/load and batch", []msg{
			{FromClient, `[{"jsonrpc":"2.0","id":1,"method":"session/load","params":{"sessionId":"old","cwd":"/w"}}]`},
			{FromAgent, `{"jsonrpc":"2.0","id":1,"result":null}`},
			{FromClient, `{"jsonrpc":"2.0","id":2,"method":"session/close","params":{"sessionId":"old"}}`},
		}, "session_start,session_end", nil},
		{"execute and failed tools", append(setup,
			msg{FromClient, `{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s","prompt":[]}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"t1","title":"go test","kind":"execute","status":"completed"}}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"t2","title":"edit","kind":"edit","status":"failed"}}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"t3","title":"grep","kind":"search","status":"completed"}}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed"}}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","id":2,"result":{"stopReason":"end_turn"}}`},
		), "session_start,turn_start,tool_edit,turn_end,session_end", func(t *testing.T, evs []event.Event) {
			if evs[2].ToolName != "go test" || len(evs[2].Paths) != 0 {
				t.Fatalf("execute = %+v", evs[2])
			}
		}},
		{"open turn closed on exit", append(setup,
			msg{FromClient, `{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"s","prompt":[]}}`},
			msg{FromAgent, `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"partial"}}}}`},
		), "session_start,turn_start,turn_context,turn_end,session_end", nil},
		{"garbage is ignored", append([]msg{{FromAgent, `not json`}, {FromClient, `{"id":`}, {FromAgent, ``}}, setup...),
			"session_start,session_end", nil},
	}
	for _, tt := range tests {
		for _, split := range []int{0, 1, 7} {
			got := observe(t, split, tt.msgs)
			if kinds(got) != tt.want {
				t.Fatalf("%s (split %d): events %s, want %s", tt.name, split, kinds(got), tt.want)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		}
	}
}

func TestObserverResynchronizesAfterGap(t *testing.T) {
	var got []event.Event
	o := newObserver("acp:test", time.Now, func(string, ...any) {})
	o.emit = func(ev event.Event) { got = append(got, ev) }
	o.feed(chunk{dir: FromClient, data: []byte(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/w"}}` + "\n" + `{"partial`)})
	// The rest of the partial message was dropped; this chunk starts mid-message.
	o.feed(chunk{dir: FromClient, gap: true, data: []byte(`tail"}` + "\n")})
	o.feed(chunk{dir: FromAgent, data: []byte(`{"jsonrpc":"2.0","id":1,"result":{"sessionId":"s"}}` + "\n")})
	if kinds(got) != "session_start" {
		t.Fatalf("events = %s", kinds(got))
	}
}
