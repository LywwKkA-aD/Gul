# Миграция Gul на Electron

**0.8.0-alpha.1:** миграция исходников завершена. `desktop/` — единственный
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
| Голос | WebRTC mono48k, AEC/NS/AGC, input gain/уровень, AudioWorklet VAD |
| Управление | Mute/deafen, устройства, gain 0–200% и local mute отдельно |
| Общение | Каналы/roster, чат с историей в памяти по каналам, RTT |
| Экран | In-app source selection, 720p30, stereo audio, watch/fullscreen/stop |
| Сохранение | Адрес/ник и password только с согласием через protected safeStorage |
| Приложение | Диагностика ZIP, GitHub update notice, tray/корректное закрытие |
| Клавиши | Toggle; hold через платформенный backend с отказом при недоступности |

Исходные цвета и локальные шрифты Gul сохранены. Секреты не хранятся в renderer
settings; raw network logs и личные адреса не входят в diagnostics.

## Что проверено

- Unit/security/lifecycle tests и порог покрытия 80% для проверяемых TS модулей.
- Самостоятельный Go broker: race/vet/build; покрытие broker — 92.8%, всего module — 87.3%.
- Реальный локальный HTTPS/TURN/REALITY smoke: certificate verification,
  правильное назначение, неверный пароль и запрещённые IP/порты.
- Два настоящих Electron клиента через удалённый сервер: голос в обе стороны,
  чат, декодированное синтетическое видео, stereo audio и 20 циклов демонстрации.

Последний пункт использует тестовые источники. Он не подтверждает native
capture выбранной игры, аппаратный encoder или качество звука на целевых ПК.

## Проверки перед стабильной версией

1. Windows 10 ↔ Ubuntu 26: реальные кадры/звук игры в обе стороны, stereo,
   задержка, корректный stop/channel switch и отсутствие audio feedback.
2. Windows native hold helper и Linux GlobalShortcuts portal: focus loss,
   отпускание клавиши, отмена разрешения, занятое сочетание и cleanup.
3. CI build/install/packaged smoke Windows/Linux/macOS и настоящий Linux
   PulseAudio loopback capture; отдельно от synthetic media tests.
4. Часовая игровая сессия с измерением CPU/RAM и потерь сети.

Windows 10/Linux loopback может включать Gul voices; отдельный вывод Gul или
выключение incoming playback предотвращает возврат голосов. На Windows 11/macOS
исключение собственного звука запрашивается и зависит от capability ОС.
Ни работающий транспорт, ни успешный installer build не заменяют эти проверки.

Команды: [README](../README.md), deployment/E2E:
[deploy/livekit](../deploy/livekit/README.md), полный план: [PLAN](../PLAN.md).
