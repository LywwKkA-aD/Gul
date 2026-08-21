# Gul

Десктопный голосовой клиент «как Discord» для компании друзей поверх готового
Mumble-сервера. Wails v3 + Go (cgo DSP) + React/TypeScript.

Главный документ — [PLAN.md](PLAN.md) (архитектура, милстоуны, правила).
Журнал решений — [docs/DECISIONS.md](docs/DECISIONS.md). Статус: **M2 — голосовое ядро**:
два Gul-клиента и официальный Mumble-клиент разговаривают в обе стороны (пока в наушниках,
без AEC/шумодава из M3).

## Версии (пины жёсткие, `@latest` запрещён)

| Компонент | Версия |
|---|---|
| Go | ≥ 1.25 |
| Wails | v3.0.0-beta.11 (go.mod + CLI) |
| Node | ≥ 22 (разработка ведётся на 24) |
| React / TypeScript | 19.2.x / 6.0.2 (не 7.x) |
| Vite / Tailwind / zustand | 8.x / 4.x / 5.x |
| gumble (форк Gul) | v0.0.0-20260821213018-6f2e820432c0 |
| mumble-server (стенд) | mumblevoip/mumble-server:v1.5.915 |
| golangci-lint | v2.13.1 |

## Подготовка окружения

```sh
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.11
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1
```

Также нужны Node ≥ 22 и Docker (для дев-стенда). Диагностика окружения: `wails3 doctor`.

## Разработка

```sh
task murmur:up      # локальный Mumble-сервер в докере (порт 64738)
task dev            # приложение в dev-режиме
task lint           # gofmt + go vet + golangci-lint + eslint
task test           # go test -race (без устройств и сети)
task test:live      # смоук-тесты против запущенного стенда
task murmur:logs    # логи сервера
task package        # упаковка под текущую ОС
```

Первый запуск: `task murmur:up`, затем `task dev`, в окне Connect указать
`127.0.0.1:64738` и ник. Отпечаток сертификата сервера пинится по TOFU при первом
подключении. Лог приложения лежит в конфиг-папке ОС, например
`~/Library/Application Support/gul/gul.log` на macOS и `%AppData%\gul\gul.log` на Windows.

## Тестовые сборки

Workflow `CI` можно запустить вручную во вкладке Actions. После успешного прогона он
прикладывает три артефакта на 14 дней (в конце имени указан commit SHA):

- `gul-windows-amd64-<sha>` — portable `gul.exe` и SHA-256;
- `gul-macos-universal-<sha>` — DMG с ad-hoc signed приложением для Apple Silicon и Intel Mac
  и SHA-256;
- `gul-linux-amd64-<sha>` — DEB для Ubuntu 24.04+/Debian 13+ x86_64 и SHA-256.

Это тестовые неподписанные релизным сертификатом сборки: Windows покажет Unknown Publisher,
а macOS — предупреждение Gatekeeper; Linux DEB не подписан ключом репозитория. Полноценные
подписанные установщики остаются задачей M4. На Windows также нужен WebView2 Runtime
(в Windows 11 он уже входит в систему). Linux-пакет устанавливается вместе с зависимостями:
`sudo apt install ./gul.deb`.
