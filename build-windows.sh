#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

go run github.com/akavel/rsrc@v0.10.2 -manifest app.exe.manifest -arch amd64 -o rsrc_windows_amd64.syso
rm -f app.exe singbox-gui.exe
GOOS=windows GOARCH=amd64 go build -a -ldflags "-H=windowsgui" -o singbox-gui.exe .

echo "Built: $(pwd)/singbox-gui.exe"
