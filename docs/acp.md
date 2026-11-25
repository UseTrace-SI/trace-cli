# Agent Client Protocol (ACP)

`trace acp` records any [Agent Client Protocol](https://agentclientprotocol.com)
agent that an editor such as Zed or a JetBrains IDE runs, including agents from
the ACP registry (`claude-agent-acp`, `codex-acp`, `gemini --acp`, ...).

```sh
trace acp [--name NAME] -- <agent command> [args...]
```

Configure the editor to launch `trace acp -- <agent>` instead of `<agent>`.
Trace starts the agent and sits between the two as a transparent stdio proxy.

## What is forwarded

Every byte is forwarded unchanged, in order and as soon as it is read, in both
directions. Trace never alters, reorders, drops or injects a message, and
never writes to its own stdout. The agent's stderr goes straight to the
editor. When the editor closes stdin, the agent's stdin is closed. SIGINT,
SIGTERM and SIGHUP are passed to the agent, and Trace exits with the agent's
status (128 + signal number when the agent is killed by a signal). If the
editor goes away, so that writing to it fails, Trace sends the agent SIGTERM,
then SIGKILL after two seconds.

In `TestProxyLatency` the proxy adds a median of 10 to 30 µs per round trip
(two hops, on Linux).

## What is recorded

A copy of each message is parsed off the forwarding path, following the ACP v1
schema (`schema/v1/schema.json` in
[agentclientprotocol/agent-client-protocol](https://github.com/agentclientprotocol/agent-client-protocol)):

| ACP message | Trace record |
| --- | --- |
| `session/new` (its `cwd` and the returned `sessionId`), `session/load`, `session/resume` | Session start in the Git repository containing `cwd` |
| `session/prompt` request | Turn start; the prompt is the text blocks, with attached resources named by URI (their contents are not recorded) |
| `session/update` `agent_message_chunk`, `agent_thought_chunk`, `plan` | Assistant response, reasoning and plan text of the turn (64 KiB each) |
| `session/update` `tool_call` / `tool_call_update` reaching `completed` | `edit`, `delete`, `move` and calls with `diff` content: edited paths. `execute` and `other`: an edit of unknown paths, which makes Trace scan the worktree. `read` with locations: read paths |
| `fs/write_text_file`, `fs/read_text_file` agent requests | Edited and read paths |
| `session/prompt` response (`stopReason`, or an error) | Turn end; a stop reason other than `end_turn` is noted in the response text |
| `session/close`, agent exit | Open turns and sessions are closed |

Events go through the same path as agent hooks: the daemon spool, started on
demand. The recorded agent is `acp:<name>`, where the name is the agent
command's base name (`acp:claude-agent-acp`) unless `--name` sets it.

Recording follows the usual rules: text is redacted before it is stored,
secret and excluded paths are dropped as for every other agent, and only
repositories where Trace is initialized (`.trace/` exists, for example after
`trace init`) are recorded. Sessions
elsewhere are proxied but not recorded. `--repo` records every session in that
repository instead of the one containing `cwd`. `trace disable` pauses
recording; the marker `$TRACE_HOME/agents/acp.disabled` (by default
`~/.trace/agents/acp.disabled`) pauses only ACP agents.

Observation never breaks the session. Messages that are not JSON, messages over
32 MiB, unknown methods and unexpected shapes are forwarded and skipped by the
recorder. If the recorder falls behind, it skips messages rather than slowing
the proxy. Problems are logged to `$TRACE_HOME/acp.log` (by default
`~/.trace/acp.log`) without message contents.

## Zed

Add a custom agent to Zed's `settings.json` (Agent Settings, External Agents,
Add Custom Agent):

```json
{
  "agent_servers": {
    "Claude Code (Trace)": {
      "type": "custom",
      "command": "trace",
      "args": ["acp", "--", "claude-agent-acp"],
      "env": {}
    },
    "Codex (Trace)": {
      "type": "custom",
      "command": "trace",
      "args": ["acp", "--", "codex-acp"],
      "env": {}
    }
  }
}
```

Use absolute paths for `trace` and the agent if the editor does not inherit
your shell's `PATH`. For an agent that needs arguments, add them after `--`,
for example `["acp", "--", "gemini", "--experimental-acp"]`.

## JetBrains IDEs

In the AI Chat tool window, choose Add Custom Agent. The IDE opens
`~/.jetbrains/acp.json`:

```json
{
  "default_mcp_settings": {},
  "agent_servers": {
    "Claude Code (Trace)": {
      "command": "/usr/local/bin/trace",
      "args": ["acp", "--", "claude-agent-acp"],
      "env": {}
    }
  }
}
```

## Limits

- Agents installed and launched by the editor from the ACP registry are not
  wrapped; add them as custom agents that run `trace acp`.
- Turns are recorded from protocol messages only. Agent transcripts, token
  usage and edits an agent makes without reporting a tool call or file request
  are not read, except through the worktree scan at turn end.
- A session loaded with `session/load` continues the recorded session with the
  same ACP session ID; history the agent replays while loading is not
  recorded again.
