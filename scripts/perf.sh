#!/usr/bin/env bash
set -euo pipefail

project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project_root"

results=$(go test ./internal/engine ./internal/cli -run '^$' \
  -bench 'Benchmark(Keyframe100ChangedFilesIn10000FileRepo|Rewind1000Files|Log5000Steps|Why5000UnrelatedChanges)$' \
  -benchtime=1x -count=3)
printf '%s\n' "$results"

check_benchmark() {
  local name=$1 limit_ns=$2 measured samples
  samples=$(awk -v name="$name" '$1 ~ ("^Benchmark" name "-[0-9]+$") {
    for (i=2; i<NF; i++) if ($(i+1)=="ns/op") { print $i; break }
  }' <<< "$results")
  if [[ $(wc -l <<< "$samples") -ne 3 ]]; then
    echo "expected three benchmark samples for $name" >&2
    exit 1
  fi
  measured=$(sort -n <<< "$samples" | sed -n '2p')
  if awk -v measured="$measured" -v limit="$limit_ns" 'BEGIN { exit !(measured > limit) }'; then
    echo "$name exceeded ${limit_ns} ns/op: $measured" >&2
    exit 1
  fi
}

# Limits allow 20% above the phase 11 targets in IMPLEMENTATION_PLAN.md.
check_benchmark Keyframe100ChangedFilesIn10000FileRepo 240000000
check_benchmark Rewind1000Files 1200000000
check_benchmark Log5000Steps 120000000
check_benchmark Why5000UnrelatedChanges 120000000

# Match the stripped binaries shipped by GoReleaser.
go build -ldflags='-s -w' -o bin/trace ./cmd/trace
perf_repo=$(mktemp -d)
cleanup() {
  if [[ -f "$perf_repo/.trace/daemon.pid" ]]; then
    ./bin/trace --repo "$perf_repo" daemon stop >/dev/null 2>&1 || true
  fi
  rm -rf "$perf_repo"
}
trap cleanup EXIT
git -C "$perf_repo" init -q
./bin/trace --repo "$perf_repo" init --agent claude-code >/dev/null
pid=$(cat "$perf_repo/.trace/daemon.pid")
if rss_output=$(ps -o rss= -p "$pid" 2>/dev/null); then
  rss_kb=$(awk '{print $1}' <<< "$rss_output")
  if [[ -z "$rss_kb" || "$rss_kb" -gt 98304 ]]; then
    echo "daemon idle RSS exceeds 96 MiB: ${rss_kb:-unavailable} KiB" >&2
    exit 1
  fi
  echo "daemon idle RSS: $rss_kb KiB"
else
  if [[ ${TRACE_PERF_ALLOW_NO_PS:-0} != 1 ]]; then
    echo "daemon RSS check could not run" >&2
    exit 1
  fi
  echo "daemon RSS check unavailable on this host" >&2
fi

python3 - "$perf_repo" <<'PY'
import os
import json
import subprocess
import sys
import time

root = sys.argv[1]
payload = '{"conversationId":"perf-turn","invocationNum":0}\n'
samples = []
for _ in range(3):
    durations = []
    for _ in range(200):
        start = time.perf_counter()
        result = subprocess.run(
            ["./bin/trace", "--repo", root, "hook", "agy", "PreInvocation"],
            input=payload, text=True, capture_output=True,
        )
        if result.returncode != 0 or result.stdout.strip() != "{}":
            raise SystemExit("hook process failed or changed agent output")
        durations.append((time.perf_counter() - start) * 1000)
    samples.append(sorted(durations)[189])
p95 = sorted(samples)[1]
print(f"full hook process p95 samples: {', '.join(f'{sample:.2f}' for sample in samples)} ms")
print(f"full hook process p95 (median of 3 runs): {p95:.2f} ms (target <20 ms)")
if p95 >= 20:
    if os.environ.get("TRACE_PERF_HOOK_ADVISORY") == "1":
        print("hosted macOS VM hook process timing is advisory", file=sys.stderr)
    else:
        raise SystemExit("full hook process exceeded 20 ms p95")

# Exercise the payload-based global routing and multi-event batch paths too.
cases = [
    ("cursor global read", "cursor-global", "beforeReadFile",
     {"workspace_roots": [root], "conversation_id": "perf-global",
      "hook_event_name": "beforeReadFile", "file_path": os.path.join(root, "file.txt")},
     '{"permission":"allow"}'),
    ("gemini global end", "gemini-global", "AfterAgent",
     {"cwd": root, "session_id": "perf-global", "hook_event_name": "AfterAgent",
      "prompt_response": "done"}, '{}'),
]
for label, agent, hook_event, body, expected in cases:
    samples = []
    for _ in range(3):
        durations = []
        for _ in range(100):
            start = time.perf_counter()
            result = subprocess.run(
                ["./bin/trace", "hook", agent, hook_event],
                input=json.dumps(body), text=True, capture_output=True,
            )
            if result.returncode != 0 or result.stdout.strip() != expected:
                raise SystemExit(f"{label} hook failed or changed agent output")
            durations.append((time.perf_counter() - start) * 1000)
        samples.append(sorted(durations)[94])
    p95 = sorted(samples)[1]
    print(f"{label} hook p95 samples: {', '.join(f'{sample:.2f}' for sample in samples)} ms")
    if p95 >= 20:
        if os.environ.get("TRACE_PERF_HOOK_ADVISORY") == "1":
            print(f"{label} timing is advisory on this host", file=sys.stderr)
        else:
            raise SystemExit(f"{label} hook exceeded 20 ms p95")
PY
