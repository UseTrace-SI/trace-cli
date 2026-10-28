#!/bin/sh
set -eu
project=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac
archive=
for candidate in "$project"/dist/trace_*_"${os}_${arch}".tar.gz; do
  if [ -f "$candidate" ]; then
    archive=$candidate
    break
  fi
done
[ -n "$archive" ] || { echo 'snapshot archive not found' >&2; exit 1; }
name=${archive##*/}
version=${name#trace_}
version=${version%_${os}_${arch}.tar.gz}
base=$(python3 - "$project/dist" <<'PY'
from pathlib import Path
import sys
print(Path(sys.argv[1]).resolve().as_uri())
PY
)
install_dir=$(mktemp -d)
trap 'rm -rf "$install_dir"' EXIT HUP INT TERM
TRACE_VERSION="v$version" TRACE_RELEASE_BASE_URL="$base" TRACE_INSTALL_DIR="$install_dir" "$project/scripts/install.sh"
"$install_dir/trace" version
TRACE_E2E_BINARY="$install_dir/trace" "$project/scripts/e2e.sh"
