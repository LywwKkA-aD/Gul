# AEC + AGC: speexdsp против WebRTC APM (AEC3) и системных эхоподавителей

## Вердикт: ЗАМЕНИТЬ

Заменить speexdsp (speex_echo_* + speex_preprocess_* в роли AEC/AGC) на webrtc-audio-processing v2.1 — AEC3 + GainController2(adaptive_digital) + HighPassFilter, всё через один экземпляр webrtc::AudioProcessing на 48 кГц / 10 мс / int16. Причина не «новее», а функциональная: заявленная цель «разговор на колонках без наушников» на разных устройствах ввода/вывода — это именно тот сценарий, который мануал Speex прямо называет неработающим, тогда как AEC3 спроектирован под дрейф часов, изменения задержки, double-talk и нелинейные искажения колонок. speexdsp имеет смысл сохранить только как resampler/jitter-утилиты; его AEC и AGC из плана убрать. Системные AEC (macOS VoiceProcessingIO, Windows AEC APO/Voice Clarity, PipeWire module-echo-cancel) — необязательные платформенные улучшения на потом, не база v1.

## Резюме

Текущий план (speexdsp AEC как основной эхоподавитель) упирается в задокументированное ограничение самого Speex: MDF-канцеллер не работает, если тактовые генераторы захвата и воспроизведения не залочены на один источник — а в Gul это ровно та ситуация (разные устройства + duplex-ring miniaudio без компенсации дрейфа). Живая современная альтернатива есть и она зрелая: webrtc-audio-processing v2.1 (freedesktop, код WebRTC M131, BSD-3 + PATENTS), где AEC3 непрерывно переоценивает задержку, детектирует дрейф (clockdrift_detector) и прокидывает его в адаптацию фильтра, плюс есть AGC2, NS и HPF в одном конвейере. Её берут PipeWire (module-echo-cancel через libspa-aec-webrtc линкуется именно с webrtc-audio-processing-2), LiveKit, Jitsi; собранная библиотека — около 900 КиБ, апстрим держит патчи под MinGW и MSVC, так что cgo-сборка под Windows реальна. Системные AEC как основной путь не годятся: в miniaudio (и master, и ветка dev-0.12) вообще нет kAudioUnitSubType_VoiceProcessingIO, а eCategory жёстко прибит к AudioCategory_Other, из-за чего Windows 11 Voice Clarity к потоку тоже не применится. Готовой продакшн-биндинги для Go нет — придётся написать свой C-шим (у LiveKit он занимает 73 строки) и вендорить исходники вместе с урезанным abseil. Вывод: AEC и AGC надо менять на WebRTC APM, speexdsp оставить максимум как ресемплер.

## Находки

### [CRITICAL] Speex MDF официально не работает при неслоченных часах — это прямой блокер сценария Gul

Мануал Speex про эхоподавитель формулирует это без оговорок: использование разных звуковых карт для захвата и воспроизведения «will *not* work, regardless of what you may think», единственное исключение — карты, у которых sampling clock залочен на один источник. Плюс отдельно оговорено, что нелинейные искажения (клиппинг, перегруз дешёвых колонок) алгоритм линейной адаптивной фильтрации не лечит в принципе. В Gul аудио-путь — miniaudio duplex с наивным ring без компенсации дрейфа на CoreAudio/PulseAudio, то есть предпосылка «часы залочены» не выполняется даже на одном устройстве, а сценарий «микрофон ноутбука + внешние колонки» ломает её гарантированно. При дрейфе MDF не деградирует плавно, а перестаёт сходиться: фильтр расходится и эхо возвращается целиком.

Источники: <https://www.speex.org/docs/manual/speex-manual/node7.html> · <https://www.speex.org/docs/manual/speex-manual.pdf>

### [MAJOR] AEC3 явно моделирует дрейф часов и переменную задержку — то, чего в MDF нет

В дереве webrtc/modules/audio_processing/aec3 (v2.1) есть echo_path_delay_estimator, render_delay_controller, render_delay_buffer, block_delay_buffer и clockdrift_detector.{h,cc}. ClockdriftDetector::Update анализирует историю оценок задержки и выдаёт уровень kNone/kProbable/kVerified; в block_processor.cc:185 это попадает в echo_path_variability.clock_drift, который дальше управляет адаптацией фильтра в EchoRemover. Важная честная оговорка: AEC3 дрейф детектирует и подстраивается (переалигнивает буфер референса и меняет режим адаптации), но сам не ресемплит — то есть он деградирует плавно вместо расхождения, но полностью снимать необходимость компенсации дрейфа в аудиослое не должен. Дополнительно у AEC3 есть подавитель остаточного эха и comfort noise, что закрывает нелинейность колонок — ровно тот класс проблем, который speexdsp не покрывает.

Источники: <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/webrtc/modules/audio_processing/aec3/clockdrift_detector.h> · <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/webrtc/modules/audio_processing/aec3/clockdrift_detector.cc> · <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/webrtc/modules/audio_processing/aec3/block_processor.cc>

### [MAJOR] Готового production-grade Go-биндинга нет — это основная статья затрат

Найдены ровно два кандидата, оба непригодны как зависимость. github.com/CoyAce/apm: BSD-3, создан 23.01.2026, последний push 04.02.2026, 4 звезды, 20 коммитов; вендорит 653 файла WebRTC APM и 1006 файлов abseil (около 8.2 МБ исходников), компилирует их прямо через #cgo CXXFLAGS. Но его CMakeLists.txt содержит только macOS-конфигурацию (WEBRTC_MAC, target_link_libraries(abseil PRIVATE "-framework CoreFoundation")), так что заявленная в README поддержка Windows не подтверждена сборкой. github.com/dnhkng/go-webrtc-apm линкуется с системной библиотекой (apt/dnf/brew), что несовместимо с требованием одного самодостаточного бинарника, и в README автор прямо пишет, что проект «fully vibe-coded». Вывод: оба годятся как референс, ни один — как go.mod-зависимость.

Источники: <https://github.com/CoyAce/apm> · <https://github.com/dnhkng/go-webrtc-apm/blob/main/README.md>

### [MAJOR] Зависимость от abseil-cpp — главная боль вендоринга

В meson.build v2.1 абсейл подтягивается как absl_base (>=20240722), absl_flags, absl_strings, absl_numeric, absl_synchronization, absl_bad_optional_access, а при отсутствии pkg-config уходит в subprojects/abseil-cpp.wrap, то есть качается на этапе сборки. Для Go-модуля это неприемлемо (сборка должна быть офлайн и без meson), поэтому abseil придётся вендорить вместе с APM — CoyAce на практике утащил около 1000 файлов. Есть и второй эффект: при cgo-сборке из исходников компилируются примерно 1650 .cc-файлов, первая чистая сборка займёт минуты. Комментарий в самом meson.build («most reliable way of building abseil due to strict C++ standard match requirements») намекает, что ABI-несовпадение по -std между abseil и APM ломает линковку — держать оба на -std=c++17 обязательно.

Источники: <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/meson.build> · <https://github.com/CoyAce/apm>

### [MAJOR] В miniaudio нет VoiceProcessingIO — системный AEC на macOS через malgo недоступен

Прямая проверка исходников: в miniaudio.h ветки master (4.1 МБ, свежая на 2026-08-19) встречаются только kAudioUnitSubType_RemoteIO и kAudioUnitSubType_HALOutput; kAudioUnitSubType_VoiceProcessingIO не встречается ни разу. В ветке dev-0.12 (снапшот от 10.05.2026) — та же картина, ноль вхождений. То есть в roadmap 0.12 VPIO не появился, и malgo его отдать не может: чтобы получить системный AEC на macOS, придётся писать собственный CoreAudio-бэкенд в обход miniaudio. Плюс у VPIO есть известные побочки, о которых сообщают на форумах Apple и JUCE: он даккает весь системный звук, даёт заметно заниженную громкость в связке «Bluetooth-выход + встроенный микрофон», и запрос kAudioOutputUnitProperty_OSWorkgroup у него падает с kAudioUnitErr_InvalidProperty.

Источники: <https://github.com/mackron/miniaudio> · <https://developer.apple.com/documentation/audiotoolbox/kaudiounitsubtype_voiceprocessingio> · <https://forum.juce.com/t/cannot-get-kaudiounitsubtype-voiceprocessingio-code-working/63789> · <https://developer.apple.com/forums/thread/701773>

### [MAJOR] На Windows системный AEC до Gul тоже не дойдёт: miniaudio жёстко ставит AudioCategory_Other

Windows 11 24H2 применяет Voice Clarity (системные AEC, подавление реверберации и шума) автоматически, но только к потокам, открытым в Communications signal processing mode. В miniaudio.h enum объявлен как MA_AudioCategory_Other = 0 с комментарием прямо в коде: «miniaudio is only caring about Other», и ma_IAudioClient2_SetClientProperties вызывается с eCategory = MA_AudioCategory_Other. Значит через malgo Voice Clarity не активируется. Второй путь, IAcousticEchoCancellationControl, требует минимум Windows Build 22621 и наличия AEC-эффекта на конкретном capture endpoint — при его отсутствии GetService возвращает E_NOINTERFACE, и на типичных десктопных USB-микрофонах его как раз нет. Легаси Voice Capture DSP (CLSID_CWMAudioAEC) существует, но это старый DMO. Итог: на Windows свой софтверный AEC обязателен.

Источники: <https://github.com/mackron/miniaudio> · <https://learn.microsoft.com/en-us/windows/win32/api/audioclient/nn-audioclient-iacousticechocancellationcontrol> · <https://learn.microsoft.com/en-us/windows-hardware/drivers/audio/windows-11-apis-for-audio-processing-objects> · <https://learn.microsoft.com/en-us/windows/win32/medfound/voicecapturedmo>

### [minor] webrtc-audio-processing v2.1: актуальная версия, живая упаковка, но апстрим-репозиторий малоактивен

Теги по GitLab API: v2.1 от 2025-01-22, v2.0 от 2025-01-08 (бамп кода до WebRTC M131), до этого v1.3 (2023-09). Последний коммит в master — 2025-11-10 («examples: Use 48 kHz by default»), то есть за 2026 год коммитов нет. Это не признак заброшенности: репозиторий по своему README сознательно является «Linux packaging friendly copy» APM с целью «make no changes to the code», реальная разработка идёт в апстриме WebRTC, а здесь только патчи портирования (гcc-15, abseil-cpp 202508, s390x, MinGW, MSVC). Упаковка живая: Alpine собирал 2.1-r2 11.07.2026, есть Debian/Arch/FreeBSD, Rust-крейт webrtc-audio-processing 2.1.0 опубликован 13.05.2026. Практический риск — не смерть проекта, а лаг относительно апстрима (M131 — это конец 2024 года) и то, что ждать быстрых багфиксов не стоит: чинить придётся своим патчем.

Источники: <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/> · <https://www.mail-archive.com/pulseaudio-discuss@lists.freedesktop.org/msg22110.html> · <https://www.mail-archive.com/pulseaudio-discuss@lists.freedesktop.org/msg22107.html> · <https://pkgs.alpinelinux.org/package/edge/community/x86_64/webrtc-audio-processing-2> · <https://docs.rs/crate/webrtc-audio-processing/latest>

### [minor] C++-only API: нужен свой C-шим для cgo, но он маленький

Публичный API — C++ (webrtc::AudioProcessingBuilder, класс AudioProcessing : public RefCountInterface, вложенные struct Config). Никакого C ABI библиотека не экспортирует, поэтому cgo напрямую её не возьмёт. Стоимость обёртки при этом низкая: у LiveKit весь шим — webrtc-sys/include/livekit/apm.h плюс webrtc-sys/src/apm.cpp на 73 строки, где создаётся APM, конфиг мапится в webrtc::AudioProcessing::Config и наружу торчат process_stream / process_reverse_stream / set_stream_delay_ms. У CoyAce/apm аналогичный bridge.h + bridge.cpp примерно на 12 КБ. Реалистичная оценка для Gul — 200–300 строк extern "C" плюс тонкий Go-слой.

Источники: <https://github.com/livekit/rust-sdks/blob/main/webrtc-sys/include/livekit/apm.h> · <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/webrtc/api/audio/audio_processing.h> · <https://github.com/CoyAce/apm>

### [minor] Сборка под Windows/MinGW возможна: апстрим держит для этого патчи

В каталоге patches/ репозитория лежат 0001-Some-fixes-for-MinGW.patch, 0001-meson-Fixes-for-MSVC-build.patch и 0001-Fix-up-XMM-intrinsics-usage-on-MSVC.patch, а коммит «patches: Track some MinGW fixups» датирован 10.01.2025 — то есть MinGW-путь поддерживается сознательно, что критично, поскольку cgo на Windows собирает именно MinGW-gcc. Оговорка: MSYS2 упаковывает только webrtc-audio-processing 0.3.1 и отдельный пакет -1 (ветка 1.x); 2.x там нет, так что готового пакета для Windows не будет, собирать надо самим. Заголовки cgo при этом придётся раздавать по GOOS: -DWEBRTC_WIN для windows, -DWEBRTC_POSIX -DWEBRTC_MAC для darwin, -DWEBRTC_POSIX -DWEBRTC_LINUX для linux, плюс -DWEBRTC_HAS_NEON -DWEBRTC_ARCH_ARM64 для arm64.

Источники: <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/> · <https://packages.msys2.org/base/mingw-w64-webrtc-audio-processing> · <https://github.com/CoyAce/apm>

### [minor] Проблема speexdsp AGC в FIXED_POINT подтверждается, но она тривиально обходится и в любом случае снимается переходом на AGC2

В libspeexdsp/preprocess.c поле int agc_enabled объявлено внутри #ifndef FIXED_POINT (строка 229), там же инициализация st->agc_enabled = 0 (строка 499) и вызов speex_compute_agc под тем же гардом (строки 949–951, 961–962). То есть в fixed-point сборке AGC действительно физически отсутствует. Но это не свойство библиотеки, а следствие define: на десктопе FIXED_POINT определять не надо, float-сборка даёт AGC штатно. Так что как аргумент против speexdsp этот пункт слабый — настоящие аргументы это дрейф часов и нелинейность. При этом APM::Config::GainController2 с adaptive_digital всё равно объективно лучше: у него есть управление аналоговым уровнем микрофона через input volume controller и настраиваемый headroom, и именно эту конфигурацию использует LiveKit.

Источники: <https://github.com/xiph/speexdsp/blob/master/libspeexdsp/preprocess.c> · <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/webrtc/api/audio/audio_processing.h> · <https://github.com/livekit/rust-sdks/blob/main/webrtc-sys/include/livekit/apm.h>

### [minor] SpeexDSP фактически заморожен по алгоритмам

Последний релиз — SpeexDSP-1.2.1 от 17.06.2022. Репозиторий xiph/speexdsp не заброшен (последние коммиты 2025 года: «testresample2: define M_PI if needed» от 05.07.2025, правки AUTHORS, «smallft: mark constants as const»), но это исключительно гигиена сборки, а не работа над эхоподавителем. MDF-алгоритм датируется серединой 2000-х и с тех пор не менялся. Для сравнения, AEC3 — это код 2018+ годов, который до сих пор развивается в апстриме WebRTC.

Источники: <https://github.com/xiph/speexdsp> · <https://github.com/xiph/speexdsp/releases>

### [info] API подходит под сетку 48 кГц / моно / 10 мс без переходников

В webrtc/api/audio/audio_processing.h есть int16-путь: virtual int ProcessStream(const int16_t* src, ...) и ProcessReverseStream(const int16_t* src, ...), плюс set_stream_delay_ms(int). Кадр APM жёстко равен 10 мс, Config::Pipeline::maximum_internal_processing_rate по умолчанию 48000. То есть 480 сэмплов int16 моно уходят в APM как есть — ни ресемплинга, ни переупаковки кадров не нужно, сетка проекта совпадает с нативной сеткой библиотеки. Порядок обработки внутри фиксирован: HPF → AEC3 → NS → AGC2.

Источники: <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/webrtc/api/audio/audio_processing.h>

### [info] Размер и CPU приемлемы для десктопа

Размер: пакет webrtc-audio-processing-2 версии 2.1-r2 в Alpine имеет installed size 898.0 KiB — это весь APM целиком (AEC3, AECM, AGC1, AGC2, NS, HPF, RNN-VAD). При статической линковке с --gc-sections плюс нужные куски abseil ориентир — порядка 1–1.5 МБ прироста бинарника, что для десктопного Wails-приложения несущественно. CPU: профили Firefox по багу 1702781 показывают для WebRTC APM единичные проценты одного ядра на поток — webrtc::aec3::MatchedFilterCore_SSE2 1.61%, webrtc::SincResampler::Convolve_SSE 1.34%, webrtc::ThreeBandFilterBank::Analysis 1.07%. Ключевой архитектурный нюанс: у APM один reverse stream, поэтому нужен ровно один экземпляр на весь клиент (микс всех удалённых участников идёт в ProcessReverseStream), и стоимость не растёт от 6 к 15 участникам. Это в разы дороже speex MDF, но абсолютные числа для десктопа незначимы.

Источники: <https://pkgs.alpinelinux.org/package/edge/community/x86_64/webrtc-audio-processing-2> · <https://bugzilla.mozilla.org/show_bug.cgi?id=1702781> · <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/webrtc/api/audio/audio_processing.h>

### [info] Лицензия BSD-3-Clause плюс PATENTS grant — MIT-совместимо

COPYING в v2.1 — стандартный трёхпунктный BSD от Google Inc. (2011). В шапках файлов дополнительно указано наличие «additional intellectual property rights grant» в файле PATENTS дерева WebRTC. Оба текста разрешительные и совместимы с MIT-проектом; требуется только сохранить уведомления при распространении бинарника. Отдельно учтите: вендоренный abseil-cpp идёт под Apache-2.0 — тоже совместимо, но это вторая лицензия, которую нужно указать в NOTICE.

Источники: <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/COPYING> · <https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/raw/v2.1/webrtc/api/audio/audio_processing.h>

### [info] PipeWire module-echo-cancel — это тот же самый AEC3, только вне вашего процесса

В meson.build PipeWire зависимость ищется в порядке webrtc-audio-processing-2 → webrtc-audio-processing-1 → webrtc-audio-processing, то есть предпочтение отдаётся ровно v2.x; плагин spa-aec-webrtc собирается из aec-webrtc.cpp и линкуется с этой библиотекой. Практический вывод для Gul: полагаться на module-echo-cancel как на платформенное преимущество Linux смысла нет — качество будет идентичным вашему встроенному APM, зато появится зависимость от того, что у пользователя PipeWire, модуль загружен и виртуальный source выбран вручную. Ценность этого пути ровно одна — PipeWire делает выравнивание потоков в своём графе с общими часами, снимая вопрос дрейфа. Как основной путь не годится, как «если пользователь уже настроил — не мешать» — вполне.

Источники: <https://docs.pipewire.org/page_module_echo_cancel.html> · <https://man.archlinux.org/man/libpipewire-module-echo-cancel.7.en>

### [info] Прочие открытые AEC: живых конкурентов WebRTC APM в 2026 нет

ewan-xu/AEC3 (standalone-извлечение AEC3, 216 звёзд) — последний push 24.02.2022 и, что важнее, в репозитории отсутствует файл лицензии, то есть юридически использовать его нельзя. Проекта «speexdsp-ng» не существует. Все крупные RTC-проекты сходятся на одном и том же коде: у LiveKit это webrtc-sys/src/apm.cpp и обёртки в client-sdk-cpp и python-sdks, у PipeWire — spa-aec-webrtc. ML-подходы (DTLN-aec и производные) существуют, показывают хорошие результаты на AEC-Challenge, но поставляются как TF-Lite модели на 1.8–10.4 млн параметров: тащить TFLite-рантайм в самодостаточный Go-бинарник ради AEC нерационально. Третьего варианта на рынке просто нет.

Источники: <https://github.com/ewan-xu/AEC3> · <https://github.com/livekit/rust-sdks/blob/main/webrtc-sys/include/livekit/apm.h> · <https://github.com/breizhn/DTLN-aec> · <https://docs.pipewire.org/page_module_echo_cancel.html>

## Рекомендации

- Вендорить webrtc-audio-processing v2.1 плюс необходимый срез abseil-cpp в internal/dsp/apm/ и написать свой C-шим (extern "C", примерно 200–300 строк) поверх webrtc::AudioProcessingBuilder. Как референс использовать livekit/rust-sdks webrtc-sys/src/apm.cpp (73 строки, показывает минимальный набор: create, process_stream, process_reverse_stream, set_stream_delay_ms) и CoyAce/apm (показывает раскладку cgo CXXFLAGS по GOOS/GOARCH), но не брать их в go.mod.
- Держать abseil и APM на одном -std=c++17: в meson.build апстрима прямо сказано, что abseil ломается при несовпадении стандарта C++. Прописать в cgo per-GOOS дефайны: windows -DWEBRTC_WIN; darwin -DWEBRTC_POSIX -DWEBRTC_MAC; linux -DWEBRTC_POSIX -DWEBRTC_LINUX; arm64 дополнительно -DWEBRTC_HAS_NEON -DWEBRTC_ARCH_ARM64. Не забыть -DWEBRTC_APM_DEBUG_DUMP=0.
- Заложить в CI, что первая чистая сборка компилирует порядка 1650 .cc-файлов и занимает минуты, и настроить кэш сборки Go. Если это окажется неприемлемо — запасной вариант: собирать статические libwebrtc-audio-processing-2.a плюс absl через meson (--default-library=static) в CI под каждую тройку GOOS/GOARCH, коммитить артефакты и линковать через #cgo LDFLAGS. Это быстрее собирается, но добавляет 5–6 бинарных артефактов в репозиторий.
- Стартовый конфиг APM для Gul: echo_canceller.enabled=true, mobile_mode=false, enforce_high_pass_filtering=true; high_pass_filter.enabled=true; gain_controller2.enabled=true с adaptive_digital.enabled=true; noise_suppression.enabled=true, level=kHigh; pipeline.maximum_internal_processing_rate=48000. Один экземпляр AudioProcessing на весь клиент.
- Не полагаться на duplex-режим miniaudio ради AEC. Референс far-end у вас есть в процессе: микшируйте всех удалённых участников в один буфер, отдавайте его в ProcessReverseStream ровно перед записью в malgo, и вызывайте set_stream_delay_ms с измеренной суммой буфера вывода и латентности устройства — AEC3 дальше сам уточнит задержку своим оценщиком. Это заодно снимает зависимость от наивного ring в duplex-пути.
- Компенсацию дрейфа всё равно реализовать: AEC3 дрейф детектирует и подстраивается, но не ресемплит. Минимально — измерять заполненность ring-буфера на окне порядка 10 секунд и применять коррекцию порядка сотни ppm через ресемплер между потоками захвата и воспроизведения. Здесь как раз пригодится speex_resampler из speexdsp, который стоит оставить в проекте.
- Пересмотреть роль RNNoise. NS в APM работает в связке с подавителем остаточного эха AEC3 и настроен совместно; добавление RNNoise после AEC3+NS с высокой вероятностью даст пересуппрессию и артефакты на речи. Сначала измерить APM в одиночку, и только если шумодава не хватает — вернуть RNNoise, но с ослабленным NS в APM.
- Если в проекте остаётся speexdsp (resampler, jitter-утилиты) — не определять FIXED_POINT. Это ровно тот define, из-за которого пропадает AGC, и float-сборка на десктопе является нормой.
- Windows Voice Clarity рассматривать как эксперимент после релиза, а не как часть v1: для его активации потребуется патчить miniaudio, чтобы eCategory ставился в AudioCategory_Communications вместо жёстко зашитого AudioCategory_Other, и при этом обязательно отключать собственный AEC3, иначе получится двойная обработка. Аналогично с macOS VoiceProcessingIO — это отдельный CoreAudio-бэкенд в обход miniaudio, единственное реальное преимущество которого в том, что он давит и звук чужих приложений.
- На Linux не подменять свой AEC на PipeWire module-echo-cancel: там внутри та же самая библиотека. Достаточно корректно работать, если пользователь уже выбрал виртуальный echo-cancel source — в этом случае имеет смысл дать в настройках Gul переключатель отключения встроенного AEC.
- В NOTICE/лицензионный раздел сборки добавить BSD-3-Clause и PATENTS из дерева WebRTC, а также Apache-2.0 для abseil-cpp. Всё совместимо с MIT, но требует сохранения уведомлений в дистрибутиве.
- Зафиксировать точную ревизию вендоренного кода (тег v2.1, коммит от 2025-01-22) и вести собственный patches/ по образцу апстрима. Апстрим-репозиторий за 2026 год не коммитил, так что багфиксы под новые компиляторы, вероятно, придётся делать самим — по образцу их же патчей для gcc-15 и abseil-cpp 202508.
