# Sing-box GUI Client (Windows)

Native Windows GUI client for `sing-box` with portable runtime behavior.

Russian version: `README.ru.md`

## Features

- Single executable target: `singbox-gui.exe`
- Embedded UI assets (frontend is built into the binary)
- Config stored near executable (`config.yaml`)
- Downloads `sing-box.exe` by selected version (`latest` or semver)
- Downloads runtime `config.json` from subscription URL (`User-Agent: sfw`)
- Process control from UI (`Start` / `Stop`)
- ANSI-aware colored log rendering in UI
- Multiple profiles (`create`, `select`, `delete`)
- RU/EN localization with language switch in UI
- `sing-box://import-remote-profile?...` protocol support
- Single-instance import behavior:
  - if app is already running, import is sent to existing window
  - existing window is focused
  - no second window is created
- Import does **not** auto-start sing-box
- Requests admin rights on startup (`runas`)

## Requirements

- Windows 10/11 x64
- Go toolchain (for local build)
- Network access for downloading `sing-box.exe` / remote config

## Build

```bash
go mod tidy
./build-windows.sh
```

Output:

```text
./singbox-gui.exe
```

`build-windows.sh` also regenerates `cmd/singbox-gui/rsrc.syso` from:

- `build/windows/app.exe.manifest`
- `build/windows/app-icon.ico` (can be generated from your SVG icon)

## Run Layout

After first start, files are created next to executable:

```text
singbox-gui.exe
config.yaml
sing-box.exe
config.json
```

## Config Format

Current config format:

```yaml
language: ru
current_profile: default
profiles:
  - name: default
    url: ""
    version: latest
```

## Protocol Import

Supported URI format:

```text
sing-box://import-remote-profile?url=https%3A%2F%2Fexample.com%2Fsub#profile-name
```

Behavior:

- `url` is required and must be `http://` or `https://`
- if `#profile-name` exists:
  - update that profile URL if it exists, or create profile
  - switch current profile to it
- if profile name is absent: apply URL to current profile
- no auto-start after import

## GitHub Actions

Workflow: `.github/workflows/build-windows-on-tag.yml`

- Trigger: push of any tag
- Result: uploaded artifact `singbox-gui-windows-<tag>`

## Repository Hygiene

Recommended ignored local artifacts:

- built exe
- runtime files (`config.yaml`, `config.json`, `sing-box.exe`)
- temporary logs

`.gitignore` is included for this.
