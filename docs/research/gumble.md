# Верификация: gumble (Mumble-клиент для Go)

## Резюме

Библиотека layeh.com/gumble формально жива (репозиторий не архивирован, vanity-домен layeh.com отдаёт go-import, `go get` работает), но фактически заморожена: последний коммит в master — d1df60a3cc14 от 2022-12-05 с сообщением "remove FUNDING.yml", а последнее функциональное изменение — 2020-03-25. Тегов нет вообще, модуль ставится только псевдоверсией v0.0.0-20221205141517-d1df60a3cc14. Самый живой форк — github.com/stieneee/gumble (последний коммит 2024-06-10): протокол Mumble 1.5, protobuf-формат аудиопакетов, закрытие аудиоканалов при отключении пользователя, миграция на google.golang.org/protobuf; он используется в живом проекте mumble-discord-bridge. Ключевые технические ограничения upstream, проверенные по исходникам: голос идёт ИСКЛЮЧИТЕЛЬНО через TCP-тоннель (UDPTunnel, тип пакета 1) — UDP voice, OCB-AES128 и CryptSetup не реализованы совсем; входящие аудиоканалы (по одному на говорящего) никогда не закрываются и отправка в них блокирующая, что подвешивает read-loop клиента; сабпакет opus вендорит исходники opus-1.1.2 только на amd64/386, а на arm64 требует системный libopus + pkg-config (проверено эмпирически сборкой на darwin/arm64 go1.26.5). Лицензия MPL-2.0 — пофайловый копилефт, немодифицированная зависимость безопасна для закрытого продукта.

## Находки

### [CRITICAL] КРИТИЧНО: отправка в аудиоканал блокирующая и выполняется из read-loop клиента

В `handleUDPTunnel` доставка пакета сделана как `ch <- &event` в НЕБУФЕРИЗОВАННЫЙ канал, и этот код выполняется в единственной горутине `Client.readRoutine()`, которая читает ВСЕ TCP-сообщения протокола:

```go
c.volatile.Lock()
for item := c.Config.AudioListeners.head; item != nil; item = item.next {
	c.volatile.Unlock()
	ch := item.streams[user]
	if ch == nil {
		ch = make(chan *AudioPacket)
		item.streams[user] = ch
		event := AudioStreamEvent{Client: c, User: user, C: ch}
		item.listener.OnAudioStream(&event)
	}
	ch <- &event
	c.volatile.Lock()
}
```

Последствие: если ваш обработчик не вычитывает `AudioStreamEvent.C` достаточно быстро (или вообще не вычитывает, например пока не готов ресемплер/энкодер), блокируется весь read-loop — перестают обрабатываться Ping, UserState, TextMessage и т.д. Через `Conn.Timeout = 20s` соединение отвалится по read deadline. Дополнительно, `OnAudioStream` вызывается синхронно из того же read-loop, поэтому в нём нельзя делать ничего долгого — только запустить горутину-потребителя и вернуть управление.

Источники: <https://github.com/layeh/gumble/blob/master/gumble/handlers.go> · <https://github.com/layeh/gumble/blob/master/gumble/client.go> · <https://github.com/layeh/gumble/blob/master/gumble/conn.go>

### [MAJOR] Upstream layeh/gumble заморожен: последний функциональный коммит — март 2020

Репозиторий НЕ архивирован (GitHub API: `"archived": false`), 180 звёзд, 57 форков, 21 открытый issue. Последний коммит в master: `d1df60a3cc14e2d47e2b46d0d887dc79c2ff64c9`, 2022-12-05, сообщение "remove FUNDING.yml" — чисто косметический. Предыдущие коммиты: 2020-08-18 "add logo", 2020-05-28 "Create FUNDING.yml", 2020-03-25 "add PermissionDeniedChannelCountLimit" — то есть последнее изменение кода датируется 25 марта 2020 года. Git-тегов нет: `https://proxy.golang.org/layeh.com/gumble/@v/list` возвращает пустой ответ, `@latest` отдаёт только псевдоверсию `v0.0.0-20221205141517-d1df60a3cc14`. Vanity-домен работает: `curl 'https://layeh.com/gumble?go-get=1'` отдаёт `<meta name="go-import" content="layeh.com/gumble git https://github.com/layeh/gumble">`. Проверено эмпирически: `go get layeh.com/gumble/gumble layeh.com/gumble/opus` успешно скачивает модуль. Автор — Tim Cooper.

Источники: <https://github.com/layeh/gumble> · <https://api.github.com/repos/layeh/gumble> · <https://api.github.com/repos/layeh/gumble/commits?per_page=5> · <https://proxy.golang.org/layeh.com/gumble/@latest> · <https://pkg.go.dev/layeh.com/gumble/gumble>

### [MAJOR] Самый живой форк — github.com/stieneee/gumble (протокол 1.5 + фикс аудиоканалов)

В сети форков (57 шт.) реально развивается один: `Stieneee/gumble`, module path `github.com/stieneee/gumble`, go 1.20. Его git-лог поверх upstream: 2024-06-02 "update to mumble protocol 1.5", 2024-06-02 "bump version and reported client name", 2024-06-05 "support version 1.5 audio packet", 2024-06-08 "close audio channels when user disconnects" (`f66c2ec91e894d0eb86c5b58312dc0f70e43dc0f`), 2024-06-09 "fix lock issue in userRemove handler", 2024-06-10 "ping go vet fix". Он объявляет `ClientVersionV1 uint32 = 1<<16|5<<8|1` и `ClientVersionV2 uint64 = 1<<48|5<<32|1<<16` (Mumble 1.5.1) и мигрировал на `google.golang.org/protobuf v1.34.1` вместо устаревшего `github.com/golang/protobuf v1.3.1`. Живой потребитель: `github.com/stieneee/mumble-discord-bridge` (go 1.25.7) требует `github.com/stieneee/gumble v0.0.0-20240610021017-a3449ae7108c`. Остальные форки слабее: `talkkonnect/gumble` — последний коммит 2022-08-21, протокол 1.4, go.mod отсутствует (GOPATH-стиль); `iotku/gumble` — коммиты до 2026-01, но экспериментальные ("Attempt to port migrate opus depenency from gopus to hraban/opus", "Add println to verify modified opus package is actually being loaded"), module path остался `layeh.com/gumble`; `DanaDynamics/gumble` (2026-06-01) — только подмена зависимости gopus на свою; `dchote/gumble` — последние коммиты 2017 года. Ни один форк, включая Stieneee, НЕ добавляет UDP voice.

Источники: <https://github.com/Stieneee/gumble> · <https://github.com/Stieneee/gumble/commit/f66c2ec91e894d0eb86c5b58312dc0f70e43dc0f> · <https://raw.githubusercontent.com/Stieneee/gumble/master/go.mod> · <https://raw.githubusercontent.com/Stieneee/mumble-discord-bridge/master/go.mod> · <https://github.com/iotku/gumble> · <https://github.com/talkkonnect/gumble> · <https://api.github.com/repos/layeh/gumble/forks>

### [MAJOR] Upstream никогда не закрывает аудиоканалы говорящих — утечка горутин и map

Docstring `AudioListener` обещает: "It is the implementer's responsibility to continuously process AudioStreamEvent.C until it is closed". Но в upstream канал НЕ закрывается никогда. Проверено grep'ом по всему репозиторию: единственные вызовы `close()` — это `close(c.connect)` (handlers.go:276), `close(c.end)` (client.go:245) и `defer close(end)` в ping.go:67. В коде `handleUDPTunnel` явно стоит комментарий-заглушка: `// TODO: decoder pool` и `// TODO: de-reference after stream is done`.

Последствия: (а) горутина-потребитель на каждого когда-либо говорившего пользователя живёт вечно, читая из канала, в который больше никто не пишет; (б) `audioEventItem.streams map[*User]chan *AudioPacket` растёт монотонно; (в) `user.decoder` (состояние Opus-декодера) тоже не освобождается. На долгоживущем боте с ротацией пользователей это накопительная утечка.

Форк Stieneee исправляет это коммитом `f66c2ec9` "close audio channels when user disconnects" (вынес доставку в замыкание `sendEvent` и добавил закрытие в обработчике userRemove), плюс следующим коммитом "fix lock issue in userRemove handler".

Источники: <https://github.com/layeh/gumble/blob/master/gumble/handlers.go> · <https://github.com/layeh/gumble/blob/master/gumble/audio.go> · <https://github.com/Stieneee/gumble/commit/f66c2ec91e894d0eb86c5b58312dc0f70e43dc0f>

### [MAJOR] layeh.com/gumble/opus — тонкая обёртка над layeh.com/gopus; libopus вендорится ТОЛЬКО на amd64/386

Сабпакет `opus` состоит из одного файла на 75 строк: он делает `gumble.RegisterAudioCodec(4, Codec)` в `init()` и делегирует всё в `layeh.com/gopus` (github.com/layeh/gopus), закреплённый в go.mod gumble как `layeh.com/gopus v0.0.0-20161224163843-0ebf989153aa`. Энкодер создаётся как `gopus.NewEncoder(48000, 1, gopus.Voip)` + `SetBitrate(gopus.BitrateMaximum)`; декодер вызывает `Decode(data, frameSize, false)` — FEC выключен.

В gopus ДВА взаимоисключающих cgo-варианта:

1. `opus_nonshared.go`, тег `// +build amd64,cgo 386,cgo` — ВЕНДОРИТ исходники opus 1.1.2 (каталог `opus-1.1.2/`, 292 файла .c/.h) и подключает .c-файлы напрямую через `#include` в cgo-преамбуле: `#cgo CFLAGS: -Iopus-1.1.2/include -Iopus-1.1.2/celt -Iopus-1.1.2/silk ...`. Системный libopus НЕ нужен.
2. `opus_shared.go`, тег `// +build !amd64,!386,cgo` — `#cgo !nopkgconfig pkg-config: opus` и `#include <opus.h>`. Требует установленный dev-пакет libopus и pkg-config. Это относится к **arm64** (Apple Silicon, Raspberry Pi, AWS Graviton) и вообще ко всему, кроме amd64/386.

Эмпирическая проверка на darwin/arm64, go1.26.5:
- сборка по умолчанию → `layeh.com/gopus: exec: "pkg-config": executable file not found in $PATH`;
- `-tags nopkgconfig` без флагов → `fatal error: 'opus.h' file not found`;
- `-tags nopkgconfig` + `CGO_CFLAGS=-I/opt/homebrew/include/opus CGO_LDFLAGS='-L/opt/homebrew/lib -lopus'` → собирается и работает;
- `GOARCH=amd64 CGO_ENABLED=1` → собирается из вендоренных исходников без системного libopus (только C-предупреждения, exit 0).

README gopus подтверждает: "opus development library (only on platforms where the shared library is used)".

Источники: <https://github.com/layeh/gumble/blob/master/opus/opus.go> · <https://github.com/layeh/gopus/blob/master/opus_nonshared.go> · <https://github.com/layeh/gopus/blob/master/opus_shared.go> · <https://github.com/layeh/gopus/blob/master/README.md> · <https://github.com/layeh/gumble/blob/master/go.mod> · <https://pkg.go.dev/layeh.com/gopus>

### [MAJOR] CGO_ENABLED=0 полностью ломает сборку: cgo обязателен

Оба файла gopus имеют в build-тегах `cgo`, поэтому при `CGO_ENABLED=0` не подходит ни один. Проверено эмпирически:

```
package buildtest
	imports layeh.com/gumble/opus
	imports layeh.com/gopus: build constraints exclude all Go files in .../layeh.com/gopus@v0.0.0-20161224163843-0ebf989153aa
```

Следствия для M0: статическая линковка «pure Go» невозможна, кросс-компиляция требует настроенного кросс-тулчейна C, а в Docker нужен образ с компилятором (не `scratch` для сборки) и, для не-amd64, `libopus-dev` + `pkg-config`. Также build-теги записаны в СТАРОМ синтаксисе (`// +build` без парной строки `//go:build`) — на go1.26 это ещё работает (проверено), но это признак незамороженной совместимости в будущем.

Источники: <https://github.com/layeh/gopus/blob/master/opus_nonshared.go> · <https://github.com/layeh/gopus/blob/master/opus_shared.go>

### [MAJOR] UDP voice channel НЕ поддерживается: весь голос идёт через TCP tunnel

Проверено grep'ом по всему репозиторию (исключая сгенерированный MumbleProto): нет ни одного вхождения `net.DialUDP`, `net.ListenUDP`, `*net.UDPConn` для голоса, и нет ни реализации OCB-AES128, ни обработки `CryptSetup`.

Что есть на самом деле:
- Транспорт один: `tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)` в `DialWithDialer`; `Conn` в conn.go — обёртка над этим TCP/TLS-соединением.
- Исходящее аудио: `Conn.WriteAudio(...)` формирует сырой Mumble-аудиопакет и пишет его через `writeHeader(1, ...)` — тип сообщения **1 = UDPTunnel**.
- Входящее аудио: в таблице `handlers` индекс 1 — это `(*Client).handleUDPTunnel`.
- Единственное использование UDP во всей библиотеке — `gumble.Ping()` в `gumble/ping.go` (`net.DialTimeout("udp", address, timeout)`), это неаутентифицированный публичный server-info ping, голос он не переносит.

По спецификации Mumble туннелирование через TCP — легитимный режим («If the UDP channel isn't available the voice packets can be transmitted through the TCP transport used for the control channel»), и docs даже рекомендуют его как первый шаг реализации. Но цена: шифрование только TLS (а не OCB-AES128), head-of-line blocking TCP, джиттер от ретрансмиссий, повышенная задержка. Плюс серверная эвристика: получив туннелированный пакет, сервер считает клиента неспособным к UDP и сам переключается на TCP для него. Ни один из проверенных форков, включая Stieneee, UDP-путь не добавляет.

Источники: <https://github.com/layeh/gumble/blob/master/gumble/conn.go> · <https://github.com/layeh/gumble/blob/master/gumble/handlers.go> · <https://github.com/layeh/gumble/blob/master/gumble/ping.go> · <https://github.com/layeh/gumble/blob/master/gumble/client.go> · <https://github.com/mumble-voip/mumble/blob/master/docs/dev/network-protocol/voice_data.md>

### [MAJOR] gumbleutil.AutoBitrate может выставить отрицательный AudioDataBytes и уронить процесс паникой

Расчёт в `gumbleutil/bitrate.go` не имеет нижней границы:

```go
const safety = 5
interval := e.Client.Config.AudioInterval
dataBytes := (*e.MaximumBitrate / (8 * (int(time.Second/interval) + safety))) - 32 - 10
e.Client.Config.AudioDataBytes = dataBytes
```

При `AudioInterval = 10ms` знаменатель равен 840, то есть `dataBytes = MaxBitrate/840 - 42`, и значение становится отрицательным при `MaxBitrate < 35280` бит/с. Дальше это значение попадает в `AudioBuffer.writeAudio` → `encoder.Encode(a, len(a), dataBytes)` → в gopus `data := make([]byte, maxDataBytes)`, что при отрицательной длине даёт runtime-панику `makeslice: len out of range` в горутине `AudioOutgoing` — то есть краш всего процесса. Форк iotku прямо чинит это коммитом "fix(bitrate): check that we don't pass negative number into make()". Смягчающее обстоятельство: `AutoBitrate` подключается только явно (`config.Attach(gumbleutil.AutoBitrate)`) или автоматически внутри `gumbleutil.Main()`.

Источники: <https://github.com/layeh/gumble/blob/master/gumbleutil/bitrate.go> · <https://github.com/layeh/gumble/blob/master/gumbleutil/main.go> · <https://github.com/layeh/gumble/blob/master/gumble/audio.go> · <https://github.com/layeh/gopus/blob/master/opus_nonshared.go> · <https://github.com/iotku/gumble/commits/master>

### [minor] Вендоренная копия libopus — версия 1.1.2 (2015 г.), заморожена

Каталог `opus-1.1.2/` был импортирован в gopus коммитом 17c8874 "import opus-1.1.2" от 2016-03-10 и с тех пор не обновлялся. Последний коммит во всём gopus — `1ee02d434e32` от 2021-05-01 "add -lm linker flag on openbsd"; версия, закреплённая в go.mod gumble (`0ebf989153aa`, 2016-12-24), даже старше и не содержит фикса для OpenBSD. Тегов в gopus тоже нет. То есть на amd64 в ваш бинарь статически линкуется кодек 2015 года выпуска, минуя системный пакетный менеджер и его обновления безопасности. На arm64, наоборот, используется системный libopus, который обновляется дистрибутивом — то есть кодек в проде будет разным на разных архитектурах.

Источники: <https://github.com/layeh/gopus> · <https://github.com/layeh/gopus/commits/master> · <https://github.com/layeh/gumble/blob/master/go.mod>

### [minor] Upstream объявляет протокол 1.3.0 и выбрасывает версию сервера — нет поддержки protobuf-аудио Mumble 1.5

В upstream: `const ClientVersion = 1<<16 | 3<<8 | 0` (то есть 1.3.0), и обработчик версии сервера ничего не сохраняет:

```go
func (c *Client) handleVersion(buffer []byte) error {
	var packet MumbleProto.Version
	if err := proto.Unmarshal(buffer, &packet); err != nil {
		return err
	}
	return nil
}
```

Поэтому клиент не может выбрать формат аудиопакета в зависимости от версии сервера. Mumble 1.5 ввёл новый protobuf-формат аудио — сообщение `MumbleUDP.Audio` (поля `target`/`context`, `sender_session`, `frame_number`, `opus_data`, `positional_data`, `volume_adjustment`) в `src/MumbleUDP.proto`. Форк Stieneee сохраняет `c.Version` и ветвится: `if c.Version.Version < uint64(1<<48|5<<32) { /* old varint decode */ } else { /* decode newer protobuf */ }`, объявляя себя как 1.5.1. Наследный (legacy) формат по-прежнему документирован в dev-доках Mumble как базовый, и mumble-discord-bridge на форке работает с современными серверами, но совместимость upstream-версии с целевым сервером 1.5.x нужно подтверждать e2e-тестом, а не предположением.

Источники: <https://github.com/layeh/gumble/blob/master/gumble/client.go> · <https://github.com/layeh/gumble/blob/master/gumble/handlers.go> · <https://github.com/mumble-voip/mumble/blob/master/src/MumbleUDP.proto> · <https://github.com/mumble-voip/mumble/blob/master/docs/dev/network-protocol/voice_data.md> · <https://github.com/Stieneee/gumble>

### [minor] Зависимость от устаревшего github.com/golang/protobuf v1.3.1, но апгрейд до shim v1.5.4 работает

go.mod gumble требует `github.com/golang/protobuf v1.3.1` — это pre-APIv2 модуль, который Go сам помечает как deprecated: `go: module github.com/golang/protobuf is deprecated: Use the "google.golang.org/protobuf" module instead.` Сгенерированный `gumble/MumbleProto/Mumble.pb.go` тоже старого стиля (`import proto "github.com/golang/protobuf/proto"`).

Эмпирическая проверка: если в проекте уже есть `google.golang.org/protobuf`, MVS корректно поднимает golang/protobuf до APIv2-шима и всё компилируется. Протестировано с `github.com/golang/protobuf v1.5.4` + `google.golang.org/protobuf v1.36.12` — сборка проходит без ошибок. То есть конфликта протобаферов при интеграции нет, но нужен явный `require github.com/golang/protobuf v1.5.4` в go.mod проекта, иначе останется v1.3.1 с собственным реестром типов. Форк Stieneee эту проблему уже решил, перейдя на `google.golang.org/protobuf v1.34.1`.

Источники: <https://github.com/layeh/gumble/blob/master/go.mod> · <https://github.com/layeh/gumble/blob/master/gumble/MumbleProto/Mumble.pb.go> · <https://raw.githubusercontent.com/Stieneee/gumble/master/go.mod>

### [minor] Нет jitter buffer, PLC и FEC; заметные аллокации на каждый пакет

В `handleUDPTunnel` номер последовательности читается и явно отбрасывается с комментарием `// TODO: use in jitter buffer` — джиттер-буфера нет вообще, пакеты отдаются listener'у в порядке прихода. Packet loss concealment не реализован (при потере пакета декодер не вызывается с nil-данными). FEC отключён: `gumble/opus` вызывает `d.Decoder.Decode(data, frameSize, false)`, где последний аргумент — это флаг FEC.

Аллокации на КАЖДЫЙ входящий пакет (при 10 мс интервале это 100 раз в секунду на говорящего): `codec.NewDecoder()` создаёт декодер по пользователю (кэшируется в `user.decoder`, но с TODO `// TODO: decoder pool`), `Decode` аллоцирует `make([]int16, channels*2880)` и возвращает срез, плюс аллоцируются `AudioPacket` и `&VoiceTarget{ID: ...}`. На исходящей стороне `gopus.Encode` делает `make([]byte, maxDataBytes)` на каждый кадр, а референсные потоки в gumbleffmpeg/gumbleopenal ещё и создают `make([]int16, frameSize)` на каждый тик.

Источники: <https://github.com/layeh/gumble/blob/master/gumble/handlers.go> · <https://github.com/layeh/gumble/blob/master/opus/opus.go> · <https://github.com/layeh/gopus/blob/master/opus_nonshared.go> · <https://github.com/layeh/gumble/blob/master/gumbleffmpeg/stream.go>

### [info] gumble.Config: поле аудиоинтервала называется AudioInterval (time.Duration), рядом AudioDataBytes

Полное определение (`gumble/config.go`):

```go
type Config struct {
	Username string
	Password string
	Tokens   AccessTokens

	// AudioInterval is the interval at which audio packets are sent. Valid
	// values are: 10ms, 20ms, 40ms, and 60ms.
	AudioInterval time.Duration
	// AudioDataBytes is the number of bytes that an audio frame can use.
	AudioDataBytes int

	Listeners      Listeners
	AudioListeners AudioListeners
}
```

Методы: `NewConfig() *Config` (ставит `AudioInterval = AudioDefaultInterval`, `AudioDataBytes = AudioDefaultDataBytes`), `(*Config) Attach(l EventListener) Detacher`, `(*Config) AttachAudio(l AudioListener) Detacher`, `(*Config) AudioFrameSize() int` = `int(c.AudioInterval/AudioDefaultInterval) * AudioDefaultFrameSize`.

Константы из `gumble/audio.go`: `AudioSampleRate = 48000`, `AudioDefaultInterval = 10 * time.Millisecond`, `AudioDefaultFrameSize = 480`, `AudioMaximumFrameSize = 2880` (60 мс), `AudioDefaultDataBytes = 40`, `AudioChannels = 1`.

Проверено эмпирически: собранный бинарь печатает `10ms 40 480` для `NewConfig()`. Подключение: `gumble.Dial(addr, config)` = `DialWithDialer(new(net.Dialer), addr, config, nil)`; полная форма `DialWithDialer(dialer *net.Dialer, addr string, config *Config, tlsConfig *tls.Config) (*Client, error)` — возвращает управление только после синхронизации состояния сервера. `gumble.DefaultPort = 64738`.

Источники: <https://github.com/layeh/gumble/blob/master/gumble/config.go> · <https://github.com/layeh/gumble/blob/master/gumble/audio.go> · <https://github.com/layeh/gumble/blob/master/gumble/client.go> · <https://pkg.go.dev/layeh.com/gumble/gumble#Config>

### [info] Исходящий звук: Client.AudioOutgoing() chan<- AudioBuffer, с задержкой ровно в один кадр

Сигнатура (`gumble/client.go`): `func (c *Client) AudioOutgoing() chan<- AudioBuffer`, где `type AudioBuffer []int16` — моно PCM, 48 кГц, signed 16-bit. Документация метода: канал ОБЯЗАН быть закрыт по окончании потока, и одновременно должен быть открыт только один канал.

Важная деталь реализации: горутина держит предыдущий буфер и отправляет его только при получении следующего — то есть в пайплайне всегда есть лаг ровно в один кадр, а последний буфер уходит с флагом `final = true` (что вызывает `encoder.Reset()`):

```go
go func() {
	var seq int64
	previous := <-ch
	for p := range ch {
		previous.writeAudio(c, seq, false)
		previous = p
		seq = (seq + 1) % math.MaxInt32
	}
	if previous != nil {
		previous.writeAudio(c, seq, true)
	}
}()
```

Канонический паттерн отправки (из `gumbleffmpeg/stream.go:157` и `gumbleopenal/stream.go:119`): взять `interval := client.Config.AudioInterval` и `frameSize := client.Config.AudioFrameSize()`, `outgoing := client.AudioOutgoing()`, `defer close(outgoing)`, крутить `time.NewTicker(interval)` и на каждый тик слать `outgoing <- gumble.AudioBuffer(int16Buffer)` длиной ровно `frameSize`. Кодирование внутри: `encoder.Encode(a, len(a), client.Config.AudioDataBytes)`.

Источники: <https://github.com/layeh/gumble/blob/master/gumble/client.go> · <https://github.com/layeh/gumble/blob/master/gumble/audio.go> · <https://github.com/layeh/gumble/blob/master/gumbleffmpeg/stream.go> · <https://github.com/layeh/gumble/blob/master/gumbleopenal/stream.go>

### [info] Входящий звук: Config.AttachAudio + AudioListener, отдельный канал на каждого говорящего

Регистрация: `func (c *Config) AttachAudio(l AudioListener) Detacher` — алиас `c.AudioListeners.Attach(l)`.

Интерфейс и типы (`gumble/audio.go`):

```go
type AudioListener interface {
	OnAudioStream(e *AudioStreamEvent)
}

type AudioStreamEvent struct {
	Client *Client
	User   *User
	C      <-chan *AudioPacket
}

type AudioPacket struct {
	Client *Client
	Sender *User
	Target *VoiceTarget
	AudioBuffer          // встроенный []int16 — декодированный PCM
	HasPosition bool
	X, Y, Z     float32
}
```

Механика (`gumble/handlers.go`, `handleUDPTunnel`, строки ~158-177): на каждый входящий аудиопакет по паре (listener, говорящий User) лениво создаётся НЕБУФЕРИЗОВАННЫЙ `chan *AudioPacket`, хранится в `audioEventItem.streams map[*User]chan *AudioPacket`, и при первом пакете от этого пользователя синхронно вызывается `item.listener.OnAudioStream(&event)` — то есть на каждого нового говорящего вы получаете свой `AudioStreamEvent` со своим каналом. Один и тот же `*AudioPacket` передаётся всем подписанным listener'ам (указатель общий, копии нет). Декодирование идёт с `AudioMaximumFrameSize` (2880 сэмплов), Opus-only: если тип пакета не 4, возвращается `errUnsupportedAudio`. Позиционные данные заполняются, если после аудио остаётся ровно 12 байт (3 × float32 LE).

Источники: <https://github.com/layeh/gumble/blob/master/gumble/audio.go> · <https://github.com/layeh/gumble/blob/master/gumble/config.go> · <https://github.com/layeh/gumble/blob/master/gumble/handlers.go> · <https://github.com/layeh/gumble/blob/master/gumble/audiolisteners.go> · <https://pkg.go.dev/layeh.com/gumble/gumble#AudioListener>

### [info] Лицензия MPL-2.0: безопасна как немодифицированная зависимость закрытого продукта

gumble распространяется под Mozilla Public License 2.0 (файл LICENSE в корне; GitHub API: `"spdx_id": "MPL-2.0"`; README: "## License\nMPL 2.0").

Что это значит практически:
- MPL-2.0 — это пофайловый (file-level, «слабый») копилефт. Копилефт распространяется только на сами файлы, покрытые лицензией, а не на ваш код.
- Использовать gumble как Go-модуль и статически влинковать его в закрытый бинарь можно; MPL FAQ прямо разрешает комбинировать MPL-код с проприетарными файлами в «Larger Work» без раскрытия вашего кода.
- Обязательства возникают, если вы МОДИФИЦИРУЕТЕ файлы gumble (форк, патч, replace с правками): изменённые файлы остаются под MPL-2.0, и их исходники нужно предоставить получателям вашего бинаря.
- В любом случае нельзя удалять/менять лицензионные заголовки и нужно уведомить получателей, где взять исходники MPL-частей.
- Go-специфика: поскольку сборка статическая, распространение бинаря = распространение gumble, поэтому уведомление о MPL-части и ссылка на исходники нужны даже без модификаций.

Источники: <https://github.com/layeh/gumble/blob/master/LICENSE> · <https://github.com/layeh/gumble/blob/master/README.md> · <https://api.github.com/repos/layeh/gumble> · <https://www.mozilla.org/en-US/MPL/2.0/> · <https://www.mozilla.org/en-US/MPL/2.0/FAQ/>

### [info] Транзитивные лицензии разнородны: PD + BSD-3 (Xiph) + BSD-3 (protobuf)

Итоговый лицензионный набор при использовании `layeh.com/gumble/gumble` + `layeh.com/gumble/opus`:
- `layeh.com/gumble` — MPL-2.0.
- `layeh.com/gopus` — public domain / Unlicense-стиль ("This is free and unencumbered software released into the public domain.").
- Вендоренные исходники внутри gopus — `opus-1.1.2/COPYING`, BSD-3-Clause, Copyright Xiph.Org, Skype Limited, Octasic, Jean-Marc Valin, Timothy B. Terriberry, CSIRO, Gregory Maxwell, Mark Borgerding, Erik de Castro Lopo. Требует сохранения текста копирайта в бинарной поставке — на amd64 этот код физически попадает в ваш бинарь, значит атрибуция обязательна.
- `github.com/golang/protobuf` — BSD-3-Clause.

Важно: `github.com/dchote/go-openal` числится в go.mod gumble, но при импорте только `gumble/gumble` и `gumble/opus` он НЕ подтягивается (проверено: `go get` добавил только golang/protobuf, gopus и gumble) — благодаря module graph pruning. Он появится только если импортировать `gumbleopenal`.

Источники: <https://github.com/layeh/gopus/blob/master/LICENSE> · <https://github.com/layeh/gopus/blob/master/opus-1.1.2/COPYING> · <https://github.com/layeh/gumble/blob/master/go.mod> · <https://github.com/layeh/gumble/blob/master/LICENSE>

### [info] Client и всё связанное с ним потоконебезопасны по контракту

Из `gumble/doc.go`: "As a general rule, a Client everything that is associated with it (Users, Channels, Config, etc.), is thread-unsafe. Accessing or modifying those structures should only be done from inside of an event listener or via Client.Do." Сигнатура: `func (c *Client) Do(f func())` — берёт `c.volatile.RLock()` на время вызова.

Следствия для M0: `Config` (включая `AudioDataBytes`, списки listener'ов) нужно полностью настроить ДО `Dial`; менять его на лету можно только внутри listener'а или через `Client.Do`. Обход `Users`/`Channels` из своих горутин без `Client.Do` — гонка. Обязательно гонять тесты с `-race`. Дополнительно в doc.go указано, что для гарантии передачи аудио на murmur-сервере рекомендуется `opusthreshold=0` — этот параметр актуален для старых серверов, современные Mumble поддерживают только Opus.

Источники: <https://github.com/layeh/gumble/blob/master/gumble/doc.go> · <https://github.com/layeh/gumble/blob/master/gumble/client.go> · <https://pkg.go.dev/layeh.com/gumble/gumble>

## Рекомендации

- Принять решение upstream vs форк ДО начала M0. Если нужен протокол Mumble 1.5 и корректное закрытие аудиопотоков — брать github.com/stieneee/gumble (последний коммит 2024-06-10, живой потребитель mumble-discord-bridge). Если хватает legacy-протокола 1.3 — брать upstream layeh.com/gumble, но сразу планировать собственный форк, потому что код не сопровождается с марта 2020.
- Зафиксировать зависимость псевдоверсией явно: тегов у layeh.com/gumble и layeh.com/gopus не существует, поэтому в go.mod нужно записать `layeh.com/gumble v0.0.0-20221205141517-d1df60a3cc14` и закоммитить go.sum. Дополнительно поднять `github.com/golang/protobuf` до v1.5.4 (APIv2-шим), иначе останется v1.3.1 со своим реестром типов.
- Обязательно обернуть AudioListener неблокирующим шимом. В OnAudioStream нельзя делать ничего тяжёлого — только запустить горутину, которая мгновенно перекладывает *AudioPacket в собственный буферизованный канал или ring buffer с политикой drop-oldest. Иначе блокирующая отправка `ch <- &event` из readRoutine подвесит весь протокольный read-loop и соединение отвалится по 20-секундному read deadline.
- Реализовать собственный жизненный цикл аудиопотоков: upstream НИКОГДА не закрывает канал AudioStreamEvent.C. Нужно подписаться на UserChange с флагом Disconnected (и/или ввести таймаут тишины, скажем 2-3 секунды без пакетов) и самостоятельно останавливать горутину-потребителя и освобождать состояние по пользователю. Иначе получим монотонный рост горутин, map streams и Opus-декодеров.
- Настроить сборку под cgo с самого начала M0: CGO_ENABLED=1 обязателен (при CGO_ENABLED=0 сборка падает с 'build constraints exclude all Go files'). Для amd64/386 ничего ставить не надо — opus 1.1.2 вендорится. Для arm64 (Apple Silicon у разработчиков, Raspberry Pi, AWS Graviton) нужны libopus-dev и pkg-config; при их отсутствии есть запасной путь `-tags nopkgconfig` с ручными CGO_CFLAGS/CGO_LDFLAGS. В multi-arch Docker это означает разные build stage и разный набор пакетов на архитектуру.
- Проверить в M0 приемлемость TCP-туннеля по задержке. Голос гарантированно идёт по TLS/TCP (UDPTunnel, тип пакета 1) — UDP voice, OCB-AES128 и CryptSetup в gumble не реализованы вообще и ни в одном форке не добавлены. Снять метрики RTT и джиттера на целевой сети до того, как архитектура M0 зафиксируется. Реализация UDP-пути — это отдельная крупная задача (OCB-AES128 + обмен CryptSetup + ping-эвристика), в M0 её закладывать не стоит.
- Не подключать gumbleutil.AutoBitrate без клампа (и не использовать gumbleutil.Main, который подключает его сам). При MaxBitrate сервера ниже ~35.3 кбит/с формула даёт отрицательный AudioDataBytes, что приводит к панике `makeslice: len out of range` в gopus.Encode и падению процесса. Проще выставлять Config.AudioDataBytes самому с явным минимумом (например, clamp в диапазон 20..120).
- Реализовать отправку строго по канону: тикер на Config.AudioInterval, кадры ровно Config.AudioFrameSize() сэмплов int16 (моно, 48 кГц), один открытый канал AudioOutgoing одновременно, обязательный close(outgoing) в defer. Учесть в расчёте end-to-end задержки, что AudioOutgoing внутри добавляет ровно один кадр буферизации, а последний кадр помечается final и сбрасывает состояние энкодера.
- Учесть потоконебезопасность: полностью сконфигурировать Config (Username, AudioInterval, AudioDataBytes, Attach/AttachAudio) ДО вызова Dial; любые последующие обращения к Client, Users, Channels, Config — только из listener'а или через Client.Do. Включить `go test -race` в CI и добавить нагрузочный сценарий с несколькими одновременно говорящими, чтобы поймать блокировки read-loop.
- Оценить замену gopus на более живой биндинг (например, github.com/hraban/opus поверх системного libopus): интерфейс кодека в gumble открытый — достаточно реализовать gumble.AudioCodec/AudioEncoder/AudioDecoder и вызвать gumble.RegisterAudioCodec(4, codec) вместо импорта layeh.com/gumble/opus. Это унифицирует поведение между amd64 и arm64 (сейчас на amd64 линкуется замороженный opus 1.1.2 от 2015 года, а на arm64 — системный libopus) и снимет зависимость от gopus, не обновлявшегося с 2021 года. Форк iotku уже пробовал этот путь.
- По лицензиям: держать gumble как НЕмодифицированную зависимость — тогда MPL-2.0 не накладывает обязательств на ваш код (пофайловый копилефт, статическая линковка в закрытый бинарь разрешена). Если придётся патчить (а это вероятно — закрытие каналов, кламп битрейта, протокол 1.5), вести изменения в публичном форке и оставить на него ссылку, потому что изменённые файлы остаются под MPL-2.0 и их исходники нужно предоставлять получателям бинаря. Собрать файл NOTICE: MPL-2.0 (gumble), BSD-3-Clause Xiph.Org (вендоренный opus 1.1.2 — он реально попадает в бинарь на amd64), BSD-3-Clause (protobuf), public domain (gopus).
- Сделать в M0 e2e smoke-тест против той версии Mumble-сервера, которая будет в проде (1.4.x / 1.5.x). Upstream объявляет себя как клиент 1.3.0 и полностью выбрасывает Version-пакет сервера, поэтому не может переключиться на protobuf-формат аудио MumbleUDP.Audio из 1.5. Совместимость через legacy-формат ожидаема, но должна быть подтверждена фактическим прогоном приёма и передачи голоса, а не предположением.
