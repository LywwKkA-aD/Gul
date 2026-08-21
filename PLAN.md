# PLAN.md — Голосовой клиент «как Дискорд» поверх Mumble

> Этот файл — главный документ проекта для Claude Code. Клади его в корень репозитория.
> Рядом лежит `CLAUDE.md` (краткая выжимка правил — черновик в конце этого файла).
> Язык общения в проекте — русский. Код, идентификаторы, коммиты — на английском.

---

## 0. Цель

Десктопное приложение для голосового общения и текстового чата в компании друзей (6–15 человек), с современным интерфейсом в духе Discord. Сервер — готовый **Mumble server (murmur)**, свой код — только клиент.

**Не-цели (v1):** видео, демонстрация экрана, мобильные платформы (эксперимент отложен в M6), федерация, свой сервер, история чата на сервере (Mumble её не хранит — принимаем как ограничение v1).

**Ключевые качества:** низкая задержка голоса, чистый звук (эхо- и шумоподавление с первого релиза), один бинарник на каждую ОС, ничего не требует установки зависимостей у пользователя.

---

## 1. Архитектура

```
┌─────────────────────────── Desktop app (Wails v3) ───────────────────────────┐
│                                                                              │
│  frontend/  React + TS + Tailwind          services/  (мост, тонкий слой)    │
│  каналы · чат · участники · настройки ◄──► Connection/Chat/Audio/Settings    │
│            Wails events ▲                      │ вызовы методов              │
│                         │                      ▼                             │
│  internal/core — состояние и оркестрация                                     │
│      │                              │                                        │
│      ▼                              ▼                                        │
│  internal/mumble (gumble)      internal/audio (malgo, движок)                │
│  TCP/TLS control + UDP voice   full-duplex устройство, пайплайн              │
│      │                              │                                        │
│      │                         internal/dsp: speexdsp (AEC/AGC), rnnoise     │
└──────┼───────────────────────────────────────────────────────────────────────┘
       ▼
  murmur (mumble-server) на VPS — готовый, ноль нашего кода
```

Принципы:
- **Весь звук и протокол — в Go.** Frontend никогда не трогает аудио и сокеты; он только рисует состояние и дергает методы сервисов.
- **UI ↔ Go** — только через сгенерированные Wails-биндинги (вызовы методов) и Wails events (push состояния в UI).
- **Одна временная сетка аудио:** 48 000 Гц, моно, кадр 10 мс = 480 сэмплов. Все звенья пайплайна работают на этой сетке — никаких ресемплеров внутри.

---

## 2. Технологический стек (точно)

| Слой | Выбор | Примечания |
|---|---|---|
| Оболочка | **Wails v3** (beta) | CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@<последняя beta>`. Версию зафиксировать в go.mod и README. Docs: https://v3.wails.io |
| Язык ядра | **Go** (последний стабильный, ≥1.23) | cgo обязателен (см. DSP) |
| Протокол Mumble | **layeh.com/gumble** (+ `gumbleutil`) | MPL-2.0. Использовать как зависимость, файлы библиотеки не форкать без нужды |
| Opus | **layeh.com/gumble/opus** | Регистрирует кодек в gumble (cgo). Проверить при сборке, нужен ли системный libopus; если да — добавить в CI и в доку сборки |
| Аудио I/O | **github.com/gen2brain/malgo** | Биндинги miniaudio, кроссплатформенно, без внешних зависимостей. Обязательно режим **full-duplex** (одно устройство, общий clock) |
| Эхоподавление + AGC | **speexdsp** (вендорим исходники C) | BSD. cgo-обёртка своя: `internal/dsp/speexdsp`. Используем `speex_echo_*` и `speex_preprocess_*` (AGC on, denoise OFF) |
| Шумоподавление + VAD | **RNNoise** (вендорим исходники C, репо xiph/rnnoise) | BSD. cgo-обёртка своя: `internal/dsp/rnnoise`. Кадр 480 сэмплов @48k — совпадает с нашей сеткой. `rnnoise_process_frame` возвращает вероятность речи → это же наш VAD |
| Frontend | **React 19 + TypeScript + Vite + Tailwind** | Состояние — zustand. Иконки — lucide-react |
| Сервер (не пишем) | **mumble-server (murmur)** | Официальный Docker-образ из репозитория mumble-voip/mumble-docker (актуальное имя образа/тег сверить в его README). В конфиг обязательно `opusthreshold=0` |
| CI | GitHub Actions | Матрица: windows-latest, macos-latest, ubuntu-latest. Артефакты: NSIS installer / .dmg / AppImage+deb через `wails3 package` |

Лицензии: gumble — MPL-2.0 (пофайловый copyleft: если правим её файлы, публикуем правки; лучше не править), rnnoise/speexdsp — BSD, miniaudio — public domain/MIT, Wails — MIT. Проект — MIT.

---

## 3. Структура репозитория

```
<app>/
├── PLAN.md                  # этот файл
├── CLAUDE.md                # правила для Claude Code (выжимка, см. §10)
├── go.mod / go.sum
├── main.go                  # wails: application, services, окно
├── Taskfile.yml             # генерится wails3, наши задачи добавлять сюда
├── build/                   # конфиги упаковки wails3
├── services/                # ТОНКИЕ Wails-сервисы: мост UI↔core, без логики
│   ├── connection.go        # Connect/Disconnect/State
│   ├── channels.go          # дерево каналов, Join(channelID)
│   ├── chat.go              # SendMessage, история сессии
│   ├── audio.go             # devices, mute/deafen, PTT state, уровни
│   └── settings.go          # load/save конфига
├── internal/
│   ├── core/                # состояние приложения, оркестрация, event bus
│   ├── mumble/              # обёртка над gumble: connect, listeners, streams
│   ├── audio/               # движок: duplex device, пайплайн, mixer, jitter
│   │   ├── engine.go        # запуск/остановка, callbacks malgo
│   │   ├── pipeline_tx.go   # mic → AEC → RNNoise → gate → int16 → gumble
│   │   ├── pipeline_rx.go   # gumble PCM → jitter → mixer → playback ring
│   │   ├── ring.go          # lock-free ring buffer (int16/float32)
│   │   └── levels.go        # RMS-метры для UI
│   ├── dsp/
│   │   ├── speexdsp/        # cgo: echo state, preprocess (AGC)
│   │   └── rnnoise/         # cgo: denoise + vad prob
│   └── config/              # ~/.config/<app>/config.json (или os-specific)
├── frontend/                # Vite + React + TS + Tailwind
│   └── src/
│       ├── app/             # экраны: Connect, Main
│       ├── components/      # ChannelTree, Chat, MemberList, BottomBar, SettingsModal
│       ├── state/           # zustand store, подписка на Wails events
│       └── bindings/        # сгенерированные wails-биндинги
├── third_party/
│   ├── rnnoise/             # вендоренные C-исходники + LICENSE
│   └── speexdsp/            # вендоренные C-исходники (только нужные файлы) + LICENSE
├── deploy/murmur/
│   ├── docker-compose.yml   # дев/прод сервер
│   └── mumble-server.ini    # opusthreshold=0, welcometext, users=64
└── .github/workflows/ci.yml
```

---

## 4. Аудиопайплайн — спецификация (сердце проекта)

### 4.1 Константы

```go
const (
    SampleRate   = 48000
    Channels     = 1
    FrameMs      = 10
    FrameSamples = SampleRate / 1000 * FrameMs // 480
    JitterMs     = 60                          // стартовый фиксированный джиттер-буфер
    AECTailMs    = 120                         // длина фильтра эха; тюнить 100–200
)
```

`gumble.Config.AudioInterval` выставить в те же 10 мс, чтобы кадры пайплайна 1:1 ложились в кадры протокола (сверить имя поля и дефолт по godoc gumble — не выдумывать).

### 4.2 Устройство

Открываем **одно full-duplex устройство malgo** (capture+playback в одном callback / с общим clock). Это обязательное условие корректной работы speex AEC: reference-сигнал (то, что реально ушло в динамик) и микрофонный кадр должны идти по одним часам. Раздельные устройства для v1 запрещены; выбор конкретных устройств (микрофон/выход) — через переинициализацию duplex-устройства с нужными ID.

### 4.3 Исходящий тракт (TX), на каждый кадр 10 мс

```
mic f32[480]
  → speex_echo_cancellation(mic, ref_from_playback, out)   // AEC, ссылка = сыгранные кадры
  → speex_preprocess_run(out)                              // ТОЛЬКО AGC (+residual echo); denoise выключен
  → rnnoise_process_frame(out) → vadProb                   // шумодав; возвращает вероятность речи
  → gate: PTT-режим (кадр идёт, пока зажата клавиша)
          или VAD-режим (vadProb > 0.6 c hangover 400 мс)
  → f32 → int16
  → в канал client.AudioOutgoing() gumble                   // gumble сам кодирует Opus и шлёт
```

Замечания:
- Порядок фиксирован: **AEC → AGC → RNNoise → gate**. Denoise в speex_preprocess выключить (`SPEEX_PREPROCESS_SET_DENOISE = off`), чтобы не давить сигнал дважды.
- RNNoise ожидает float-кадры 480 сэмплов @48k; учесть масштаб (библиотека исторически работает с диапазоном s16 в float — проверить по README и юнит-тесту на синусе).
- В mute — кадры не отправляем вовсе (а не шлём тишину).

### 4.4 Входящий тракт (RX)

```
gumble AudioStream (декодированный PCM int16 на каждого говорящего)
  → per-user jitter buffer (фиксированный, JitterMs; интерфейс позволяет заменить на адаптивный)
  → mixer: сумма float32 всех активных + soft-clip (tanh или простой limiter)
  → playback ring buffer
  → playback-часть duplex callback: отдаёт кадр в динамик И кладёт его копию
    в AEC reference ring (это тот самый ref для speex_echo_cancellation)
```

Замечания:
- Входящий звук через gumble: `Config.AttachAudio(...)` / события аудиопотоков — точные сигнатуры взять из godoc gumble, не по памяти.
- Per-user громкость (множитель) и локальный mute применяются в миксере.
- `deafen` = не играем никого и (по протоколу Mumble) выставляем self-deaf.

### 4.5 Правила реализации аудио-кода

- **Ноль аллокаций и ноль блокировок в audio callback.** Все буферы преаллоцированы; обмен между callback и горутинами — через lock-free ring / каналы с достаточным буфером и политикой drop-oldest.
- DSP-состояния (echo state, preprocess state, rnnoise state) живут в одной аудио-горутине; из других горутин к ним не прикасаться.
- Метры уровня (RMS вход/выход) считаются в пайплайне и раз в ~50 мс публикуются в UI событием.
- Все cgo-обёртки — с `runtime.Pinner`/копированием по необходимости, финализаторы не использовать: явные `Close()`.
- Golden-тесты DSP: прогнать WAV-фикстуры (речь+шум, речь+эхо) через обёртки и сравнить метрики (снижение RMS шума, ERLE для AEC) с порогами. Фикстуры маленькие, класть в testdata/.

---

## 5. Слой Mumble (internal/mumble)

- Подключение: адрес, ник, пароль (опц.), TLS. В dev допускаем self-signed через флаг конфига `insecure_tls=true` (в UI — явный чекбокс «доверять сертификату», по умолчанию off).
- Клиентский сертификат (идентификация юзера в Mumble): сгенерировать при первом запуске, хранить в конфиг-папке, подсовывать в gumble.Dial. Это даёт стабильную «учётку» без регистрации.
- Слушатели gumble → внутренние события core: ConnectionState, ChannelTree, UserJoined/Left/Moved, TalkingChanged, TextMessage, PermissionDenied.
- Реконнект: экспоненциальный backoff (1s→30s cap), восстановление канала, в UI — статус «переподключение…».
- Текст: Mumble передаёт HTML — на выходе в UI санитизировать (белый список: b/i/u/a/br), на входе отправлять plain text с экранированием.

## 6. Frontend (frontend/)

Экраны:
1. **Connect** — адрес сервера, ник, кнопка Connect, чекбокс trust-cert, последние серверы.
2. **Main** — трёхколоночная раскладка:
   - слева: имя сервера + дерево каналов, у каждого голосового участника — аватар-кружок и подсветка при речи;
   - центр: чат текущего канала (история сессии), инпут снизу;
   - справа (опц., скрываемая): список участников канала;
   - снизу слева: карточка себя + кнопки mic-mute / deafen / settings (как в Discord).
3. **Settings (модал)** — вкладки: Audio (устройства, режим PTT/VAD, порог VAD, тест микрофона с живым метром, ползунок выходной громкости), Hotkeys (PTT-клавиша), About.

Дизайн: тёмная тема по умолчанию, палитра своя (НЕ копировать дискордовскую 1-в-1), скругления 8px, плотная типографика, анимации только на подсветку речи и hover. Все состояния (говорит/мьют/деф) — иконками на аватаре.

Состояние: zustand-store, наполняется исключительно Wails-событиями (`connection:state`, `channels:tree`, `user:talking`, `chat:message`, `audio:levels`, `audio:devices`). Методы сервисов вызываются из компонентов через сгенерированные биндинги. Никакого polling.

---

## 7. Милстоуны

Работать строго по порядку. Каждый милстоун = отдельная ветка `m<N>-<slug>` и PR. Вперёд не забегать.

### M0 — Каркас и стенд (цель: «пустое приложение видит сервер»)
- [ ] `wails3 init` (шаблон react-ts), зафиксировать версии Wails/Go/Node в README.
- [ ] `deploy/murmur/docker-compose.yml` + `mumble-server.ini` (`opusthreshold=0`); `task murmur:up`.
- [ ] Подключить gumble: Connect-экран → соединение → в логах дерево каналов и события.
- [ ] CI: build-матрица 3 ОС (пока без упаковки), gofmt+vet+golangci-lint, frontend lint+build.
- **Готово, когда:** приложение собирается на 3 ОС в CI; локально коннектится к докер-murmur и печатает каналы/юзеров.

### M1 — Текст и навигация (цель: «полноценный текстовый клиент»)
- [ ] Дерево каналов в UI (реальное, с юзерами), Join по клику/даблклику.
- [ ] Чат: приём/отправка, история сессии, автоскролл, санитизация HTML.
- [ ] Статусы соединения + автореконнект с восстановлением канала.
- [ ] Клиентский сертификат: генерация и переиспользование.
- **Готово, когда:** два инстанса приложения на одной машине переписываются и ходят по каналам; убийство сервера → реконнект без рестарта клиента.

### M2 — Голос без DSP (цель: «слышим друг друга»)
- [ ] malgo full-duplex устройство, сетка 48k/10ms; выбор устройств в Settings.
- [ ] TX: mic → int16 → AudioOutgoing (без AEC/шумодава), mute.
- [ ] RX: gumble streams → фиксированный джиттер 60 мс → миксер → playback; deafen; per-user volume.
- [ ] Индикация речи (событие talking из gumble) в дереве каналов.
- [ ] RMS-метры → Settings «тест микрофона».
- **Готово, когда:** два клиента разговаривают через докер-murmur; наушники обязательны (эха ещё нет); CPU в разговоре < ~5% на среднем ноуте.

### M3 — DSP: AEC + шумодав + VAD (цель: «звук как у больших»)
- [ ] Вендор speexdsp + cgo-обёртка: `EchoState` (frame 480, tail из AECTailMs), `Preprocess` (AGC on, denoise off, residual echo attach).
- [ ] Reference-тракт: playback-кадры → AEC ref ring; выравнивание задержки.
- [ ] Вендор rnnoise + cgo-обёртка; включить в TX после AEC; вернуть vadProb.
- [ ] Режимы Voice activity (порог+hangover, настройка в UI) и Push-to-talk (пока клавиша при фокусе окна).
- [ ] Golden-тесты DSP на WAV-фикстурах; бенчмарк кадра (цель: обработка 10 мс кадра ≪ 10 мс).
- **Готово, когда:** разговор на колонках без наушников не даёт слышимого эха собеседнику; шум клавиатуры/фона ощутимо подавлен; VAD не режет начала слов.

### M4 — UX и упаковка (цель: «можно раздать друзьям»)
- [ ] Глобальный PTT-хоткей вне фокуса окна (кандидат: golang.design/x/hotkey; изучить поддержку в Wails v3 — если есть штатный механизм, взять его; на macOS учесть разрешение Accessibility/Input Monitoring).
- [ ] Полировка UI: анимация подсветки речи, тултипы, звуки join/leave (тихие), трей-иконка с mute.
- [ ] Настройки сохраняются (config.json), миграция версий конфига.
- [ ] `wails3 package`: NSIS (Win), .app+dmg (macOS), AppImage+deb (Linux). Артефакты в GitHub Releases по тегу.
- [ ] Прод-сервер: поднять murmur на VPS по deploy/murmur, задокументировать в docs/SERVER.md (порт 64738 tcp+udp, superuser, бэкап ini+sqlite murmur).
- **Готово, когда:** свежий человек скачивает установщик из Releases, подключается к прод-серверу и разговаривает; onboarding ≤ 2 минуты.

### M5 — Качество звука 2.0 (по желанию, после реальных прогонов)
- [ ] Адаптивный джиттер-буфер (или speex_jitter) вместо фиксированного.
- [ ] Автоопределение задержки AEC ref (корреляция) вместо ручной константы.
- [ ] Метрики в debug-оверлей: RTT, потери, размер джиттера, ERLE.

### M6 — Мобильный эксперимент (отдельная ветка, без обещаний)
- [ ] `wails3` сборка Android: микрофон из Go-части, системный VOICE_COMMUNICATION (AEC от ОС → speex на мобилках выключить), foregroundService для фона.
- [ ] iOS по остаточному принципу. Статус мобильной поддержки Wails — experimental; любые падения фиксировать issue в апстрим, архитектуру под мобилки не ломать.

---

## 8. Дев-стенд

```yaml
# deploy/murmur/docker-compose.yml — скелет; имя образа/тег СВЕРИТЬ с README mumble-voip/mumble-docker
services:
  murmur:
    image: <official-mumble-server-image>
    restart: unless-stopped
    ports: ["64738:64738/tcp", "64738:64738/udp"]
    volumes:
      - ./mumble-server.ini:/etc/mumble/mumble-server.ini:ro   # путь сверить с README образа
      - murmur-data:/data
volumes: { murmur-data: {} }
```

`mumble-server.ini` минимум: `opusthreshold=0`, `users=64`, `welcometext`, `serverpassword` (для прод-VPS). Локально клиент ходит с `insecure_tls=true`.

Taskfile-задачи: `task dev` (wails3 dev), `task murmur:up|down`, `task test`, `task lint`, `task package`.

## 9. CI (.github/workflows/ci.yml)

- Триггеры: PR и push в main; release-workflow по тегу `v*`.
- Матрица: ubuntu / windows / macos. Шаги: setup Go+Node → системные зависимости Linux для Wails (GTK/WebKit по докам v3; список взять из `wails3 doctor`) → `task lint` → `go test ./...` → build → (в release) `wails3 package` + upload artifacts.
- cgo: rnnoise/speexdsp собираются из third_party статически — внешних dev-пакетов не требовать; если libopus всё же нужен системный — поставить в CI и написать это в README «Сборка из исходников».

---

## 10. Правила работы для Claude Code

1. **Северная звезда — этот файл.** Перед задачей перечитай нужный раздел. Работай только в рамках текущего милстоуна; заметил нужное вне рамок — оставь `TODO(m<N>):` и иди дальше.
2. **API не выдумывать.** Перед использованием gumble / malgo / speexdsp / rnnoise / Wails открыть godoc, README или исходники зависимости и сверить сигнатуры. Особенно: поля gumble.Config, способ отдачи исходящего звука, события входящих аудиопотоков, duplex-режим malgo.
3. **Аудио-инварианты неприкосновенны:** сетка 48k/10ms/480, один duplex-девайс, порядок AEC→AGC→RNNoise→gate, ноль аллокаций/блокировок в callback. Изменение инварианта = отдельное обсуждение в PR, не молча.
4. **Тонкие сервисы.** В services/ — только маршалинг и делегирование в internal/core. Логика в сервисах запрещена.
5. **Каждый PR:** собирается на 3 ОС (CI зелёный), `gofmt`/`go vet`/линтер чистые, приложен короткий ручной сценарий проверки («запусти два клиента, …»). Conventional commits (`feat:`, `fix:`, `chore:`…).
6. **Зависимости** добавлять скупо и с обоснованием в PR. Electron-подобное, дублирующие UI-киты, тяжёлые state-менеджеры — нет.
7. **Секреты и артефакты** (сертификаты, config.json, wav-записи отладки) — в .gitignore с первого коммита.
8. **Неоднозначность** — не гадать: выбери консервативный вариант, пометь `// DECISION:` комментарием и вынеси вопрос в описание PR.
9. **Лицензии:** файлы gumble не модифицировать (MPL-2.0); вендоренные rnnoise/speexdsp — с их LICENSE в third_party/.
10. **Прогресс** отмечать чекбоксами в §7 этого файла в том же PR, что закрывает задачу.

## 11. Риски и запасные ходы

| Риск | План Б |
|---|---|
| Wails v3 beta ломает API между релизами | Версия запинена; апгрейд — отдельный PR по changelog |
| malgo duplex капризит на конкретной ОС | Изолировано в internal/audio/engine.go; fallback — раздельные устройства + автоопределение задержки ref (M5) раньше срока |
| speex AEC не сходится (дрейф часов) | Проверить, что реально один duplex-девайс; увеличить tail; крайний случай — режим «наушники/PTT» остаётся полнофункциональным |
| gumble давно без релизов | Протокол стабилен; при баге — точечный vendor-патч отдельным коммитом с пометкой лицензии |
| Глобальные хоткеи на macOS требуют разрешений | UI-подсказка с инструкцией; PTT при фокусе окна как fallback |

## 12. CLAUDE.md (черновик — положить в корень)

```markdown
# CLAUDE.md
Проект: десктопный голосовой клиент поверх Mumble. Wails v3 + Go core + React/TS.
Главный документ: PLAN.md (архитектура §1–6, текущий милстоун §7, правила §10).

Команды: task dev · task murmur:up · task test · task lint · task package
Стенд: локальный murmur в докере, клиент с insecure_tls=true.

Жёсткие правила:
- Аудио-сетка 48k/10ms/480; один full-duplex девайс; порядок AEC→AGC→RNNoise→gate.
- Ноль аллокаций/блокировок в аудио-callback.
- services/ — тонкие; логика в internal/. UI не трогает звук и сеть.
- Сигнатуры внешних API сверять с godoc/исходниками, не по памяти.
- Работать в рамках текущего милстоуна PLAN.md §7; коммиты — conventional.
```

## 13. Стартовый промпт для Claude Code (первое сообщение в пустом репо)

> Прочитай PLAN.md целиком. Мы делаем M0. Составь короткий план шагов M0 своими словами (5–8 пунктов), дождись моего «ок», затем выполняй: инициализируй Wails v3 проект по §2–3, подними дев-стенд murmur по §8, реализуй подключение gumble с выводом дерева каналов в лог, настрой CI по §9. По ходу соблюдай §10. В конце — инструкция, как мне это запустить и проверить руками.

Справка по самому Claude Code (установка, настройка, hooks): https://docs.claude.com/en/docs/claude-code/overview
