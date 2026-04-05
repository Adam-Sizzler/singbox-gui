#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

go run github.com/akavel/rsrc@v0.10.2 \
  -manifest build/windows/app.exe.manifest \
  -ico build/windows/app-icon.ico \
  -arch amd64 \
  -o cmd/singbox-gui/rsrc.syso
rm -f singbox-gui.exe
GOOS=windows GOARCH=amd64 go build -a -ldflags "-H=windowsgui" -o singbox-gui.exe ./cmd/singbox-gui

echo "Built: $(pwd)/singbox-gui.exe"
