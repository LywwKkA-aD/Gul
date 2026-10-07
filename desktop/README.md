# Gul desktop

Единственный клиент Gul **0.8.0-alpha.3**: Electron, TypeScript, React и
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
собирает установщик текущей платформы. Native Windows hold helper собирается
`node scripts/build-ptt.mjs` на Windows x64 с MSVC. На Linux установите
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
Набор — 258 unit tests, на macOS 256 passed/2 platform skips; TS line coverage —
90.80%. Installed packaged smoke
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
