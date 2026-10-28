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
work_agy=$(mktemp -d)
work_cursor=$(mktemp -d)
work_fork=$(mktemp -d)
fork_target="${work_fork}-fork-1-e2e"
cleanup() {
  "$binary" --repo "$work" daemon stop >/dev/null 2>&1 || true
  "$binary" --repo "$work_agy" daemon stop >/dev/null 2>&1 || true
  "$binary" --repo "$work_cursor" daemon stop >/dev/null 2>&1 || true
  "$binary" --repo "$work_fork" daemon stop >/dev/null 2>&1 || true
  "$binary" --repo "$fork_target" daemon stop >/dev/null 2>&1 || true
  rm -rf "$work" "$work_agy" "$work_cursor" "$work_fork" "$fork_target"
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
prompt_output=$(printf '{"conversation_id":"cursor-e2e","hook_event_name":"beforeSubmitPrompt","prompt":"Fix Cursor file"}\n' | "$binary" --repo "$work_cursor" hook cursor beforeSubmitPrompt)
[ "$prompt_output" = '{"continue":true}' ] || { echo 'Cursor prompt hook output changed agent behavior' >&2; exit 1; }
printf '{"conversation_id":"cursor-e2e","hook_event_name":"beforeReadFile","file_path":"%s/main.go"}\n' "$work_cursor" | "$binary" --repo "$work_cursor" hook cursor beforeReadFile >/dev/null
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
echo 'e2e passed'
