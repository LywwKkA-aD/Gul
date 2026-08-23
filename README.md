# Gul

Десктопный голосовой клиент «как Discord» для компании друзей поверх готового
Mumble-сервера. Wails v3 + Go (cgo DSP) + React/TypeScript.

Главный документ — [PLAN.md](PLAN.md) (архитектура, милстоуны, правила).
Журнал решений — [docs/DECISIONS.md](docs/DECISIONS.md). Статус: **M3 — DSP alpha**:
двусторонний голос с Gul и официальным Mumble-клиентом, WebRTC AEC3, шумоподавление,
RNNoise, VAD и Push-to-talk при фокусе окна.

Актуальную публичную alpha-сборку можно скачать во вкладке
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
| gumble (форк Gul) | v0.0.0-20260822211756-970c56146e90 |
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

Также нужны Node ≥ 22 и Docker (для дев-стенда). Диагностика окружения: `wails3 doctor`.

## Разработка

```sh
task murmur:up      # локальный Mumble-сервер в докере (порт 64738)
task dev            # приложение в dev-режиме
task lint           # версии + заголовки + gofmt + go vet + golangci-lint + eslint
task test           # go test -race (без устройств и сети)
task test:live      # смоук-тесты против запущенного стенда
task murmur:logs    # логи сервера
task package        # упаковка под текущую ОС
```

Первый запуск: `task murmur:up`, затем `task dev`, в окне Connect указать
`127.0.0.1:64738` и ник. Отпечаток сертификата сервера пинится по TOFU при первом
подключении. Лог приложения лежит в конфиг-папке ОС, например
`~/Library/Application Support/gul/gul.log` на macOS и `%AppData%\gul\gul.log` на Windows.

По умолчанию dev-сервер доступен только с этого компьютера. Для теста в доверенной
локальной сети запустите его так:

```sh
MUMBLE_BIND_ADDRESS=0.0.0.0 \
MUMBLE_SUPERUSER_PASSWORD='замените-на-длинный-пароль' \
task murmur:up
```

Второй клиент подключается к `<LAN-IP-хоста>:64738`. Этот dev-стенд отключает autoban,
поэтому не выставляйте его напрямую в интернет.

## Публичное подключение

В production-форме по умолчанию указан
`wss://murmur.gulvox.com/mumble`. Введите выданный владельцем сервера пароль:
клиент использует его и для доступа к WSS relay, и внутри зашифрованной Mumble-сессии.
Пароль не включается в URL и не сохраняется в диагностике.

Внешний WSS проверяется через системные центры сертификации, а внутри него остаётся
обычный Mumble TLS с TOFU-пином сертификата. Прямые адреса `host:64738` по-прежнему
поддерживаются для локальных стендов и обычных Mumble-серверов. Устройство production
relay и его ограничения описаны в [deploy/relay/README.md](deploy/relay/README.md).

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
