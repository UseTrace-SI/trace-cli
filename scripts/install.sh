#!/bin/sh
# Trace release installer. Compatible with POSIX sh on macOS and Linux.
set -eu

fail() { printf 'Trace installer: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'HELP'
Install the Trace CLI for macOS or Linux (Intel and ARM).

Usage: sh install.sh [--version VERSION] [--install-dir DIRECTORY]

  --version VERSION      Install a specific release, e.g. v1.0.0 (default: latest)
  --install-dir PATH     Binary directory (default: ~/.local/bin)
  -h, --help             Show this help

Environment: TRACE_VERSION and TRACE_INSTALL_DIR provide the same defaults.
No sudo is needed for the default directory. Shell profiles are not modified.
HELP
}

tag=${TRACE_VERSION:-}
bundle_required=${TRACE_REQUIRE_COMPANIONS:-1}
install_dir=${TRACE_INSTALL_DIR:-"$HOME/.local/bin"}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) [ "$#" -ge 2 ] || fail '--version requires a value'; tag=$2; shift 2 ;;
    --install-dir) [ "$#" -ge 2 ] || fail '--install-dir requires a value'; install_dir=$2; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown argument: $1 (use --help)" ;;
  esac
done

for tool in uname tr curl awk tar mktemp mkdir install mv ln rm cp; do
  command -v "$tool" >/dev/null 2>&1 || fail "required command not found: $tool"
done
if command -v shasum >/dev/null 2>&1; then checksum_tool=shasum
elif command -v sha256sum >/dev/null 2>&1; then checksum_tool=sha256sum
else fail 'install shasum or sha256sum to verify the download'; fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$os" in darwin|linux) ;; *) fail "unsupported operating system: $os (macOS and Linux are supported)" ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) fail "unsupported architecture: $arch" ;; esac

fetch() {
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 \
    --connect-timeout 15 --max-time 180 --retry 2 --retry-delay 1 \
    --fail --location --silent --show-error "$@"
}
if [ -z "$tag" ]; then
  manifest=$(fetch --max-filesize 65536 https://usetrace.si/stack.json) || fail 'could not read the coordinated release manifest'
  tag=$(printf '%s\n' "$manifest" | awk -F '"' '$2 == "version" { print $4; exit }')
  [ -n "$tag" ] || fail 'coordinated release manifest has no version'
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac
printf '%s\n' "$tag" | awk '/^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$/ { valid=1 } END { exit !valid }' || fail 'invalid release version (expected v1.0.0 or a semantic prerelease)'
version=${tag#v}
archive="trace_${version}_${os}_${arch}.tar.gz"
base=${TRACE_RELEASE_BASE_URL:-"https://usetrace.si/downloads/$tag"}
case "$base" in https://*) ;; *) fail 'release download URL must use HTTPS' ;; esac
[ -n "$install_dir" ] || fail 'install directory must not be empty'
case "$install_dir" in /*) ;; *) install_dir="$PWD/$install_dir" ;; esac

work_dir=$(mktemp -d) || fail 'could not create a temporary directory'
staged=''
alias_staged=''
bundle_stage=''
replacing=0
installed=0
managed='trace trc'
[ "$bundle_required" != 1 ] || managed='trace trc trace-brain trace-graph'
cleanup() {
  if [ "$replacing" = 1 ] && [ "$installed" != 1 ]; then
    rollback_ok=1
    for binary in $managed; do
      rm -f "$install_dir/$binary" || rollback_ok=0
      if [ -e "$work_dir/previous/$binary" ] || [ -L "$work_dir/previous/$binary" ]; then
        cp -pP "$work_dir/previous/$binary" "$install_dir/$binary" || rollback_ok=0
      fi
    done
    if [ "$rollback_ok" != 1 ]; then
      printf 'Trace rollback needs attention; previous files are preserved in %s/previous\n' "$work_dir" >&2
      return
    fi
  fi
  rm -rf "$work_dir"
  [ -z "$staged" ] || rm -f "$staged"
  [ -z "$alias_staged" ] || rm -f "$alias_staged"
  [ -z "$bundle_stage" ] || rm -rf "$bundle_stage"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
printf 'Downloading Trace %s for %s/%s…\n' "$tag" "$os" "$arch"
fetch -o "$work_dir/$archive" "$base/$archive" || fail 'release download failed; your existing installation was not changed'
fetch -o "$work_dir/checksums.txt" "$base/checksums.txt" || fail 'could not download release checksums'
expected=$(awk -v file="$archive" '$2 == file { sum=$1; count++ } END { if (count == 1) print sum }' "$work_dir/checksums.txt")
printf '%s\n' "$expected" | awk 'length($0) == 64 && $0 !~ /[^0-9a-fA-F]/ { valid=1 } END { exit !valid }' || fail 'missing, invalid, or ambiguous release checksum'
if [ "$checksum_tool" = shasum ]; then
  actual=$(shasum -a 256 "$work_dir/$archive" | awk '{print $1}')
else actual=$(sha256sum "$work_dir/$archive" | awk '{print $1}'); fi
[ "$actual" = "$expected" ] || fail 'checksum mismatch; your existing installation was not changed'
printf 'SHA-256 checksum verified.\n'
tar -xzf "$work_dir/$archive" -C "$work_dir" trace || fail 'could not extract the Trace executable'
[ -f "$work_dir/trace" ] && [ ! -L "$work_dir/trace" ] || fail 'release archive does not contain a regular Trace executable'
if [ "$bundle_required" = 1 ]; then
  for companion in trace-brain trace-graph; do
    tar -xzf "$work_dir/$archive" -C "$work_dir" "$companion" || fail "bundle is missing $companion; existing Trace was not changed"
    [ -f "$work_dir/$companion" ] && [ ! -L "$work_dir/$companion" ] || fail "bundle contains an invalid $companion"
  done
fi
mkdir -p "$install_dir" || fail "cannot create install directory: $install_dir"
[ ! -d "$install_dir/trace" ] && [ ! -d "$install_dir/trc" ] || fail 'trace or trc is a directory; choose another install location'
if [ "$bundle_required" = 1 ]; then
  bundle_stage=$(mktemp -d "$install_dir/.trace-bundle.XXXXXX") || fail 'could not stage the companion bundle'
  for companion in trace-brain trace-graph; do
    [ ! -d "$install_dir/$companion" ] || fail "$companion is a directory; choose another install location"
    install -m 0755 "$work_dir/$companion" "$bundle_stage/$companion" || fail "could not stage $companion"
  done
fi
mkdir "$work_dir/previous" || fail 'could not prepare installation rollback'
for binary in $managed; do
  if [ -e "$install_dir/$binary" ] || [ -L "$install_dir/$binary" ]; then
    cp -pP "$install_dir/$binary" "$work_dir/previous/$binary" || fail "could not back up $binary; existing installation was not changed"
  fi
done
staged=$(mktemp "$install_dir/.trace-install.XXXXXX") || fail "cannot write to $install_dir"
alias_staged="${staged}.trc"
install -m 0755 "$work_dir/trace" "$staged" || fail 'could not stage the executable; existing Trace was not changed'
ln -s trace "$alias_staged" || fail 'could not stage the trc alias'
replacing=1
mv -f "$staged" "$install_dir/trace" || fail 'could not replace the Trace executable'
staged=''
mv -f "$alias_staged" "$install_dir/trc" || fail 'Trace installed, but trc alias replacement failed'
alias_staged=''
if [ "$bundle_required" = 1 ]; then
  for companion in trace-brain trace-graph; do
    mv -f "$bundle_stage/$companion" "$install_dir/$companion" || fail "Trace installed, but $companion installation failed; rerun the installer"
  done
  printf 'Installed Brain and Graph companions.\n'
fi
installed=1
printf '\nInstalled Trace %s and trc in %s\n' "$tag" "$install_dir"
case ":${PATH:-}:" in
  *:"$install_dir":*) printf 'Verify: trace version\n' ;;
  *) printf 'For this terminal, run:\n  export PATH="%s:$PATH"\nThen verify: trace version\n' "$install_dir" ;;
esac
printf 'In a Git repository, start recording with: trace init --agent codex\n'
