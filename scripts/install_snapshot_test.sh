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
install_dir=$(mktemp -d)
trap 'rm -rf "$install_dir"' EXIT HUP INT TERM
mkdir "$install_dir/transport"
# Serve this offline standalone fixture through a transport shim. Production
# installer protocol restrictions remain HTTPS-only; no file/http bypass exists.
cat > "$install_dir/transport/curl" <<'PY'
#!/usr/bin/env python3
import os,pathlib,shutil,sys,urllib.parse
args=sys.argv[1:];url=urllib.parse.urlsplit(args[-1])
if url.scheme!='https' or url.netloc!='snapshot.invalid' or '/' in url.path.lstrip('/'):
 raise SystemExit('unexpected fixture URL')
shutil.copyfile(pathlib.Path(os.environ['TRACE_SNAPSHOT_SOURCE'])/url.path.lstrip('/'),args[args.index('-o')+1])
PY
chmod +x "$install_dir/transport/curl"
PATH="$install_dir/transport:$PATH" TRACE_SNAPSHOT_SOURCE="$project/dist" TRACE_REQUIRE_COMPANIONS=0 TRACE_VERSION="v$version" TRACE_RELEASE_BASE_URL="https://snapshot.invalid" TRACE_INSTALL_DIR="$install_dir" "$project/scripts/install.sh"
"$install_dir/trace" version
TRACE_E2E_BINARY="$install_dir/trace" "$project/scripts/e2e.sh"
