#!/bin/sh
set -eu
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in darwin|linux) ;; *) echo "unsupported OS: $os" >&2; exit 1 ;; esac
case "$arch" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "unsupported architecture: $arch" >&2; exit 1 ;; esac
tag=${TRACE_VERSION:-}
if [ -z "$tag" ]; then
  release_url=$(curl -fLsS -o /dev/null -w '%{url_effective}' https://github.com/UseTrace-SI/trace-cli/releases/latest)
  tag=${release_url##*/}
fi
version=${tag#v}
archive="trace_${version}_${os}_${arch}.tar.gz"
base=${TRACE_RELEASE_BASE_URL:-"https://github.com/UseTrace-SI/trace-cli/releases/download/$tag"}
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT HUP INT TERM
curl -fLsS -o "$temp_dir/$archive" "$base/$archive"
curl -fLsS -o "$temp_dir/checksums.txt" "$base/checksums.txt"
expected=$(awk -v file="$archive" '$2 == file { print $1 }' "$temp_dir/checksums.txt")
if [ -z "$expected" ]; then echo 'archive checksum is missing' >&2; exit 1; fi
if command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$temp_dir/$archive" | awk '{ print $1 }')
else
  actual=$(sha256sum "$temp_dir/$archive" | awk '{ print $1 }')
fi
if [ "$actual" != "$expected" ]; then echo 'archive checksum mismatch' >&2; exit 1; fi
tar -xzf "$temp_dir/$archive" -C "$temp_dir" trace
install_dir=${TRACE_INSTALL_DIR:-"$HOME/.local/bin"}
mkdir -p "$install_dir"
install -m 0755 "$temp_dir/trace" "$install_dir/trace"
ln -sf trace "$install_dir/trc"
echo "Installed trace and trc in $install_dir"
