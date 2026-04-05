# sing-box GUI Client (Windows, Go)

Нативный Windows GUI-клиент для запуска `sing-box` с portable-логикой:

- `config.yaml` рядом с `.exe`
- скачивание `sing-box.exe` нужной версии (или `latest`)
- скачивание `config.json` по URL с заголовком `User-Agent: sfw`
- запуск `sing-box.exe run -c config.json --disable-color`
- автоостановка старого процесса при повторном `Start`
- завершение процесса при закрытии окна
- переключаемая кнопка `Start/Stop`
- авто-тема по системной настройке Windows (light/dark), без ручного переключателя
- монохромный вывод логов в UI (без ANSI-цвета, чтобы избежать мерцания)

## Сборка

```bash
go mod tidy
./build-windows.sh
```

Если собираете вручную, обязательно вшивайте manifest (Common Controls v6), иначе на части систем возможна ошибка `TTM_ADDTOOL failed`.
Для сборки без консольного окна используйте `-ldflags "-H=windowsgui"`.

## Структура после запуска

```text
/singbox-gui.exe
/config.yaml
/sing-box.exe
/config.json
```

## Конфиг

`config.yaml` создается автоматически при первом запуске:

```yaml
url: ""
version: latest
```

## Требование прав администратора

При запуске приложение проверяет права и, если нужно, перезапускает себя через `runas`.
