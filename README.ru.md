# singbox-wrapper (Windows)

Нативный Windows GUI-клиент для `sing-box` с portable-логикой.

English version: `README.md`
Репозиторий: `https://github.com/Adam-Sizzler/singbox-wrapper`

## Возможности

- Итоговый бинарник: `singbox-gui.exe`
- Фронтенд встроен в бинарник
- Конфиг хранится рядом с `.exe` (`config.yaml`)
- Загрузка `sing-box.exe` по выбранной версии (`latest` или semver)
- Загрузка runtime-файла `config.json` по URL (`User-Agent: sfw/<версия-приложения>`, например `sfw/v26.4.12`)
- Управление процессом из UI (`Start` / `Stop`)
- Блок релиза приложения в UI:
  - показывает текущую версию приложения
  - показывает версию последнего релиза (если доступна новая)
  - кнопка автообновления скачивает релиз и перезапускает приложение
- Переключение `selector`/`outbound` из UI через Clash API (без рестарта ядра)
- Цветной вывод логов в UI с поддержкой ANSI
- Профили (`создать`, `выбрать`, `удалить`)
- Локализация RU/EN с переключением языка в UI
- Runtime-конфиг перед запуском автоматически дополняется `experimental.clash_api` на `127.0.0.1` c динамическим портом и секретом
- Автообновление runtime-конфига из URL:
  - интервал по умолчанию: 12 часов
  - `0` = отключено
  - перед заменой текущего `config.json` новый файл проходит валидацию
  - при фоновом обновлении ядро автоматически не перезапускается
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
- C/C++ toolchain для cgo-сборки (`mingw-w64` при сборке на Linux)
- WebView2 runtime на машине пользователя (в Windows 11 обычно уже установлен)
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
auto_update_hours: 12
current_profile: default
profiles:
  - name: default
    url: ""
    version: latest
    selector_selections:
      my-selector: proxy-a
```

## Импорт по протоколу

Поддерживаемый формат URI:

```text
sing-box://import-remote-profile?url=https%3A%2F%2Fexample.com%2Fsub#profile-name
```

Импорт с явной версией ядра:

```text
sing-box://import-remote-profile?url=https%3A%2F%2Fexample.com%2Fsub&version=1.12.0#profile-name
```

Поведение:

- параметр `url` обязателен и должен быть `http://` или `https://`
- необязательный параметр `version` задает версию ядра для импортируемого профиля (`latest` по умолчанию)
- если есть `#profile-name`:
  - обновляется URL существующего профиля или создается новый профиль
  - текущим становится этот профиль
- если имя профиля отсутствует: URL применяется к текущему профилю
- автозапуск после импорта отключен

## Автообновление

`auto_update_hours` управляет фоновым обновлением `config.json` по URL активного профиля:

- `12` по умолчанию
- `0` отключает автообновление
- любое положительное значение — интервал в часах

`config.json` заменяется только если скачанный файл валидный JSON.

## Selector и Clash API

- Если в runtime-конфиге есть outbound типа `selector`, в UI показываются dropdown-поля выбора.
- При запущенном ядре переключение выполняется live через `PUT /proxies/{selector}` (Clash API), без перезапуска процесса.
- Выбранный outbound сохраняется в профиль (`selector_selections`) и автоматически применяется после следующего запуска ядра.
