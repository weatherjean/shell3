#!/bin/sh
# shell3 installer.
#
#   curl -fsSL https://raw.githubusercontent.com/weatherjean/shell3/main/install.sh | sh
#
# Downloads the prebuilt shell3 binary for your OS/arch from the latest GitHub
# release and installs it to ~/.local/bin (override with PREFIX=/some/dir).
# Set VERSION=vX.Y.Z to pin a specific release instead of the latest.
#
# Unix only (Linux, macOS). Windows is not supported.

set -eu

REPO="weatherjean/shell3"
BINARY="shell3"
PREFIX="${PREFIX:-$HOME/.local/bin}"

say()  { printf '%s\n' "$*"; }
err()  { printf 'error: %s\n' "$*" >&2; exit 1; }

# --- pick a downloader -------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  dl()      { curl -fsSL "$1"; }            # to stdout
  dl_file() { curl -fsSL "$1" -o "$2"; }    # to file
elif command -v wget >/dev/null 2>&1; then
  dl()      { wget -qO- "$1"; }
  dl_file() { wget -qO "$2" "$1"; }
else
  err "need curl or wget to download shell3"
fi

# --- detect OS and arch ------------------------------------------------------
os="$(uname -s)"
case "$os" in
  Linux)  os="linux" ;;
  Darwin) os="darwin" ;;
  *)      err "unsupported OS: $os (shell3 supports Linux and macOS only)" ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64)  arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)             err "unsupported architecture: $arch" ;;
esac

# --- resolve the release version --------------------------------------------
version="${VERSION:-}"
if [ -z "$version" ]; then
  say "Resolving latest release..."
  version="$(dl "https://api.github.com/repos/$REPO/releases/latest" \
    | tr ',' '\n' \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -n1)"
  [ -n "$version" ] || err "could not determine the latest release (try VERSION=vX.Y.Z)"
fi

# Asset names drop the leading "v" from the tag (goreleaser .Version).
ver_no_v="${version#v}"
asset="${BINARY}_${ver_no_v}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$version/$asset"

# --- download and unpack -----------------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

say "Downloading $asset ($version)..."
dl_file "$url" "$tmp/$asset" || err "download failed: $url"

# Best-effort checksum verification against the release's checksums.txt.
if dl_file "https://github.com/$REPO/releases/download/$version/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
  sum=""
  if command -v sha256sum >/dev/null 2>&1; then
    sum="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    sum="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
  fi
  if [ -n "$sum" ]; then
    grep -q "$sum" "$tmp/checksums.txt" || err "checksum mismatch for $asset"
    say "Checksum OK."
  fi
fi

tar -xzf "$tmp/$asset" -C "$tmp" || err "could not extract $asset"
[ -f "$tmp/$BINARY" ] || err "archive did not contain a '$BINARY' binary"

# --- install -----------------------------------------------------------------
mkdir -p "$PREFIX"
install -m 0755 "$tmp/$BINARY" "$PREFIX/$BINARY" 2>/dev/null \
  || { cp "$tmp/$BINARY" "$PREFIX/$BINARY" && chmod 0755 "$PREFIX/$BINARY"; }

say ""
say "Installed $BINARY $version to $PREFIX/$BINARY"

# Warn if the install dir isn't on PATH.
case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *)
    say ""
    say "Note: $PREFIX is not on your PATH. Add it, e.g.:"
    say "  echo 'export PATH=\"$PREFIX:\$PATH\"' >> ~/.profile && . ~/.profile"
    ;;
esac

say ""
say "Get started:  $BINARY boot   &&   $BINARY"
