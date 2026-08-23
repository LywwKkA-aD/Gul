# PLAN.md — Голосовой клиент «как Дискорд» поверх Mumble

> Главный документ проекта для Claude Code. Лежит в корне репозитория, рядом — `CLAUDE.md` (краткая выжимка правил).
> Язык общения в проекте — русский. Код, идентификаторы, коммиты — на английском.
> Журнал решений — `docs/DECISIONS.md`. Исследования, на которых основан план, — `docs/research/`
> (верификация каждого факта по первоисточникам, 2026-08-21). Дизайн-истина — прототип
> `Gul-Prototype-offline.html` (распакованный исходник: `docs/design/prototype-source.html`).

---

## 0. Цель

Десктопное приложение для голосового общения и текстового чата в компании друзей (6–15 человек), с современным интерфейсом. Сервер — готовый **mumble-server (murmur)**, свой код — только клиент.

**Не-цели (v1):** видео, демонстрация экрана, мобильные платформы (эксперимент отложен в M6), федерация, свой сервер, свой UDP-транспорт, история чата на сервере (Mumble её не хранит — принимаем как ограничение v1).

**Ключевые качества:**
- чистый звук: эхоподавление (разговор на колонках без наушников) и шумоподавление с первого релиза;
- низкая задержка **в рамках TCP-транспорта** (голос идёт через Mumble UDPTunnel поверх TLS/TCP — см. §4.7 про бюджет латентности и DECISIONS.md);
- один самодостаточный бинарник на каждую ОС: все C-зависимости вендорятся и линкуются статически. Оговорка: WebView системный (WebView2 на Windows, WebKit на macOS — предустановлены; WebKitGTK на Linux — пакет дистрибутива);
- ничего не требует установки зависимостей у пользователя.

---

## 1. Архитектура

```
┌──────────────────────────── Desktop app (Wails v3) ──────────────────────────┐
│                                                                              │
│  frontend/  React + TS + Tailwind          services/  (мост, тонкий слой)    │
│  каналы · чат · участники · настройки ◄──► Connection/Chat/Audio/Settings    │
│            Wails events ▲                      │ вызовы методов              │
│                         │                      ▼                             │
│  internal/core — состояние и оркестрация, event bus                          │
│      │                              │                                        │
│      ▼                              ▼                                        │
│  internal/mumble                internal/audio — движок                      │
│  (форк gumble, passthrough,     два устройства miniaudio (не duplex!),       │
│   неблокирующие обёртки)        lock-free ring'и, DSP-горутина, джиттер      │
│      │                              │                                        │
│      │                          internal/dsp: apm (WebRTC AEC3+AGC2+NS),     │
│      │                          rnnoise (denoise+VAD), opus (libopus 1.6.1)  │
└──────┼───────────────────────────────────────────────────────────────────────┘
       ▼  Mumble TLS/TCP (весь трафик, включая голос — UDPTunnel)
  либо напрямую host:64738, либо внутри wss://host/mumble:443 → gul-relay (наш
  байтовый насос на VPS, см. §5) → mumble-server v1.5.915 — готовый, ноль нашего кода
```

Принципы:
- **Весь звук и протокол — в Go/C.** Frontend никогда не трогает аудио и сокеты; он только рисует состояние и дергает методы сервисов.
- **UI ↔ Go** — только через сгенерированные Wails-биндинги (вызовы методов) и Wails events (push состояния в UI).
- **Одна временная сетка аудио:** 48 000 Гц, моно, кадр 10 мс = 480 сэмплов. Все звенья пайплайна работают на этой сетке. Входящие кадры других длин (официальный клиент шлёт 20 мс) переупаковываются на границе приёма.
- **Realtime-поток аудио — только C.** Callback'и устройств не вызывают Go, не аллоцируют и не берут локов: только memcpy в/из lock-free ring. Вся DSP-работа — в выделенной Go-горутине (`runtime.LockOSThread`), которая дренирует ring'и кадрами по 10 мс.

---

## 2. Технологический стек (точно)

| Слой | Выбор | Пин | Примечания |
|---|---|---|---|
| Оболочка | **Wails v3** (beta) | `v3.0.0-beta.11` в go.mod И в `go install .../wails3@v3.0.0-beta.11` | Никогда `@latest`: беты — ежедневные nightly с master. Бинарных релизов у v3 нет (с beta.8), CLI только через go install. Docs: https://v3.wails.io — при расхождениях верить исходникам (v3/go.mod, v3/pkg/application) |
| Язык ядра | **Go** | **1.26.7** | cgo обязателен, C++17 для APM; patch-версия закреплена из-за исправлений безопасности stdlib |
| Протокол Mumble | **github.com/stieneee/gumble** (живой форк layeh/gumble) | псевдоверсия `v0.0.0-20240610021017-a3449ae7108c` | MPL-2.0. Протокол 1.5 (protobuf-аудио), закрытие аудиоканалов. С M2 — собственный публичный форк с влитой веткой `feat/opus-passthrough` (коммит `4bdfe39e`), см. §5 |
| Opus | **вендоренный libopus 1.6.1** + свой `gumble.AudioCodec` | tarball c downloads.xiph.org по SHA256 | BSD-3. Float-сборка БЕЗ DRED/OSCE/BWE, без intrinsics/RTCD (цена ~8-10% CPU при абсолютных ~1% ядра). НЕ импортировать `stieneee/gumble/opus` (тянет системный libopus через pkg-config). 137 stub-.c, +443 КБ к бинарнику — проверено сборкой |
| AEC + AGC + NS | **webrtc-audio-processing v2.1** (freedesktop, WebRTC M131): AEC3 + GainController2 + NoiseSuppression + HPF | тег `v2.1` + свой каталог patches/ | BSD-3 + PATENTS; вендорится со срезом abseil-cpp (Apache-2.0), единый `-std=c++17`. C-шим свой (~200-300 строк extern "C", референс — livekit/rust-sdks webrtc-sys/src/apm.cpp). speexdsp из проекта исключён |
| Шумодав + VAD | **RNNoise, ветка main** (НЕ тег v0.2 и НЕ ветка master) | коммит main от 2025-02-22 | BSD-3. Модель main ~1.34M параметров (против 0.06M у «RNNoise 2018»). Веса: один раз собрать `weights_blob.bin` (dump_weights_blob, факт: **14.75 МБ** — float32-веса всех слоёв 11.5 МБ + int8-копии семи слоёв 2.8 МБ; compute_linear предпочитает float, безопасного урезания нет — conv1/dense_out/vad_dense существуют только во float, отсутствующая запись молча зануляет слой; детали в third_party/rnnoise/VERSION), закоммитить, грузить через `rnnoise_model_from_buffer` + `go:embed`; сборка с `-DUSE_WEIGHTS_FILE`. За Go-интерфейсом `Denoiser` (замена на DeepFilterNet/FastEnhancer — вопрос одного файла) |
| Аудио I/O | **вендоренный miniaudio 0.11.25** (`miniaudio.c`/`.h`) + собственная тонкая cgo-обёртка | файлы из репо mackron/miniaudio + patches/ | MIT-0/public domain. НЕ gen2brain/malgo (не экспонирует нужные поля) и НЕ duplex-режим (см. §4.2). Два патча: WASAPI `eCategory = AudioCategory_Communications` (Windows 11 Voice Clarity); опциональный `kAudioUnitSubType_VoiceProcessingIO` на macOS |
| Frontend | **React 19 + TypeScript + Vite 8 + Tailwind v4** | TypeScript **6.0.2** (НЕ 7.x до выхода 7.1 — не работает typescript-eslint) | Состояние — zustand v5 (setState из Wails-колбэков вне React-дерева — это фича). Иконки — **@phosphor-icons/react** 2.1.10, только точечные импорты (не баррель). Шаблон Wails даёт React 18 — апгрейд до 19 руками сразу после init |
| Сервер (не пишем) | **mumblevoip/mumble-server** | тег `v1.5.915` | Конфиг через `MUMBLE_CONFIG_*`, том `/data` (UID/GID 10000). 1.6 — RC, не брать. `opusthreshold` больше не трогаем (дефолт нормальный в 1.4+) |
| CI | GitHub Actions | матрица ubuntu-24.04 / windows-2025 / macos-latest | Linux-база: GTK4 + WebKitGTK 6.0 (`libgtk-4-dev`, `libwebkitgtk-6.0-dev`); `-tags gtk3` — только временный фолбэк, удаляется в v3.1 |

Лицензии: проект — MIT. Зависимости: gumble-форк MPL-2.0 (пофайловый copyleft: наши правки его файлов публикуются — поэтому свой форк публичный), webrtc-audio-processing BSD-3 + PATENTS, abseil Apache-2.0, libopus BSD-3, rnnoise BSD-3, miniaudio MIT-0. В дистрибутив кладётся NOTICE со всеми уведомлениями; `third_party/*/LICENSE` + `VERSION` (upstream-коммит и команда обновления) обязательны. Запрещено: `USE_GPL_FFTW3` и обычные GPL-зависимости; допустимое исключение — runtime GCC/libstdc++/libgcc под GCC Runtime Library Exception, полный текст которой входит в Windows-дистрибутив.

---

## 3. Структура репозитория

```
Gul/
├── PLAN.md                  # этот файл
├── CLAUDE.md                # правила для Claude Code (выжимка)
├── docs/
│   ├── DECISIONS.md         # журнал решений
│   ├── research/            # верификация зависимостей, разбор прототипа
│   └── design/prototype-source.html
├── go.mod / go.sum
├── main.go                  # wails: application, services, окно
├── Taskfile.yml             # генерится wails3, наши задачи добавлять сюда
├── build/                   # конфиги упаковки wails3
├── services/                # ТОНКИЕ Wails-сервисы: мост UI↔core, без логики
│   ├── connection.go        # Connect/Disconnect/State
│   ├── channels.go          # дерево каналов, Join(channelID)
│   ├── chat.go              # SendMessage, история сессии
│   ├── audio.go             # devices, mute/deafen, PTT state, уровни
│   └── settings.go          # load/save конфига, тема (accent/density/glow)
├── internal/
│   ├── core/                # состояние приложения, оркестрация, event bus
│   ├── mumble/              # обёртка над форком gumble: connect, неблокирующий
│   │                        # приём/отправка, lifecycle стримов, реконнект, TOFU
│   ├── audio/               # движок: устройства, ring'и, DSP-горутина, mixer
│   │   ├── engine.go        # запуск/остановка, выбор устройств, watchdog
│   │   ├── miniaudio/       # своя cgo-обёртка над vendored miniaudio (+ C ring'и)
│   │   ├── pipeline_tx.go   # mic → APM → RNNoise → gate → opus → WriteAudio
│   │   ├── pipeline_rx.go   # opus decode → jitter → mixer → playback + AEC ref
│   │   ├── jitter.go        # per-user адаптивный джиттер под TCP-берсты
│   │   ├── drift.go         # оценка дрейфа часов capture/playback (ppm)
│   │   └── levels.go        # RMS-метры для UI
│   ├── dsp/
│   │   ├── apm/             # cgo+C++ шим: webrtc AudioProcessing (AEC3/AGC2/NS/HPF)
│   │   ├── rnnoise/         # cgo: Denoiser интерфейс, vad_prob
│   │   └── opus/            # cgo: свой AudioCodec для gumble (encoder/decoder)
│   └── config/              # конфиг-папка ОС: config.json, клиентский сертификат,
│                            # TOFU-отпечатки серверов
├── frontend/                # Vite 8 + React 19 + TS 6.0.2 + Tailwind v4
│   └── src/
│       ├── app/             # экраны: ConnectScreen, MainScreen (композиция)
│       ├── components/      # ChannelTree, Chat, Composer, MemberList, BottomBar,
│       │   └── ui/          # SettingsModal; примитивы: IconButton, Avatar, Field...
│       ├── state/           # zustand store + theme.ts (applyTheme → CSS vars)
│       ├── styles/          # tokens.css (дословно из прототипа) + index.css
│       ├── assets/fonts/    # вендоренные woff2 (IBM Plex, Martian Mono) + OFL
│       └── bindings/        # сгенерированные wails-биндинги
├── cmd/
│   ├── gul-dsp/             # офлайн-кит слепого A/B шумодава (WAV → два кандидата)
│   └── gul-relay/           # WSS-релей: wss://host/mumble на 443 → murmur 64738
├── internal/relay/          # обработчик релея (bearer, лимиты, насос, серты)
├── internal/relayproto/     # контракт клиент↔релей: путь, субпротокол, bearer, лимит
├── third_party/
│   ├── webrtc-apm/          # webrtc-audio-processing v2.1 + срез abseil + patches/
│   ├── opus/                # libopus 1.6.1 (include, celt, silk, src) + stubs
│   ├── rnnoise/             # ветка main + weights_blob.bin
│   ├── miniaudio/           # miniaudio.c/.h + patches/
│   ├── toolchain-runtime/   # лицензии GCC/MinGW runtime (статическая Windows-сборка)
│   └── README.md            # upstream-коммиты, SHA256, команды обновления
├── deploy/
│   ├── murmur/              # docker-compose дев/прод сервера (v1.5.915, MUMBLE_CONFIG_*)
│   └── relay/               # Containerfile + quadlet релея (rootless, least-privilege)
├── scripts/                 # vendor-*.sh, collect-licenses.sh, проверки версии/заголовков
└── .github/workflows/ci.yml # матрица 3 ОС, релиз по тегу v*, статический релей
```

---

## 4. Аудиопайплайн — спецификация (сердце проекта)

### 4.1 Константы

```go
const (
    SampleRate    = 48000
    Channels      = 1
    FrameMs       = 10
    FrameSamples  = SampleRate / 1000 * FrameMs // 480
    JitterStartMs = 80    // стартовая глубина адаптивного джиттер-буфера
    JitterMaxMs   = 500   // потолок роста при TCP-берстах
)
```

`gumble.Config.AudioInterval = 10ms` (поле подтверждено в форке; `Config.AudioFrameSize()` = 480).

### 4.2 Устройства: два независимых, НЕ duplex

Duplex-режим miniaudio (`ma_device_type_duplex`) **запрещён**: на WASAPI/CoreAudio/PulseAudio это наивный ring между двумя асинхронными устройствами — при рассинхроне кадры молча выбрасываются или заменяются тишиной, и мы об этом не узнаём (подтверждено кодом miniaudio, см. docs/research/alternatives/audio-io.md).

Вместо этого:
- два независимых устройства (capture и playback), открытые через нашу обёртку `internal/audio/miniaudio`; формат запрашиваем s16/48000/mono, `PeriodSizeInFrames = 480`, `NoFixedSizedCallback = 0`;
- после инициализации снять фактические параметры (`CaptureInternalSampleRate` и т.д.) и логировать, где включился внутренний ресемплер miniaudio;
- **оценка дрейфа — наша** (`internal/audio/drift.go`): счётчик кадров в каждом callback + монотонное время → ppm на окне ~10 с. Коррекция: drop/dup одного кадра в reference-тракте раз в N секунд (AEC3 переживает скачок и переоценивает задержку); дробный ресемплер — запасной ход, если метрики покажут необходимость;
- смена/отключение устройства: notification callback miniaudio в обёртке + watchdog; на Windows рассмотреть `NoAutoStreamRouting = 1` (открытый дедлок miniaudio #1149).

### 4.3 Исходящий тракт (TX), на каждый кадр 10 мс

```
mic s16[480]  (из capture ring)
  → apm.ProcessStream(int16)         // внутри APM фиксированно: HPF → AEC3 → NS(soft) → AGC2
  → int16 → float32 В ШКАЛЕ S16      // прямой cast, БЕЗ деления на 32768!
  → rnnoise_process_frame → vadProb  // основной DNN-шумодав; NS в APM выставлен мягко
  → gate: PTT (кадр идёт, пока зажата клавиша)
          или VAD (гистерезис: открытие vadProb>0.6, закрытие <0.4, hangover 300 мс)
  → float32 → int16 с клиппингом [-32768, 32767]   // RNNoise выход не ограничивает
  → opus encode (свой энкодер)        // application по битрейту как у офиц. клиента,
  → client.Conn.WriteAudio(...)       // НАПРЯМУЮ: свой sequence, final на последнем кадре
```

Замечания:
- `Client.AudioOutgoing()`/`OpusOutgoing()` **не использовать**: небуферизованный канал, скрытые +10 мс (придержанный кадр), аллокация на кадр, игнорирование ошибок. Пишем через `Conn.WriteAudio` из своей sender-горутины; между DSP-горутиной и sender — буферизованный канал с drop-oldest и счётчиком дропов.
- Масштаб RNNoise — ловушка номер один: подача нормализованного ±1.0 даёт нерабочий шумодав и vad_prob = 0 навсегда (детектор тишины `E < 0.04`). Юнит-тест обязателен: синус ~0.3 FS в правильной шкале → vad_prob > 0.
- В mute кадры не отправляем вовсе (не шлём тишину). AGC2 в APM — единственный AGC тракта.
- Битрейт: подписаться на `ServerConfigEvent.MaximumBitrate`, `OPUS_SET_BITRATE` с клампом (никакого `gumbleutil.AutoBitrate` — паника при низком MaxBitrate), CBR/CVBR для ровного потока в TCP.
- Роль RNNoise после AEC3+NS проверяется в M3 слепым A/B (риск пересуппрессии): конфигурация по умолчанию — NS в APM `kLow/kModerate` + RNNoise; запасная — APM `kHigh` без RNNoise (тогда VAD-источник — energy+hangover или включаем RNNoise только как VAD).

### 4.4 Входящий тракт (RX)

```
пакеты из нашей неблокирующей обёртки над gumble (сырые opus-кадры, passthrough)
  → per-user: opus decode (свой декодер, переиспользуемый буфер + sync.Pool)
  → repack: кадры 480/960/1920/2880 сэмплов → очередь кадров 480
  → per-user адаптивный jitter buffer (старт 80 мс, рост до 500 мс на TCP-берстах,
    плавное сжатие обратно; PLC на пропуске — opus decode с data=nil)
  → mixer: сумма float32 активных + per-user volume + локальный mute + soft-clip (tanh)
  → финальный микс s16[480]:
      1) apm.ProcessReverseStream(int16)   // reference для AEC3 — РОВНО то, что уйдёт в динамик
      2) → playback ring → playback callback
  → apm.set_stream_delay_ms(измеренная задержка вывода: буфер + латентность устройства;
    AEC3 дальше уточняет сам)
```

Замечания:
- `deafen` = не играем никого + self-deaf по протоколу.
- Per-user volume и mute храним по стабильному ключу `User.Hash` (отпечаток клиентского сертификата, фолбэк UserID) — переживает реконнект собеседника.
- Форк передаёт protobuf-аудио, но теряет positional/context/volume_adjustment поля — не рассчитывать на них. Наличие sequence в пакетах форка сверить по исходникам на M2 (нужен джиттеру для детекции пропусков).

### 4.5 Слой gumble — неблокирующие обёртки (обязательны)

- **Приём**: `OnAudioStream` никогда не работает в read-loop. На каждый `AudioStreamEvent` — своя горутина, которая мгновенно перекладывает пакеты в буферизованный канал (20–50 кадров) с drop-oldest. Блокировка read-loop = отвал по 20-секундному deadline (ping не уходит).
- **Lifecycle стримов**: форк закрывает каналы при дисконнекте пользователя, но добавить свой таймаут тишины (2–3 с) и освобождение per-user состояния (декодер, джиттер) по `UserChange{Disconnected}`.
- **Passthrough**: `Config.OpusPassthrough = true` + обязательная заглушка `gumble.RegisterAudioCodec(4, &stubCodec{})` ДО подключения (иначе в Authenticate уйдёт `Opus: false`).

### 4.6 Правила реализации аудио-кода

- **Ноль аллокаций, ноль блокировок, ноль вызовов Go в audio callback** (callback — чистый C: memcpy в/из lock-free ring).
- DSP-состояния (APM, RNNoise, opus) живут в одной DSP-горутине с `runtime.LockOSThread`; из других горутин к ним не прикасаться.
- Все cgo-обёртки — явные `Close()`, без финализаторов.
- Метры уровня (RMS вход/выход) считаются в пайплайне, публикуются в UI событием раз в ~50 мс.
- Golden-тесты DSP: WAV-фикстуры (речь+шум, речь+эхо) через обёртки, метрики (снижение RMS шума, ERLE) против порогов. Фикстуры маленькие, в testdata/.
- Весь пайплайн (TX+RX+ring+джиттер+микшер) тестируем без устройств: интерфейсы источника/приёмника кадров + синтетический clock. Тесты с реальными устройствами — за build-тегом, в CI не идут.
- Бенчмарк кадра: обработка 10 мс кадра (APM+RNNoise+encode) ≪ 10 мс; замерить влияние GC (при необходимости `debug.SetMemoryLimit`/GOGC).

### 4.7 Бюджет латентности

Целевой mouth-to-ear на хорошей сети: **≤ 250 мс**. Разбивка (примерно): capture период 10 + ring 10 + APM <1 + RNNoise ~20 (алгоритмическая: окно + отложенный кадр) + encode <1 + сеть (TCP RTT/2 + сервер) + джиттер 80–200 + decode <1 + playback 10–20.

Измерение: `gumble.VoiceTargetLoopback` (ID 31) — сервер возвращает наш звук нам же; замер вносится в DoD M2 и M3. TCP-специфика: при потере пакета весь поток ждёт ретрансмит (head-of-line blocking) — джиттер-буфер обязан переживать берсты в сотни мс, это цена отказа от собственного UDP (осознанное решение, DECISIONS.md).

---

## 5. Слой Mumble (internal/mumble)

- Зависимость: до M2 — `github.com/stieneee/gumble` (пин псевдоверсией). С M2 — собственный публичный форк (обязательство MPL-2.0 при правках) с влитой веткой `feat/opus-passthrough`; go.mod переключить на него. // DECISION: имя/владелец форка — решить при создании.
- Подключение: адрес, ник, пароль (опц.), TLS. Две формы адреса (internal/mumble/endpoint.go): прямая `host:64738` и релейная `wss://host/mumble`.
- **WSS-релей (с v0.3.0-alpha.2)**: прямой долгоживущий Mumble TCP на 64738 в ряде сетей режется провайдером каждые ~20 с, поэтому прод ходит через собственный релей на HTTPS-порту 443 (`cmd/gul-relay`, `internal/relay`). Релей — непрозрачный байтовый насос с фиксированной целью (только loopback-murmur), не терминирует доверие: **внутри WSS — полный Mumble TLS с тем же TOFU-отпечатком**, что и при прямом подключении. Авторизация релея — bearer, производный от пароля сервера (`internal/relayproto`: PBKDF2 v2, v1-HMAC принимается в окне депрекации); одна полная Mumble-пачка = одно WS-сообщение, лимит `relayproto.MaxMessageBytes` с обеих сторон. Релей ничего не знает о 48k/10ms/480 и не трогает аудио-инварианты.
- **Модель доверия — TOFU как у самого Mumble**: при первом подключении сохраняем SHA-256 отпечаток сертификата сервера в конфиг, при смене — явное предупреждение с подтверждением. `insecure_tls` — только для локального докер-стенда, за явным dev-флагом.
- Клиентский сертификат: генерация при первом запуске, хранение в конфиг-папке (ключ 0600), подсовывать в Dial. Это стабильная «учётка» (User.Hash) без регистрации.
- Слушатели → внутренние события core: ConnectionState, ChannelTree, UserJoined/Left/Moved, TalkingChanged, TextMessage, PermissionDenied.
- Реконнект: экспоненциальный backoff (1s→30s cap), восстановление канала, статус «переподключение…» в UI.
- Текст: Mumble передаёт HTML — на выходе в UI санитизировать (белый список: b/i/u/a/br), на входе отправлять plain text с экранированием.
- Потокобезопасность: Config полностью настроить ДО Dial; обращения к Client/Users/Channels — только из listener'ов или через `Client.Do`. `go test -race` в CI обязателен.
- Whisper/shout — готовый API voicetarget; channel listeners 1.5 при необходимости — вручную через `WriteProto` (полей в .proto достаточно).

## 6. Frontend (frontend/)

**Дизайн-истина — прототип** (решение в DECISIONS.md): светлая схема (белое полотно + серый хром #EEF0F4) с тёмно-синим сайдбаром #1B2440; НЕ тёмная тема. Полный разбор токенов: docs/research/prototype-analysis.md.

**Семантика каналов — «комнаты»** (решение в DECISIONS.md): клик по каналу = перейти в него, как смена комнаты, без коннотации «подключиться к голосу». «В голосе» — отдельное явное состояние со своей индикацией и явным входом (M2). Активная текстовая лента одна — канал присутствия (ограничение протокола); текст из чужих каналов возможен только как нотификации через ChannelListener (сервер доставляет листенерам и текст, проверено по murmur v1.5.915) — бэклог M2+.

- `styles/tokens.css` — блок `:root` из прототипа **дословно** (единственный источник правды по цвету, ритму, времени; производные цвета — `color-mix(in oklab, ...)` от `--accent`). Tailwind v4: подключение через `@theme inline` для базовых токенов; производные использовать как `var(...)` в произвольных значениях, не плодить утилиты.
- Тема: props accent (#2F52DE | #1F6FB2 | #3E4FA8 | #5B4BD6) / density (Компакт|Средняя|Воздух → `--row-pad-y`, `--item-h`) / glow (Нет|Тонкое|Заметное → `--glow-spread`) — через `applyTheme()` в `state/theme.ts`, пишущую CSS-переменные на `document.documentElement` (как в прототипе). UI для них — секция «Внешний вид» в Settings.
- Иконки: `@phosphor-icons/react`, пары regular/fill = семантика выключено/включено. Только точечные импорты.
- Шрифты: IBM Plex + Martian Mono, woff2 (latin + cyrillic подсеты, weights 400/500), вендорены локально, `font-display: swap`, OFL-лицензии рядом. Никаких запросов в сеть.
- Приёмы из прототипа сохранить: границы box-shadow-ring'ом (не border — нет сдвига при фокусе); скролл только у внутренних контейнеров; `minmax(0,1fr)` в grid и `min-w-0` у flex-детей; `prefers-reduced-motion` — блок как есть.

Экраны:
1. **Connect** — адрес, ник, Connect, TOFU-статус сертификата, последние серверы.
2. **Main** — трёхколоночная раскладка (240px / 1fr / auto): дерево каналов с подсветкой речи (ореол gul-halo), чат сессии, участники (скрываемые), снизу карточка себя + mic-mute / deafen / settings.
3. **Settings (модал)** — вкладки: Звук (устройства, PTT/VAD, порог, тест микрофона с метром, громкость), Клавиши (PTT-хоткей: показывать фактически назначенную комбинацию — на Wayland её выбирает композитор), Внешний вид (accent/density/glow), О программе.

Состояние: zustand v5, наполняется исключительно Wails-событиями (`connection:state`, `channels:tree`, `user:talking`, `chat:message`, `audio:levels`, `audio:devices`). Никакого polling. Мок-данные прототипа не тянуть, но их типы — основа доменных типов (Person, VoiceParticipant, Message, Channel).

Чек-лист состояний для приёмки M1 — сценарии A/B/C из прототипа: есть говорящие (ореолы, эквалайзер), всё пусто (empty states), реконнект (uiLocked, disabled + opacity, пинг «— мс», баннер).

---

## 7. Милстоуны

Работать строго по порядку. Каждый милстоун = отдельная ветка `m<N>-<slug>` и PR. Вперёд не забегать.

### M0 — Каркас и стенд (цель: «пустое приложение видит сервер»)
- [x] `wails3 init -t react` (шаблона react-ts НЕ существует; react = TS-вариант). Сразу после: React 18→19, TypeScript 6.0.2, Tailwind v4, `npm run build` для проверки. Версии Wails/Go/Node зафиксировать в README.
- [x] `deploy/murmur/docker-compose.yml`: `mumblevoip/mumble-server:v1.5.915`, конфиг через `MUMBLE_CONFIG_*`, `MUMBLE_SUPERUSER_PASSWORD` задать явно; `task murmur:up`.
- [x] Подключить форк gumble (пин коммита): Connect-экран → соединение → дерево каналов и события в лог. TOFU-заготовка (сохранение отпечатка).
- [x] Смоук-тест против сервера v1.5.915: подключение, каналы, текст (голос — M2).
- [x] CI: матрица 3 ОС, gofmt+vet+golangci-lint, frontend lint (typescript-eslint на TS 6.0.2) + build, `go test -race`. Кэш Go build (APM появится в M3, но кэш нужен сразу).
- [x] Структурное логирование с уровнями + ротация в конфиг-папке (наблюдаемость с первого дня).
- **Готово, когда:** приложение собирается на 3 ОС в CI; локально коннектится к докер-murmur и печатает каналы/юзеров.

### M1 — Текст и навигация (цель: «полноценный текстовый клиент»)
- [x] tokens.css из прототипа + примитивы ui/ (IconButton, Avatar, Field...) + витрина-страница (свотчи токенов, иконки, аватары во всех состояниях) как дев-экран.
- [x] Дерево каналов в UI (реальное, с юзерами), Join по клику/даблклику.
- [x] Чат: приём/отправка, история сессии, автоскролл, санитизация HTML, группировка сообщений head/notHead.
- [x] Статусы соединения + автореконнект с восстановлением канала; сценарий C (uiLocked).
- [x] Клиентский сертификат: генерация, переиспользование; TOFU-диалог при смене отпечатка сервера.
- [x] Команда «собрать диагностику» (логи + версия + устройства).
- **Готово, когда:** два инстанса переписываются и ходят по каналам; убийство сервера → реконнект без рестарта; UI соответствует прототипу по чек-листу A/B/C.

### M2 — Голос без DSP (цель: «слышим друг друга»)
- [x] Вендор miniaudio + своя cgo-обёртка: два устройства s16/48k/480, C-ring'и, выбор устройств в Settings, watchdog смены устройств.
- [x] Вендор libopus 1.6.1 + internal/dsp/opus (encoder/decoder, conformance-тест с pion/opus как независимой проверкой декода).
- [x] Свой форк gumble с passthrough; регистрация stub-кодека; TX: mic → s16 → encode → `Conn.WriteAudio` (свой sequence, final); mute.
- [x] RX: неблокирующая обёртка приёма → per-user decode → repack 20мс→10мс → джиттер (адаптивный) → микшер → playback; deafen; per-user volume по User.Hash.
- [x] Оценка дрейфа (drift.go) — пока только метрика в лог.
- [x] Индикация речи (talking) в дереве; RMS-метры → «тест микрофона».
- [x] Замер mouth-to-ear через VoiceTargetLoopback (ID 31) — зафиксирован в docs/latency.md (M2 базлайн: медиана 95 мс на loopback).
- **Готово, когда:** два клиента разговаривают через докер-murmur В НАУШНИКАХ (эха ещё нет); наш клиент разговаривает с ОФИЦИАЛЬНЫМ клиентом Mumble в обе стороны; собеседник вышел/зашёл — его громкость сохранилась; CPU в разговоре < ~5%.

### M3 — DSP: AEC3 + шумодав + VAD (цель: «звук как у больших»)
- [x] Вендор webrtc-audio-processing v2.1 + срез abseil + C-шим (референс livekit apm.cpp); конфиг: echo_canceller on, HPF on, gain_controller2 adaptive on, NS soft; ProcessStream/ProcessReverseStream/set_stream_delay_ms в пайплайне по §4.3–4.4. ERLE-смоук 23.9 дБ, 64 мкс/кадр, 0 аллокаций.
- [x] Вендор RNNoise (main) + weights blob + go:embed; Denoiser-интерфейс; юнит-тест масштаба (синус 0.3 FS → vad_prob 0.99; нормализованный вход — 0, ловушка запинена тестом). ~0.49 мс/кадр на реальном входе (бенчмарк со свежим кадром на итерацию; затухающий буфер занижает в 10 раз).
- [x] Gate: VAD (гистерезис + hangover, настройка в UI) и Push-to-talk (пока при фокусе окна: слушатели keydown/keyup с гардами на инпуты/repeat/blur, клавиша настраивается, индикация в BottomBar).
- [x] Дрейф-коррекция reference-тракта — по событиям кольца вместо ppm (точнее: reference = ровно принятое к воспроизведению; underruns докармливаются тишиной; см. DECISIONS 2026-08-22).
- [x] Golden-тесты DSP на WAV-фикстурах (ERLE 32.8 дБ через полный тракт, шум −15.8 дБ при речи +1.1 дБ, детерминизм bit-exact); бенчмарк кадра: TX-тик 0.58 мс = 6% слота (RNNoise 0.44 + Opus 0.08 + APM 0.06), RX 9.6 мкс, 0 аллокаций.
- [x] Слепой A/B на своих записях: APM+RNNoise (NS soft) vs APM-only (NS high) — A победил в обеих парах, дефолт подтверждён (DECISIONS 2026-08-22).
- **Готово, когда:** разговор на колонках без наушников не даёт слышимого эха собеседнику (в т.ч. с USB-микрофоном и раздельными устройствами); шум клавиатуры ощутимо подавлен; VAD не режет начала слов; замер mouth-to-ear не деградировал против M2 больше чем на бюджет RNNoise.

### M4 — UX и упаковка (цель: «можно раздать друзьям»)
- [ ] Глобальный PTT: штатный `app.GlobalShortcut` Wails v3. Windows/X11 — прямо; Wayland — XDG-портал (показывать фактическую комбинацию, путь отказа); macOS — разрешения Accessibility/Input Monitoring с подсказкой в UI.
- [ ] Полировка UI: анимации подсветки речи, тултипы, тихие звуки join/leave, трей-иконка с mute (штатный `app.SystemTray`).
- [ ] Настройки сохраняются (config.json), миграция версий конфига.
- [ ] `wails3 package`: NSIS (Win), .app+dmg (macOS), AppImage+deb (Linux). Артефакты в GitHub Releases по тегу.
- [ ] Дистрибуция: macOS — Apple Developer ID + notarization + stapling (заложить 99 USD/год и время) + NSMicrophoneUsageDescription + обработка отказа в микрофоне; Windows — WebView2 bootstrapper в NSIS, подпись или осознанно принятый SmartScreen; Linux — GTK4/WebKitGTK 6.0, база Ubuntu 24.04+.
- [ ] Прод-сервер: murmur v1.5.915 на VPS по deploy/murmur, docs/SERVER.md (порт 64738 tcp+udp, superuser, бэкап /data).
- **Готово, когда:** свежий человек скачивает установщик из Releases, подключается к прод-серверу и разговаривает; onboarding ≤ 2 минуты (с учётом системных диалогов разрешений).

### M5 — Качество звука 2.0 (по желанию, после реальных прогонов)
- [ ] Метрики в debug-оверлей: RTT, джиттер, глубина буфера, дропы, дрейф ppm, ERLE.
- [ ] Дробный ресемплер дрейфа (если drop/dup слышен).
- [ ] Deep PLC libopus за build-тегом (если TCP-берсты дают слышимые провалы; +1.75 МБ).
- [ ] Эксперименты с системными улучшениями: VoiceProcessingIO на macOS (переключатель, два известных дефекта — AGC Apple и BT+встроенный микрофон), Voice Clarity на Windows 11 (IAcousticEchoCancellationControl; при активации глушить свой AEC3).
- [ ] Пересмотр FastEnhancer/faster-enhancer.c (замена RNNoise, если C-рантайм дозрел — вотчлист от 2026-08).
- [ ] На Linux: переключатель «использовать PipeWire echo-cancel source» (свой AEC выключать).

### M6 — Мобильный эксперимент (отдельная ветка, без обещаний)
- [ ] Wails v3 mobile — экспериментальный и вне контракта совместимости беты. Android: системный VOICE_COMMUNICATION (AEC от ОС → свой AEC3 выключить), foregroundService. iOS по остаточному принципу. Архитектуру под мобилки не ломать.

---

## 8. Дев-стенд

```yaml
# deploy/murmur/docker-compose.yml
services:
  mumble:
    image: mumblevoip/mumble-server:v1.5.915
    restart: unless-stopped
    ports: ["64738:64738/tcp", "64738:64738/udp"]
    environment:
      MUMBLE_CONFIG_WELCOMETEXT: "Gul dev"
      MUMBLE_CONFIG_USERS: "64"
      MUMBLE_SUPERUSER_PASSWORD: "devsuperuser"   # иначе сгенерится и утонет в логах
    volumes:
      - mumble-data:/data        # владелец UID/GID 10000:10000
volumes: { mumble-data: {} }
```

Локально клиент ходит с dev-флагом insecure TLS (прод — только TOFU). Для отладки AEC — VoiceTargetLoopback (ID 31), второй участник не нужен.

Taskfile-задачи: `task dev` (wails3 dev) · `task murmur:up|down` · `task test` · `task lint` · `task package`.

## 9. CI (.github/workflows/ci.yml)

- Триггеры: PR и push в main; release-workflow по тегу `v*`.
- Матрица: ubuntu-24.04 / windows-2025 / macos-latest. Шаги: setup Go 1.26.7 + Node → Linux-зависимости Wails (GTK4/WebKitGTK 6.0; сверить с `wails3 doctor`) → `task lint` → `go test -race ./...` → `govulncheck` → build → (release) package + upload.
- cgo: всё из third_party статически, внешних dev-пакетов не требовать. APM+abseil — единый `-std=c++17`; per-GOOS дефайны (WEBRTC_WIN / WEBRTC_POSIX+WEBRTC_MAC / WEBRTC_POSIX+WEBRTC_LINUX, на arm64 + WEBRTC_HAS_NEON). Первая чистая сборка APM ~1650 .cc — обязателен кэш сборки Go; запасной ход: собирать статические .a через meson в CI и линковать.
- Аудио-тесты в CI — только без устройств (синтетический clock); device-тесты за build-тегом локально.
- Conformance Opus: энкод эталонного WAV → декод libopus и pion/opus (интероп-сетка без внешних зависимостей).

---

## 10. Правила работы для Claude Code

1. **Северная звезда — этот файл.** Перед задачей перечитай нужный раздел. Работай только в рамках текущего милстоуна; нужное вне рамок — `TODO(m<N>):` и дальше. Решения — в docs/DECISIONS.md.
2. **API не выдумывать.** Перед использованием форка gumble / miniaudio / APM / rnnoise / libopus / Wails — открыть исходники зависимости (документация Wails местами отстаёт от кода — верить v3/go.mod и исходникам) и сверить сигнатуры. Первичная верификация уже сделана в docs/research/ — при сомнениях начинать оттуда.
3. **Аудио-инварианты неприкосновенны:** сетка 48k/10ms/480; ДВА независимых устройства (duplex запрещён); realtime-callback — чистый C без Go/аллокаций/локов; DSP-состояния в одной горутине; порядок тракта §4.3–4.4 (APM → RNNoise → gate); типы по шагам (int16 на границах APM и Opus, float в шкале s16 для RNNoise); `AudioOutgoing()` и блокирующий приём запрещены. Изменение инварианта = отдельное обсуждение в PR, не молча.
4. **Тонкие сервисы.** В services/ — только маршалинг и делегирование в internal/core. Логика в сервисах запрещена.
5. **Каждый PR:** CI зелёный на 3 ОС, gofmt/vet/линтер чистые, `go test -race`, приложен короткий ручной сценарий проверки. Conventional commits.
6. **Зависимости** добавлять скупо, с обоснованием в PR. Новые C/C++-зависимости — только вендором в third_party с LICENSE и VERSION.
7. **Секреты и артефакты** (сертификаты, config.json, wav-записи, weights) — в .gitignore с первого коммита (weights_blob.bin — исключение, он в репо).
8. **Неоднозначность** — не гадать: консервативный вариант + `// DECISION:` + вопрос в PR.
9. **Лицензии:** файлы форка gumble править только в нашем публичном форке (MPL-2.0); NOTICE поддерживать актуальным; GPL-опции зависимостей запрещены, кроме отдельно документированного GCC runtime под Runtime Library Exception.
10. **Прогресс** отмечать чекбоксами в §7 в том же PR, что закрывает задачу.

## 11. Риски и запасные ходы

| Риск | План Б |
|---|---|
| Wails v3 beta: nightly-нумерация, даты 3.0 нет | Жёсткий пин beta.11; апгрейд отдельным PR по changelog. **Триггер отката:** нет RC к готовности M2 или сломан cgo на одной из ОС → Wails v2.14.0 (теряем GlobalShortcut → golang.design/x/hotkey) |
| Вендоринг APM+abseil тяжёл в сборке | Запасной: статические .a через meson в CI + `#cgo LDFLAGS` |
| AEC3 не справляется при экстремальном дрейфе | Дрейф-коррекция §4.2; крайний случай — режим «наушники/PTT» полнофункционален |
| RNNoise после APM даёт пересуппрессию | A/B в M3; конфиг APM-only (NS high) как запасной |
| TCP-задержка неприемлема на плохой сети | VPS ближе к пользователям; v2 — собственный UDP-путь (большая задача: OCB-AES128 + CryptSetup) |
| Форк gumble требует правок | Наш публичный форк уже в плане (MPL-2.0 соблюдён) |
| Wayland: глобальный PTT через портал может не дать нужную комбинацию | Показывать фактическую, PTT при фокусе окна как fallback |
| macOS: нотаризация/разрешения затягивают M4 | Заложено в M4 отдельным пунктом; тестировать на чистой машине |
| miniaudio 0.12 не выйдет / без дрейф-фикса | Мы на вендоренном 0.11.25 со своими патчами; на 0.12 не рассчитываем |

## 12. CLAUDE.md (выжимка — лежит в корне, поддерживать в синхроне)

Файл CLAUDE.md в корне — краткая выжимка этого плана (инварианты, команды, стек). При изменении инвариантов здесь — обновить и его в том же PR.

## 13. Стартовый промпт для M0

> Прочитай PLAN.md целиком и docs/DECISIONS.md. Мы делаем M0. Составь короткий план шагов M0 своими словами (5–8 пунктов), дождись моего «ок», затем выполняй: инициализируй Wails v3 проект по §2–3 (пин beta.11, react-шаблон, апгрейд фронтенда), подними дев-стенд murmur по §8, подключи форк gumble с выводом дерева каналов в лог, настрой CI по §9. По ходу соблюдай §10. В конце — инструкция, как запустить и проверить руками.
