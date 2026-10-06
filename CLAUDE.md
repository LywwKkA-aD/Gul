# CLAUDE.md
Проект: десктопный голосовой клиент. Текущий локальный прототип полностью на LiveKit; Wails v3 + Go core + React/TS.
Главный документ: PLAN.md (архитектура §1–6, текущий милстоун §7, правила §10).
Журнал решений: docs/DECISIONS.md. Верификация зависимостей: docs/research/.
Дизайн-истина: прототип (docs/design/prototype-source.html) — светлая схема
с тёмным сайдбаром, иконки Phosphor, токены дословно из :root.

Команды: task dev · task murmur:up · task test · task lint · task package
Стенд: mumblevoip/mumble-server:v1.5.915 в докере; отладка AEC — VoiceTargetLoopback (ID 31).

Транспорт текущего GUI: LiveKit SFU 1.13.8, Go SDK 2.18.1, JS SDK 2.22.3.
Голос и чат идут из Go через LiveKit; JS companion отвечает только за экран.
Нативный аудиодвижок, PTT, DSP и устройства сохранены. Приём screen audio тоже
идёт через Go и общий deafen/mixer. Legacy Mumble/Hysteria/REALITY пакеты сохранены
для регрессий, но не входят в runtime граф основного клиента. Удалённые VPS не менялись.

M8.1: полный клиент пока только с loopback broker. `scripts/livekit-local.sh` —
локальный стенд; `scripts/livekit-client.sh` — отдельный macOS Gul LiveKit bundle.
Настройки в `gul-livekit`, абсолютный `GUL_CONFIG_DIR` позволяет несколько клиентов.
`scripts/livekit-native.sh` и `GUL_LIVEKIT_LAB=1` сохраняют отдельную лабораторию экрана.
Инструкции и ограничения: docs/LIVEKIT-LOCAL.md.

Стек (пины жёсткие, @latest запрещён): Wails v3.0.0-beta.11 · Go ≥1.26 ·
форк stieneee/gumble (с M2 — свой форк + OpusPassthrough) · вендоренные libopus 1.6.1,
webrtc-audio-processing v2.1 (AEC3), RNNoise (ветка main), miniaudio 0.11.25 ·
React 19 + Vite 8 + Tailwind v4 + zustand v5 + TypeScript 6.0.2 (не 7.x).

Жёсткие правила:
- Аудио-сетка 48k/10ms/480. ДВА независимых устройства; duplex-режим miniaudio запрещён.
- Realtime-callback — чистый C: ноль Go, аллокаций, локов; только memcpy в ring.
- Тракт TX: mic s16 → APM (HPF→AEC3→NS→AGC2) → float в ШКАЛЕ S16 → RNNoise → gate → s16 → opus → LiveKit RTP. В legacy Mumble — Conn.WriteAudio напрямую.
- Тракт RX: RTP reorder → Opus/PLC decode → repack → адаптивный джиттер → микшер → ProcessReverseStream → playback. OnAudioStream никогда не блокирует read-loop.
- DSP-состояния — в одной горутине (LockOSThread); явные Close(), без финализаторов.
- services/ — тонкие; логика в internal/. UI не трогает нативный голос; screen controller владеет JS SDK и браузерным захватом.
- Сигнатуры внешних API сверять с исходниками (доки Wails отстают от кода); первичная верификация — docs/research/.
- Работать в рамках текущего милстоуна PLAN.md §7; коммиты — conventional; go test -race в CI.
- Язык общения — русский; код, идентификаторы, коммиты — на английском. Без эмодзи.
