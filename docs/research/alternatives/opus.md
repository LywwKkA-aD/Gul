# Opus-кодек: gopus против вендоренного libopus 1.6.1

## Вердикт: ЗАМЕНИТЬ

Заменить layeh.com/gumble/opus + gopus на собственный пакет: вендоренный libopus 1.6.1 (подмножество include/celt/silk/src, 137 .c через cgo-stubs) плюс ~60-строчный адаптер под gumble.AudioCodec с gumble.RegisterAudioCodec(4, codec). Интерфейсы gumble тривиальны (ID/NewEncoder/NewDecoder, Encode(pcm []int16, mframeSize, maxDataBytes int) ([]byte, error), Decode(data []byte, frameSize int) ([]int16, error)), так что стоимость адаптера — один вечер, а проверенная стоимость вендоринга — 4.1 МБ исходников и +443 КБ бинарника. Собирать float-сборку БЕЗ DRED/OSCE/BWE и без интринсик-RTCD: измеренная плата за отсутствие SIMD — 8-10% CPU, а весь энкод и так занимает ~1% ядра. hraban/opus и pion/opus не подходят: первый не обновлялся с сентября 2023 и завязан на pkg-config, второй (v0.1.0, июнь 2026) — только декодер, публичного энкодера в релизе нет.

## Резюме

Текущая связка layeh.com/gumble/opus → gopus безнадёжно устарела: вендорится libopus 1.1.2 (2015) под сборочными тегами `amd64,cgo 386,cgo`, а форк github.com/stieneee/gopus, от которого реально зависит stieneee/gumble, вообще выкинул вендоренные исходники и требует системный libopus через pkg-config на всех платформах — это прямо противоречит цели «один самодостаточный бинарник». Актуальная версия на 2026-08-21 — libopus 1.6.1 (14 января 2026), BSD-3 + royalty-free патентная лицензия. Я эмпирически проверил вендоринг: для float-сборки без DNN нужно 137 .c-файлов (celt+silk+src+include = 4.1 МБ, 307 файлов, коллизий имён нет), они собираются одним `go build` через 137 однострочных stub-.c и рукописный config.h из 9 строк — холодная сборка 2.3 с, прирост бинарника +443 КБ (stripped), кросс-компиляция darwin/amd64 с arm64 прошла. CPU на кадре 10 мс (M4, mono 48k, VOIP 40 kbps, complexity 10): энкод 88 мкс (0.88% ядра), декод 8 мкс (0.08%) — 14 входящих потоков это ~1.1% одного ядра; чистый C без SIMD медленнее всего на 8-10%. Новые ML-фичи (DRED, deep PLC, OSCE/BWE) выключены по умолчанию, требуют float-сборки и добавляют +5 МБ к статической библиотеке; главное — DRED и in-band FEC бесполезны, потому что голос у нас идёт по TCP-туннелю gumble, где потерь пакетов нет, есть только задержка и head-of-line blocking. Официальный клиент Mumble при этом вообще перестал бандлить opus (в 3rdparty нет opus-src) и линкуется с системным libopus, то есть на современных дистрибутивах он уже получает 1.5/1.6.

## Находки

### [CRITICAL] gopus вендорит libopus 1.1.2 (2015) и только под amd64/386 — подтверждено в исходниках

В layeh.com/gopus файл opus_nonshared.go начинается с `// +build amd64,cgo 386,cgo` и через cgo-преамбулу инклюдит ~130 .c-файлов из каталога opus-1.1.2. Файл opus_shared.go имеет тег `// +build !amd64,!386,cgo` и директиву `#cgo !nopkgconfig pkg-config: opus`, то есть на arm64 (Apple Silicon, ARM-ноутбуки, Linux arm64) вендоринг не работает вовсе и нужен системный libopus. Последняя версия модуля layeh.com/gopus в proxy.golang.org — v0.0.0-20210501142526 (1 мая 2021), лицензия Public domain (Unlicense). Разрыв с текущим libopus 1.6.1 — примерно 11 лет разработки: за это время добавились улучшения SILK/CELT, ARM NEON/DOTPROD и x86 AVX2 интринсики, ML-фичи 1.5/1.6.

Источники: <https://github.com/layeh/gopus> · <https://github.com/layeh/gopus/blob/master/opus_nonshared.go> · <https://proxy.golang.org/layeh.com/gopus/@latest>

### [CRITICAL] Форк stieneee/gopus вообще удалил вендоренный libopus — нужен системный на всех платформах

github.com/stieneee/gumble в go.mod требует github.com/stieneee/gopus v0.0.0-20210424193312-6d10f6090335. В HEAD этого форка два последних коммита называются «experiment with only shared library» и «experiement with only shared library», после чего в репозитории остался единственный Go-файл opus_shared.go с `#cgo !nopkgconfig pkg-config: opus` — каталога opus-1.1.2 и opus_nonshared.go больше нет. То есть выбранный в плане путь (stieneee/gumble + его пакет opus) требует libopus-dev на сборочной машине и корректной линковки на Windows/macOS/Linux, что ломает цель «один самодостаточный бинарник» ещё до вопроса о версии кодека. Обойти можно тегом `nopkgconfig` и ручными CGO_CFLAGS/CGO_LDFLAGS, но это уже полработы собственного пакета.

Источники: <https://github.com/stieneee/gumble/blob/master/go.mod> · <https://github.com/Stieneee/gopus>

### [MAJOR] DRED и in-band FEC для нас бесполезны: голос идёт по TCP, потерь пакетов нет

DRED (Deep REDundancy) восстанавливает утраченные пакеты, кодируя избыточность прошлого аудио в padding текущего пакета. У нас voice туннелируется по TCP через gumble, где потерь нет по определению — есть задержка и head-of-line blocking. Дополнительно DRED непрозрачен в API: нужен отдельный OpusDREDDecoder (opus_dred_decoder_create, opus_dred_alloc, opus_dred_parse, opus_dred_process, opus_decoder_dred_decode), то есть переписывание всего приёмного тракта, а не один CTL. По замерам на DNN-сборке OPUS_SET_DRED_DURATION(100) добавляет к энкоду ~13 мкс/кадр (109.2 → 122.1 мкс), и при CBR он вообще не попадает в поток — DRED требует запаса битрейта. Официальный клиент Mumble DRED и decode_fec тоже не использует: он вызывает opus_decode_float(..., 0) и делает PLC передачей nullptr.

Источники: <https://github.com/xiph/opus/blob/main/include/opus.h> · <https://github.com/mumble-voip/mumble/blob/master/src/mumble/AudioOutputSpeech.cpp> · <https://opus-codec.org/release/stable/2025/12/15/libopus-1_6.html>

### [MAJOR] pion/opus v0.1.0 — чистый Go, но только декодер; энкодера в релизе нет

Модуль github.com/pion/opus, последняя версия v0.1.0 от 8 июня 2026, MIT. В корне модуля нет encoder.go — только decoder.go, errors.go, table_of_contents_header.go; CELT-энкодер есть, но лежит в internal/celt/encoder.go и не импортируется извне, SILK-энкодера нет вообще. Роадмап (issue #9) до сих пор перечисляет «SILK Encoder / CELT Encoder» как планы v0.0.2. Декодер при этом рабочий: я прогнал интероп-тест (пакеты кодировались вендоренным libopus 1.6.1, декодировались pion) — VOIP 24k, VOIP 40k, AUDIO 64k, LOWDELAY 96k, по 500 кадров 10 мс моно, 500/500 успешных декодов без ошибок в каждом режиме, стоимость 28.4 / 18.7 / 11.0 / 11.0 мкс на кадр, то есть в 2-3 раза медленнее libopus, но абсолютно приемлемо. Как замена gopus не годится: без энкодера в Mumble не поговоришь, а тянуть cgo ради энкода и чистый Go ради декода — худшее из двух миров.

Источники: <https://github.com/pion/opus> · <https://github.com/pion/opus/issues/9> · <https://proxy.golang.org/github.com/pion/opus/@latest>

### [MAJOR] gopkg.in/hraban/opus.v2 — тонкая MIT-обёртка, но без релизов с сентября 2023 и на pkg-config

Канонический путь модуля — gopkg.in/hraban/opus.v2 (github.com/hraban/opus/v2 в proxy не резолвится). Последняя версия в proxy.golang.org: v2.0.0-20230925203106-0188a62cb302, 25 сентября 2023 — почти три года без релизов на 2026-08-21. cgo-преамбула в opus.go: `#cgo pkg-config: opus` + `#include <opus.h>`, то есть по умолчанию динамическая линковка с системным libopus и libopusfile; README упоминает build-тег nolibopusfile для статики без файлового API. Слинковать со своим вендоренным статическим libopus технически можно (CGO_CFLAGS/CGO_LDFLAGS или замена директив в форке), но это уже форк, и тогда выгоднее написать свои 120 строк адаптера и владеть версионированием. Лицензия MIT — совместима.

Источники: <https://github.com/hraban/opus> · <https://proxy.golang.org/gopkg.in/hraban/opus.v2/@latest>

### [MAJOR] Настройки энкодера у официального Mumble: три application-режима и CBR, а gumble их игнорирует

Mumble в src/mumble/AudioInput.cpp выбирает режим по целевому битрейту: opus_encoder_create(SAMPLE_RATE, 1, OPUS_APPLICATION_RESTRICTED_LOWDELAY) при >=64 кбит/с, OPUS_APPLICATION_AUDIO при >=32 кбит/с, иначе OPUS_APPLICATION_VOIP; ставит opus_encoder_ctl(OPUS_SET_VBR(0)) (CBR) и OPUS_SET_BITRATE(iAudioQuality) из настроек/лимита сервера, кодирует opus_encode на iFramesPerPacket * (SAMPLE_RATE/100). Декодер: opus_decoder_create(48000), opus_decode_float(..., decode_fec=0), PLC через nullptr. Адаптер gumble делает грубее: gopus.NewEncoder(48000, 1, gopus.Voip) + SetBitrate(BitrateMaximum), а фактический битрейт ограничивается сверху параметром maxDataBytes = Config.AudioDataBytes, у которого дефолт AudioDefaultDataBytes = 40 байт на 10 мс = ровно 32 кбит/с. При этом gumble принимает MaxBandwidth от сервера и просто кладёт его в ServerConfigEvent.MaximumBitrate — на энкодер не применяет (handlers.go строки 341-343 и 1294-1296). Свой кодек это чинит: OPUS_SET_BITRATE из MaximumBitrate, режим по битрейту как у Mumble, CBR/CVBR для ровного потока в TCP-туннеле.

Источники: <https://github.com/mumble-voip/mumble/blob/master/src/mumble/AudioInput.cpp> · <https://github.com/mumble-voip/mumble/blob/master/src/mumble/AudioOutputSpeech.cpp> · <https://github.com/stieneee/gumble/blob/master/gumble/audio.go> · <https://github.com/stieneee/gumble/blob/master/opus/opus.go>

### [minor] ML-фичи (DRED/deep PLC/OSCE/BWE) выключены по умолчанию, требуют float и стоят +5 МБ

В CMakeLists.txt libopus 1.6.1 опции OPUS_DRED и OPUS_OSCE объявлены с default OFF; в configure.ac enable_dred, enable_deep_plc, enable_osce по умолчанию no. Более того, configure.ac явно запрещает сочетание: «--enable-fixed-point cannot be used with --deep-plc, --enable-dred, and --enable-osce» — то есть нейросетевые фичи только во float-сборке. Измеренные размеры .a на arm64: базовая float без DNN — 1.87 МБ; +deep PLC — 3.63 МБ; +DRED +deep PLC +OSCE/BWE — 6.83 МБ. Основная масса — блобы весов в dnn/: bbwenet_data.c 9.6 МБ исходника, nolace_data.c 4.5 МБ, fargan_data.c 2.9 МБ, lace_data.c 1.7 МБ, plc_data.c 1.7 МБ, dred_rdovae_dec_data.c 1.2 МБ (весь каталог dnn/ — 22 МБ). Активация на стороне декодера через OPUS_SET_COMPLEXITY: в src/opus_decoder.c deep PLC включается при complexity>=5, OSCE LACE при >=6, NoLACE при >=7, BWE при >=4 плюс OPUS_SET_OSCE_BWE(1); дефолт complexity декодера = 0, то есть всё выключено. Есть также OPUS_SET_DNN_BLOB для подгрузки весов извне бинарника.

Источники: <https://github.com/xiph/opus/blob/main/CMakeLists.txt> · <https://github.com/xiph/opus/blob/main/configure.ac> · <https://github.com/xiph/opus/blob/main/src/opus_decoder.c> · <https://github.com/xiph/opus/blob/main/include/opus_defines.h>

### [minor] Deep PLC — единственная ML-фича с реальным смыслом у нас, но дорогая на кадр потери

Хотя потерь в TCP нет, jitter-буфер клиента сам роняет опоздавшие кадры (head-of-line blocking на TCP как раз создаёт всплески), и тогда работает concealment. Замеры на сборке с --enable-deep-plc (libopus.a = 3.63 МБ, +1.75 МБ к базовой): нормальный декод при decoder complexity=0 — 6.7 мкс/кадр, при complexity=5 — 6.8 мкс (накладных расходов на неутраченных кадрах практически нет); OSCE LACE (cplx=6) — 7.0 мкс, NoLACE (cplx=7) — 7.7 мкс, NoLACE+BWE — 7.6 мкс. Но на прогоне с 5% concealed-кадров средняя стоимость декода прыгает с 6.9 мкс (классический PLC) до 27.2 мкс (deep PLC), то есть каждый скрываемый кадр обходится примерно в 400 мкс — при пачке подряд идущих потерь на слабом CPU это заметно. Вывод: в первый релиз не включать, но оставить как флаг сборки — включение стоит одного OPUS_SET_COMPLEXITY на декодере.

Источники: <https://github.com/xiph/opus/blob/main/src/opus_decoder.c> · <https://github.com/xiph/opus/blob/main/lpcnet_sources.mk>

### [minor] tphakala/go-opus — свежий чистый Go-порт libopus 1.6.1, но энкодер только CELT и репозиторий младенческий

github.com/tphakala/go-opus: v1.0.0 в proxy.golang.org помечен временем 2026-08-21T10:39:11Z, то есть тег поставлен буквально сегодня; на GitHub 2 звезды, 1 watcher, 1 форк, 119 коммитов. Лицензия BSD-3 (как у libopus). Заявлено: декодер проходит все 12 conformance-векторов RFC 6716 (CELT, SILK, hybrid, PLC, FEC/LBRR), fixed-point энкодер даёт байт-идентичные libopus пакеты, скорость 1.6-2.0x от C на энкоде и 1.4-1.5x на декоде, ноль аллокаций на кадр. Блокирующее ограничение: энкодер CELT-only by design, «SILK-only and hybrid encoding are not on the roadmap for now». Для VoIP-речи на 24-40 kbps это принципиально худший режим, чем SILK/hybrid, которые Opus выбирает в OPUS_APPLICATION_VOIP. Плюс социальная незрелость: один автор, тег v1.0.0 в день первого знакомства. Отслеживать стоит, использовать сейчас — нет.

Источники: <https://github.com/tphakala/go-opus> · <https://proxy.golang.org/github.com/tphakala/go-opus/@latest>

### [minor] Ограничение cgo: пофайловые флаги для x86 SIMD недоступны, RTCD придётся отключить

libopus раскладывает интринсики по отдельным группам исходников с разными флагами компилятора: CELT_SOURCES_SSE/SSE2/SSE4_1/AVX2, SILK_SOURCES_SSE4_1/AVX2, SILK_SOURCES_FIXED_SSE4_1, DNN_SOURCES_AVX2 и т.д., плюс run-time CPU detection. cgo применяет единый CFLAGS ко всем .c файлам пакета, поэтому собрать AVX2-вариант рядом с базовым внутри одного пакета нельзя (а глобальный -mavx2 сделает бинарник несовместимым со старыми CPU). Практичное решение — собирать чистый C (эквивалент --disable-intrinsics --disable-rtcd): измеренная плата 8-10% CPU на энкоде при абсолютной величине ~1% ядра. Если когда-нибудь понадобится, SSE4.1/AVX2-файлы можно вынести в отдельные Go-подпакеты со своими #cgo CFLAGS и build-тегами на GOARCH — символы C линкуются между пакетами, — но это оптимизация без повода. Отдельно: cgo убивает кросс-компиляцию из коробки, для трёх ОС нужны либо нативные CI-раннеры, либо zig cc как CC; но проект уже вендорит speexdsp и RNNoise через cgo, так что этот счёт уже оплачен и libopus не добавляет к нему ничего.

Источники: <https://github.com/xiph/opus/blob/main/celt_sources.mk> · <https://github.com/xiph/opus/blob/main/silk_sources.mk> · <https://github.com/xiph/opus/blob/main/CMakeLists.txt>

### [info] Актуальный libopus на 2026 — 1.6.1 (14 января 2026); BSD-3 + royalty-free патенты

Хронология: 1.5 (машинное обучение в энкодере и декодере: Deep PLC, DRED, амбисоника 4/5 порядка), 1.6 от 15 декабря 2025 (нейросетевой wideband→fullband bandwidth extension BWE, представленный на WASPAA 2025; Opus HD с поддержкой 96 кГц; существенное улучшение DRED; новый 24-битный API энкодера/декодера; улучшения fixed-point), 1.6.1 от 14 января 2026 — мелкие исправления после 1.6. Файл COPYING в тарболе — трёхпунктная BSD (Xiph.Org, Skype, Octasic, Mozilla, Amazon и др.) плюс явная royalty-free патентная лицензия; с MIT совместимо полностью. Из новых фич для нас релевантны нулевые: 96 кГц и 24-битный API не нужны при протоколе Mumble с int16/48 кГц моно.

Источники: <https://opus-codec.org/release/stable/2025/12/15/libopus-1_6.html> · <https://opus-codec.org/release/stable/2026/01/14/libopus-1_6_1.html> · <https://github.com/xiph/opus/releases> · <https://downloads.xiph.org/releases/opus/opus-1.6.1.tar.gz>

### [info] Вендоринг libopus 1.6.1 через cgo проверен практикой: работает, 137 файлов, +443 КБ к бинарнику

Я собрал рабочий Go-пакет в scratchpad/cgotest. Метод: скопировать из тарбола только include/, celt/, silk/, src/ (после удаления dnn/doc/tests/m4/cmake/meson остаётся 4.1 МБ, 307 файлов: 195 .c и 102 .h), сгенерировать в каталоге Go-пакета по одному stub-файлу `z_<путь>.c` вида `#include "../third_party/opus/celt/bands.c"` для каждого из 137 исходников float-сборки (11 OPUS_SOURCES + 3 OPUS_SOURCES_FLOAT + 18 CELT_SOURCES + 77 SILK_SOURCES + 28 SILK_SOURCES_FLOAT), положить рукописный config.h из 9 строк (OPUS_BUILD, PACKAGE_VERSION, VAR_ARRAYS, HAVE_LRINTF, HAVE_LRINT, FLOAT_APPROX, ENABLE_HARDENING) и одну cgo-директиву с -I на include/celt/silk/silk/float/src. Коллизий базовых имён между celt/silk/src нет (проверено), поэтому плоская генерация безопасна. Результат: `go build` без cmake/autotools, холодная сборка 2.3 с на M4, opus_get_version_string() возвращает «libopus 1.6.1-gul», энкод/декод работают. Размер бинарника: 3 432 402 vs 2 492 514 байт у пустого Go-бинарника (+940 КБ), со `-ldflags=-s -w` — 2 101 058 vs 1 657 858 (+443 КБ). Кросс-компиляция GOARCH=amd64 с CGO_CFLAGS="-arch x86_64" на darwin/arm64 отработала.

Источники: <https://downloads.xiph.org/releases/opus/opus-1.6.1.tar.gz> · <https://github.com/xiph/opus/blob/main/opus_sources.mk> · <https://github.com/xiph/opus/blob/main/silk_sources.mk> · <https://github.com/xiph/opus/blob/main/celt_sources.mk>

### [info] CPU на кадр 10 мс: энкод ~0.9-1.2% ядра, декод ~0.08%; без SIMD потери всего 8-10%

Замеры на Apple M4, mono 48 кГц, кадр 480 сэмплов, libopus 1.6.1 статически, 6000 кадров на конфигурацию. Сборка с NEON+DOTPROD: VOIP 24 kbps cplx10 — энкод 112.3 мкс/кадр (1.12% от 10 мс), декод 6.9 мкс (0.07%); VOIP 40 kbps cplx10 — энкод 88.1 мкс, декод 8.0 мкс; VOIP 40 kbps cplx5 — энкод 51.3 мкс; VOIP 40k + in-band FEC при packet_loss_perc=20 — энкод 141.5 мкс; AUDIO 64 kbps — энкод 23.8 мкс; RESTRICTED_LOWDELAY 96 kbps — энкод 25.9 мкс. Сборка --disable-intrinsics --disable-rtcd (чистый C): те же сценарии дают 121.5 / 96.4 / 54.3 / 152.6 / 27.9 / 30.1 мкс, то есть регрессия 8-10%. Практический вывод для компании 15 человек: 14 входящих потоков декодируются за ~110 мкс на каждые 10 мс = ~1.1% одного ядра, энкод — ещё ~1%. Размер статической библиотеки: 1 874 824 байта (float, NEON) и 1 790 512 байт (чистый C). Бенчмарки: scratchpad/bench/bench.c

Источники: <https://opus-codec.org/release/stable/2026/01/14/libopus-1_6_1.html>

### [info] Официальный клиент Mumble больше не бандлит opus и линкуется с системным libopus

В master у mumble-voip/mumble в .gitmodules 12 сабмодулей (minhook, speexdsp, rnnoise, tracy, nlohmann_json, SPSCQueue, cmake-compiler-flags, flag-icons, utfcpp, soci, spdlog, CLI11) — opus среди них нет; в листинге каталога 3rdparty каталога opus-src тоже нет (есть speexdsp, speexdsp-build, rnnoise-src, rnnoise-build и т.д.). Старые инструкции build_linux.md с опцией -Dbundled-opus=OFF устарели. Практическое следствие: официальные клиенты на современных дистрибутивах уже работают с libopus 1.5/1.6, то есть наш вендоринг 1.6.1 не создаёт рассинхрона, а наоборот выравнивает нас с ними.

Источники: <https://github.com/mumble-voip/mumble/blob/master/.gitmodules> · <https://github.com/mumble-voip/mumble/tree/master/3rdparty>

### [info] Интерфейсы кодека в gumble тривиальны — адаптер занимает менее 100 строк

В gumble/audiocodec.go форка stieneee: `func RegisterAudioCodec(id int, codec AudioCodec)` с проверкой 0 <= id < 8 (Opus = 4, константа audioCodecIDOpus); `type AudioCodec interface { ID() int; NewEncoder() AudioEncoder; NewDecoder() AudioDecoder }`; `type AudioEncoder interface { ID() int; Encode(pcm []int16, mframeSize, maxDataBytes int) ([]byte, error); Reset() }`; `type AudioDecoder interface { ID() int; Decode(data []byte, frameSize int) ([]int16, error); Reset() }`. Константы в gumble/audio.go: AudioSampleRate = 48000, AudioChannels = 1, AudioDefaultInterval = 10ms, AudioDefaultFrameSize = 480, AudioMaximumFrameSize = 2880 (60 мс), AudioDefaultDataBytes = 40. Регистрация идёт через init() в пакете opus, поэтому достаточно НЕ импортировать github.com/stieneee/gumble/opus и вызвать RegisterAudioCodec(4, ownCodec) самим — конфликта не будет. Единственная шероховатость интерфейса: Decode обязан вернуть свежий []int16 на каждый кадр, что даёт аллокацию на каждые 10 мс на каждого говорящего; лечится sync.Pool внутри нашего декодера при копировании в возвращаемый слайс.

Источники: <https://github.com/stieneee/gumble/blob/master/gumble/audiocodec.go> · <https://github.com/stieneee/gumble/blob/master/gumble/audio.go>

## Рекомендации

- Завести internal/audio/opus (или gulopus) — собственный пакет: третьей стороной в third_party/opus положить подмножество тарбола opus-1.6.1 (include, celt, silk, src; удалить dnn, doc, tests, m4, cmake, meson, win32 и корневые autotools-файлы, оставив COPYING) — 4.1 МБ, 307 файлов; сгенерировать 137 stub-файлов z_<путь>.c с одним #include каждый и рукописный config.h. Рабочий прототип лежит в scratchpad/cgotest — он собирается одним go build и печатает 'libopus 1.6.1-gul'.
- Скрипт обновления вендора держать в репозитории (скачать тарбол по SHA256 с downloads.xiph.org, распаковать, отфильтровать, перегенерировать stubs из celt_sources.mk/silk_sources.mk/opus_sources.mk), чтобы подъём на 1.6.2/1.7 был командой, а не археологией — именно этого не сделал layeh и поэтому застрял на 1.1.2.
- Собирать float-вариант БЕЗ DRED, OSCE и BWE: они выключены по умолчанию, требуют float, дают +5 МБ к .a и не решают нашу проблему, потому что по TCP-туннелю пакеты не теряются. In-band FEC (OPUS_SET_INBAND_FEC) по той же причине не включать — он только съест битрейт (замерено: +53 мкс/кадр на энкоде при packet_loss_perc=20).
- Не включать интринсики/RTCD в cgo-сборке (эквивалент --disable-intrinsics --disable-rtcd): пофайловые -msse4.1/-mavx2 в cgo невозможны, а измеренная цена чистого C — 8-10% при абсолютных 0.9-1.2% ядра на кадр 10 мс.
- В адаптере не повторять ошибку gumble: подписаться на ServerConfigEvent.MaximumBitrate и вызывать OPUS_SET_BITRATE, выбирая application по битрейту как официальный Mumble (>=64k — RESTRICTED_LOWDELAY, >=32k — AUDIO, иначе VOIP), ставить OPUS_SET_VBR(0) или CVBR для ровного потока в TCP, и передавать в Encode maxDataBytes, согласованный с битрейтом, а не жёсткие 40 байт из AudioDefaultDataBytes.
- Регистрировать кодек самим: gumble.RegisterAudioCodec(4, ownCodec) и НЕ импортировать github.com/stieneee/gumble/opus — тогда его init() не отработает и зависимость на stieneee/gopus (а с ней и требование системного libopus через pkg-config) уходит из go.mod полностью.
- Оставить deep PLC как выключаемый build-тег на будущее: сборка с --enable-deep-plc стоит +1.75 МБ к .a, накладных расходов на нормальных кадрах почти нет (6.7 → 6.8 мкс), но каждый скрываемый кадр обходится ~400 мкс. Включать только если после запуска увидим реальные underrun-ы jitter-буфера из-за head-of-line blocking TCP; переключатель — один OPUS_SET_COMPLEXITY(5) на декодере.
- Внутри декодера завести переиспользуемый буфер и sync.Pool: интерфейс gumble.AudioDecoder обязывает возвращать новый []int16 на каждые 10 мс на каждого говорящего, при 14 собеседниках это 1400 аллокаций в секунду только на приёме.
- Добавить в CI conformance-прогон: закодировать эталонный WAV своим энкодером и проверить, что декодируется libopus и (как независимая проверка) pion/opus v0.1.0 — интероп в обе стороны у меня прошёл 500/500 на VOIP 24k/40k, AUDIO 64k и LOWDELAY 96k, так что это дешёвая регрессионная сетка без внешних зависимостей.
- hraban/opus, pion/opus и tphakala/go-opus не брать сейчас, но поставить tphakala/go-opus в вотчлист: если он через год-два дорастёт до SILK/hybrid-энкодера и обзаведётся сообществом, это единственный путь убрать cgo из аудио-тракта целиком — правда, speexdsp и RNNoise всё равно останутся на cgo, так что выигрыш будет только частичный.
