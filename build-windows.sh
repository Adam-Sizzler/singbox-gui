#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

go run github.com/akavel/rsrc@v0.10.2 \
  -manifest build/windows/app.exe.manifest \
  -ico build/windows/app-icon.ico \
  -arch amd64 \
  -o cmd/singbox-gui/rsrc.syso
rm -f singbox-wrapper.exe singbox-gui.exe

release_tag="${APP_RELEASE_TAG:-}"
if [ -z "$release_tag" ]; then
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    release_tag="$(git describe --tags --exact-match 2>/dev/null || true)"
  fi
fi
if [ -z "$release_tag" ]; then
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    release_tag="$(git describe --tags --abbrev=0 2>/dev/null || true)"
  fi
fi
if [ -z "$release_tag" ]; then
  release_tag="dev"
fi
release_tag="$(printf '%s' "$release_tag" | tr -d '\r\n')"

ldflags="-H=windowsgui -X singbox-gui-client/internal/app.appReleaseTag=${release_tag}"

: "${CC:=x86_64-w64-mingw32-gcc}"
: "${CXX:=x86_64-w64-mingw32-g++}"
shim_include="$(pwd)/build/windows/msheaders"

if ! command -v "$CC" >/dev/null 2>&1; then
  echo "error: C compiler not found: $CC" >&2
  echo "hint: install mingw-w64 (x86_64) or override CC/CXX environment variables." >&2
  exit 1
fi
if ! command -v "$CXX" >/dev/null 2>&1; then
  echo "error: C++ compiler not found: $CXX" >&2
  echo "hint: install mingw-w64 (x86_64) or override CC/CXX environment variables." >&2
  exit 1
fi

CGO_CXXFLAGS="-I${shim_include} ${CGO_CXXFLAGS:-}" \
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC="$CC" CXX="$CXX" \
  go build -a -ldflags "$ldflags" -o singbox-wrapper.exe ./cmd/singbox-gui

echo "Built: $(pwd)/singbox-wrapper.exe"
