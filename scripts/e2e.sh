#!/bin/sh
set -eu
project=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
binary=${TRACE_E2E_BINARY:-"$project/bin/trace"}
if [ ! -x "$binary" ]; then
  echo 'build bin/trace before running e2e' >&2
  exit 1
fi
PATH="$(dirname "$binary"):$PATH"
export PATH
work=$(mktemp -d)
work_goose=$(mktemp -d)
work_kiro=$(mktemp -d)
work_agy=$(mktemp -d)
work_cursor=$(mktemp -d)
work_cursor_global=$(mktemp -d)
work_windsurf_global=$(mktemp -d)
work_copilot_global=$(mktemp -d)
work_gemini_global=$(mktemp -d)
work_gemini_project=$(mktemp -d)
work_qwen_global=$(mktemp -d)
work_factory_global=$(mktemp -d)
work_amp_global=$(mktemp -d)
work_kilo_global=$(mktemp -d)
work_opencode_global=$(mktemp -d)
work_pi_global=$(mktemp -d)
work_omp_global=$(mktemp -d)
work_cline=$(mktemp -d)
work_cline_global=$(mktemp -d)
cline_config=$(mktemp -d)
work_auggie=$(mktemp -d)
work_openhands=$(mktemp -d)
work_openclaude=$(mktemp -d)
work_poolside=$(mktemp -d)
work_hermes=$(mktemp -d)
work_crewai=$(mktemp -d)
work_aider=$(mktemp -d)
work_codebuff=$(mktemp -d)
work_continue=$(mktemp -d)
work_vibe=$(mktemp -d)
work_crush=$(mktemp -d)
work_aider_run=$(mktemp -d)
work_claude_global=$(mktemp -d)
work_kimi_global=$(mktemp -d)
work_codex_global=$(mktemp -d)
work_zoo=$(mktemp -d)
mock_agents=$(mktemp -d)
hermes_profile=$(mktemp -d)
work_fork=$(mktemp -d)
fork_target="${work_fork}-fork-1-e2e"
cleanup() {
  for dir in "$work" "$work_goose" "$work_kiro" "$work_agy" "$work_cursor" "$work_cursor_global" "$work_windsurf_global" "$work_copilot_global" "$work_gemini_global" "$work_gemini_project" "$work_qwen_global" "$work_factory_global" "$work_amp_global" "$work_kilo_global" "$work_opencode_global" "$work_pi_global" "$work_omp_global" "$work_cline" "$work_cline_global" "$work_auggie" "$work_openhands" "$work_openclaude" "$work_poolside" "$work_hermes" "$work_crewai" "$work_aider" "$work_codebuff" "$work_continue" "$work_vibe" "$work_crush" "$work_aider_run" "$work_claude_global" "$work_kimi_global" "$work_codex_global" "$work_zoo" "$work_fork" "$fork_target"; do
    pid=$(cat "$dir/.trace/daemon.pid" 2>/dev/null || true)
    if ! "$binary" --repo "$dir" daemon stop >/dev/null 2>&1; then
      continue
    fi
    case "$pid" in
      ''|*[!0-9]*) continue ;;
    esac
    attempts=0
    while [ -e "$dir/.trace/daemon.pid" ] && [ "$attempts" -lt 100 ]; do
      sleep 0.05
      attempts=$((attempts+1))
    done
    sleep 0.05
  done
  attempts=0
  until rm -rf "$work" "$work_goose" "$work_kiro" "$work_agy" "$work_cursor" "$work_cursor_global" "$work_windsurf_global" "$work_copilot_global" "$work_gemini_global" "$work_gemini_project" "$work_qwen_global" "$work_factory_global" "$work_amp_global" "$work_kilo_global" "$work_opencode_global" "$work_pi_global" "$work_omp_global" "$work_cline" "$work_cline_global" "$cline_config" "$work_auggie" "$work_openhands" "$work_openclaude" "$work_poolside" "$work_hermes" "$hermes_profile" "$work_crewai" "$work_aider" "$work_codebuff" "$work_continue" "$work_vibe" "$work_crush" "$work_aider_run" "$work_claude_global" "$work_kimi_global" "$work_codex_global" "$work_zoo" "$mock_agents" "$work_fork" "$fork_target" 2>/dev/null; do
    attempts=$((attempts+1))
    [ "$attempts" -lt 20 ] || return 1
    sleep 0.05
  done
}
trap cleanup EXIT INT TERM
git -C "$work" init -q
printf 'start\n' > "$work/file.txt"
mkdir "$work/nested"
"$binary" --repo "$work/nested" init >/dev/null
[ -d "$work/.trace" ] && [ ! -e "$work/nested/.trace" ] || { echo 'init used a nested directory instead of the Git root' >&2; exit 1; }
printf '{"session_id":"e2e-session","hook_event_name":"SessionStart"}\n' | "$binary" --repo "$work" hook claude-code SessionStart
for step in 1 2 3 4 5; do
  prompt="turn $step"
  if [ "$step" -eq 2 ]; then prompt='turn 2 ghp_abcdefghijklmnopqrstuvwxyz'; fi
  printf '{"session_id":"e2e-session","hook_event_name":"UserPromptSubmit","prompt":"%s"}\n' "$prompt" | "$binary" --repo "$work" hook claude-code UserPromptSubmit
  printf '%s\n' "$step" > "$work/file.txt"
  printf '{"session_id":"e2e-session","hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"%s/file.txt"}}\n' "$work" | "$binary" --repo "$work" hook claude-code PostToolUse
  if [ "$step" -eq 1 ]; then
    printf 'created\n' > "$work/added.txt"
    printf '{"session_id":"e2e-session","hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"%s/added.txt"}}\n' "$work" | "$binary" --repo "$work" hook claude-code PostToolUse
  fi
  if [ "$step" -eq 3 ]; then
    rm "$work/added.txt"
    printf '{"session_id":"e2e-session","hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"%s/added.txt"}}\n' "$work" | "$binary" --repo "$work" hook claude-code PostToolUse
  fi
  printf '{"session_id":"e2e-session","hook_event_name":"Stop","last_assistant_message":"Finished turn %s"}\n' "$step" | "$binary" --repo "$work" hook claude-code Stop
  n=0
  while :; do
    log_json=$("$binary" --repo "$work" --json log) || { echo 'trace log failed' >&2; exit 1; }
    if printf '%s' "$log_json" | python3 -c 'import json,sys; sys.exit(len(json.load(sys.stdin)) < int(sys.argv[1]))' "$((step+1))"; then break; fi
    n=$((n+1)); [ "$n" -lt 100 ] || { echo "step $step not recorded" >&2; exit 1; }
    sleep 0.05
  done
done
"$binary" --repo "$work/nested" --json status | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["daemon_up"] and s["latest_step"] >= 5, s'
"$binary" --repo "$work" --json show 2 | python3 -c 'import json,re,sys; s=json.load(sys.stdin); assert re.fullmatch(r"turn 2 ⟨redacted:github-token:[0-9a-f]{8}⟩", s["prompt"]), s["prompt"]'
"$binary" --repo "$work" --json show 5 | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["step"] == 5 and "file.txt" in s["files"] and "Finished turn 5" in s["reasoning"], s'
"$binary" --repo "$work" --json why file.txt:1 | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s[0]["step"] == 5, s'
"$binary" --repo "$work" export --format agent-trace --out "$work/.trace/e2e-agent-trace.jsonl" >/dev/null
python3 -c 'import json,sys; rows=[json.loads(line) for line in open(sys.argv[1])]; assert len(rows)>=5 and all(row["version"]=="0.1.0" for row in rows)' "$work/.trace/e2e-agent-trace.jsonl"
"$binary" --repo "$work" rewind 2 >/dev/null
[ "$(cat "$work/file.txt")" = 2 ] || { echo 'rewind did not restore step 2' >&2; exit 1; }
[ "$(cat "$work/added.txt")" = created ] || { echo 'rewind did not restore created file' >&2; exit 1; }
safety=$("$binary" --repo "$work" --json log | python3 -c 'import json,sys; print([row["step"] for row in json.load(sys.stdin) if row["tag"]=="pre-rewind"][-1])')
"$binary" --repo "$work" rewind "$safety" >/dev/null
[ "$(cat "$work/file.txt")" = 5 ] || { echo 'safety rewind did not restore step 5' >&2; exit 1; }
[ ! -e "$work/added.txt" ] || { echo 'safety rewind did not restore deletion' >&2; exit 1; }
"$binary" --repo "$work" resume 2 --agent codex --no-launch >/dev/null
[ "$(cat "$work/file.txt")" = 2 ] || { echo 'resume did not restore step 2' >&2; exit 1; }
grep -F 'turn 1' "$work/.trace/context.md" >/dev/null || { echo 'resume context is missing turn 1' >&2; exit 1; }
grep -F 'turn 2' "$work/.trace/context.md" >/dev/null || { echo 'resume context is missing turn 2' >&2; exit 1; }
if grep -aF 'ghp_abcdefghijklmnopqrstuvwxyz' "$work"/.trace/trace.db* >/dev/null; then
  echo 'fixture secret leaked into database' >&2
  exit 1
fi
git -C "$work_agy" init -q
printf 'before\n' > "$work_agy/main.go"
"$binary" --repo "$work_agy" init --agent agy >/dev/null
grep -F ' hook agy Stop' "$work_agy/.agents/hooks.json" >/dev/null
transcript="$work_agy/.trace/agy-transcript.jsonl"
printf '{"step_index":0,"type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>Update main.go</USER_REQUEST>"}\n' > "$transcript"
printf '{"conversationId":"agy-e2e","transcriptPath":"%s","workspacePaths":["%s"]}\n' "$transcript" "$work_agy" | "$binary" --repo "$work_agy" hook agy SessionStart >/dev/null
printf '{"conversationId":"agy-e2e","transcriptPath":"%s","invocationNum":0}\n' "$transcript" | "$binary" --repo "$work_agy" hook agy PreInvocation >/dev/null
printf 'after\n' > "$work_agy/main.go"
printf '{"step_index":1,"type":"PLANNER_RESPONSE","status":"DONE","thinking":"The file needs an update.","content":"Updated it.","tool_calls":[{"name":"replace_file_content","args":{"TargetFile":"%s/main.go"}}]}\n' "$work_agy" >> "$transcript"
printf '{"conversationId":"agy-e2e","transcriptPath":"%s","toolCall":{"name":"replace_file_content","args":{"TargetFile":"%s/main.go"}}}\n' "$transcript" "$work_agy" | "$binary" --repo "$work_agy" hook agy PostToolUse >/dev/null
stop_output=$(printf '{"conversationId":"agy-e2e","transcriptPath":"%s","fullyIdle":true}\n' "$transcript" | "$binary" --repo "$work_agy" hook agy Stop)
[ "$stop_output" = '{"decision":"allow"}' ] || { echo 'Agy stop hook output changed agent behavior' >&2; exit 1; }
n=0
while :; do
  if "$binary" --repo "$work_agy" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["step"]==1 and s["prompt"]=="Update main.go" and "file needs an update" in s["reasoning"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Agy turn not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_cursor" init -q
printf 'before\n' > "$work_cursor/main.go"
"$binary" --repo "$work_cursor" init --agent cursor >/dev/null
grep -F ' hook cursor afterAgentResponse' "$work_cursor/.cursor/hooks.json" >/dev/null
printf '{"conversation_id":"cursor-e2e","hook_event_name":"sessionStart"}\n' | "$binary" --repo "$work_cursor" hook cursor sessionStart >/dev/null
duplicate_output=$(printf '{"workspace_roots":["%s"],"conversation_id":"cursor-e2e","hook_event_name":"beforeSubmitPrompt","prompt":"duplicate"}' "$work_cursor" | "$binary" hook cursor-global beforeSubmitPrompt)
[ "$duplicate_output" = '{"continue":true}' ] || { echo 'Cursor global duplicate hook changed behavior' >&2; exit 1; }
prompt_output=$(printf '{"conversation_id":"cursor-e2e","hook_event_name":"beforeSubmitPrompt","prompt":"Fix Cursor file"}\n' | "$binary" --repo "$work_cursor" hook cursor beforeSubmitPrompt)
[ "$prompt_output" = '{"continue":true}' ] || { echo 'Cursor prompt hook output changed agent behavior' >&2; exit 1; }
cursor_read_output=$(printf '{"conversation_id":"cursor-e2e","hook_event_name":"beforeReadFile","file_path":"%s/main.go"}\n' "$work_cursor" | "$binary" --repo "$work_cursor" hook cursor beforeReadFile)
[ "$cursor_read_output" = '{"permission":"allow"}' ] || { echo 'Cursor read hook did not allow the read' >&2; exit 1; }
cursor_read_output=$(printf '{}' | "$binary" hook cursor-global beforeReadFile)
[ "$cursor_read_output" = '{"permission":"allow"}' ] || { echo 'Cursor skipped read hook did not allow the read' >&2; exit 1; }
printf 'after\n' > "$work_cursor/main.go"
printf '{"conversation_id":"cursor-e2e","hook_event_name":"afterFileEdit","file_path":"%s/main.go"}\n' "$work_cursor" | "$binary" --repo "$work_cursor" hook cursor afterFileEdit >/dev/null
printf '{"conversation_id":"cursor-e2e","hook_event_name":"afterAgentThought","text":"It needs a change."}\n' | "$binary" --repo "$work_cursor" hook cursor afterAgentThought >/dev/null
printf '{"conversation_id":"cursor-e2e","hook_event_name":"afterAgentResponse","text":"Changed the file."}\n' | "$binary" --repo "$work_cursor" hook cursor afterAgentResponse >/dev/null
printf '{"conversation_id":"cursor-e2e","hook_event_name":"stop"}\n' | "$binary" --repo "$work_cursor" hook cursor stop >/dev/null
n=0
while :; do
  if "$binary" --repo "$work_cursor" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["step"]==1 and s["prompt"]=="Fix Cursor file" and "It needs a change" in s["reasoning"] and "Changed the file" in s["reasoning"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Cursor turn not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_gemini_global" init -q
printf 'before\n' > "$work_gemini_global/main.txt"
output=$(cd "$work" && printf '{"cwd":"%s","session_id":"gemini-global","hook_event_name":"BeforeAgent","prompt":"Change main"}' "$work_gemini_global" | "$binary" hook gemini-global BeforeAgent)
[ "$output" = '{}' ] || { echo 'Gemini hook changed behavior' >&2; exit 1; }
n=0
while [ ! -e "$work_gemini_global/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Gemini global daemon did not start' >&2; exit 1; }
  sleep 0.05
done
printf 'after\n' > "$work_gemini_global/main.txt"
(cd "$work" && printf '{"cwd":"%s","session_id":"gemini-global","hook_event_name":"AfterTool","tool_name":"write_file","tool_input":{"file_path":"main.txt"}}' "$work_gemini_global" | "$binary" hook gemini-global AfterTool >/dev/null)
(cd "$work" && printf '{"cwd":"%s","session_id":"gemini-global","hook_event_name":"AfterAgent","prompt_response":"Done"}' "$work_gemini_global" | "$binary" hook gemini-global AfterAgent >/dev/null)
n=0
while :; do
  if "$binary" --repo "$work_gemini_global" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="gemini" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Gemini global turn not recorded' >&2; exit 1; }
  sleep 0.05
done
printf '{"cwd":"%s","session_id":"gemini-global","hook_event_name":"BeforeAgent","prompt":"End session"}' "$work_gemini_global" | "$binary" hook gemini-global BeforeAgent >/dev/null
printf 'unreported change\n' > "$work_gemini_global/main.txt"
printf '{"cwd":"%s","session_id":"gemini-global","hook_event_name":"SessionEnd"}' "$work_gemini_global" | "$binary" hook gemini-global SessionEnd >/dev/null
n=0
while :; do
  if "$binary" --repo "$work_gemini_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="End session" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Gemini session end did not capture changes' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_gemini_project" init -q
"$binary" --repo "$work_gemini_project" --json init --agent gemini | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["initialized"] and "hooks.enabled=true" in d["additional_setup"]'
"$binary" --repo "$work_gemini_project" --json agent add gemini | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["added"] and "hooks.enabled=true" in d["additional_setup"]'
python3 -c 'import json,sys; p=sys.argv[1]; d=json.load(open(p)); d["hooks"]["enabled"]=True; open(p,"w").write(json.dumps(d))' "$work_gemini_project/.gemini/settings.json"
output=$(printf '{"cwd":"%s","session_id":"gemini-project","hook_event_name":"BeforeAgent","prompt":"duplicate"}' "$work_gemini_project" | "$binary" hook gemini-global BeforeAgent)
[ "$output" = '{}' ] || { echo 'Gemini duplicate hook changed behavior' >&2; exit 1; }
(cd "$work" && printf '{"cwd":"%s","session_id":"gemini-project","hook_event_name":"BeforeAgent","prompt":"project prompt"}' "$work_gemini_project" | "$binary" hook gemini BeforeAgent >/dev/null)
(cd "$work" && printf '{"cwd":"%s","session_id":"gemini-project","hook_event_name":"AfterAgent"}' "$work_gemini_project" | "$binary" hook gemini AfterAgent >/dev/null)
n=0
while :; do
  if "$binary" --repo "$work_gemini_project" --json show 1 2>/dev/null | python3 -c 'import json,sys; assert json.load(sys.stdin)["prompt"]=="project prompt"' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Gemini project hook duplicate' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_amp_global" init -q
printf 'before\n' > "$work_amp_global/main.txt"
(cd "$work" && printf '{"cwd":"%s","session_id":"amp-global","prompt":"Change main"}' "$work_amp_global" | "$binary" hook amp-global turn_start)
n=0
while [ ! -e "$work_amp_global/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Amp global daemon did not start' >&2; exit 1; }
  sleep 0.05
done
printf 'after\n' > "$work_amp_global/main.txt"
(cd "$work" && printf '{"cwd":"%s","session_id":"amp-global"}' "$work_amp_global" | "$binary" hook amp-global tool_edit)
(cd "$work" && printf '{"cwd":"%s","session_id":"amp-global"}' "$work_amp_global" | "$binary" hook amp-global turn_end)
n=0
while :; do
  if "$binary" --repo "$work_amp_global" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="amp" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Amp global turn not recorded' >&2; exit 1; }
  sleep 0.05
done
for pair in "pi:$work_pi_global" "omp:$work_omp_global" "kilo:$work_kilo_global" "opencode:$work_opencode_global"; do
  agent=${pair%%:*}
  agent_work=${pair#*:}
  git -C "$agent_work" init -q
  printf 'before\n' > "$agent_work/main.txt"
  (cd "$work" && printf '{"cwd":"%s","session_id":"global-session","prompt":"Change main"}' "$agent_work" | "$binary" hook "$agent-global" turn_start)
  n=0
  while [ ! -e "$agent_work/.trace/spool.ready" ]; do
    n=$((n+1)); [ "$n" -lt 100 ] || { echo "$agent global daemon did not start" >&2; exit 1; }
    sleep 0.05
  done
  printf 'after\n' > "$agent_work/main.txt"
  (cd "$work" && printf '{"cwd":"%s","session_id":"global-session"}' "$agent_work" | "$binary" hook "$agent-global" tool_edit)
  if [ "$agent" = kilo ] || [ "$agent" = opencode ]; then
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-session","paths":["%s/main.txt","%s/.env"]}' "$agent_work" "$agent_work" "$agent_work" | "$binary" hook "$agent-global" file_read)
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-session","tool_name":"edit","paths":["%s/main.txt"]}' "$agent_work" "$agent_work" | "$binary" hook "$agent-global" tool_edit)
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-session","context_type":"response","text":"Done ghp_abcdefghijklmnopqrstuvwxyz"}' "$agent_work" | "$binary" hook "$agent-global" turn_context)
  fi
  (cd "$work" && printf '{"cwd":"%s","session_id":"global-session"}' "$agent_work" | "$binary" hook "$agent-global" turn_end)
  n=0
  while :; do
    if "$binary" --repo "$agent_work" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]==sys.argv[1] and s["prompt"]=="Change main" and "main.txt" in s["files"]' "$agent" 2>/dev/null; then break; fi
    n=$((n+1)); [ "$n" -lt 100 ] || { echo "$agent global turn not recorded" >&2; exit 1; }
    sleep 0.05
  done
done
for activity_repo in "$work_kilo_global" "$work_opencode_global"; do
  "$binary" --repo "$activity_repo" --json show 1 | python3 -c 'import json,sys; s=json.load(sys.stdin); assert "Done" in s["reasoning"] and "ghp_abcdefghijklmnopqrstuvwxyz" not in s["reasoning"] and "edit" in s["tool_calls"]'
  python3 -c 'import json,sqlite3,sys; db=sqlite3.connect(sys.argv[1]); files=json.loads(db.execute("select files_read from turns where seq=1").fetchone()[0]); assert files==["main.txt"], files' "$activity_repo/.trace/trace.db"
done
printf '{"cwd":"%s","session_id":"opencode-v1","prompt":"Legacy prompt"}' "$work_opencode_global" | "$binary" hook opencode-v1-global turn_start
printf 'legacy\n' > "$work_opencode_global/main.txt"
printf '{"cwd":"%s","session_id":"opencode-v1"}' "$work_opencode_global" | "$binary" hook opencode-v1-global tool_edit
printf '{"cwd":"%s","session_id":"opencode-v1"}' "$work_opencode_global" | "$binary" hook opencode-v1-global turn_end
n=0
while :; do
  if "$binary" --repo "$work_opencode_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="opencode" and s["prompt"]=="Legacy prompt" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'OpenCode v1 global turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_kilo_global" agent add kilo >/dev/null
(cd "$work" && printf '{"cwd":"%s","session_id":"kilo-overlap","project_loaded":true,"prompt":"duplicate prompt"}' "$work_kilo_global" | "$binary" hook kilo-global turn_start)
printf '{"session_id":"kilo-overlap","prompt":"project prompt"}' | "$binary" --repo "$work_kilo_global" hook kilo turn_start
printf '{"session_id":"kilo-overlap"}' | "$binary" --repo "$work_kilo_global" hook kilo turn_end
n=0
while :; do
  if "$binary" --repo "$work_kilo_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; assert json.load(sys.stdin)["prompt"]=="project prompt"' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Kilo project and global overlap not handled' >&2; exit 1; }
  sleep 0.05
done
# A project plugin on disk does not suppress the global observer if Kilo did not load it.
(cd "$work" && printf '{"cwd":"%s","session_id":"kilo-global-only","project_loaded":false,"prompt":"global prompt"}' "$work_kilo_global" | "$binary" hook kilo-global turn_start)
(cd "$work" && printf '{"cwd":"%s","session_id":"kilo-global-only","project_loaded":false}' "$work_kilo_global" | "$binary" hook kilo-global turn_end)
n=0
while :; do
  if "$binary" --repo "$work_kilo_global" --json show 3 2>/dev/null | python3 -c 'import json,sys; assert json.load(sys.stdin)["prompt"]=="global prompt"' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Kilo inactive project plugin suppressed global recording' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_copilot_global" init -q
printf 'before\n' > "$work_copilot_global/main.txt"
printf '{"cwd":"%s","sessionId":"copilot-global","prompt":"Change main"}' "$work_copilot_global" | "$binary" hook copilot-global userPromptSubmitted
printf '{"cwd":"%s","sessionId":"copilot-global","agentId":"child"}' "$work_copilot_global" | "$binary" hook copilot-global agentStop
n=0
while [ ! -e "$work_copilot_global/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Copilot daemon not ready' >&2; exit 1; }
  sleep 0.05
done
printf 'after\n' > "$work_copilot_global/main.txt"
printf '{"cwd":"%s","sessionId":"copilot-global"}' "$work_copilot_global" | "$binary" hook copilot-global agentStop
n=0
while :; do
  if "$binary" --repo "$work_copilot_global" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="copilot" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Copilot global turn not recorded' >&2; exit 1; }
  sleep 0.05
done
printf '{"cwd":"%s","sessionId":"copilot-global","prompt":"Interrupted edit"}' "$work_copilot_global" | "$binary" hook copilot-global userPromptSubmitted
printf 'interrupted\n' > "$work_copilot_global/main.txt"
printf '{"cwd":"%s","sessionId":"copilot-global","reason":"abort"}' "$work_copilot_global" | "$binary" hook copilot-global sessionEnd
n=0
while :; do
  if "$binary" --repo "$work_copilot_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Interrupted edit" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Copilot interrupted turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_copilot_global" agent add copilot >/dev/null
printf '{"cwd":"%s","sessionId":"copilot-project","prompt":"duplicate"}' "$work_copilot_global" | "$binary" hook copilot-global userPromptSubmitted
printf '{"cwd":"%s","sessionId":"copilot-project","prompt":"project prompt"}' "$work_copilot_global" | "$binary" --repo "$work_copilot_global" hook copilot userPromptSubmitted
printf '{"cwd":"%s","sessionId":"copilot-project"}' "$work_copilot_global" | "$binary" --repo "$work_copilot_global" hook copilot agentStop
n=0
while :; do
  if "$binary" --repo "$work_copilot_global" --json show 3 2>/dev/null | python3 -c 'import json,sys; assert json.load(sys.stdin)["prompt"]=="project prompt"' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Copilot project overlap not handled' >&2; exit 1; }
  sleep 0.05
done

git -C "$work_windsurf_global" init -q
printf 'before\n' > "$work_windsurf_global/main.txt"
(cd "$work_windsurf_global" && printf '{"trajectory_id":"windsurf-global","agent_action_name":"pre_user_prompt","tool_info":{"user_prompt":"Change main"}}' | "$binary" hook windsurf-global pre_user_prompt)
n=0
while [ ! -e "$work_windsurf_global/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Windsurf daemon not ready' >&2; exit 1; }
  sleep 0.05
done
printf 'after\n' > "$work_windsurf_global/main.txt"
(cd "$work_windsurf_global" && printf '{"trajectory_id":"windsurf-global","agent_action_name":"post_write_code","tool_info":{"file_path":"%s/main.txt"}}' "$work_windsurf_global" | "$binary" hook windsurf-global post_write_code)
(cd "$work_windsurf_global" && printf '{"trajectory_id":"windsurf-global","agent_action_name":"post_cascade_response","tool_info":{"response":"Done"}}' | "$binary" hook windsurf-global post_cascade_response)
n=0
while :; do
  if "$binary" --repo "$work_windsurf_global" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="windsurf" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Windsurf global turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_windsurf_global" agent add windsurf >/dev/null
for scope in windsurf-global windsurf; do
  (cd "$work_windsurf_global" && printf '{"trajectory_id":"windsurf-global","agent_action_name":"pre_user_prompt","tool_info":{"user_prompt":"Project overlap"}}' | "$binary" hook "$scope" pre_user_prompt)
done
printf 'project edit\n' > "$work_windsurf_global/main.txt"
for scope in windsurf-global windsurf; do
  (cd "$work_windsurf_global" && printf '{"trajectory_id":"windsurf-global","agent_action_name":"post_write_code","tool_info":{"file_path":"%s/main.txt"}}' "$work_windsurf_global" | "$binary" hook "$scope" post_write_code)
done
for scope in windsurf-global windsurf; do
  (cd "$work_windsurf_global" && printf '{"trajectory_id":"windsurf-global","agent_action_name":"post_cascade_response","tool_info":{"response":"Done"}}' | "$binary" hook "$scope" post_cascade_response)
done
n=0
while :; do
  if "$binary" --repo "$work_windsurf_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Project overlap" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Windsurf project overlap turn not recorded' >&2; exit 1; }
  sleep 0.05
done
python3 -c 'import sqlite3,sys; db=sqlite3.connect(sys.argv[1]); n=db.execute("select count(*) from ops o join turns t on o.turn_id=t.id where t.seq=2 and o.source=\"tool\" and o.start_line is null").fetchone()[0]; assert n==1,n' "$work_windsurf_global/.trace/trace.db"

git -C "$work_factory_global" init -q
printf 'before\n' > "$work_factory_global/main.txt"
output=$(cd "$work" && printf '{"cwd":"%s","session_id":"factory-global","hook_event_name":"UserPromptSubmit","prompt":"Change main"}' "$work_factory_global" | "$binary" hook factory-global UserPromptSubmit)
[ -z "$output" ] || { echo 'Factory hook produced output' >&2; exit 1; }
n=0
while [ ! -e "$work_factory_global/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Factory daemon not ready' >&2; exit 1; }
  sleep 0.05
done
printf 'after\n' > "$work_factory_global/main.txt"
printf '{"cwd":"%s","session_id":"factory-global","hook_event_name":"SubagentStop","task_result":"Subagent finished"}' "$work_factory_global" | "$binary" hook factory-global SubagentStop
printf '{"cwd":"%s","session_id":"factory-global","hook_event_name":"Notification","notification_type":"idle_prompt"}' "$work_factory_global" | "$binary" hook factory-global Notification
n=0
while :; do
  if "$binary" --repo "$work_factory_global" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="factory" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Factory cancelled turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_factory_global" --json messages | python3 -c 'import json,sys; assert any("Subagent finished" in m["content"] for m in json.load(sys.stdin))'
printf '{"cwd":"%s","session_id":"factory-global","hook_event_name":"UserPromptSubmit","prompt":"End session"}' "$work_factory_global" | "$binary" hook factory-global UserPromptSubmit
printf 'unreported change\n' > "$work_factory_global/main.txt"
printf '{"cwd":"%s","session_id":"factory-global","hook_event_name":"SessionEnd"}' "$work_factory_global" | "$binary" hook factory-global SessionEnd
n=0
while :; do
  if "$binary" --repo "$work_factory_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="End session" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Factory session end did not capture changes' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_factory_global" agent add factory >/dev/null
(cd "$work" && printf '{"cwd":"%s","session_id":"factory-project","hook_event_name":"UserPromptSubmit","prompt":"Project route"}' "$work" | FACTORY_PROJECT_DIR="$work_factory_global" "$binary" hook factory UserPromptSubmit)
printf 'project change\n' > "$work_factory_global/main.txt"
(cd "$work" && printf '{"cwd":"%s","session_id":"factory-project","hook_event_name":"Stop"}' "$work" | FACTORY_PROJECT_DIR="$work_factory_global" "$binary" hook factory Stop)
n=0
while :; do
  if "$binary" --repo "$work_factory_global" --json show 3 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Project route" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Factory project route not recorded' >&2; exit 1; }
  sleep 0.05
done

git -C "$work_qwen_global" init -q
printf 'before\n' > "$work_qwen_global/main.txt"
output=$(cd "$work" && printf '{"cwd":"%s","session_id":"qwen-global","hook_event_name":"UserPromptSubmit","prompt":"internal continuation"}' "$work_qwen_global" | "$binary" hook qwen-global UserPromptSubmit)
[ "$output" = '{}' ] || { echo 'Qwen hook changed behavior' >&2; exit 1; }
n=0
while [ ! -e "$work_qwen_global/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Qwen daemon did not start' >&2; exit 1; }
  sleep 0.05
done
(cd "$work" && printf '{"cwd":"%s","session_id":"qwen-global","hook_event_name":"UserPromptSubmit","submitted_prompt":"Change main"}' "$work_qwen_global" | "$binary" hook qwen-global UserPromptSubmit >/dev/null)
(cd "$work" && printf '{"cwd":"%s","session_id":"qwen-global","agent_id":"child","hook_event_name":"Stop"}' "$work_qwen_global" | "$binary" hook qwen-global Stop >/dev/null)
printf 'after\n' > "$work_qwen_global/main.txt"
(cd "$work" && printf '{"cwd":"%s","session_id":"qwen-global","hook_event_name":"StopFailure"}' "$work_qwen_global" | "$binary" hook qwen-global StopFailure >/dev/null)
n=0
while :; do
  if "$binary" --repo "$work_qwen_global" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="qwen" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Qwen global turn not recorded' >&2; exit 1; }
  sleep 0.05
done
printf '{"cwd":"%s","session_id":"qwen-global","hook_event_name":"UserPromptSubmit","submitted_prompt":"End session"}' "$work_qwen_global" | "$binary" hook qwen-global UserPromptSubmit >/dev/null
printf 'unreported change\n' > "$work_qwen_global/main.txt"
printf '{"cwd":"%s","session_id":"qwen-global","hook_event_name":"SessionEnd"}' "$work_qwen_global" | "$binary" hook qwen-global SessionEnd >/dev/null
n=0
while :; do
  if "$binary" --repo "$work_qwen_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="End session" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Qwen session end did not capture changes' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_qwen_global" agent add qwen >/dev/null
(cd "$work" && printf '{"cwd":"%s","session_id":"qwen-project","hook_event_name":"UserPromptSubmit","submitted_prompt":"Project route"}' "$work_qwen_global" | "$binary" hook qwen UserPromptSubmit >/dev/null)
printf 'project change\n' > "$work_qwen_global/main.txt"
(cd "$work" && printf '{"cwd":"%s","session_id":"qwen-project","hook_event_name":"Stop"}' "$work_qwen_global" | "$binary" hook qwen Stop >/dev/null)
n=0
while :; do
  if "$binary" --repo "$work_qwen_global" --json show 3 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Project route" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Qwen project route not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_cursor_global" init -q
printf 'before\n' > "$work_cursor_global/main.go"
multi_output=$(printf '{"workspace_roots":["%s","%s"],"conversation_id":"multi","hook_event_name":"beforeSubmitPrompt","prompt":"ambiguous"}' "$work_cursor_global" "$work" | "$binary" hook cursor-global beforeSubmitPrompt)
[ "$multi_output" = '{"continue":true}' ] && [ ! -e "$work_cursor_global/.trace" ] || { echo 'Cursor multiroot was not skipped' >&2; exit 1; }
(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"cursor-global-e2e","hook_event_name":"sessionStart"}' "$work_cursor_global" | "$binary" hook cursor-global sessionStart >/dev/null)
n=0
while [ ! -e "$work_cursor_global/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Cursor global daemon did not start' >&2; exit 1; }
  sleep 0.05
done
global_output=$(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"cursor-global-e2e","hook_event_name":"beforeSubmitPrompt","prompt":"Global cursor turn"}' "$work_cursor_global" | "$binary" hook cursor-global beforeSubmitPrompt)
[ "$global_output" = '{"continue":true}' ] || { echo 'Cursor global hook changed behavior' >&2; exit 1; }
printf 'after\n' > "$work_cursor_global/main.go"
(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"cursor-global-e2e","hook_event_name":"subagentStop","modified_files":["main.go"],"summary":"Subagent completed edit"}' "$work_cursor_global" | "$binary" hook cursor-global subagentStop >/dev/null)
(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"cursor-global-e2e","hook_event_name":"afterFileEdit","file_path":"%s/main.go"}' "$work_cursor_global" "$work_cursor_global" | "$binary" hook cursor-global afterFileEdit >/dev/null)
(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"cursor-global-e2e","hook_event_name":"stop"}' "$work_cursor_global" | "$binary" hook cursor-global stop >/dev/null)
n=0
while :; do
  if "$binary" --repo "$work_cursor_global" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="cursor" and s["prompt"]=="Global cursor turn" and "main.go" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Cursor global turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_cursor_global" --json messages | python3 -c 'import json,sys; assert any("Subagent completed edit" in m["content"] for m in json.load(sys.stdin))'
printf '{"workspace_roots":["%s"],"conversation_id":"cursor-global-e2e","hook_event_name":"beforeSubmitPrompt","prompt":"End session"}' "$work_cursor_global" | "$binary" hook cursor-global beforeSubmitPrompt >/dev/null
printf 'unreported change\n' > "$work_cursor_global/main.go"
printf '{"workspace_roots":["%s"],"session_id":"cursor-global-e2e","hook_event_name":"sessionEnd"}' "$work_cursor_global" | "$binary" hook cursor-global sessionEnd >/dev/null
n=0
while :; do
  if "$binary" --repo "$work_cursor_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="End session" and "main.go" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Cursor session end did not capture changes' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_cursor_global" agent add cursor >/dev/null
(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"cursor-project","hook_event_name":"beforeSubmitPrompt","prompt":"Project route"}' "$work_cursor_global" | "$binary" hook cursor beforeSubmitPrompt >/dev/null)
printf 'project change\n' > "$work_cursor_global/main.go"
(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"cursor-project","hook_event_name":"stop"}' "$work_cursor_global" | "$binary" hook cursor stop >/dev/null)
n=0
while :; do
  if "$binary" --repo "$work_cursor_global" --json show 3 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Project route" and "main.go" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Cursor project route not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_fork" init -q
printf 'base\n' > "$work_fork/fork.txt"
git -C "$work_fork" add fork.txt
git -C "$work_fork" -c user.name=Trace -c user.email=trace@example.invalid -c commit.gpgsign=false commit -qm initial
hook_path=$(git -C "$work_fork" rev-parse --git-path hooks/post-commit)
case "$hook_path" in /*) ;; *) hook_path="$work_fork/$hook_path" ;; esac
printf '#!/bin/sh\nprintf chained > "%s/chain-ran"\n' "$work_fork" > "$hook_path"
chmod +x "$hook_path"
"$binary" --repo "$work_fork" init >/dev/null
printf '{"session_id":"fork-e2e","hook_event_name":"UserPromptSubmit","prompt":"Change fork.txt"}\n' | "$binary" --repo "$work_fork" hook claude-code UserPromptSubmit >/dev/null
printf 'changed\n' > "$work_fork/fork.txt"
printf '{"session_id":"fork-e2e","hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"%s/fork.txt"}}\n' "$work_fork" | "$binary" --repo "$work_fork" hook claude-code PostToolUse >/dev/null
printf '{"session_id":"fork-e2e","hook_event_name":"Stop"}\n' | "$binary" --repo "$work_fork" hook claude-code Stop >/dev/null
n=0
while :; do
  if "$binary" --repo "$work_fork" --json show 1 2>/dev/null | python3 -c 'import json,sys; assert json.load(sys.stdin)["step"]==1' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'fork source turn not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_fork" add fork.txt
git -C "$work_fork" -c user.name=Trace -c user.email=trace@example.invalid -c commit.gpgsign=false commit -qm changed
[ "$(cat "$work_fork/chain-ran")" = chained ] || { echo 'existing post-commit hook was not chained' >&2; exit 1; }
commit_sha=$(git -C "$work_fork" rev-parse HEAD)
n=0
while :; do
  if python3 - "$work_fork/.trace/trace.db" "$commit_sha" 2>/dev/null <<'PY'
import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    row = db.execute('SELECT k.seq FROM commits c JOIN keyframes k ON k.id=c.keyframe_id WHERE c.sha=?', (sys.argv[2],)).fetchone()
assert row == (1,), row
PY
  then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'commit not linked to keyframe' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_fork" fork 1 --agent agy --no-launch --name e2e >/dev/null
[ "$(cat "$fork_target/fork.txt")" = changed ] || { echo 'fork tree differs from keyframe' >&2; exit 1; }
grep -F 'Change fork.txt' "$fork_target/.trace/context.md" >/dev/null || { echo 'fork context missing' >&2; exit 1; }
printf '{"conversationId":"forked-session"}\n' | "$binary" --repo "$fork_target" hook agy SessionStart >/dev/null
n=0
while :; do
  if python3 - "$fork_target/.trace/trace.db" 2>/dev/null <<'PY'
import sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    row = db.execute('SELECT forked_from FROM sessions WHERE agent=?', ('agy',)).fetchone()
assert row and row[0].endswith(':1'), row
PY
  then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'fork origin not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_cline" init -q
printf 'before\n' > "$work_cline/main.txt"
printf 'context\n' > "$work_cline/context.txt"
"$binary" --repo "$work_cline" init --agent cline >/dev/null
output=$(printf '{"taskId":"cline-e2e","hookName":"agent_start","taskStart":{"taskMetadata":{"initialTask":"Change main"}}}' | "$work_cline/.cline/hooks/TaskStart")
[ "$output" = '{"cancel":false}' ] || { echo 'Cline task hook changed agent behavior' >&2; exit 1; }
printf '{"taskId":"cline-e2e","hookName":"prompt_submit","userPromptSubmit":{"prompt":"Change main"}}' | "$work_cline/.cline/hooks/UserPromptSubmit" >/dev/null
printf '{"taskId":"cline-e2e","workspaceRoots":["%s"],"postToolUse":{"toolName":"read_files","success":true,"parameters":{"files":[{"path":"main.txt"},{"path":"context.txt"}]}}}' "$work_cline" | "$work_cline/.cline/hooks/PostToolUse" >/dev/null
printf 'after\n' > "$work_cline/main.txt"
# Completion must pick up edits even when no supported tool hook reported them.
printf '{"taskId":"cline-e2e","hookName":"agent_end","turn":{"outputText":"Finished Cline work"}}' | "$work_cline/.cline/hooks/TaskComplete" >/dev/null
n=0
while :; do
  if "$binary" --repo "$work_cline" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="cline" and s["prompt"]=="Change main" and "main.txt" in s["files"] and "Finished Cline work" in s["reasoning"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Cline turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_cline" --json messages | python3 -c 'import json,sys; assert any(m["role"]=="assistant" and "Finished Cline work" in m["content"] for m in json.load(sys.stdin))'
python3 - "$work_cline/.trace/trace.db" <<'PY'
import json, sqlite3, sys
with sqlite3.connect(sys.argv[1]) as db:
    row = db.execute('SELECT files_read FROM turns WHERE prompt=?', ('Change main',)).fetchone()
    count = db.execute('SELECT count(*) FROM turns WHERE prompt=?', ('Change main',)).fetchone()[0]
assert row and json.loads(row[0]) == ['context.txt', 'main.txt'], row
assert count == 1, count
PY
git -C "$work_auggie" init -q
printf 'before\n' > "$work_auggie/main.txt"
auggie_multi_output=$(cd "$work" && printf '{"cwd":"%s","workspace_roots":["%s","%s"],"conversation_id":"ambiguous","user_prompt":"ambiguous"}' "$work" "$work_auggie" "$work" | "$binary" hook auggie PromptSubmit)
[ -z "$auggie_multi_output" ] && [ ! -e "$work_auggie/.trace" ] || { echo 'Auggie multiroot was not skipped' >&2; exit 1; }
(cd "$work" && printf '{"cwd":"%s","workspace_roots":["%s"],"conversation_id":"auggie-e2e"}' "$work" "$work_auggie" | "$binary" hook auggie SessionStart)
n=0
while [ ! -e "$work_auggie/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Auggie daemon did not start' >&2; exit 1; }
  sleep 0.05
done
(cd "$work" && printf '{"cwd":"%s","workspace_roots":["%s"],"conversation_id":"auggie-e2e","user_prompt":"Change main"}' "$work" "$work_auggie" | "$binary" hook auggie PromptSubmit)
printf 'after\n' > "$work_auggie/main.txt"
(cd "$work" && printf '{"cwd":"%s","workspace_roots":["%s"],"conversation_id":"auggie-e2e","tool_name":"edit","tool_input":{"path":"main.txt"}}' "$work" "$work_auggie" | "$binary" hook auggie PostToolUse)
(cd "$work" && printf '{"cwd":"%s","workspace_roots":["%s"],"conversation_id":"auggie-e2e","agent_stop_cause":"end_turn","conversation":{"agentTextResponse":"Auggie finished"}}' "$work" "$work_auggie" | "$binary" hook auggie Stop)
n=0
while :; do
  if "$binary" --repo "$work_auggie" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="auggie" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Auggie turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_auggie" --json messages | python3 -c 'import json,sys; assert any("Auggie finished" in m["content"] for m in json.load(sys.stdin))'
(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"auggie-e2e","user_prompt":"End session"}' "$work_auggie" | "$binary" hook auggie PromptSubmit)
printf 'unreported change\n' > "$work_auggie/main.txt"
(cd "$work" && printf '{"workspace_roots":["%s"],"conversation_id":"auggie-e2e"}' "$work_auggie" | "$binary" hook auggie SessionEnd)
n=0
while :; do
  if "$binary" --repo "$work_auggie" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="End session" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Auggie session end did not capture changes' >&2; exit 1; }
  sleep 0.05
done
for agent in claude-code kimi codex; do
  case "$agent" in
    claude-code) run_repo=$work_claude_global; tool=Write ;;
    kimi) run_repo=$work_kimi_global; tool=Write ;;
    codex) run_repo=$work_codex_global; tool=Write ;;
  esac
  git -C "$run_repo" init -q
  printf 'before\n' > "$run_repo/main.txt"
  (cd "$work" && printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"SessionStart"}' "$run_repo" | "$binary" hook "$agent" SessionStart)
  n=0
  while [ ! -e "$run_repo/.trace/spool.ready" ]; do
    n=$((n+1)); [ "$n" -lt 100 ] || { echo "$agent daemon did not start" >&2; exit 1; }
    sleep 0.05
  done
  if [ "$agent" = kimi ]; then
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"UserPromptSubmit","prompt":[{"type":"text","text":"Change main"}]}' "$run_repo" | "$binary" hook "$agent" UserPromptSubmit)
  else
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"UserPromptSubmit","prompt":"Change main"}' "$run_repo" | "$binary" hook "$agent" UserPromptSubmit)
  fi
  printf 'after\n' > "$run_repo/main.txt"
  if [ "$agent" = kimi ]; then
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"PostToolUse","tool_name":"Read","tool_input":{"path":"main.txt"}}' "$run_repo" | "$binary" hook "$agent" PostToolUse)
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"PostToolUse","tool_name":"%s","tool_input":{"path":"main.txt"}}' "$run_repo" "$tool" | "$binary" hook "$agent" PostToolUse)
  elif [ "$agent" != codex ]; then
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"PostToolUse","tool_name":"%s","tool_input":{"file_path":"main.txt"}}' "$run_repo" "$tool" | "$binary" hook "$agent" PostToolUse)
  fi
  if [ "$agent" = codex ]; then
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"Interrupt"}' "$run_repo" | "$binary" hook "$agent" Interrupt)
  else
    (cd "$work" && printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"Stop"}' "$run_repo" | "$binary" hook "$agent" Stop >/dev/null)
  fi
  n=0
  while :; do
    if "$binary" --repo "$run_repo" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
    n=$((n+1)); [ "$n" -lt 100 ] || { echo "$agent turn not recorded in its workspace" >&2; exit 1; }
    sleep 0.05
  done
done
printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"UserPromptSubmit","prompt":[{"type":"text","text":"Interrupted turn"}]}' "$work_kimi_global" | "$binary" hook kimi UserPromptSubmit
printf '{"cwd":"%s","session_id":"global-e2e","agent_id":"child","hook_event_name":"StopFailure"}' "$work_kimi_global" | "$binary" hook kimi StopFailure
printf 'interrupted edit\n' > "$work_kimi_global/main.txt"
printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"SessionEnd","reason":"interrupted"}' "$work_kimi_global" | "$binary" hook kimi SessionEnd
n=0
while :; do
  if "$binary" --repo "$work_kimi_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Interrupted turn" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Kimi session end did not capture changes' >&2; exit 1; }
  sleep 0.05
done
printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"UserPromptSubmit","prompt":"Recover from failure"}' "$work_claude_global" | "$binary" hook claude-code UserPromptSubmit
printf '{"cwd":"%s","session_id":"global-e2e","agent_id":"child","hook_event_name":"StopFailure"}' "$work_claude_global" | "$binary" hook claude-code StopFailure
printf 'unreported edit\n' > "$work_claude_global/main.txt"
printf '{"cwd":"%s","session_id":"global-e2e","hook_event_name":"StopFailure"}' "$work_claude_global" | "$binary" hook claude-code StopFailure
n=0
while :; do
  if "$binary" --repo "$work_claude_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Recover from failure" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Claude failure did not capture unreported edits' >&2; exit 1; }
  sleep 0.05
done
printf '{"cwd":"%s","session_id":"global-e2e","prompt":"Run unknown tool"}' "$work_codex_global" | "$binary" hook codex UserPromptSubmit
printf 'changed by unreported tool\n' > "$work_codex_global/main.txt"
printf '{"cwd":"%s","session_id":"global-e2e","last_assistant_message":"Done"}' "$work_codex_global" | "$binary" hook codex Stop >/dev/null
n=0
while :; do
  if "$binary" --repo "$work_codex_global" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Run unknown tool" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Codex stop did not capture unreported edits' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_zoo" init -q
printf 'before\n' > "$work_zoo/main.txt"
(cd "$work" && printf '{"cwd":"%s","session_id":"zoo-e2e","prompt":"Fix main ghp_abcdefghijklmnopqrstuvwxyz"}' "$work_zoo" | "$binary" hook zoo turn_start)
n=0
while [ ! -e "$work_zoo/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Zoo daemon did not start' >&2; exit 1; }
  sleep 0.05
done
printf 'after\n' > "$work_zoo/main.txt"
(cd "$work" && printf '{"cwd":"%s","session_id":"zoo-e2e"}' "$work_zoo" | "$binary" hook zoo tool_edit)
(cd "$work" && printf '{"cwd":"%s","session_id":"zoo-e2e"}' "$work_zoo" | "$binary" hook zoo turn_end)
n=0
while :; do
  if "$binary" --repo "$work_zoo" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="zoo" and "ghp_abcdefghijklmnopqrstuvwxyz" not in s["prompt"] and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Zoo task not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_cline_global" init -q
printf 'before\n' > "$work_cline_global/main.txt"
CLINE_DIR="$cline_config" "$binary" setup --agent cline >/dev/null
output=$(printf '{"taskId":"multi-cline","workspaceRoots":["%s","%s"]}' "$work_cline_global" "$work" | "$cline_config/hooks/TaskStart")
[ "$output" = '{"cancel":false}' ] && [ ! -e "$work_cline_global/.trace" ] || { echo 'Cline multi-root task was not skipped' >&2; exit 1; }
output=$(printf '{"taskId":"global-cline","workspaceRoots":["%s"]}' "$work_cline_global" | "$cline_config/hooks/TaskStart")
[ "$output" = '{"cancel":false}' ] || { echo 'global Cline hook changed agent behavior' >&2; exit 1; }
n=0
while [ ! -e "$work_cline_global/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'global Cline daemon did not start' >&2; exit 1; }
  sleep 0.05
done
printf '{"taskId":"global-cline","workspaceRoots":["%s"],"userPromptSubmit":{"prompt":"Change main"}}' "$work_cline_global" | "$cline_config/hooks/UserPromptSubmit" >/dev/null
printf 'after\n' > "$work_cline_global/main.txt"
printf '{"taskId":"global-cline","workspaceRoots":["%s"],"postToolUse":{"toolName":"write_to_file","success":true,"parameters":{"path":"main.txt"}}}' "$work_cline_global" | "$cline_config/hooks/PostToolUse" >/dev/null
printf '{"taskId":"global-cline","workspaceRoots":["%s"]}' "$work_cline_global" | "$cline_config/hooks/TaskComplete" >/dev/null
n=0
while :; do
  if "$binary" --repo "$work_cline_global" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="cline" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'global Cline turn not recorded' >&2; exit 1; }
  sleep 0.05
done
CLINE_DIR="$cline_config" "$binary" setup --agent cline --remove >/dev/null
git -C "$work_openhands" init -q
printf 'before\n' > "$work_openhands/main.txt"
if "$binary" --repo "$work_openhands" init --agent openhands >/dev/null 2>&1; then
  echo 'OpenHands init accepted a missing project hooks file' >&2; exit 1
fi
[ ! -e "$work_openhands/.trace" ] || { echo 'Failed OpenHands init created Trace state' >&2; exit 1; }
mkdir -p "$work_openhands/.openhands"
printf '%s\n' '{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"user-hook"}]}]}' > "$work_openhands/.openhands/hooks.json"
"$binary" --repo "$work_openhands" init --agent openhands >/dev/null
"$binary" --repo "$work_openhands" agent add openhands >/dev/null
python3 - "$work_openhands/.openhands/hooks.json" <<'PY'
import json, sys
hooks = json.load(open(sys.argv[1]))
commands = [h['command'] for group in hooks['UserPromptSubmit'] for h in group['hooks']]
assert 'user-hook' in commands and sum(' hook openhands ' in c for c in commands) == 1
PY
(cd "$work" && printf '{"session_id":"openhands-e2e","working_dir":"%s"}' "$work_openhands" | "$binary" hook openhands SessionStart)
n=0
while [ ! -e "$work_openhands/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'OpenHands daemon did not start' >&2; exit 1; }
  sleep 0.05
done
(cd "$work" && printf '{"session_id":"openhands-e2e","working_dir":"%s","message":"Change main"}' "$work_openhands" | "$binary" hook openhands UserPromptSubmit)
printf 'after\n' > "$work_openhands/main.txt"
(cd "$work" && printf '{"session_id":"openhands-e2e","working_dir":"%s"}' "$work_openhands" | "$binary" hook openhands Stop)
n=0
while :; do
  if "$binary" --repo "$work_openhands" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="openhands" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'OpenHands turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_openhands" agent remove openhands >/dev/null
python3 - "$work_openhands/.openhands/hooks.json" <<'PY'
import json, sys
hooks = json.load(open(sys.argv[1]))
commands = [h['command'] for group in hooks['UserPromptSubmit'] for h in group['hooks']]
assert commands == ['user-hook']
PY
git -C "$work_openclaude" init -q
printf 'before\n' > "$work_openclaude/main.txt"
(cd "$work" && printf '{"session_id":"openclaude-e2e","cwd":"%s","hook_event_name":"SessionStart"}' "$work_openclaude" | "$binary" hook openclaude SessionStart)
n=0
while [ ! -e "$work_openclaude/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'OpenClaude daemon did not start' >&2; exit 1; }
  sleep 0.05
done
(cd "$work" && printf '{"session_id":"openclaude-e2e","cwd":"%s","hook_event_name":"UserPromptSubmit","prompt":"Change main"}' "$work_openclaude" | "$binary" hook openclaude UserPromptSubmit)
(cd "$work" && printf '{"session_id":"openclaude-e2e","agent_id":"child","cwd":"%s","hook_event_name":"StopFailure"}' "$work_openclaude" | "$binary" hook openclaude StopFailure)
printf 'after\n' > "$work_openclaude/main.txt"
(cd "$work" && printf '{"session_id":"openclaude-e2e","cwd":"%s","hook_event_name":"PostToolUse","tool_name":"Write","tool_input":{"file_path":"%s/main.txt"}}' "$work_openclaude" "$work_openclaude" | "$binary" hook openclaude PostToolUse)
(cd "$work" && printf '{"session_id":"openclaude-e2e","cwd":"%s","hook_event_name":"Stop"}' "$work_openclaude" | "$binary" hook openclaude Stop)
n=0
while :; do
  if "$binary" --repo "$work_openclaude" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="openclaude" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'OpenClaude turn not recorded' >&2; exit 1; }
  sleep 0.05
done
(cd "$work" && printf '{"session_id":"openclaude-e2e","cwd":"%s","hook_event_name":"UserPromptSubmit","prompt":"Try again"}' "$work_openclaude" | "$binary" hook openclaude UserPromptSubmit)
printf 'failed edit\n' > "$work_openclaude/main.txt"
(cd "$work" && printf '{"session_id":"openclaude-e2e","cwd":"%s","hook_event_name":"StopFailure","last_assistant_message":"Unable to finish"}' "$work_openclaude" | "$binary" hook openclaude StopFailure)
n=0
while :; do
  if "$binary" --repo "$work_openclaude" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="openclaude" and s["prompt"]=="Try again" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'OpenClaude failed turn not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_poolside" init -q
printf 'before\n' > "$work_poolside/main.txt"
output=$(printf '{"session_id":"pool-e2e","cwd":"%s"}' "$work_poolside" | "$binary" hook poolside SessionStart)
[ -z "$output" ] || { echo 'Poolside hook changed agent behavior' >&2; exit 1; }
n=0
while [ ! -e "$work_poolside/.trace/spool.ready" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Poolside daemon did not start' >&2; exit 1; }
  sleep 0.05
done
printf '{"session_id":"pool-e2e","cwd":"%s","prompt":"Change main"}' "$work_poolside" | "$binary" hook poolside UserPromptSubmit
printf 'after\n' > "$work_poolside/main.txt"
# Stop requests a scan even when the editing tool is not reported to Trace.
printf '{"session_id":"pool-e2e","cwd":"%s"}' "$work_poolside" | "$binary" hook poolside Stop
n=0
while :; do
  if "$binary" --repo "$work_poolside" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="poolside" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Poolside turn not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_hermes" init -q
printf 'before\n' > "$work_hermes/main.txt"
HERMES_HOME="$hermes_profile" "$binary" setup --agent hermes >/dev/null
TRACE_HERMES_REPO="$work_hermes" PYTHONDONTWRITEBYTECODE=1 python3 - "$hermes_profile/plugins/trace-observer/__init__.py" <<'PY'
import importlib.util, os, sys, time
spec = importlib.util.spec_from_file_location('trace_observer', sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
class Context:
    def __init__(self): self.hooks = {}
    def register_hook(self, name, callback): self.hooks[name] = callback
ctx = Context()
module.register(ctx)
ctx.hooks['pre_llm_call'](session_id='hermes-e2e', user_message='Change main')
module._events.join()
ready = os.path.join(os.environ['TRACE_HERMES_REPO'], '.trace', 'spool.ready')
for _ in range(100):
    if os.path.exists(ready): break
    time.sleep(.05)
else: raise RuntimeError('Hermes daemon did not start')
with open(os.path.join(os.environ['TRACE_HERMES_REPO'], 'main.txt'), 'w') as f: f.write('after\n')
ctx.hooks['post_tool_call'](session_id='hermes-e2e', tool_name='write_file', args={'path':'main.txt'}, status='ok')
ctx.hooks['post_llm_call'](session_id='hermes-e2e', assistant_response='Done ghp_abcdefghijklmnopqrstuvwxyz')
module._events.join()
ctx.hooks['pre_llm_call'](session_id='hermes-e2e', user_message='Failed turn')
module._events.join()
with open(os.path.join(os.environ['TRACE_HERMES_REPO'], 'main.txt'), 'w') as f: f.write('failed edit\n')
ctx.hooks['on_session_end'](session_id='hermes-e2e', failed=True, completed=False)
module._events.join()
PY
n=0
while :; do
  if "$binary" --repo "$work_hermes" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="hermes" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Hermes turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_hermes" --json messages | python3 -c 'import json,sys; assert any(m["role"]=="assistant" and "Done" in m["content"] and "ghp_abcdefghijklmnopqrstuvwxyz" not in m["content"] for m in json.load(sys.stdin))'
n=0
while :; do
  if "$binary" --repo "$work_hermes" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["prompt"]=="Failed turn" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Hermes failed turn not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_crewai" init -q
printf 'before\n' > "$work_crewai/main.txt"
"$binary" --repo "$work_crewai" init --agent crewai >/dev/null
PYTHONDONTWRITEBYTECODE=1 python3 - "$work_crewai" <<'PY'
import importlib.util, os, sys, types
repo = sys.argv[1]
pkg = types.ModuleType('crewai'); events = types.ModuleType('crewai.events')
class Start: pass
class End: pass
class Fail: pass
class Bus:
    def __init__(self): self.handlers = {}
    def on(self, kind):
        def register(fn): self.handlers[kind] = fn; return fn
        return register
bus = Bus()
class Base:
    def __init__(self): self.setup_listeners(bus)
events.BaseEventListener = Base; events.CrewKickoffStartedEvent = Start; events.CrewKickoffCompletedEvent = End; events.CrewKickoffFailedEvent = Fail
sys.modules['crewai'] = pkg; sys.modules['crewai.events'] = events
spec = importlib.util.spec_from_file_location('trace_crewai', os.path.join(repo, '.trace/integrations/trace_crewai.py'))
module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
listener = module.TraceCrewListener()
bus.handlers[Start](None, types.SimpleNamespace(event_id='crew-e2e', inputs={'prompt':'Change main'}))
listener._events.join()
with open(os.path.join(repo, 'main.txt'), 'w') as f: f.write('after\n')
bus.handlers[End](None, types.SimpleNamespace(started_event_id='crew-e2e'))
listener._events.join()
PY
n=0
while :; do
  if "$binary" --repo "$work_crewai" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="crewai" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'CrewAI execution not recorded' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_aider" init -q
printf 'before\n' > "$work_aider/main.txt"
"$binary" --repo "$work_aider" init --agent aider >/dev/null
PYTHONDONTWRITEBYTECODE=1 python3 - "$work_aider" <<'PY'
import importlib.util, os, sys
repo = sys.argv[1]
spec = importlib.util.spec_from_file_location('trace_aider', os.path.join(repo, '.trace/integrations/trace_aider.py'))
module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
class Coder:
    def run(self, message):
        traced._events.join()
        with open(os.path.join(repo, 'main.txt'), 'w') as f: f.write('after\n')
        return 'done ghp_abcdefghijklmnopqrstuvwxyz'
traced = module.TraceAider(Coder())
assert traced.run('Change main') == 'done ghp_abcdefghijklmnopqrstuvwxyz'
traced.finish()
traced._events.join()
PY
n=0
while :; do
  if "$binary" --repo "$work_aider" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="aider" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Aider scripted turn not recorded' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_aider" --json messages | python3 -c 'import json,sys; messages=json.load(sys.stdin); assert any(m["role"]=="assistant" and "done" in m["content"] and "ghp_abcdefghijklmnopqrstuvwxyz" not in m["content"] for m in messages)'
if command -v node >/dev/null 2>&1; then
  git -C "$work_codebuff" init -q
  printf 'before\n' > "$work_codebuff/main.txt"
  "$binary" --repo "$work_codebuff" init --agent codebuff >/dev/null
  node --input-type=module - "$work_codebuff" <<'JS'
import { pathToFileURL } from 'node:url';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';
const repo = process.argv[2];
const { traceCodebuff } = await import(pathToFileURL(join(repo, '.trace/integrations/trace_codebuff.mjs')).href);
const client = { async run() { await new Promise(r => setTimeout(r, 200)); writeFileSync(join(repo, 'main.txt'), 'after\n'); return 'done'; } };
const traced = traceCodebuff(client);
if (await traced.run({prompt: 'Change main'}) !== 'done') throw new Error('result changed');
traced.finish();
await new Promise(r => setTimeout(r, 600));
JS
  n=0
  while :; do
    if "$binary" --repo "$work_codebuff" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="codebuff" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
    n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Codebuff SDK turn not recorded' >&2; exit 1; }
    sleep 0.05
  done
fi
for agent in continue vibe crush aider; do
  case "$agent" in
    continue) run_repo=$work_continue; run_binary=cn ;;
    vibe) run_repo=$work_vibe; run_binary=vibe ;;
    crush) run_repo=$work_crush; run_binary=crush ;;
    aider) run_repo=$work_aider_run; run_binary=aider ;;
  esac
  git -C "$run_repo" init -q
  printf 'before\n' > "$run_repo/main.txt"
  printf '#!/bin/sh\nprintf "agent output"\nprintf "after\\n" > main.txt\n' > "$mock_agents/$run_binary"
  chmod +x "$mock_agents/$run_binary"
  output=$(cd "$run_repo" && PATH="$mock_agents:$PATH" "$binary" run "$agent" --prompt 'Change main')
  [ "$output" = 'agent output' ] || { echo "$agent launcher changed output" >&2; exit 1; }
  "$binary" --repo "$run_repo" --json show 1 | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]==sys.argv[1] and s["prompt"]=="Change main" and "main.txt" in s["files"]' "$agent"
  printf '#!/bin/sh\nprintf "agent error" >&2\nexit 7\n' > "$mock_agents/$run_binary"
  chmod +x "$mock_agents/$run_binary"
  if (cd "$run_repo" && PATH="$mock_agents:$PATH" "$binary" run "$agent" --prompt 'Fail' >/dev/null 2>"$run_repo/error.txt"); then
    echo "$agent launcher lost exit status" >&2; exit 1
  else
    status=$?
    [ "$status" -eq 7 ] && [ "$(cat "$run_repo/error.txt")" = 'agent error' ] || { echo "$agent launcher changed error behavior" >&2; exit 1; }
  fi
done
python3 - "$binary" "$work_vibe" "$mock_agents" <<'PY'
import os, pty, select, subprocess, sys, time
binary, repo, mock = sys.argv[1:]
vibe = os.path.join(mock, 'vibe')
with open(vibe, 'w') as f:
    f.write('#!/bin/sh\n[ -t 1 ] && [ -t 2 ] || exit 19\nprintf "terminal streams preserved\\n"\n')
os.chmod(vibe, 0o700)
try:
    master, slave = pty.openpty()
except OSError:
    sys.exit(0)  # PTYs can be unavailable in restricted CI sandboxes.
env = dict(os.environ, PATH=mock + os.pathsep + os.environ['PATH'])
proc = subprocess.Popen([binary, '--repo', repo, 'run', 'vibe', '--prompt', 'terminal test'],
                        cwd=repo, stdout=slave, stderr=slave, env=env)
os.close(slave)
data = bytearray(); deadline = time.monotonic() + 15
while proc.poll() is None and time.monotonic() < deadline:
    if select.select([master], [], [], 0.1)[0]:
        try: data.extend(os.read(master, 4096))
        except OSError: break
if proc.poll() is None:
    proc.kill(); proc.wait(); raise AssertionError('terminal run timed out')
while select.select([master], [], [], 0)[0]:
    try:
        chunk = os.read(master, 4096)
        if not chunk: break
        data.extend(chunk)
    except OSError: break
os.close(master)
assert proc.returncode == 0 and b'terminal streams preserved' in data, (proc.returncode, bytes(data))
PY
git -C "$work_goose" init -q
printf 'before\n' > "$work_goose/main.txt"
"$binary" --repo "$work_goose" init --agent goose >/dev/null
goose_plugin="$work_goose/.agents/plugins/trace"
(cd "$work" && printf '{"session_id":"goose-e2e","event":"UserPromptSubmit","message":"Change main"}\n' | PLUGIN_ROOT="$goose_plugin" "$binary" hook goose UserPromptSubmit)
sleep 0.3
printf 'after\n' > "$work_goose/main.txt"
(cd "$work" && printf '{"session_id":"goose-e2e","event":"Stop","last_assistant_message":"finished"}\n' | PLUGIN_ROOT="$goose_plugin" "$binary" hook goose Stop)
n=0
while :; do
  if "$binary" --repo "$work_goose" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="goose" and s["prompt"]=="Change main" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Goose project hook routed incorrectly' >&2; exit 1; }
  sleep 0.05
done
git -C "$work_kiro" init -q
printf 'before\n' > "$work_kiro/main.txt"
"$binary" --repo "$work_kiro" init --agent kiro >/dev/null
(cd "$work" && printf '{"session_id":"kiro-overlap","cwd":"%s","prompt":"duplicate"}' "$work_kiro" | "$binary" hook kiro-global UserPromptSubmit)
(cd "$work" && printf '{"session_id":"kiro-overlap","cwd":"%s","prompt":"project prompt"}' "$work_kiro" | "$binary" hook kiro UserPromptSubmit)
sleep 0.3
printf 'after\n' > "$work_kiro/main.txt"
(cd "$work" && printf '{"session_id":"kiro-overlap","cwd":"%s"}' "$work_kiro" | "$binary" hook kiro-global Stop)
(cd "$work" && printf '{"session_id":"kiro-overlap","cwd":"%s"}' "$work_kiro" | "$binary" hook kiro Stop)
n=0
while :; do
  if "$binary" --repo "$work_kiro" --json show 1 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="kiro" and s["prompt"]=="project prompt" and "main.txt" in s["files"]' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Kiro project/global overlap was not resolved' >&2; exit 1; }
  sleep 0.05
done
"$binary" --repo "$work_kiro" agent remove kiro >/dev/null
(cd "$work" && printf '{"session_id":"kiro-global","cwd":"%s","prompt":"global prompt"}' "$work_kiro" | "$binary" hook kiro-global UserPromptSubmit)
sleep 0.3
printf 'later\n' > "$work_kiro/main.txt"
(cd "$work" && printf '{"session_id":"kiro-global","cwd":"%s"}' "$work_kiro" | "$binary" hook kiro-global Stop)
n=0
while :; do
  if "$binary" --repo "$work_kiro" --json show 2 2>/dev/null | python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["agent"]=="kiro" and s["prompt"]=="global prompt"' 2>/dev/null; then break; fi
  n=$((n+1)); [ "$n" -lt 100 ] || { echo 'Kiro global turn was not recorded' >&2; exit 1; }
  sleep 0.05
done
echo 'e2e passed'
