#!/usr/bin/env bash
set -euo pipefail

project_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$project_root"

results=$(go test ./internal/engine ./internal/cli -run '^$' \
  -bench 'Benchmark(Keyframe100ChangedFilesIn10000FileRepo|Rewind1000Files|Log5000Steps|Why5000UnrelatedChanges)$' \
  -benchtime=1x -count=1)
printf '%s\n' "$results"

check_benchmark() {
  local name=$1 limit_ns=$2 measured
  measured=$(awk -v name="$name" '$1 ~ ("^Benchmark" name "-[0-9]+$") {
    for (i=2; i<NF; i++) if ($(i+1)=="ns/op") { print $i; exit }
  }' <<< "$results")
  if [[ -z "$measured" ]]; then
    echo "missing benchmark: $name" >&2
    exit 1
  fi
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

go build -o bin/trace ./cmd/trace
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
import subprocess
import sys
import time

root = sys.argv[1]
payload = '{"conversationId":"perf-turn","invocationNum":0}\n'
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
p95 = sorted(durations)[189]
print(f"full hook process p95: {p95:.2f} ms")
if p95 >= 20:
    raise SystemExit("full hook process exceeded 20 ms p95")
PY
