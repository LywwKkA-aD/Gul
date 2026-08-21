# DECISIONS — журнал решений проекта

Формат: дата · решение · обоснование. Новые записи добавлять сверху.

## 2026-08-21 — Протокол: ChannelListener получает и текст, не только аудио

Проверено по src/murmur/Messages.cpp тега v1.5.915, Server::msgTextMessage:
текст, адресованный каналу (channel_id) или дереву (tree_id), доставляется
участникам канала И его слушателям — m_channelListenerManager
.getListenersForChannel (строки 1617, 1657). Ожидание «листенеры — только
аудио» опровергнуто. Сопутствующие факты из того же файла:
- отправитель всегда исключается из доставки (users.remove(uSource), 1682) —
  локальное эхо на клиенте обязательно (реализовано в M1);
- слать текст в канал можно, не находясь в нём: нужен только
  ChanACL::TextMessage на целевой канал (1608);
- подписка слушателем требует ChanACL::Listen; серверные лимиты
  listenersperchannel/listenersperuser: отрицательное значение — безлимит,
  0 — полный запрет ChannelListener на сервере, положительное — лимит;
  отказ по лимиту частичный (continue, не return) — батч подписок
  применяется в части разрешённого;
- слушатели связанных (linked) каналов текст НЕ получают — линки в
  msgTextMessage не участвуют;
- опции сервера, отключающей доставку текста именно слушателям, нет
  (косвенно фичу целиком гасит лимит 0); внешний RPC-фильтр
  textMessageFilterSig может отклонить сообщение только целиком.
Проверка независимо продублирована вторым чтением исходника (включая
ACL.cpp: для отправки нужен Traverse по цепочке, Write включает TextMessage).
Клиентская сторона: форк gumble уже содержит поле listening_channel_add в
protobuf (Mumble.pb.go), высокоуровневого API нет — добавим тонкие методы в
своём форке в M2; gumble анонсирует версию 1.5.1 (> 1.4.0), сервер считает
его listener-aware. Следствие: нотификации и параллельные текстовые ленты из
каналов, где мы не находимся, реализуемы через слушателей — это вход для
выбора семантики каналов (комнаты vs Discord-конвенция).

## 2026-08-21 — DSP: WebRTC APM (AEC3) вместо speexdsp

speexdsp исключён из проекта: его MDF-эхоподавитель по документации самого Speex
не работает при незалоченных часах захвата/воспроизведения — а это наш базовый
сценарий (два устройства, дрейф). Берём webrtc-audio-processing v2.1 (freedesktop,
WebRTC M131, BSD-3 + PATENTS): AEC3 + GainController2 + NS + HPF одним конвейером,
int16/48k/10ms нативно. C-шим пишем свой (~200-300 строк, референс — LiveKit apm.cpp).
Вендорим с срезом abseil (Apache-2.0), единый -std=c++17, свой patches/.
Основание: docs/research/alternatives/aec.md.

## 2026-08-21 — Opus: вендоренный libopus 1.6.1 + свой кодек

layeh/gopus (libopus 1.1.2, только amd64) и stieneee/gopus (удалил вендоринг,
требует системный libopus через pkg-config) отклонены оба. Вендорим libopus 1.6.1
(BSD-3, float, без DRED/OSCE/BWE, без intrinsics/RTCD) + собственный
gumble.AudioCodec; в форке gumble включаем OpusPassthrough (ветка
feat/opus-passthrough) со stub-кодеком. Deep PLC — build-тег на будущее (M5).
Проверено эмпирически: 137 stub-.c, +443 КБ к бинарнику, энкод ~1% ядра.
Основание: docs/research/alternatives/opus.md.

## 2026-08-21 — Аудио I/O: два устройства вместо duplex, своя обёртка вместо malgo

ma_device_type_duplex запрещён (наивный ring с молчаливым дропом/тишиной —
невидимый саботаж AEC). Открываем два независимых устройства, дрейф считаем сами
(ppm по счётчикам кадров), realtime-callback — чистый C без Go. gen2brain/malgo
заменяется собственной тонкой cgo-обёрткой над вендоренным miniaudio 0.11.25
с двумя патчами: WASAPI AudioCategory_Communications (Voice Clarity Win11),
опциональный VoiceProcessingIO на macOS. На miniaudio 0.12 не рассчитываем.
Отклонены: PortAudio (нет релизов с 2021, системная зависимость), cubeb (Rust,
нет Go-биндингов), SDL3 (нет синхронного duplex, динамическая загрузка),
свои бэкенды с нуля (6-10 недель, не окупается).
Основание: docs/research/alternatives/audio-io.md.

## 2026-08-21 — Шумодав: RNNoise ветка main, за интерфейсом Denoiser

Остаётся RNNoise, но ветка main (модель 2025-01, ~1.34M параметров), не тег v0.2.
Веса — weights_blob.bin (~1.3 МБ) в репо + go:embed + -DUSE_WEIGHTS_FILE.
Прячем за Go-интерфейсом Denoiser (кадр 480) для замены без переписывания пайплайна.
Роль после APM проверяется слепым A/B в M3 (риск пересуппрессии с NS APM).
Отклонены: DeepFilterNet (апстрим заморожен с 2024-09, CI красный, Rust-тулчейн,
+40 мс), GTCRN/Silero/ONNX-путь (16 кГц + 11-80 МБ рантайма), TEN VAD (лицензия
с field-of-use ограничениями). Вотчлист: FastEnhancer / faster-enhancer.c —
пересмотреть в конце 2026.
Основание: docs/research/alternatives/noise-suppression.md.

## 2026-08-21 — Оболочка: Wails v3 подтверждён; отклонены Tauri v2, Electron, webview_go, fyne/gio

Wails v3 — единственный вариант с Go+cgo-ядром в одном процессе с UI и
типизированными биндингами; бонус — штатные GlobalShortcut и SystemTray.
Пин v3.0.0-beta.11 жёстко (беты — nightly, @latest запрещён). Триггер отката:
нет RC к готовности M2 или сломан cgo на одной ОС → Wails v2.14.0.
Отклонены: Tauri v2 (Go-ядро становится sidecar-процессом + Rust-тулчейн без
потребляемых выгод), Electron (миф про «бесплатный AEC»: Chromium гасит только
собственный плейбек — electron#47043; бандл 100+ МБ), webview_go (мёртв с 2024-08),
fyne/gio (живы, но нативный UI = выбросить HTML-прототип).
Фронтенд-пины: Vite 8 (Rolldown, stable), TypeScript 6.0.2 (НЕ 7.x до 7.1 —
не работает typescript-eslint), zustand v5, @phosphor-icons/react 2.1.10
(низкая частота релизов принята осознанно).
Основание: docs/research/alternatives/shell-frontend.md.

## 2026-08-21 — Mumble: форк подтверждён углублённо; сервер v1.5.915

stieneee/gumble — единственная живая Go-реализация протокола 1.5 (проверены все
25+ форков layeh/gumble). Пин: v0.0.0-20240610021017-a3449ae7108c. С M2 — свой
публичный форк (обязательство MPL-2.0) с влитой веткой feat/opus-passthrough
(4bdfe39e, 2026-02-16). Блокирующая отправка из read-loop в форке сохранилась —
неблокирующие обёртки обязательны. AudioOutgoing/OpusOutgoing не использовать
(скрытые +10 мс, аллокации) — прямой Conn.WriteAudio. Сервер:
mumblevoip/mumble-server:v1.5.915 (стабильный 2026-07-19), 1.6 — RC, не брать.
Основание: docs/research/alternatives/mumble-ecosystem.md.

## 2026-08-21 — Транспорт голоса: TCP-туннель

Принимаем факт, что gumble передаёт голос только через UDPTunnel поверх TLS/TCP
(UDP-крипто в библиотеке не реализовано). Собственный UDP-путь в v1 не строим.
Следствие: джиттер-буфер проектируется под TCP-берсты после ретрансмита
(сотни миллисекунд), а не под микроджиттер 60 мс. Замер mouth-to-ear задержки
на реальной сети — обязательная часть M2.
Основание: docs/research/gumble.md, docs/research/plan-critique.md.

## 2026-08-21 — Библиотека Mumble: форк stieneee/gumble

Вместо замороженного upstream layeh/gumble (последнее функциональное изменение —
март 2020) используем живой форк github.com/stieneee/gumble: протокол Mumble 1.5,
protobuf-формат аудиопакетов, закрытие аудиоканалов при отключении пользователя,
миграция на google.golang.org/protobuf. Используется живым проектом
mumble-discord-bridge. Пин — псевдоверсией конкретного коммита.
Основание: docs/research/gumble.md.

## 2026-08-21 — Дизайн: прототип — единственный источник правды

Gul-Prototype-offline.html (распакованный исходник — docs/design/prototype-source.html)
нормативен для UI. Следствия, перекрывающие PLAN.md §2 и §6:
- схема светлая (белое полотно + серый хром) с тёмно-синим сайдбаром #1B2440,
  а не «тёмная тема по умолчанию»;
- иконки — Phosphor (пары regular/fill как семантика состояния), а не lucide-react;
- дизайн-токены переносятся дословно из блока :root прототипа в
  frontend/src/styles/tokens.css; производные цвета — color-mix(in oklab) от --accent;
- настройки темы (accent / density / glow) — через CSS-переменные на
  document.documentElement, как в прототипе.
Основание: docs/research/prototype-analysis.md.
