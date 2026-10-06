# Gul

Десктопный голосовой клиент для компании друзей: Wails v3 + Go (cgo DSP) +
React/TypeScript. Тестовая версия **0.7.0-alpha.1** переведена на **LiveKit**: голос,
каналы, текстовый чат и демонстрация экрана работают в обычном интерфейсе Gul.
Нативные WebRTC AEC3, RNNoise, VAD/PTT и выбор устройств сохранены.

Главный документ — [PLAN.md](PLAN.md), решения — [docs/DECISIONS.md](docs/DECISIONS.md).
Запуск полного локального клиента и ограничения — [docs/LIVEKIT-LOCAL.md](docs/LIVEKIT-LOCAL.md).
Серверный стенд работает только на компьютере, где его запустили; удалённое
подключение и авторизация LiveKit требуют отдельного этапа. Windows/game audio
ещё не проверены. На macOS возможны зависание запуска аудио и ошибка первого
подключения панели демонстраций; это пока версия для испытаний.

**0.7.0-alpha.1** выпускается как prerelease для локальных тестов. Этот клиент
не подключается к действующему Mumble/VLESS-серверу. Для текущего общения
сохраните **[0.6.0-alpha.3](https://github.com/LywwKkA-aD/Gul/releases/tag/v0.6.0-alpha.3)**
со встроенными Hysteria 2 и VLESS + REALITY и запускайте новую сборку отдельно.
Удалённый сервер этой публикацией не переносится. Описание тестовой версии —
[release notes](.github/release-notes/v0.7.0-alpha.1.md), сборки —
[Releases](https://github.com/LywwKkA-aD/Gul/releases).

## Версии (пины жёсткие, `@latest` запрещён)

| Компонент | Версия |
|---|---|
| Go | 1.26.7 |
| Wails | v3.0.0-beta.11 (go.mod + CLI) |
| Node | ≥ 22 (разработка ведётся на 24) |
| React / TypeScript | 19.2.x / 6.0.2 (не 7.x) |
| Vite / Tailwind / zustand | 8.x / 4.x / 5.x |
| LiveKit SFU / Go SDK / JS SDK | 1.13.8 / 2.18.1 / 2.22.3 |
| gumble (legacy, форк Gul) | v0.0.0-20260824160029-7999640c1fef |
| Hysteria core / extras (legacy) | v2.13.0 |
| REALITY handshake / Xray server (legacy) | v26.3.27 (адаптация MPL-2.0 / отдельный сервер) |
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
bash scripts/livekit-local.sh up       # локальный SFU и broker
bash scripts/livekit-client.sh         # полный macOS-клиент, отдельный bundle
# либо task dev для разработки под текущую ОС
task lint
task test
GUL_LIVEKIT_LIVE=1 GOTOOLCHAIN=go1.26.7 go test -race -tags live ./internal/livekit
```

Адрес: `http://127.0.0.1:8787`, любой ник, пароль пустой. Настройки и лог текущего
клиента находятся в конфиг-папке `gul-livekit`; рабочая папка `gul` не используется.
Стенд не предназначен для публикации в интернете. `task murmur:up` и
`task test:live` сохраняются для регрессий legacy-транспорта.

## Публичное подключение в опубликованной Mumble-версии

Владелец сервера разворачивает официальный Hysteria v2.13.0 и Mumble:
[готовый стенд для VPS 1 vCPU / 1 ГБ](deploy/hysteria/README.md). Собственный Gul
relay больше не нужен. Старые материалы в `deploy/relay` описывают предыдущую схему.

В форме подключения укажите `hysteria2://voice.example.org` (по умолчанию UDP 443),
ник и отдельно пароль. Для настроенной на сервере обфускации используйте
`?obfs=salamander` или `?obfs=gecko`. Пароль применяется к Hysteria auth,
Mumble serverPassword и выбранной обфускации; его нельзя вставлять в URL.
Адрес без схемы также означает Hysteria, а не прямое подключение к Mumble.

Для TCP владелец добавляет [VLESS + REALITY](deploy/reality/README.md) и выдаёт
полную ссылку Gul вида `vless://host?flow=none&pbk=KEY&security=reality&sid=HEX&sni=DOMAIN&type=tcp`.
Вставьте её целиком в поле адреса, пароль введите отдельно. Это профиль Gul,
а не универсальная VLESS-ссылка с UUID. Он использует TCP 443 без Vision.
Параметры профиля не публикуйте; смена транспорта выполняется явной сменой адреса.

Внешний Hysteria TLS проверяется через системные центры сертификации; REALITY —
по закреплённому ключу из профиля. В обоих случаях внутренний Mumble TLS сохраняет
TOFU-пин и устойчивую клиентскую идентичность, привязанные к адресу VPS.
Старые сохранённые адреса `wss://...` нужно явно заменить на новый адрес,
выданный владельцем сервера. Работу из конкретных сетей провайдеров нужно
проверять живым разговором: Hysteria требует доступного UDP, REALITY — TCP до VPS.
Ни один транспорт не гарантирует доступность заблокированного IP.

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

Для `0.7.0-alpha.1` на том же компьютере нужно отдельно запустить локальный
[LiveKit-стенд](docs/LIVEKIT-LOCAL.md); одного скачанного клиента для подключения
к друзьям через прежний VPS недостаточно. macOS-сборка имеет отдельный bundle ID
`io.github.lywwkkaad.gul.livekit`, настройки хранятся в `gul-livekit`.
Имя исполняемого файла и Linux-пакета остаётся `gul`: не заменяйте рабочую
установку `0.6.0-alpha.3`, если она нужна для текущего разговора.

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
