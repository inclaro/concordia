#!/usr/bin/env bash
# Install Concordia from a public GitHub release of inclaro/concordia, then install the pinned
# codebase-memory-mcp (CBM) build with `concordia cbm install`.
#
# Needs: bash, curl, and tar. cosign is optional and recommended.
set -euo pipefail

repository=inclaro/concordia
version=
dir=
stage=

usage() {
  cat <<'EOF'
Usage: install.sh [--version <tag>] [--dir <dir>]

Installs Concordia from a public GitHub release of inclaro/concordia, then runs `concordia cbm install`.

  --version <tag>  Release tag, for example v1.2.3. Default: the latest release.
  --dir <dir>      Install directory for concordia and codebase-memory-mcp. Default: ~/.local/bin.
  -h, --help       Show this help.

Needs curl and tar. No GitHub login is needed.
When cosign is on the PATH, the installer verifies the signature of the checksum file. Without cosign it checks
the checksum only and prints a warning.

Concordia never replaces a different codebase-memory-mcp build. When another build is found, the installer
reports it and exits with a non-zero code after Concordia is installed.
EOF
}

fail() {
  printf 'install.sh: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n $stage ]]; then
    rm -rf "$stage"
  fi
}
trap cleanup EXIT INT TERM

while [[ $# -gt 0 ]]; do
  case $1 in
    --version)
      [[ $# -ge 2 ]] || fail '--version needs a value'
      version=$2
      shift 2
      ;;
    --dir)
      [[ $# -ge 2 ]] || fail '--dir needs a value'
      dir=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      fail "unknown argument: $1"
      ;;
  esac
done

if [[ -z $dir ]]; then
  [[ -n ${HOME:-} ]] || fail 'HOME is not set; use --dir'
  dir=$HOME/.local/bin
fi

command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v tar >/dev/null 2>&1 || fail 'tar is required'

case "$(uname -s)" in
  Linux) goos=linux ;;
  Darwin) goos=darwin ;;
  *) fail "unsupported operating system: $(uname -s); use install.ps1 on Windows" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

sha256_of() {
  local output
  if command -v sha256sum >/dev/null 2>&1; then
    output=$(sha256sum "$1")
  elif command -v shasum >/dev/null 2>&1; then
    output=$(shasum -a 256 "$1")
  else
    fail 'sha256sum or shasum is required'
  fi
  printf '%s' "${output%% *}"
}

if [[ -z $version ]]; then
  version=$(curl -fsSL "https://api.github.com/repos/$repository/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1) ||
    version=
  [[ -n $version ]] || fail "cannot find the latest release of $repository"
fi
tag=$version
[[ $tag == v* ]] || tag=v$tag
archive=concordia_${tag#v}_${goos}_${goarch}.tar.gz
checksums=concordia_checksums.txt
signature=$checksums.sigstore.json

stage=$(mktemp -d "${TMPDIR:-/tmp}/concordia-install.XXXXXX")
printf 'Downloading Concordia %s (%s/%s)\n' "$tag" "$goos" "$goarch"
for f in "$archive" "$checksums" "$signature"; do
  curl -fsSL -o "$stage/$f" "https://github.com/$repository/releases/download/$tag/$f" ||
    fail "cannot download release $tag of $repository"
done

if command -v cosign >/dev/null 2>&1; then
  (cd "$stage" && cosign verify-blob --bundle "$signature" \
    --certificate-identity "https://github.com/$repository/.github/workflows/release.yml@refs/tags/$tag" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com "$checksums") ||
    fail "signature verification failed for $checksums"
else
  printf 'install.sh: warning: cosign is not on the PATH; the signature is NOT verified, only the checksum.\n' >&2
  printf 'install.sh: warning: install cosign (https://docs.sigstore.dev/) for a verified install.\n' >&2
fi

expected=$(awk -v name="$archive" '$2 == name {print $1}' "$stage/$checksums")
[[ -n $expected ]] || fail "$checksums has no entry for $archive"
[[ $(sha256_of "$stage/$archive") == "$expected" ]] || fail "checksum verification failed for $archive"

mkdir -p "$stage/extract" "$dir"
tar -xzf "$stage/$archive" -C "$stage/extract" concordia
[[ -f $stage/extract/concordia ]] || fail "$archive does not contain concordia"
install -m 0755 "$stage/extract/concordia" "$dir/.concordia.new"
mv -f "$dir/.concordia.new" "$dir/concordia"
printf 'Installed concordia %s to %s\n' "$tag" "$dir/concordia"

# The pinned CBM build and its checks live in the Go command only.
if ! "$dir/concordia" cbm install --dir "$dir"; then
  printf 'install.sh: concordia is installed, but concordia cbm install failed (see above).\n' >&2
  exit 1
fi
case ":$PATH:" in
  *":$dir:"*) ;;
  *) printf 'install.sh: warning: %s is not on your PATH.\n' "$dir" >&2 ;;
esac
