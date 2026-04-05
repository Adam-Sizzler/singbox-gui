#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

go run github.com/akavel/rsrc@v0.10.2 \
  -manifest build/windows/app.exe.manifest \
  -ico build/windows/app-icon.ico \
  -arch amd64 \
  -o cmd/singbox-gui/rsrc.syso
rm -f singbox-gui.exe

release_tag="${APP_RELEASE_TAG:-}"
if [ -z "$release_tag" ]; then
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    release_tag="$(git describe --tags --exact-match 2>/dev/null || true)"
  fi
fi
if [ -z "$release_tag" ]; then
  release_tag="dev"
fi
release_tag="$(printf '%s' "$release_tag" | tr -d '\r\n')"

ldflags="-H=windowsgui -X singbox-gui-client/internal/app.appReleaseTag=${release_tag}"
GOOS=windows GOARCH=amd64 go build -a -ldflags "$ldflags" -o singbox-gui.exe ./cmd/singbox-gui

echo "Built: $(pwd)/singbox-gui.exe"
