# Миграция Gul на Electron

**0.8.0-alpha.4:** миграция исходников завершена. `desktop/` — единственный
клиент на Electron/TypeScript/React/LiveKit JS. `server/` — самостоятельный
Go broker. Legacy client/services/frontend, native Go DSP, старые lab/deploy
и сборочные файлы удалены. Сохранены иконки, нужные attribution и
`docs/design/prototype-source.html` как референс палитры.

## Итоговая архитектура

Renderer владеет всем media через Chromium и LiveKit JS; main — полномочиями
сессии, транспортом и доступом к ОС. Preload предоставляет узкий IPC bridge.
Видео/PCM не передаются через IPC. Voice и screen имеют отдельные Rooms;
просмотр не зависит от собственного захвата.

Официальный Xray v26.3.27 поставляется отдельным executable с SHA-256/лицензией.
Main обслуживает аутентифицированный SOCKS, verified inner TLS, WSS bridge
и TURN/TCP bridge. HTTP session proxy не используется как обход маршрутизации
ICE. Путь фиксирован, redirect и прямой fallback запрещены; grant/epoch fence
закрывает старые потоки при смене канала и выходе.

## Перенесённые функции

| Область | Реализация |
|---|---|
| Голос | Mono48k, WebRTC AEC/AGC, RNNoise, input gain/уровень, AudioWorklet VAD |
| Управление | Mute/deafen, устройства, gain 0–200% и local mute отдельно |
| Общение | Каналы/roster, чат с историей в памяти по каналам, RTT |
| Экран | Выбор источника, automatic system audio, 720p30, viewer gain 0–200%, fullscreen/stop |
| Сохранение | Адрес/ник и password только с согласием через protected safeStorage |
| Приложение | Диагностика ZIP, GitHub update notice, tray/корректное закрытие |
| Клавиши | Toggle; hold через платформенный backend с отказом при недоступности |

Исходные цвета и локальные шрифты Gul сохранены. Секреты не хранятся в renderer
settings; raw network logs и личные адреса не входят в diagnostics.

## Что проверено

- 348 unit/security/lifecycle tests: локально macOS 346 passed/2 platform skips;
  покрытие TS строк/ветвей/функций — 89.66%/90.46%/88.70%, порог 80%.
- Самостоятельный Go broker: race/vet/build; broker coverage — 92.8%,
  всего module — 87.3%.
- Installed packaged smoke с sandbox на четырёх CI целях: Windows x64,
  Linux x64, macOS arm64/x64; Linux DEB действительно установлен.
- Windows native F8 hook: регистрация/освобождение. Linux GlobalShortcuts
  portal: семь DBus сценариев lifecycle/отказов.
- Локальный HTTPS/TURN/REALITY smoke: certificate verification, назначение,
  неверный пароль и запрещённые адреса/порты.
- Два настоящих Electron клиента через обновлённый удалённый VPS: двусторонний
  synthetic voice, чат, decoded video/stereo audio и 20 циклов демонстрации.
- Native Linux X11/Xvfb/PulseAudio capture: movingframes, 720p bounds и
  stereo PCM 440 Гц L / 660 Гц R через TURN/TCP; разделение 49 дБ.
  Собственный Gul 880 Гц исключён на 52 дБ, обычный вывод сохранён.

Synthetic источники проверяют медиатранспорт. Native hook registration/portal
сценарии не проверяют физическое удержание клавиши в игре. Windows CI runner
не заменяет runtime проверку компьютера с Windows 10.

## Проверки перед стабильной версией

1. Физический микрофон, качество AEC/NS и корректный выбор устройств на целевых ПК.
2. Windows 10 ↔ Ubuntu 26/Wayland: реальная игра/звук в обе стороны, задержка,
   stop/channel switch и отсутствие audio feedback.
3. Физическое hold PTT: отпускание клавиши, focus loss и отсутствие
   залипшего микрофона на целевых компьютерах.
4. Часовая игровая сессия с измерением CPU/RAM и потерь сети.

Демонстрация автоматически запрашивает изображение и системный звук;
у зрителя отдельная громкость 0–200%. Linux helper исключает Gul из захвата,
сохраняя обычный вывод. Windows alpha.4 использует встроенный WASAPI helper
с исключением дерева Gul и проверкой поддержки API. PCM идёт по ограниченному
локальному WebSocket, разрешённому только текущему окну/захвату; через IPC
передаются лишь параметры разрешения. При недоступности API передаётся только
видео. На macOS own-audio exclusion запрашивается и зависит от источника.

Alpha.4 добавляет RNNoise в голосовой AudioWorklet и реальное переподключение
capture при изменении AEC/NS/AGC. Отмена, mute/PTT и поздние ошибки процессора
не открывают необработанный микрофон. Новый picker внутри Gul показывает
превью и подтверждение; Linux Wayland сохраняет системный portal.

Релизный workflow публикует материалы после успешных проверок;
публикация GitHub релиза отдельно подтверждается его результатом. Команды: [README](../README.md), deployment/E2E:
[deploy/livekit](../deploy/livekit/README.md), полный план: [PLAN](../PLAN.md).
