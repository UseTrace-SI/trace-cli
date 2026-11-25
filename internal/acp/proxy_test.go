package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/event"
)

// The test binary doubles as the fake ACP agent: Run starts os.Args[0] with
// ACP_FAKE_AGENT set.
func TestMain(m *testing.M) {
	if mode := os.Getenv("ACP_FAKE_AGENT"); mode != "" {
		os.Exit(fakeAgent(mode))
	}
	os.Exit(m.Run())
}

// fakeAgent answers a scripted session taken from the ACP v1 protocol
// documentation (initialization, session setup, prompt turn, tool calls).
// It copies every byte it receives to ACP_FAKE_IN and every byte it sends to
// ACP_FAKE_OUT so tests can compare both sides of the proxy.
func fakeAgent(mode string) int {
	in, _ := os.Create(os.Getenv("ACP_FAKE_IN"))
	out, _ := os.Create(os.Getenv("ACP_FAKE_OUT"))
	defer in.Close()
	defer out.Close()
	w := io.MultiWriter(os.Stdout, out)
	send := func(s string) { _, _ = io.WriteString(w, s+"\n") }
	r := bufio.NewReaderSize(io.TeeReader(os.Stdin, in), 1<<20)
	cwd := os.Getenv("ACP_FAKE_CWD")
	if mode == "bigexit" {
		// Exit right after writing more than the pipe buffers.
		send(`{"jsonrpc":"2.0","method":"big","params":"` + strings.Repeat("y", 200001) + `"}`)
		return 0
	}
	if mode == "tree" {
		child := exec.Command("sleep", "30")
		if child.Start() == nil {
			_ = os.WriteFile(os.Getenv("ACP_FAKE_OUT")+".pid", []byte(fmt.Sprint(child.Process.Pid)), 0o600)
		}
		mode = "echo"
	}
	if mode == "grandchild" {
		// A process the agent started keeps the agent's stdout open.
		child := exec.Command("sleep", "30")
		child.Stdout = os.Stdout
		if child.Start() == nil {
			_ = os.WriteFile(os.Getenv("ACP_FAKE_OUT")+".pid", []byte(fmt.Sprint(child.Process.Pid)), 0o600)
		}
		send(`{"jsonrpc":"2.0","method":"hello"}`)
		return 0
	}
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var m struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			_ = json.Unmarshal(line, &m)
			id := string(m.ID)
			switch m.Method {
			case "initialize":
				if mode == "crash" {
					fmt.Fprintln(os.Stderr, "fake agent crashing")
					return 3
				}
				send(`{"jsonrpc":"2.0","id":` + id + `,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"promptCapabilities":{"image":true,"audio":true,"embeddedContext":true}},"authMethods":[]}}`)
			case "session/new":
				send(`{"jsonrpc":"2.0","id":` + id + `,"result":{"sessionId":"sess_abc123def456"}}`)
			case "session/prompt":
				if mode == "malformed" {
					send(`this is not json {`)
					send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":42}}}}`)
				}
				if mode == "huge" {
					big := strings.Repeat("x", 8<<20)
					send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"` + big + `"}}}}`)
				}
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"plan","entries":[{"content":"Check for syntax errors","priority":"high","status":"pending"},{"content":"Identify potential type issues","priority":"medium","status":"pending"}]}}}`)
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"The user wants a review."}}}}`)
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"agent_message_chunk","messageId":"msg_agent_c42b9","content":{"type":"text","text":"I'll analyze your code for potential issues. "}}}}`)
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"tool_call","toolCallId":"call_001","title":"Reading configuration file","name":"read_file","kind":"read","status":"pending","locations":[{"path":"` + cwd + `/README.md"}]}}}`)
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"tool_call_update","toolCallId":"call_001","status":"completed","content":[{"type":"content","content":{"type":"text","text":"Found 3 configuration files..."}}]}}}`)
				send(`{"jsonrpc":"2.0","id":5,"method":"session/request_permission","params":{"sessionId":"sess_abc123def456","toolCall":{"toolCallId":"call_002"},"options":[{"optionId":"allow-once","name":"Allow once","kind":"allow_once"},{"optionId":"reject-once","name":"Reject","kind":"reject_once"}]}}`)
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"tool_call","toolCallId":"call_002","title":"Editing main.go","kind":"edit","status":"in_progress","locations":[{"path":"` + cwd + `/main.go","line":1}]}}}`)
				_ = os.WriteFile(filepath.Join(cwd, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600)
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"tool_call_update","toolCallId":"call_002","status":"completed","content":[{"type":"diff","path":"` + cwd + `/main.go","oldText":"package main\n","newText":"package main\n\nfunc main() {}\n"}]}}}`)
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"tool_call","toolCallId":"call_003","title":"Write .env","kind":"edit","status":"completed","content":[{"type":"diff","path":"` + cwd + `/.env","newText":"TOKEN=x"}]}}}`)
				send(`{"jsonrpc":"2.0","id":7,"method":"fs/write_text_file","params":{"sessionId":"sess_abc123def456","path":"` + cwd + `/notes.txt","content":"notes"}}`)
				// Like a real agent, wait for the editor to confirm the write.
				for {
					reply, err := r.ReadBytes('\n')
					if bytes.Contains(reply, []byte(`"id":7`)) || err != nil {
						break
					}
				}
				send(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done: main.go now has a main function."}}}}`)
				send(`{"jsonrpc":"2.0","id":` + id + `,"result":{"stopReason":"end_turn"}}`)
			case "echo":
				send(strings.TrimRight(string(line), "\n"))
			}
		}
		if err != nil {
			if mode == "ignore-eof" {
				time.Sleep(time.Minute)
			}
			if mode == "exit7" {
				return 7
			}
			return 0
		}
	}
}

type recorder struct {
	mu     sync.Mutex
	events []event.Event
}

func (r *recorder) sink(ev event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) kinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, ev := range r.events {
		out = append(out, string(ev.Kind))
	}
	return out
}

// client is a fake editor: it writes requests and reads agent messages
// through the proxy, answering the agent's requests like Zed would.
type client struct {
	t     *testing.T
	in    *io.PipeWriter
	out   *bufio.Reader
	wrote bytes.Buffer
	read  bytes.Buffer
}

func (c *client) send(s string) {
	c.t.Helper()
	c.wrote.WriteString(s + "\n")
	if _, err := io.WriteString(c.in, s+"\n"); err != nil {
		c.t.Fatalf("writing to proxy: %v", err)
	}
}

// until reads messages until one carries the given id and no method.
func (c *client) until(id string) {
	c.t.Helper()
	for {
		line, err := c.out.ReadBytes('\n')
		c.read.Write(line)
		if err != nil {
			c.t.Fatalf("reading from proxy before response %s: %v", id, err)
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		switch {
		case m.Method == "session/request_permission":
			c.send(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":{"outcome":{"outcome":"selected","optionId":"allow-once"}}}`)
		case m.Method == "fs/write_text_file":
			c.send(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":null}`)
		case m.Method == "" && string(m.ID) == id:
			return
		}
	}
}

type harness struct {
	t         *testing.T
	prompt    string
	dir       string
	client    *client
	rec       *recorder
	sink      func(event.Event)
	signals   chan os.Signal
	stopGrace time.Duration
	result    chan error
	stdout    *io.PipeReader
	logMu     sync.Mutex
	logs      []string
	stderr    bytes.Buffer
	stdinIn   *io.PipeReader
}

func startProxy(t *testing.T, mode string, setup ...func(*harness)) *harness {
	t.Helper()
	dir := t.TempDir()
	cwd := filepath.Join(dir, "project")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACP_FAKE_AGENT", mode)
	t.Setenv("ACP_FAKE_IN", filepath.Join(dir, "agent-in"))
	t.Setenv("ACP_FAKE_OUT", filepath.Join(dir, "agent-out"))
	t.Setenv("ACP_FAKE_CWD", cwd)
	h := &harness{t: t, dir: cwd, prompt: "Can you analyze this code for potential issues?", rec: &recorder{}, result: make(chan error, 1)}
	for _, f := range setup {
		f(h)
	}
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	h.stdout, h.stdinIn = stdoutR, stdinR
	h.client = &client{t: t, in: stdinW, out: bufio.NewReaderSize(stdoutR, 1<<20)}
	cfg := Config{
		Args: []string{os.Args[0]}, Name: "acp:fake",
		Stdin: stdinR, Stdout: stdoutW, Stderr: &lockedBuffer{b: &h.stderr, mu: &h.logMu},
		Signals: h.signals, StopGrace: h.stopGrace,
		Sink: func(ev event.Event) {
			if h.sink != nil {
				h.sink(ev)
				return
			}
			h.rec.sink(ev)
		},
		Logf: func(f string, a ...any) {
			h.logMu.Lock()
			h.logs = append(h.logs, fmt.Sprintf(f, a...))
			h.logMu.Unlock()
		},
	}
	go func() {
		err := Run(cfg)
		_ = stdoutW.Close()
		h.result <- err
	}()
	return h
}

type lockedBuffer struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (h *harness) wait() error {
	h.t.Helper()
	// Drain anything left so the proxy can finish writing.
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(&h.client.read, h.client.out); close(drained) }()
	select {
	case err := <-h.result:
		<-drained
		return err
	case <-time.After(20 * time.Second):
		h.t.Fatal("proxy did not exit")
		return nil
	}
}

func (h *harness) session() {
	c := h.client
	c.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true,"writeTextFile":true},"terminal":true},"clientInfo":{"name":"zed","title":"Zed","version":"0.200.0"}}}`)
	c.until("0")
	c.send(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"` + h.dir + `","mcpServers":[{"name":"filesystem","command":"/path/to/mcp-server","args":["--stdio"],"env":[]}]}}`)
	c.until("1")
	c.send(`{"jsonrpc":"2.0","id":2,"method":"session/prompt","params":{"sessionId":"sess_abc123def456","prompt":[{"type":"text","text":"` + h.prompt + `"},{"type":"resource","resource":{"uri":"file:///home/user/project/main.py","mimeType":"text/x-python","text":"def process_data(items):\n    for item in items:\n        print(item)"}}]}}`)
	c.until("2")
}

func TestProxyForwardsByteForByteAndRecordsTurn(t *testing.T) {
	h := startProxy(t, "script")
	h.session()
	_ = h.client.in.Close()
	if err := h.wait(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	agentIn, _ := os.ReadFile(filepath.Join(filepath.Dir(h.dir), "agent-in"))
	agentOut, _ := os.ReadFile(filepath.Join(filepath.Dir(h.dir), "agent-out"))
	if !bytes.Equal(agentIn, h.client.wrote.Bytes()) {
		t.Fatalf("editor->agent bytes differ:\nsent %q\ngot  %q", h.client.wrote.String(), agentIn)
	}
	if !bytes.Equal(agentOut, h.client.read.Bytes()) {
		t.Fatalf("agent->editor bytes differ:\nsent %q\ngot  %q", agentOut, h.client.read.String())
	}
	want := []string{"session_start", "turn_start", "file_read", "tool_edit", "tool_edit", "tool_edit", "turn_context", "turn_context", "turn_context", "turn_end", "session_end"}
	if got := h.rec.kinds(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
	ev := h.rec.events
	for _, e := range ev {
		if e.Agent != "acp:fake" || e.AgentSessionID != "sess_abc123def456" || e.Worktree != h.dir || e.At == 0 {
			t.Fatalf("event identity: %+v", e)
		}
	}
	if ev[1].Prompt != "Can you analyze this code for potential issues?\n@file:///home/user/project/main.py" {
		t.Fatalf("prompt = %q", ev[1].Prompt)
	}
	if ev[2].Paths[0] != h.dir+"/README.md" {
		t.Fatalf("read = %+v", ev[2])
	}
	if ev[3].ToolName != "Editing main.go" || ev[3].Paths[0] != h.dir+"/main.go" {
		t.Fatalf("edit = %+v", ev[3])
	}
	if ev[5].Paths[0] != h.dir+"/notes.txt" || ev[5].ToolName != "" {
		t.Fatalf("fs write = %+v", ev[5])
	}
	if ev[6].ContextType != "thinking" || !strings.HasPrefix(ev[7].Text, "Plan:\n- [pending] Check for syntax errors") {
		t.Fatalf("context = %+v %+v", ev[6], ev[7])
	}
	if ev[8].Text != "I'll analyze your code for potential issues. Done: main.go now has a main function." {
		t.Fatalf("response = %q", ev[8].Text)
	}
	if len(h.logs) != 0 {
		t.Fatalf("unexpected observation log: %v", h.logs)
	}
}

func TestProxySurvivesMalformedAndHugeMessages(t *testing.T) {
	for _, mode := range []string{"malformed", "huge"} {
		t.Run(mode, func(t *testing.T) {
			h := startProxy(t, mode)
			h.session()
			_ = h.client.in.Close()
			if err := h.wait(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			agentOut, _ := os.ReadFile(filepath.Join(filepath.Dir(h.dir), "agent-out"))
			if !bytes.Equal(agentOut, h.client.read.Bytes()) {
				t.Fatalf("agent->editor bytes differ (%d vs %d bytes)", len(agentOut), h.client.read.Len())
			}
			kinds := strings.Join(h.rec.kinds(), ",")
			if !strings.Contains(kinds, "turn_start") || !strings.Contains(kinds, "turn_end") {
				t.Fatalf("turn not recorded after %s message: %s; logs %v", mode, kinds, h.logs)
			}
			if mode == "huge" {
				for _, ev := range h.rec.events {
					if len(ev.Text) > maxContext {
						t.Fatalf("context not bounded: %d bytes", len(ev.Text))
					}
				}
			}
		})
	}
}

func TestProxyPropagatesAgentExitStatus(t *testing.T) {
	h := startProxy(t, "crash")
	h.client.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":1}}`)
	err := h.wait()
	if exit, ok := err.(ExitError); !ok || exit.Code != 3 {
		t.Fatalf("Run = %v, want exit 3", err)
	}
	h.logMu.Lock()
	defer h.logMu.Unlock()
	if !strings.Contains(h.stderr.String(), "fake agent crashing") {
		t.Fatalf("agent stderr not forwarded: %q", h.stderr.String())
	}

	h = startProxy(t, "exit7")
	_ = h.client.in.Close() // editor EOF reaches the agent as stdin EOF
	if exit, ok := h.wait().(ExitError); !ok || exit.Code != 7 {
		t.Fatalf("want exit 7 after stdin EOF")
	}
}

func TestProxyStopsAgentWhenEditorDisconnects(t *testing.T) {
	h := startProxy(t, "echo")
	h.client.send(`{"jsonrpc":"2.0","method":"echo"}`)
	if _, err := h.client.out.ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	// The editor stops reading and closes its end of the agent's output; the
	// next agent message fails to write and the proxy stops the agent. Stdin
	// stays open, as when an editor crashes without closing it.
	_ = h.stdout.Close()
	h.client.send(`{"jsonrpc":"2.0","method":"echo"}`)
	select {
	case err := <-h.result:
		if exit, ok := err.(ExitError); !ok || exit.Code != 128+15 || exit.Signal != syscall.SIGTERM {
			t.Fatalf("Run = %v, want SIGTERM exit", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("proxy kept running after the editor disconnected")
	}
}

func TestProxyRunReportsMissingAgent(t *testing.T) {
	err := Run(Config{Args: []string{filepath.Join(t.TempDir(), "missing-agent")}, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard, Sink: func(event.Event) {}})
	if err == nil {
		t.Fatal("missing agent started")
	}
}

// TestProxyLatency measures the round trip of small messages through the
// proxy against the same agent without it.
func TestProxyLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("latency measurement")
	}
	const n = 2000
	msg := `{"jsonrpc":"2.0","method":"echo","params":{"sessionId":"sess_abc123def456","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}`
	measure := func(proxied bool) time.Duration {
		h := startProxy(t, "echo")
		var in io.Writer = h.client.in
		var out = h.client.out
		if !proxied {
			// Direct: talk to a second fake agent with no proxy in between.
			return directRoundTrip(t, msg, n)
		}
		samples := make([]time.Duration, 0, n)
		for i := 0; i < n; i++ {
			start := time.Now()
			_, _ = io.WriteString(in, msg+"\n")
			if _, err := out.ReadBytes('\n'); err != nil {
				t.Fatal(err)
			}
			samples = append(samples, time.Since(start))
		}
		_ = h.client.in.Close()
		_ = h.wait()
		return median(samples)
	}
	direct := measure(false)
	proxied := measure(true)
	t.Logf("median round trip over %d messages: direct %v, through proxy %v, added %v (two hops)", n, direct, proxied, proxied-direct)
	if proxied-direct > 5*time.Millisecond {
		t.Fatalf("proxy adds %v per round trip", proxied-direct)
	}
}

func directRoundTrip(t *testing.T, msg string, n int) time.Duration {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ACP_FAKE_AGENT=echo")
	in, _ := cmd.StdinPipe()
	outPipe, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	out := bufio.NewReader(outPipe)
	samples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		_, _ = io.WriteString(in, msg+"\n")
		if _, err := out.ReadBytes('\n'); err != nil {
			t.Fatal(err)
		}
		samples = append(samples, time.Since(start))
	}
	_ = in.Close()
	_ = cmd.Wait()
	return median(samples)
}

func median(d []time.Duration) time.Duration {
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[len(d)/2]
}

func TestProxyDoesNotWaitForGrandchildOutput(t *testing.T) {
	h := startProxy(t, "grandchild")
	t.Cleanup(func() {
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(h.dir), "agent-out.pid")); err == nil {
			var pid int
			if _, err := fmt.Sscan(string(b), &pid); err == nil && pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	start := time.Now()
	if err := h.wait(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("proxy waited %v for a grandchild holding stdout", elapsed)
	}
	if !strings.Contains(h.client.read.String(), `"hello"`) {
		t.Fatalf("output before exit lost: %q", h.client.read.String())
	}
}

func TestProxyStopsAgentThatIgnoresStdinEOF(t *testing.T) {
	h := startProxy(t, "ignore-eof", func(h *harness) { h.stopGrace = 100 * time.Millisecond })
	_ = h.client.in.Close()
	err := h.wait()
	if exit, ok := err.(ExitError); !ok || exit.Signal != syscall.SIGTERM || exit.Code != 128+15 {
		t.Fatalf("Run = %#v, want SIGTERM", err)
	}
}

func TestProxyForwardsSignals(t *testing.T) {
	signals := make(chan os.Signal, 1)
	h := startProxy(t, "tree", func(h *harness) { h.signals = signals })
	pidFile := filepath.Join(filepath.Dir(h.dir), "agent-out.pid")
	var pid int
	t.Cleanup(func() {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	h.client.send(`{"jsonrpc":"2.0","method":"echo"}`)
	if _, err := h.client.out.ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscan(string(b), &pid); err != nil {
		t.Fatal(err)
	}
	signals <- syscall.SIGINT
	if exit, ok := h.wait().(ExitError); !ok || exit.Signal != syscall.SIGINT {
		t.Fatalf("want agent killed by forwarded SIGINT")
	}
	// The agent's own child is in its process group and gets the signal too.
	deadline := time.Now().Add(5 * time.Second)
	for !processGone(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived the forwarded SIGINT", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A slow editor must still receive all output an agent wrote before exiting.
func TestProxyDeliversEverythingToSlowEditorAfterExit(t *testing.T) {
	h := startProxy(t, "bigexit")
	var got bytes.Buffer
	buf := make([]byte, 16<<10)
	for {
		n, err := h.client.out.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := <-h.result; err != nil {
		t.Fatalf("Run: %v", err)
	}
	want, _ := os.ReadFile(filepath.Join(filepath.Dir(h.dir), "agent-out"))
	if len(want) < 200001 || !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("slow editor got %d of %d bytes", got.Len(), len(want))
	}
}

// processGone reports whether pid has exited (or is a zombie awaiting reaping).
func processGone(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	return err == nil && strings.Contains(string(b), ") Z ")
}
