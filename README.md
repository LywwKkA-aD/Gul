# Gul

Десктопный голосовой клиент «как Discord» для компании друзей поверх готового
Mumble-сервера. Wails v3 + Go (cgo DSP) + React/TypeScript.

Главный документ — [PLAN.md](PLAN.md) (архитектура, милстоуны, правила).
Журнал решений — [docs/DECISIONS.md](docs/DECISIONS.md). Статус: **alpha**:
двусторонний голос с Gul и официальным Mumble-клиентом, WebRTC AEC3, шумоподавление,
RNNoise, VAD и Push-to-talk при фокусе окна.

Трафик голосовых сессий Gul идёт через встроенное официальное ядро **Hysteria 2**:
`Gul -> Hysteria server -> Mumble`. Пользователям не нужно устанавливать
прокси отдельно. Голос, чат и управление идут внутри Mumble TLS;
аудиодвижок и интерфейс работают локально, как раньше.

Эта рабочая версия **0.6.0-alpha.1** переводит подключения на Hysteria.
Ранее опубликованные сборки могут использовать старый транспорт.
Публичные alpha-сборки находятся во вкладке
[Releases](https://github.com/LywwKkA-aD/Gul/releases). Это prerelease для тестов,
а не окончательно подписанный установщик.

## Версии (пины жёсткие, `@latest` запрещён)

| Компонент | Версия |
|---|---|
| Go | 1.26.7 |
| Wails | v3.0.0-beta.11 (go.mod + CLI) |
| Node | ≥ 22 (разработка ведётся на 24) |
| React / TypeScript | 19.2.x / 6.0.2 (не 7.x) |
| Vite / Tailwind / zustand | 8.x / 4.x / 5.x |
| gumble (форк Gul) | v0.0.0-20260824160029-7999640c1fef |
| Hysteria core / extras | v2.13.0 |
| mumble-server (стенд) | mumblevoip/mumble-server:v1.5.915 |
| golangci-lint | v2.13.1 |

Версия приложения задаётся один раз — константой `Version` в
`internal/core/app.go`. `scripts/check-version.sh` выводит из неё каждое
представление (`build/config.yml`, plist-файлы, `info.json`, NSIS, nfpm,
версия DEB, `frontend/package.json` и lock-файл) и падает при расхождении; проверка входит в
`task lint` и в CI. При смене версии правится константа, затем
`bash scripts/check-version.sh` показывает, какие копии остались старыми.

## Подготовка окружения

```sh
go install github.com/go-task/task/v3/cmd/task@v3.53.1
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.11
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1
```

Также нужны Node ≥ 22, Python 3 ≥ 3.10 (проверки конфигурации/CI) и Docker
(для дев-стенда). Диагностика окружения: `wails3 doctor`.

## Разработка

```sh
task murmur:up      # Mumble для автоматических live-тестов (loopback 64738)
task dev            # приложение в dev-режиме
task lint           # версии + заголовки + gofmt + go vet + golangci-lint + eslint
task test           # go test -race (без устройств и сети)
task test:live      # тестовый Hysteria перед запущенным Mumble
task murmur:logs    # логи сервера
task package        # упаковка под текущую ОС
```

Для ручного запуска `task dev` нужен Hysteria-сервер из инструкции ниже.
Прямое подключение GUI к `127.0.0.1:64738` больше не используется; локальный
Mumble-стенд служит для live-тестов. Отпечаток внутреннего Mumble-сертификата
пинится по TOFU при первом подключении. Лог приложения лежит в конфиг-папке ОС, например
`~/Library/Application Support/gul/gul.log` на macOS и `%AppData%\gul\gul.log` на Windows.

Dev-стенд доступен только с этого компьютера и отключает autoban для тестов;
не публикуйте его напрямую в интернет.

## Публичное подключение

Владелец сервера разворачивает официальный Hysteria v2.13.0 и Mumble:
[готовый стенд для VPS 1 vCPU / 1 ГБ](deploy/hysteria/README.md). Собственный Gul
relay больше не нужен. Старые материалы в `deploy/relay` описывают предыдущую схему.

В форме подключения укажите `hysteria2://voice.example.org` (по умолчанию UDP 443),
ник и отдельно пароль. Для настроенной на сервере обфускации используйте
`?obfs=salamander` или `?obfs=gecko`. Пароль применяется к Hysteria auth,
Mumble serverPassword и выбранной обфускации; его нельзя вставлять в URL.
Адрес без схемы также означает Hysteria, а не прямое подключение к Mumble.

Внешний Hysteria TLS проверяется через системные центры сертификации. Внутри
него используется Mumble TLS с TOFU-пином и устойчивой клиентской идентичностью.
Старые сохранённые адреса `wss://...` нужно явно заменить на новый адрес,
выданный владельцем сервера. Работу из конкретных сетей провайдеров нужно
проверять живым разговором: этот QUIC-транспорт требует доступного UDP до VPS.

## Тестовые сборки

Workflow `CI` можно запустить вручную во вкладке Actions. После успешного прогона он
прикладывает три артефакта на 14 дней (в конце имени указан commit SHA):

- `gul-windows-amd64-<sha>` — portable ZIP с `gul.exe`, лицензиями и SHA-256;
- `gul-macos-universal-<sha>` — DMG с ad-hoc signed приложением для Apple Silicon и Intel Mac
  и SHA-256;
- `gul-linux-amd64-<sha>` — DEB для Ubuntu 24.04+/Debian 13+ x86_64 и SHA-256.

Это тестовые неподписанные релизным сертификатом сборки: Windows может показать Unknown Publisher,
а macOS — предупреждение Gatekeeper; Linux DEB не подписан ключом репозитория. Полноценные
подписанные установщики остаются задачей M4. На Windows также нужен WebView2 Runtime
(в Windows 11 он уже входит в систему). Linux-пакет устанавливается вместе с зависимостями:
`sudo apt install ./gul-linux-amd64.deb`.

### Значок в трее на Linux

Значка может не быть, и это не поломка сборки. Трей на Linux — это
`StatusNotifierItem`, которому нужен «наблюдатель» на шине сессии. В KDE, XFCE,
Cinnamon и Budgie он штатный; **в GNOME его нет** — а GNOME стоит по умолчанию
в Ubuntu, то есть ровно там, куда целится наш DEB. Лечится расширением
[AppIndicator and KStatusNotifierItem Support](https://extensions.gnome.org/extension/615/appindicator-support/).

Приложение от значка не зависит: на Linux закрытие окна завершает программу,
поэтому без трея ничего не теряется и микрофон не остаётся открытым в фоне.
Поведение кнопки закрытия по системам разное и намеренно:

| Система | Закрытие окна | Где живёт приложение после |
|---|---|---|
| macOS | прячет окно | меню-бар (он есть всегда) |
| Windows | закрывает окно | область уведомлений (она есть всегда) |
| Linux | **завершает программу** | нигде: трей не гарантирован |

## Кросс-сборка через Docker

`task build`/`task package` для чужой ОС уходят в контейнер `wails-cross`
(`task setup:docker`). Это неофициальные сборки для разработки, а не
эквивалент релиза: контейнер собирает C++ через Zig и libc++ вместо MinGW GCC
со статическим libstdc++, не линкует `-extldflags=-static`, не кладёт
лицензионный комплект, не делает установщики и сам не генерирует
Windows-ресурсы и манифест (`.syso`). Об этом печатается баннер при каждом
запуске. Сводить два рантайма в один не планируется: релизные артефакты
выпускает только `.github/workflows/ci.yml` на нативных раннерах, и это
единственный путь релиза.
