# Sing-box GUI Client (Windows)

Нативный Windows GUI-клиент для `sing-box` с portable-логикой.

English version: `README.md`

## Возможности

- Итоговый бинарник: `singbox-gui.exe`
- Фронтенд встроен в бинарник
- Конфиг хранится рядом с `.exe` (`config.yaml`)
- Загрузка `sing-box.exe` по выбранной версии (`latest` или semver)
- Загрузка runtime-файла `config.json` по URL (`User-Agent: sfw`)
- Управление процессом из UI (`Start` / `Stop`)
- Цветной вывод логов в UI с поддержкой ANSI
- Профили (`создать`, `выбрать`, `удалить`)
- Локализация RU/EN с переключением языка в UI
- Поддержка протокола `sing-box://import-remote-profile?...`
- Поведение single-instance для импорта:
  - если приложение уже запущено, импорт отправляется в текущее окно
  - текущее окно получает фокус
  - второе окно не создается
- После импорта sing-box автоматически не запускается
- При старте запрашиваются права администратора (`runas`)

## Требования

- Windows 10/11 x64
- Go toolchain (для локальной сборки)
- Сеть для загрузки `sing-box.exe` и удаленного конфига

## Сборка

```bash
go mod tidy
./build-windows.sh
```

Результат:

```text
./singbox-gui.exe
```

`build-windows.sh` также пересоздает `cmd/singbox-gui/rsrc.syso` из:

- `build/windows/app.exe.manifest`
- `build/windows/app-icon.ico` (можно генерировать из SVG-иконки)

## Файлы после запуска

После первого запуска рядом с `exe` создаются:

```text
singbox-gui.exe
config.yaml
sing-box.exe
config.json
```

## Формат конфига

Текущий формат `config.yaml`:

```yaml
language: ru
current_profile: default
profiles:
  - name: default
    url: ""
    version: latest
```

## Импорт по протоколу

Поддерживаемый формат URI:

```text
sing-box://import-remote-profile?url=https%3A%2F%2Fexample.com%2Fsub#profile-name
```

Поведение:

- параметр `url` обязателен и должен быть `http://` или `https://`
- если есть `#profile-name`:
  - обновляется URL существующего профиля или создается новый профиль
  - текущим становится этот профиль
- если имя профиля отсутствует: URL применяется к текущему профилю
- автозапуск после импорта отключен

## GitHub Actions

Workflow: `.github/workflows/build-windows-on-tag.yml`

- Триггер: push любого тега
- Результат: artifact `singbox-gui-windows-<tag>`

## Чистота репозитория

Рекомендуется игнорировать локальные артефакты:

- собранный exe
- runtime-файлы (`config.yaml`, `config.json`, `sing-box.exe`)
- временные логи

Для этого добавлен `.gitignore`.
