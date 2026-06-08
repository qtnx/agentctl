#!/usr/bin/env sh
set -eu

repo="${AGENTCTL_REPO:-qtnx/agentctl}"
version="${1:-${AGENTCTL_VERSION:-latest}}"
install_dir="${AGENTCTL_INSTALL_DIR:-/usr/local/bin}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "agentctl installer requires $1" >&2
    exit 1
  fi
}

need curl
need tar

download() {
  attempt=1
  while :; do
    if curl --fail --location --silent --show-error "$@"; then
      return 0
    fi
    if [ "$attempt" -ge 3 ]; then
      return 1
    fi
    attempt=$((attempt + 1))
    sleep 2
  done
}

latest_version() {
  if command -v gh >/dev/null 2>&1; then
    if gh release view --repo "$repo" --json tagName --jq .tagName 2>/dev/null; then
      return 0
    fi
  fi
  download "https://api.github.com/repos/${repo}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

download_release_asset() {
  asset="$1"
  dest="$2"
  if command -v gh >/dev/null 2>&1; then
    if gh release download "$version" --repo "$repo" --pattern "$asset" --output "$dest" --clobber 2>/dev/null; then
      return 0
    fi
    echo "gh release download failed for $asset; falling back to curl" >&2
  fi
  download "${base_url}/${asset}" -o "$dest"
}

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin|linux) ;;
  *)
    echo "unsupported OS: $os" >&2
    exit 1
    ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    echo "unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

if [ "$version" = "latest" ]; then
  version="$(latest_version)"
  if [ -z "$version" ]; then
    echo "could not resolve latest agentctl release" >&2
    exit 1
  fi
fi

case "$version" in
  v*) ;;
  *) version="v${version}" ;;
esac

tmp="${TMPDIR:-/tmp}/agentctl-install.$$"
archive="agentctl_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${repo}/releases/download/${version}"

cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

mkdir -p "$tmp"
download_release_asset "$archive" "$tmp/$archive"
download_release_asset "checksums.txt" "$tmp/checksums.txt"

expected="$(grep " ${archive}$" "$tmp/checksums.txt" | awk '{print $1}')"
if [ -z "$expected" ]; then
  echo "checksum for $archive not found" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
else
  need shasum
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
fi

if [ "$expected" != "$actual" ]; then
  echo "checksum mismatch for $archive" >&2
  exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp"
bin="$tmp/agentctl_${version}_${os}_${arch}/agentctl"
if [ ! -f "$bin" ]; then
  echo "agentctl binary not found in archive" >&2
  exit 1
fi

if mkdir -p "$install_dir" 2>/dev/null && [ -w "$install_dir" ]; then
  install -m 0755 "$bin" "$install_dir/agentctl"
elif command -v sudo >/dev/null 2>&1; then
  sudo mkdir -p "$install_dir"
  sudo install -m 0755 "$bin" "$install_dir/agentctl"
else
  install_dir="${HOME}/.local/bin"
  mkdir -p "$install_dir"
  install -m 0755 "$bin" "$install_dir/agentctl"
fi

echo "agentctl ${version} installed to ${install_dir}/agentctl"
"${install_dir}/agentctl" version || true
