# Работа с Gul

Текущий клиент **0.8.0-alpha.8**: Electron + TypeScript + React + LiveKit JS.
Официальный Xray v26.3.27 встроен отдельным executable. Go находится только в
`server/` и обслуживает broker API. Legacy клиент и эксперименты удалены.

Главные документы: [PLAN.md](PLAN.md), [решения](docs/DECISIONS.md),
[сервер](docs/SERVER.md), [deployment](deploy/livekit/README.md).
Дизайн-референс: `docs/design/prototype-source.html`; исходную палитру Gul
сохраняем, layout каналов/чата/участников адаптирован под голосовое приложение.

## Рабочие области

- `desktop/src/main`: безопасность, SessionAuthority, сохранение серверов,
  транспорт, capture dialogs, PTT, диагностика, tray и update notice.
- `desktop/src/preload`: узкий IPC bridge; renderer не получает Node API.
- `desktop/src/renderer`: React UI и LiveKit media lifecycle.
- `desktop/src/transport`: фиксированный REALITY/SOCKS/TLS/WSS/TURN путь.
- `desktop/native/ptt`: узкие OS adapters глобального удержания клавиши.
- `server`: самостоятельный Go module, broker и JSON wire model.
- `deploy/livekit`: действующий deployment и отдельный локальный Docker fixture.

## Команды

```sh
cd desktop
npm ci
npm run vendor:xray
npm run dev
npm run check
npm run format:check
npm run test:coverage
```

Из корня: `go -C server test -race ./...`, `go -C server vet ./...`,
`python3 -m unittest discover -s deploy/livekit -p 'test_*.py'`.
E2E и подготовка fixture описаны в deployment README. Node.js 24; Python 3;
OpenSSL в PATH для transport tests. Go версия закреплена в `server/go.mod`.

## Обязательные ограничения

- TDD для новых поведения/регрессий; unit coverage не менее 80% строк,
  ветвей и функций. Security/media lifecycle проверять интеграционно.
- Не публиковать пароли, JWT, broker bearer, профили, IP пользователей,
  сырые сетевые логи или приватные fixture configs.
- Renderer sandbox/contextIsolation; проверять IPC отправителя, входы,
  media grants и эпохи. Не добавлять прямой сетевой fallback.
- Chromium владеет кодированием и playback; native screen PCM приходит через
  приватный Linux source или ограниченный Windows WebSocket, без JSON IPC.
  Voice DSP/RNNoise не применяется к stereo screen audio.
- Password storage только по явному согласию через защищённый safeStorage;
  Linux basic_text запрещён. Не записывать пароль в localStorage/settings.
- Смена канала/выход должны остановить capture и закрыть старые потоки;
  поздние callbacks не меняют новую сессию.
- Xray скачивать только по закреплённому manifest SHA-256. Пины зависимостей
  менять с проверкой исходников/API и тестами; `@latest` не использовать.
- Не считать synthetic E2E доказательством native game capture, Windows 10
  runtime, hardware encoding или отсутствия audio loopback.
- Код/идентификаторы/коммиты — английские, общение/пользовательские тексты —
  русские. Небольшие модули, максимум 800 строк, без эмодзи.

Linux и поддерживаемый Windows process loopback исключают Gul voices.
При недоступном Windows API не использовать общий output mix: только видео.
macOS exclusion зависит от ОС/источника. Native capture capability проверяется
по фактическому backend, а не номеру ОС. Физический mic/игра Windows 10 ↔ Ubuntu 26,
Wayland и часовой soak проверяются отдельно. Synthetic Windows PCM bridge
и runner без render endpoint не доказывают native Windows 10 audio exclusion.
GitHub release публикует CI после проверок. Коммиты — `[feat]`, `[fix]`, `[ref]`,
`[docs]`, `[test]`, `[updt]`, `[del]`; перед commit выполнить подходящие проверки.
