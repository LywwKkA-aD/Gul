# Экосистема Mumble: проверка форка stieneee/gumble и сервера

## Вердикт: ОСТАВИТЬ С ИЗМЕНЕНИЯМИ

Оставляем Mumble-сервер (официальный образ mumblevoip/mumble-server:v1.5.915) и форк stieneee/gumble — альтернатив с протоколом 1.5 в Go на 2026 год не существует, а переписывать протокол самим нерентабельно. Пинить github.com/stieneee/gumble v0.0.0-20240610021017-a3449ae7108c (коммит a3449ae7108c3d336c823c9911a3e61a92fe285f, 2024-06-10, master, тегов в репозитории нет). Изменения к плану: форкнуть репозиторий в свою организацию и влить туда ветку feat/opus-passthrough (4bdfe39e2829561a78c7f6444d3905d61a6f9ce0, псевдоверсия v0.0.0-20260216161421-4bdfe39e2829), чтобы уйти от gopus с его динамической линковкой к системному libopus и подключить собственный статически слинкованный Opus. Обязательна обёртка над аудиоканалами с неблокирующей отдачей, иначе read-loop встаёт вместе с ping и клиента отключает сервер.

## Резюме

Форк github.com/stieneee/gumble подтверждается: это единственная живая Go-реализация протокола Mumble 1.5 — проверены все 25+ форков layeh/gumble (DanaDynamics 2026-06, iotku 2026-01, hugmouse 2026-01, LeoVerto 2025-11), ни один из них 1.5 не поддерживает, а talkkonnect тянет собственный форк от 2022 года на 1.3. Форк реально эксплуатируется в 2026: mumble-discord-bridge (190 звёзд) и blokadainfo/bib-comms пинят ровно тот же коммит. Однако две посылки исходного плана оказались неверны: stieneee/gopus не вендорит opus 1.1.2 под amd64, он вообще удалил вендоринг и линкуется с системным libopus через pkg-config, что прямо конфликтует с целью «один самодостаточный бинарник»; и в форке есть неучтённая ветка feat/opus-passthrough от 2026-02-16, которая добавляет сквозную передачу сырых Opus-кадров и полностью снимает вопрос с gopus. Блокирующая отправка в небуферизованные аудиоканалы прямо из read-loop сохранилась в неизменном виде — обёртка обязательна, иначе медленный потребитель роняет ping и весь контрольный канал. Серверная сторона проблем не вызывает: актуальный стабильный mumble-server v1.5.915 (2026-07-19), официальный Docker-образ обновлён 2026-07-27, конфиг через MUMBLE_CONFIG_*, а сервер 1.5/1.6 держит legacy- и protobuf-клиентов одновременно и никого не отвергает.

## Находки

### [MAJOR] Премиса неверна: stieneee/gopus не вендорит opus 1.1.2, а требует системный libopus — это ломает цель самодостаточного бинарника

Коммит 6d10f60 «experiement with only shared library» удалил файл opus_nonshared.go целиком (382 строки) — тот самый вендоренный opus 1.1.2 с ограничением amd64. В репозитории остался единственный Go-файл opus_shared.go с директивой `// #cgo !nopkgconfig pkg-config: opus`. Хорошая новость: древнего 1.1.2 и amd64-only ограничения в выбранном форке нет. Плохая: сборка требует opus development library, а бинарник получает динамическую зависимость на libopus, что прямо противоречит требованию «один самодостаточный бинарник на Windows/macOS/Linux». Обойти можно тегом сборки `nopkgconfig` с ручными CGO_CFLAGS/CGO_LDFLAGS на статический libopus.a, но чище — уйти на OpusPassthrough и собственный биндинг. Лицензия gopus — Unlicense (public domain), с MIT совместима.

Источники: <https://github.com/Stieneee/gopus> · <https://github.com/Stieneee/gopus/commit/6d10f60903357fb45bd3fb1a2c27c7befcd384ab> · <https://github.com/Stieneee/gopus/blob/master/opus_shared.go>

### [MAJOR] Блокирующая отправка в небуферизованные аудиоканалы прямо из read-loop сохранилась полностью

В gumble/handlers.go:106-122 замыкание sendEvent создаёт небуферизованный канал `ch := make(chan *AudioPacket)` и выполняет `ch <- event` синхронно. Вызывается оно из handleUDPTunnel, который дёргается напрямую из единственной горутины readRoutine (gumble/client.go:238-249: `handlers[pType](c, data)` без go). Следствие: если потребитель аудио задержится хоть на кадр, встаёт весь TCP read-loop — перестают обрабатываться Ping, ChannelState, UserState, TextMessage, и сервер отключит клиента по таймауту. Коммит f66c2ec9 «close audio channels when user disconnects» только вынес этот код в замыкание и добавил закрытие каналов при отключении пользователя, семантику отправки он не менял. В ветке feat/opus-passthrough то же самое. Обёртка обязана вычитывать канал в отдельной горутине с буфером и стратегией drop-oldest.

Источники: <https://github.com/Stieneee/gumble/blob/master/gumble/handlers.go> · <https://github.com/Stieneee/gumble/blob/master/gumble/client.go> · <https://github.com/Stieneee/gumble/commit/f66c2ec91e894d0eb86c5b58312dc0f70e43dc0f>

### [MAJOR] Неучтённая в плане ветка feat/opus-passthrough (2026-02-16) снимает всю проблему с Opus

master форка датирован 2024-06-10, но поле pushed_at репозитория показывает 2026-02-16 — это как раз ветка feat/opus-passthrough, четыре коммита поверх master. Добавляет: `Config.OpusPassthrough bool`; поле `AudioPacket.OpusData []byte`; метод `Client.OpusOutgoing() chan<- []byte`; функцию writeOpusAudio; и ветвление в handleUDPTunnel по обоим путям (legacy varint и protobuf), где при включённом флаге декодер вообще не создаётся, а сырые Opus-байты копируются в событие. Это ровно то, чего просили в upstream-issue layeh/gumble#24 «Expose raw Opus data» с 2016 года. Даёт полный контроль над кодеком: свой libopus (статически слинкованный, актуальной версии), свои настройки FEC/DTX/complexity, отсутствие лишнего цикла decode-encode. Проверено локально: ветка собирается (go build ./gumble/...) и проходит go vet на Go 1.26.5 darwin/arm64. Псевдоверсия для пина резолвится: v0.0.0-20260216161421-4bdfe39e2829.

Источники: <https://github.com/Stieneee/gumble/tree/feat/opus-passthrough> · <https://github.com/Stieneee/gumble/compare/master...feat/opus-passthrough> · <https://github.com/layeh/gumble/issues/24>

### [minor] В режиме OpusPassthrough клиент объявит серверу Opus: false, если не зарегистрирован кодек

gumble/client.go:132 формирует Authenticate как `Opus: proto.Bool(getAudioCodec(audioCodecIDOpus) != nil)`. Флаг кодека выставляется только импортом пакета gumble/opus, который в init() вызывает RegisterAudioCodec(4, ...) и тянет за собой gopus и libopus. То есть отказ от gopus ради passthrough молча ломает объявление поддержки Opus. Обход проверен на практике: зарегистрировать заглушку без внешних зависимостей — тип с методами ID() int (возвращает 4), NewEncoder() и NewDecoder() (возвращают nil, в passthrough-режиме они не вызываются) и вызвать gumble.RegisterAudioCodec(4, &stub{}) до подключения. Собирается и работает без cgo вообще.

Источники: <https://github.com/Stieneee/gumble/blob/master/gumble/client.go> · <https://github.com/Stieneee/gumble/blob/master/gumble/audiocodec.go> · <https://github.com/Stieneee/gumble/blob/master/opus/opus.go>

### [minor] AudioOutgoing и OpusOutgoing держат один кадр в запасе — плюс 10 мс к исходящей задержке

В gumble/client.go:183-197 (и симметрично в OpusOutgoing) цикл устроен так: `previous := <-ch`, затем в цикле отправляется previous, а не только что полученный p. Кадр удерживается, чтобы при закрытии канала успеть пометить последний пакет флагом terminator (final=true). Цена — постоянные лишние 10 мс на пути отправки при кадре 10 мс. При целевой низкой задержке это заметная доля бюджета. Обходится вызовом client.Conn.WriteAudio(...) напрямую из своей горутины с собственным управлением sequence и флагом final — метод экспортирован.

Источники: <https://github.com/Stieneee/gumble/blob/master/gumble/client.go> · <https://github.com/Stieneee/gumble/blob/master/gumble/conn.go>

### [minor] Лицензия gumble — MPL-2.0, а не MIT: раз форк неизбежен, возникает обязательство публикации

И layeh/gumble, и Stieneee/gumble под MPL-2.0. Это пофайловый слабый копилефт: связывать с MIT- или закрытым приложением можно, и §3.3 прямо разрешает распространять Larger Work на своих условиях, но любые изменения в самих файлах gumble остаются под MPL и исходники этих файлов должны быть доступны получателям. Поскольку план предполагает собственный форк (влить passthrough, починить блокирующую отправку), обязательство станет реальным — нужен публичный репозиторий форка. Остальная цепочка чистая: stieneee/gopus — Unlicense, сервер mumble-voip/mumble — BSD-3-Clause, mumble-voip/mumble-docker — BSD-3-Clause.

Источники: <https://github.com/Stieneee/gumble> · <https://github.com/layeh/gumble> · <https://github.com/mumble-voip/mumble-docker>

### [minor] Форк теряет часть полей protobuf-аудио: позиционные данные, context и volume_adjustment

В protobuf-ветке handleUDPTunnel (handlers.go, около строки 244) стоит голый комментарий `// TODO positional audio` — позиционные координаты в новом формате не разбираются вовсе, хотя в legacy-ветке они читаются. Кроме того из сообщения MumbleUDP.Audio используется только GetTarget(), а поля context (0 обычная речь, 1 крик в канал, 2 шёпот пользователю, 3 получено через channel listener) и volume_adjustment игнорируются. Практический эффект для Gul: позиционный звук не нужен, но нельзя отличить обычную речь от шёпота/крика на приёме, и не применяется серверная поправка громкости для listener-ов. Для компании 6-15 человек некритично, но если захочется индикации «шепчет» в UI — это надо будет допилить в форке.

Источники: <https://github.com/Stieneee/gumble/blob/master/gumble/handlers.go> · <https://github.com/Stieneee/gumble/blob/master/gumble/proto/MumbleUDP.proto>

### [minor] Channel listeners 1.5 доступны только вручную через WriteProto; whisper есть в готовом API

Вендоренный .proto в форке действительно версии 1.5 (присутствуют PluginDataTransmission, SuggestConfig и прочее), и в UserState есть поля listening_channel_add = 21, listening_channel_remove = 22, listening_volume_adjustment = 23 с вложенным VolumeAdjustment. Но высокоуровневого Go-API для них в пакете gumble нет — grep по listening/Listening в gumble/*.go пуст. Использовать можно, собрав MumbleProto.UserState руками и отправив через экспортированный client.Conn.WriteProto(...). А вот whisper/shout поддержан полноценно: gumble/voicetarget.go даёт VoiceTarget с AddUser/AddChannel/Clear и предопределённый VoiceTargetLoopback (ID 31) для эхо-теста через сервер — последнее удобно для отладки AEC. Важная деталь про пути импорта: форк перенёс сгенерированный код в gumble/proto/MumbleProto и gumble/proto/MumbleUDPProto, а старый каталог gumble/MumbleProto удалён; путь модуля изменён с layeh.com/gumble на github.com/stieneee/gumble.

Источники: <https://github.com/Stieneee/gumble/blob/master/gumble/proto/Mumble.proto> · <https://github.com/Stieneee/gumble/blob/master/gumble/voicetarget.go> · <https://github.com/Stieneee/gumble/blob/master/go.mod>

### [info] Аудиосетка gumble совпадает с сеткой Gul бит-в-бит

Проверено запуском реального кода против пина v0.0.0-20240610021017-a3449ae7108c: AudioSampleRate = 48000, AudioChannels = 1, AudioDefaultInterval = 10ms, AudioDefaultFrameSize = 480, AudioMaximumFrameSize = 2880 (60 мс). Это ровно 48 кГц / моно / 10 мс / 480 сэмплов из плана — ни ресемплинга, ни переупаковки кадров на стыке с DSP-цепочкой не потребуется. Отдельно: пакет gumble собирается вообще без cgo и без libopus (подтягивается только google.golang.org/protobuf v1.34.1), cgo нужен исключительно подпакету gumble/opus.

Источники: <https://github.com/Stieneee/gumble/blob/master/gumble/audio.go>

### [info] Альтернатив с протоколом 1.5 в Go нет — проверены все живые форки и независимые реализации

Форки layeh/gumble по свежести: DanaDynamics/gumble (2026-06-01, два коммита — только смена gopus-зависимости на свою), iotku/gumble (2026-01-13, bitrate-фикс, попытки stereo и миграции на hraban/opus, протокол 1.3), hugmouse/gumble (2026-01-13, единственный коммит с bitrate-фиксом), LeoVerto/gumble (2025-11-04, хардкод stereo и переименование модуля). Ни в одном нет 1.5. talkkonnect (354 звезды, активен 2026-08) тянет github.com/talkkonnect/gumble v0.0.0-20220821092831 — 2022 год, протокол 1.3. Независимая реализация CodingVoid/gomble (MIT, 14 звёзд) интересна тем, что единственная в Go умеет настоящий UDP-голос, но содержательные коммиты кончились в феврале 2022 (дальше только бампы зависимостей), автор сам пишет «still very much experimental», заявлена только Linux и в TODO висит «make library capable of using TLS certificates». Как замена не рассматривается.

Источники: <https://github.com/layeh/gumble/forks> · <https://github.com/CodingVoid/gomble> · <https://github.com/talkkonnect/talkkonnect/blob/master/go.mod> · <https://github.com/iotku/gumble>

### [info] Форк объявляет себя версией 1.5.1 и корректно согласует формат аудио с сервером любой версии

gumble/client.go:34-35: ClientVersionV1 = 1<<16 | 5<<8 | 1 и ClientVersionV2 = 1<<48 | 5<<32 | 1<<16, то есть 1.5.1 в обоих форматах; Release объявляется строкой "stieneee/gumble", Os/OsVersion заполняются из runtime.GOOS/GOARCH. handleVersion (handlers.go:79-97) корректно разбирает и VersionV2, и legacy VersionV1 с пересчётом в 64-битный формат. Выбор формата аудио динамический и по версии СЕРВЕРА: handlers.go:127 `if c.Version.Version < uint64(1<<48|5<<32)` — legacy varint, иначе protobuf; на отправке симметрично audio.go:73. Значит форк остаётся работоспособным и против сервера 1.4. Мелочь на заметку: client.Version читается из горутины отправки без синхронизации и не проверяется на nil — отправлять аудио строго после StateSynced.

Источники: <https://github.com/Stieneee/gumble/blob/master/gumble/client.go> · <https://github.com/Stieneee/gumble/blob/master/gumble/handlers.go> · <https://github.com/Stieneee/gumble/blob/master/gumble/audio.go>

### [info] Сервер 1.5/1.6 обслуживает legacy- и protobuf-клиентов одновременно, отказов в подключении нет

В src/MumbleProtocol.h:118 задано `constexpr Version::full_t PROTOBUF_INTRODUCTION_VERSION = Version::fromComponents(1, 5, 0)`, и в кодировщике сосуществуют полные пары методов prepareAudioPacket_legacy/_protobuf, updateAudioPacket_legacy/_protobuf, encodePingPacket_legacy/_protobuf. Функция protocolVersionsAreCompatible (MumbleProtocol.cpp:69-72) сравнивает лишь то, по одну ли сторону границы 1.5.0 находятся версии, и используется она в Server.cpp:1343 исключительно для решения, можно ли переиспользовать уже закодированный пакет при рассылке группе получателей — при несовпадении сервер просто перекодирует пакет под нужное поколение протокола. Никакого разрыва соединения по версии нет: клиенты 1.3/1.4 и 1.5+ спокойно сидят на одном сервере.

Источники: <https://github.com/mumble-voip/mumble/blob/master/src/MumbleProtocol.h> · <https://github.com/mumble-voip/mumble/blob/master/src/MumbleProtocol.cpp> · <https://github.com/mumble-voip/mumble/blob/master/src/murmur/Server.cpp>

### [info] Актуальный сервер — v1.5.915 (2026-07-19); 1.6 пока RC и брать его не стоит

Стабильные релизы: v1.5.915 (2026-07-19), v1.5.901 (2026-05-22), v1.5.857 (2025-10-21). Параллельно есть v1.6.870 (2026-03-02), помеченный в GitHub как prerelease — RC новой ветки 1.6.x. Официальные Docker-теги обновлены 2026-07-27: latest, v1.5, v1.5.915, v1.5.915-1, а также v1.6, v1.6.870 и варианты с суффиксом -acme (встроенный ACME-клиент lego для автополучения сертификатов Let's Encrypt). Переход на 1.6 несёт неприятные для эксплуатации изменения: новая схема БД и новый бэкенд с миграцией, переезд клиента на Qt6, и смена CLI-флагов (-ini на --ini, -fg на --foreground), из-за чего ломаются systemd-юниты. Отдельно любопытно для DSP-части проекта: в 1.6 шумодав ReNameNoise заменён на RNNoise версии 0.2 — то есть апстрим Mumble пришёл ровно к тому же выбору, что и план Gul.

Источники: <https://github.com/mumble-voip/mumble/releases> · <https://hub.docker.com/r/mumblevoip/mumble-server/tags> · <https://www.mumble.info/blog/mumble-1.6.870-rc/> · <https://www.mumble.info/blog/mumble-1.5.634/>

### [info] Конфигурация официального образа: переменные MUMBLE_CONFIG_*, том /data, UID/GID 10000

Формат подтверждён по README официального репозитория mumble-voip/mumble-docker (BSD-3-Clause, 370 звёзд, push 2026-07-27). Любая опция murmur.ini задаётся как MUMBLE_CONFIG_<configName>; имя регистронезависимо и допускает подчёркивания, то есть MUMBLE_CONFIG_dbhost, MUMBLE_CONFIG_DBHOST и MUMBLE_CONFIG_DB_HOST эквивалентны. Entrypoint собирает из них конфиг на лету. Значения со спецсимволами (например запятыми) нужно дополнительно оборачивать в кавычки внутри кавычек. Альтернатива — docker/podman secrets из /run/secrets с теми же именами. Дополнительные переменные вне схемы: MUMBLE_SUPERUSER_PASSWORD (иначе пароль генерируется случайно при первом старте), MUMBLE_CUSTOM_CONFIG_FILE (при его задании ВСЕ MUMBLE_CONFIG_* игнорируются), MUMBLE_ACCEPT_UNKNOWN_SETTINGS, MUMBLE_CHOWN_DATA, MUMBLE_VERBOSE, PUID/PGID. Постоянные данные (по умолчанию SQLite) — том /data, процесс по умолчанию UID:GID 10000:10000. Порт 64738 нужно публиковать и по TCP, и по UDP.

Источники: <https://github.com/mumble-voip/mumble-docker> · <https://hub.docker.com/r/mumblevoip/mumble-server>

### [info] Форк реально эксплуатируется в 2026 году, и все консьюмеры пинят один и тот же коммит

Stieneee/mumble-discord-bridge (190 звёзд, MIT, последний push 2026-07-10, go 1.25.7) и blokadainfo/bib-comms — headless Mumble-клиент (MIT, push 2026-02-05, go 1.25.2) — оба в go.mod указывают ровно github.com/stieneee/gumble v0.0.0-20240610021017-a3449ae7108c. Это подтверждает и жизнеспособность пина, и то, что автор форка сам гоняет его в проекте с заметной аудиторией, то есть регрессии 1.5 будут замечены. Псевдоверсия подтверждена ответом Go-прокси: {"Version":"v0.0.0-20240610021017-a3449ae7108c","Time":"2024-06-10T02:10:17Z"}. Тегов и релизов в репозитории форка нет, issues отключены (open_issues_count 0), звёзд 0 — то есть канала для багрепортов наружу фактически нет, что усиливает аргумент за собственный форк.

Источники: <https://github.com/Stieneee/mumble-discord-bridge/blob/master/go.mod> · <https://github.com/blokadainfo/bib-comms> · <https://proxy.golang.org/github.com/stieneee/gumble/@latest>

### [info] Ветка udp в форке мертва — рассчитывать на UDP-голос в gumble нельзя

Ветка udp содержит унаследованные от layeh коммиты 65164058 «add OCB implementation» (2016-12-13) и 068d231a «udp WIP» (2017-09-15), отстаёт от master на 29 коммитов и опережает лишь на 2. Содержимое — реализация OCB2-шифрования и наброски в client.go/handlers.go, до рабочего состояния не доведено и за девять лет никем не тронуто. Отправка аудио в форке идёт исключительно через c.WritePacket(uint16(1), data), то есть TCP-туннель UDPTunnel. Вывод: план держать голос по TCP-туннелю остаётся единственно возможным на gumble, и на кадре 10 мс стоит заранее закладываться на TCP head-of-line blocking и джиттер-буфер на приёме.

Источники: <https://github.com/Stieneee/gumble/tree/udp> · <https://github.com/Stieneee/gumble/blob/master/gumble/conn.go>

### [info] На будущее: dchote/go-mumble-server — Go-сервер Mumble с UDP и переиспользуемой библиотекой протокола

MIT, последний push 2026-07-25, 8 звёзд, статус Beta. Заявлено: реализация сервера Mumble с нуля на Go, single static binary без рантайм-зависимостей, голос по UDP с AEAD-шифрованием и фолбэком в TCP-туннель, встроенный REST API со Swagger и Vue 3 UI, и главное — библиотека протокола вынесена в отдельный переиспользуемый пакет pkg/mumble, пригодный для написания клиентов. Потенциально это будущая замена gumble с настоящим UDP, но сейчас брать нельзя: бета, 8 звёзд, бинарники только под Linux amd64/arm64, единственный мейнтейнер. Стоит держать в поле зрения. Официальный альтернативный сервер mumble-voip/grumble (295 звёзд) фактически заброшен — последний push 2024-12-12.

Источники: <https://github.com/dchote/go-mumble-server> · <https://github.com/mumble-voip/grumble>

## Рекомендации

- Пинить github.com/stieneee/gumble v0.0.0-20240610021017-a3449ae7108c (коммит a3449ae7108c3d336c823c9911a3e61a92fe285f, master, 2024-06-10). Тегов и релизов в репозитории нет, поэтому только псевдоверсия по коммиту; ветку udp игнорировать — она мертва с 2017 года.
- Форкнуть Stieneee/gumble в свою организацию и влить туда ветку feat/opus-passthrough (4bdfe39e2829561a78c7f6444d3905d61a6f9ce0). У апстрим-форка отключены issues и 0 звёзд — канала для багрепортов нет, а править код всё равно придётся. Свой форк также закрывает обязательство MPL-2.0 публиковать изменённые файлы.
- Включить Config.OpusPassthrough и вести Opus самостоятельно: свой cgo-биндинг со статически слинкованным современным libopus вместо github.com/stieneee/gopus, который через `pkg-config: opus` тянет динамическую зависимость на системную библиотеку и ломает цель одного самодостаточного бинарника. Заодно получаете контроль над FEC, DTX и complexity.
- При включённом OpusPassthrough обязательно зарегистрировать заглушку кодека до подключения: gumble.RegisterAudioCodec(4, &stubCodec{}) с методами ID() → 4, NewEncoder() → nil, NewDecoder() → nil. Иначе client.go:132 отправит в Authenticate поле Opus: false. В passthrough-режиме NewEncoder/NewDecoder не вызываются, nil безопасен — проверено запуском.
- Обёртка над аудиоприёмом обязана полностью развязать read-loop: на каждый AudioStreamEvent поднимать свою горутину, немедленно перекладывать пакеты в буферизованный канал (например 20-50 кадров, то есть 200-500 мс) со стратегией drop-oldest при переполнении, и никогда не выполнять DSP или обращения к устройству вывода прямо в горутине, читающей event.C. Небуферизованный `ch <- event` в handlers.go:106-122 идёт синхронно из readRoutine и остановит вместе с аудио весь контрольный канал, включая Ping.
- Не использовать Client.AudioOutgoing()/OpusOutgoing() на горячем пути — они удерживают один кадр ради флага terminator и добавляют 10 мс. Вызывать client.Conn.WriteAudio(...) напрямую, самостоятельно ведя sequence и выставляя final=true на последнем кадре реплики.
- Сервер: mumblevoip/mumble-server:v1.5.915 (стабильный релиз 2026-07-19, образ обновлён 2026-07-27). Ветку 1.6 не брать до выхода стабильной — там новая схема БД с миграцией, Qt6 и изменённые CLI-флаги (-ini → --ini). Публиковать 64738 и по TCP, и по UDP, том на /data, следить за правами для UID:GID 10000:10000.
- Конфигурировать сервер через MUMBLE_CONFIG_* (имена регистронезависимы, подчёркивания допустимы: MUMBLE_CONFIG_DB_HOST == MUMBLE_CONFIG_dbhost). Пароль суперпользователя задать явно через MUMBLE_SUPERUSER_PASSWORD, иначе он сгенерируется случайно при первом старте и найти его можно будет только в логах. Отдельно проверить MUMBLE_CONFIG_BANDWIDTH: это потолок битрейта на пользователя, и при повышении качества Opus он станет ограничителем.
- Если остаётесь на встроенном энкодере gumble (без passthrough) — обязательно поднять Config.AudioDataBytes: значение по умолчанию AudioDefaultDataBytes = 40 байт на кадр 10 мс даёт всего 32 кбит/с, что для цели «чистый звук» маловато. В passthrough-режиме этот параметр не участвует, writeOpusAudio его не проверяет.
- Для отладки AEC использовать встроенный gumble.VoiceTargetLoopback (ID 31) — сервер возвращает отправленный звук обратно клиенту, что даёт готовый петлевой тракт без второго участника.
- Channel listeners 1.5 при необходимости реализовывать вручную: высокоуровневого API нет, но поля есть в .proto (UserState: listening_channel_add = 21, listening_channel_remove = 22, listening_volume_adjustment = 23) и отправляются через экспортированный client.Conn.WriteProto(&MumbleProto.UserState{...}). Whisper/shout уже готов — gumble/voicetarget.go.
- Заложить в приёмный тракт джиттер-буфер: голос идёт по TCP-туннелю (UDP в gumble нет и не появится), а значит гарантированы head-of-line blocking и всплески джиттера при потерях в сети — на кадре 10 мс это слышно сразу.
