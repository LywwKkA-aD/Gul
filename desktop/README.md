# Gul desktop

Единственный клиент Gul **0.8.0-alpha.5**: Electron, TypeScript, React и
LiveKit JS. Chromium владеет голосом, экраном и playback; официальный
Xray v26.3.27 поставляется отдельным executable. Go клиент отсутствует;
серверный broker находится в `../server/`.

[Возможности и установка](../README.md) · [Архитектура](../PLAN.md) ·
[Состояние миграции](../docs/ELECTRON-MIGRATION.md) ·
[Локальный стенд](../deploy/livekit/README.md)

## Запуск

Нужны Node.js 24, npm, Python 3 и OpenSSL в PATH для transport tests.
На Windows используется `python`, на macOS/Linux — `python3`.

```sh
npm ci
npm run vendor:xray
npm run dev
```

Xray скачивается по закреплённому archive SHA-256; executable не хранится
в Git. `npm run pack` создаёт распакованное приложение; `npm run dist -- --publish never`
собирает установщик текущей платформы. Windows x64 с MSVC собирает оба
помощника: `npm run build:ptt` и `npm run build:audio`. WASAPI helper проверяет
process loopback и исключает дерево процессов Gul; без поддержки API доступно
только видео, без общего микса. На Linux установите
`g++ pkg-config libglib2.0-dev libpulse-dev` и перед запуском/упаковкой выполните
`npm run build:ptt` и `npm run build:audio`. Последний помощник захватывает звуки
приложений отдельно от Gul через локальный PulseAudio/PipeWire socket.
Проверка исключения/сохранения устройств/динамических потоков/очистки:
`python3 native/audio-capture/integration.py --helper resources/audio-capture/linux-x64/gul-audio`
в отдельном аудиостенде.

## Проверки

```sh
npm run check
npm run format:check
npm run test:coverage
npm audit --audit-level=moderate
```

Test runner требует минимум 80% строк, ветвей и функций проверяемых TS модулей.
Локально 367 tests: 365 passed/2 platform skips на macOS. TS coverage:
89.68% строк, 90.39% ветвей, 88.46% функций. Installed packaged smoke
с sandbox прошёл на Windows/Linux/macOS arm64/x64; Linux DEB установлен.
Windows F8 hook registration/dispose и семь Linux portal DBus сценариев прошли.
Для реального media E2E сначала подготовьте отдельный REALITY/LiveKit stand:

```sh
npm run build
GUL_ELECTRON_STAND_DIR=/absolute/path/to/stand \
  npm run test:integration -- e2e/desktop.live.spec.ts
```

Этот сценарий использует два Electron клиента, synthetic голос, видео и stereo
screen audio; он прошёл через обновлённый VPS с 20 циклами демонстрации.
Native Linux X11/Xvfb/PulseAudio capture подтвердил movingframes, 720p bounds
и stereo PCM 440 Гц L / 660 Гц R через TURN/TCP: разделение 49 дБ,
исключение собственного Gul 880 Гц — 52 дБ при сохранении обычного вывода.
`e2e/sdp-sdk.spec.ts` использует закреплённый LiveKit PCTransport и настоящий
Chromium; `e2e/sdp-bundle.live.spec.ts` проверяет одиночные повторные запуски,
ответы SFU, позднего третьего зрителя и повторный вход без SDK fallback.
Новые проверки: `e2e/voice-noise.live.spec.ts` сравнивает записанную речь с
синтетическим шумом при выключенном/включённом шумодаве через два клиента;
`e2e/windows-screen-pcm.spec.ts` проверяет stereo PCM bridge/worklet и очистку
без записи личного рабочего стола. В CI этот Windows backend проверяется на
Windows и Linux; macOS использует собственный Chromium capture и проверяет
упаковку/picker отдельно. `e2e/capture-picker-ui.spec.ts` использует
только синтетические источники. Native picker test запускается только на Linux
в отдельном рабочем столе с `GUL_ELECTRON_ISOLATED_DESKTOP=1`.

Windows `npm run build:audio` компилирует protocol/queue/quality tests и probe
исключения собственного процесса. При отсутствии render endpoint runner
пропускает аудиопробу, поэтому успешный CI не доказывает захват на Windows 10.
RNNoise WASM 0.2.1 закреплён в lockfile и включён в voice worklet, исполняется
без сети; PCM микрофона не передаётся через IPC. Голосовой AudioContext —
48 kHz, обработанный микрофон mono; screen audio остаётся stereo без DSP.
Алгоритмическая задержка RNNoise с адаптером render blocks — около 30 мс.

Physical mic, игра Windows 10 ↔ Ubuntu 26,
физическое удержание клавиши, Wayland на Ubuntu 26 и часовой soak требуют отдельных проверок.
Для packaged smoke задайте `GUL_PACKAGED_APP_PATH` и запустите
`npm run test:integration -- e2e/packaged.spec.ts`.

Секреты принадлежат main; renderer sandboxed и получает только media grants.
Saved password использует защищённый safeStorage после явного согласия;
диагностика не содержит credentials, профили, IP или чат. Screen capture
автоматически запрашивает изображение и системный звук вместе после выбора
источника, viewer gain 0–200%; watch разрешения
на местный capture не требует. Ограничения loopback и статус проверок
описаны в root README.
