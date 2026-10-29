package hermes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UseTrace-SI/trace-cli/internal/adapters/hookcmd"
	"github.com/UseTrace-SI/trace-cli/internal/event"
	"github.com/UseTrace-SI/trace-cli/internal/textutil"
)

const marker = "# Managed by Trace Hermes observer\n"
const manifest = "name: trace-observer\nversion: \"1.0\"\ndescription: Local Trace observer\nprovides_hooks:\n  - pre_llm_call\n  - post_llm_call\n  - post_tool_call\n  - on_session_end\n  - on_session_finalize\n"

// Setup installs a profile-scoped observer. Hermes requires its own explicit
// plugin enablement; no existing Hermes configuration is modified here.
func Setup(home string, remove bool) error {
	profile := strings.TrimSpace(os.Getenv("HERMES_HOME"))
	if profile == "" {
		profile = filepath.Join(home, ".hermes")
	}
	if !filepath.IsAbs(profile) {
		return fmt.Errorf("Hermes profile directory must be absolute")
	}
	dir := filepath.Join(profile, "plugins", "trace-observer")
	module := filepath.Join(dir, "__init__.py")
	manifestPath := filepath.Join(dir, "plugin.yaml")
	exe, err := json.Marshal(hookcmd.Path())
	if err != nil {
		return err
	}
	program := marker + strings.ReplaceAll(pythonPlugin, "__TRACE_EXECUTABLE__", string(exe))
	previous := strings.Replace(program, `            if session not in _active or not (kwargs.get("interrupted") or kwargs.get("failed") is True or kwargs.get("completed") is False):`, `            if session not in _active or not kwargs.get("interrupted"):`, 1)
	current, err := os.ReadFile(module)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading Hermes plugin: %w", err)
	}
	if err == nil && string(current) != program && string(current) != previous {
		return fmt.Errorf("refusing to replace modified Hermes plugin at %s", module)
	}
	manifestBytes, mErr := os.ReadFile(manifestPath)
	if mErr != nil && !os.IsNotExist(mErr) {
		return fmt.Errorf("reading Hermes manifest: %w", mErr)
	}
	if mErr == nil && string(manifestBytes) != manifest {
		return fmt.Errorf("refusing to replace user Hermes manifest at %s", manifestPath)
	}
	if remove {
		if err == nil {
			if err := os.Remove(module); err != nil {
				return err
			}
		}
		if mErr == nil {
			if err := os.Remove(manifestPath); err != nil {
				return err
			}
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating Hermes plugin directory: %w", err)
	}
	if err := os.WriteFile(module, []byte(program), 0600); err != nil {
		return fmt.Errorf("writing Hermes observer: %w", err)
	}
	if err := os.WriteFile(manifestPath, []byte(manifest), 0600); err != nil {
		return fmt.Errorf("writing Hermes manifest: %w", err)
	}
	return nil
}

// Parse converts bounded observer payloads; command output and arguments are
// deliberately not retained.
func Parse(name string, body []byte, root string) ([]event.Event, error) {
	var p struct {
		Session  string `json:"session_id"`
		Prompt   string `json:"user_message"`
		Response string `json:"assistant_response"`
		Tool     string `json:"tool_name"`
		Path     string `json:"path"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parsing Hermes observer: %w", err)
	}
	if p.Session == "" {
		return nil, nil
	}
	e := event.Event{Agent: "hermes", AgentSessionID: p.Session, Worktree: root, At: time.Now().UnixMilli()}
	switch name {
	case "pre_llm_call":
		e.Kind, e.Prompt = event.TurnStart, p.Prompt
	case "post_llm_call", "on_session_end":
		scan := e
		scan.Kind = event.ToolEdit
		result := []event.Event{scan.Sanitized()}
		if name == "post_llm_call" && p.Response != "" {
			response := e
			response.Kind, response.ContextType = event.TurnContext, "response"
			response.Text = textutil.TruncateBytes(p.Response, 64<<10)
			result = append(result, response.Sanitized())
		}
		e.Kind = event.TurnEnd
		return append(result, e.Sanitized()), nil
	case "on_session_finalize":
		scan := e
		scan.Kind = event.ToolEdit
		end := e
		end.Kind = event.TurnEnd
		e.Kind = event.SessionEnd
		return []event.Event{scan.Sanitized(), end.Sanitized(), e.Sanitized()}, nil
	case "post_tool_call":
		if p.Status != "" && p.Status != "ok" {
			return nil, nil
		}
		switch p.Tool {
		case "write_file", "patch", "terminal":
			e.Kind = event.ToolEdit
		case "read_file":
			e.Kind = event.FileRead
		default:
			return nil, nil
		}
		e.ToolName = p.Tool
		if p.Path != "" {
			e.Paths = []string{p.Path}
		}
	default:
		return nil, nil
	}
	return []event.Event{e.Sanitized()}, nil
}

const pythonPlugin = `import json
import os
import queue
import subprocess
import threading

_TRACE = __TRACE_EXECUTABLE__
_events = queue.Queue(maxsize=256)
_active = set()
_known = set()

def _worker():
    while True:
        name, payload, repo = _events.get()
        try:
            subprocess.run([_TRACE, "hook", "hermes", name], cwd=repo,
                           input=json.dumps(payload), text=True,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                           timeout=2, check=False)
        except Exception:
            pass
        finally:
            _events.task_done()

def _emit(name, **kwargs):
    try:
        repo = os.environ.get("TRACE_HERMES_REPO", "")
        if not os.path.isabs(repo) or kwargs.get("parent_session_id"):
            return
        session = kwargs.get("session_id")
        if not isinstance(session, str) or not session:
            return
        if name == "pre_llm_call":
            _known.add(session)
            _active.add(session)
        elif name == "post_tool_call" and session not in _active:
            return
        elif name == "post_llm_call":
            if session not in _active:
                return
            _active.discard(session)
        elif name == "on_session_end":
            if session not in _active or not (kwargs.get("interrupted") or kwargs.get("failed") is True or kwargs.get("completed") is False):
                return
            _active.discard(session)
        elif name == "on_session_finalize":
            if session not in _known:
                return
            _known.discard(session)
            _active.discard(session)
        payload = {"session_id": session}
        for key in ("user_message", "assistant_response", "tool_name", "status"):
            value = kwargs.get(key)
            if isinstance(value, str) and len(value) <= 65536:
                payload[key] = value
        args = kwargs.get("args")
        if isinstance(args, dict) and isinstance(args.get("path"), str) and len(args["path"]) <= 4096:
            payload["path"] = args["path"]
        _events.put_nowait((name, payload, repo))
    except Exception:
        pass

def register(ctx):
    threading.Thread(target=_worker, daemon=True, name="trace-hermes").start()
    for name in ("pre_llm_call", "post_llm_call", "post_tool_call", "on_session_end", "on_session_finalize"):
        ctx.register_hook(name, lambda _name=name, **kw: _emit(_name, **kw))
`
