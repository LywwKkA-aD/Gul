# Верификация: malgo / miniaudio

## Резюме

malgo — живой, но неспешно обновляемый проект (последний тег v0.11.26 от 2026-08-18, вендорится miniaudio 0.11.25). Full-duplex поддерживается штатно: `malgo.DefaultDeviceConfig(malgo.Duplex)` + один `DataProc func(pOutputSample, pInputSamples []byte, framecount uint32)`, где input и output приходят в одном вызове. Формат f32/48000/mono запрашивается напрямую, а при несовпадении с нативным форматом miniaudio сам ресемплит (по умолчанию linear + ФНЧ); при `NoFixedSizedCallback = 0` (дефолт) и `PeriodSizeInFrames = 480` приложению гарантированно приходит ровно 480 фреймов на вызов, причём в duplex playback-буфер принудительно приравнивается к capture-буферу. Главный риск для speex AEC — не API, а бэкенд: на WASAPI и ALSA duplex идёт синхронным циклом read→callback→write (input/output жёстко связаны), а на CoreAudio и PulseAudio — через «наивный» промежуточный ring buffer `ma_duplex_rb` без компенсации дрейфа часов, с подстановкой тишины при недоборе. Отдельно: `Context.Devices(malgo.Duplex)` возвращает список playback-устройств (capture нужно запрашивать отдельно), а `DeviceID.Pointer()` подтекает памятью C-кучи.

## Находки

### [MAJOR] На macOS (CoreAudio) и PulseAudio duplex идёт через «наивный» ring buffer с подстановкой тишины — главный риск для AEC

miniaudio делит бэкенды на синхронные (реализованы onDeviceRead/onDeviceWrite) и асинхронные. Проверено по коду:
- WASAPI: `onDeviceRead = ma_device_read__wasapi`, `onDeviceWrite = ma_device_write__wasapi`, `onDeviceDataLoop = NULL` → duplex обрабатывается в `ma_device_audio_thread__default_read_write` одним потоком по схеме, документированной в самом коде: "The process is: onDeviceRead() -> convert -> callback -> convert -> onDeviceWrite()". Input и output в одном вызове жёстко связаны, ring buffer не используется.
- ALSA: то же самое (`ma_device_read__alsa` / `ma_device_write__alsa`, `onDeviceDataLoop = NULL`).
- CoreAudio: полностью асинхронный — два ОТДЕЛЬНЫХ AudioUnit (`coreaudio.audioUnitCapture`, `coreaudio.audioUnitPlayback`), callbacks `ma_on_input__coreaudio` / `ma_on_output__coreaudio` зовут `ma_device_handle_backend_data_callback()`, который для duplex гоняет данные через `pDevice->duplexRB`.
- PulseAudio: `onDeviceDataLoop = ma_device_data_loop__pulse`, но `ma_duplex_rb_init` вызывается явно внутри `ma_device_init__pulse` с комментарием, что общий механизм не сработает, "which is not the case for PulseAudio".

Что делает этот RB (комментарий автора miniaudio дословно): "At the moment this is just a simple naive implementation, but in the future I want to implement some dynamic resampling to seamlessly handle desyncs." То есть компенсации расхождения тактовых частот capture и playback НЕТ.

Последствия для speex AEC на macOS/Pulse:
1) callback драйвится PLAYBACK-стороной; capture-данные читаются из RB. В `ma_device__handle_duplex_callback_playback` при нехватке данных подставляется тишина: "If there's not enough data in there for the whole frameCount frames we just use silence instead for the input data" (буфер `silentInputFrames`).
2) В `ma_device__handle_duplex_callback_capture` при переполнении: "Overrun. Not enough room in the ring buffer for input frame. Excess frames are dropped."
Любая вставка тишины/дроп меняет задержку между far-end и near-end → фильтр speex AEC расходится и должен переучиваться.

На Linux по умолчанию будет выбран именно PulseAudio: приоритет бэкендов в miniaudio §15 — WASAPI > DirectSound > WinMM > Core Audio > sndio > audio(4) > OSS > PulseAudio > ALSA > JACK > .... Чтобы получить синхронный ALSA-путь, надо явно передать список: `malgo.InitContext([]malgo.Backend{malgo.BackendAlsa}, ...)`.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.h> · <https://raw.githubusercontent.com/gen2brain/malgo/master/context.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/enumerations.go>

### [MAJOR] Context.Devices(malgo.Duplex) возвращает playback-устройства, а не duplex-пары

Реализация в context.go:
```go
result := C.ma_context_get_devices(ctx.cptr(), &playbackDevices, &playbackDeviceCount, &captureDevices, &captureDeviceCount)
...
devices := playbackDevices
deviceCount := int(playbackDeviceCount)
if kind == Capture {
	devices = captureDevices
	deviceCount = int(captureDeviceCount)
}
```
То есть любой `kind`, кроме `Capture` (включая `Duplex` и `Loopback`), молча отдаёт список PLAYBACK-устройств. Понятия «duplex-устройство» на уровне перечисления не существует ни в malgo, ни в miniaudio — надо звать `Devices(malgo.Playback)` и `Devices(malgo.Capture)` отдельно и самому сопоставлять пару, относящуюся к одному физическому устройству. В issue #23 пользователь как раз наступил на `ctx.Devices(malgo.Duplex)`.

Выбор конкретных устройств внутри одного duplex-девайса делается двумя независимыми ID:
```go
cfg.Capture.DeviceID  = capInfos[i].ID.Pointer()
cfg.Playback.DeviceID = playInfos[j].ID.Pointer()
```
Документация miniaudio: `playback.pDeviceID` — "Only if requesting a playback or duplex device", `capture.pDeviceID` — "Only if requesting a capture, duplex or loopback device". `nil` = системное устройство по умолчанию (и в комментарии к `InitDevice` явно предупреждают: не полагаться на то, что первое устройство в списке — дефолтное).

`DeviceInfo` содержит `ID DeviceID`, `Name() string`, `IsDefault uint32`, `FormatCount uint32`, `Formats []DataFormat`, где `DataFormat{Format, Channels, SampleRate, Flags}` — нативные форматы устройства, по ним можно заранее проверить наличие 48000 Hz. Детали по конкретному ID — `ctx.DeviceInfo(kind, id, malgo.Shared)`.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/context.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device_info.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device.go> · <https://github.com/gen2brain/malgo/issues/23> · <https://raw.githubusercontent.com/gen2brain/malgo/master/_examples/enumeration/enumeration.go>

### [MAJOR] macOS: CoreAudio-настройки не экспонированы в malgo — нельзя попросить нативные 48k, всегда будет ресемплинг из системной частоты

В `DeviceConfig` malgo прямым текстом висит комментарий: `// TODO: Add support for coreaudio, opensl, aaudio` — экспонированы только Wasapi, Alsa, Pulse и AAudio. Соответственно `coreaudio.allowNominalSampleRateChange` недоступен.

В miniaudio эта опция управляет ровно нашим сценарием: "Desktop only. When enabled, allows the sample rate of the device to be changed at the operating system level... useful if you want to use a sample rate that is known to be natively supported by the hardware thereby avoiding the cost of resampling. When set to false, the sample rate currently set by the operating system will always be used." В коде при `allowNominalSampleRateChange == false` выполняется `bestFormat.mSampleRate = origFormat.mSampleRate;` — то есть частота устройства остаётся системной, а 48000 достигаются внутренним ресемплером miniaudio.

Другие ограничения CoreAudio-бэкенда: exclusive mode не поддерживается вовсе (`return MA_SHARE_MODE_NOT_SUPPORTED`), а для duplex принудительно поднимается число периодов: "Need at least 3 periods for duplex" (`if (data.periodsIn < 3 && pConfig->deviceType == ma_device_type_duplex) data.periodsIn = 3;`) — то есть заявленные 10 мс на macOS дадут буферизацию минимум ~30 мс на capture-стороне плюс проход через duplex ring buffer.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/device_config.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.h>

### [MAJOR] Известные issues по duplex: стуттеринг на WASAPI, ошибка ring buffer на macOS при ресемплинге, открытый дедлок в WASAPI routing

miniaudio:
- #81 (закрыт) "stuttering audio using duplex with WASAPI" — Windows 10, после ~10 минут duplex-стриминга появляются гэпы "of about ~20ms (most often, but have seen 10ms and 30ms ones)". Ровно тот класс дефекта, который убивает сходимость AEC.
- #191 (закрыт) "Bug with duplex on macOS" — при необходимости ресемплинга рвётся capture-цикл с сообщением "Failed to commit capture PCM frames to ring buffer" (в commit_write передавался счётчик фреймов в device-формате вместо client-формата). Подтверждает, что путь duplexRB + ресемплинг на macOS исторически хрупкий.
- #397, #429 (закрыты) — искажения/«разваленный» звук в simple_duplex, в т.ч. на DirectSound + ma_performance_profile_low_latency.
- #654 (закрыт) — "[IOS] Audio crackling in full-duplex mode".
- #1149 (ОТКРЫТ, заведён 2026-08-18 на v0.11.25, воспроизводится и в dev) — дедлок в WASAPI notification callback при auto stream routing: `IAudioClient::Initialize` блокируется, ожидая Audio Service, пока тот ждёт возврата из notification-колбэка. Воркэраунд автора issue — `noAutoStreamRouting = true` + собственный notification client, который на reroute лишь выставляет флаг, обрабатываемый в основном потоке.

Исправления duplex в истории miniaudio (CHANGES.md): v0.11.5 — "Fix a bug with fixed sized callbacks that results in glitches in duplex mode" и фикс WASAPI duplex при разных нативных частотах; v0.11.11 — overrun recovery для capture/duplex; v0.11.12 — краш при rerouting playback-стороны duplex-устройства; v0.11.15 — падение инициализации duplex на части бэкендов.

malgo:
- #62 (ОТКРЫТ, 2025-03-15) "how to use Acoustic Echo Cancellation(AEC) with malgo ?" — ровно наш сценарий: при одновременной работе микрофона и динамиков запись загрязняется. Ответа мейнтейнера нет; единственный комментарий — предложение стороннего человека сделать биндинг SpeexDSP. Готового AEC в malgo/miniaudio нет.

Источники: <https://github.com/mackron/miniaudio/issues/81> · <https://github.com/mackron/miniaudio/issues/191> · <https://github.com/mackron/miniaudio/issues/1149> · <https://github.com/mackron/miniaudio/issues/654> · <https://github.com/mackron/miniaudio/blob/master/CHANGES.md> · <https://github.com/gen2brain/malgo/issues/62>

### [minor] DeviceID.Pointer() утекает памятью C-кучи на каждый вызов

device_info.go:
```go
func (d *DeviceID) Pointer() unsafe.Pointer {
	return C.CBytes(d[:])
}
```
`C.CBytes` по документации cgo: "The C array is allocated in the C heap using malloc. It is the caller's responsibility to arrange for it to be freed, such as by calling C.free". malgo нигде эту память не освобождает, а `DeviceConfig.toC()` только копирует указатель в `deviceConfig.capture.pDeviceID` / `playback.pDeviceID`. В issue #23 мейнтейнер утверждает "No need to free anything in this case, it is not allocated from Go" — это неточно.

Практически: утечка ~sizeof(ma_device_id) байт (на Windows/WASAPI ma_device_id — это WCHAR[256], т.е. сотни байт) на каждый вызов `Pointer()`. Одноразово безобидно, но при переоткрытии устройства в цикле (реконнект, смена устройства, hot-plug) накапливается. Освобождать самому из Go-кода нельзя без собственного `C.free`, а указатель нужен живым как минимум до `InitDevice`.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/device_info.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device_config.go> · <https://pkg.go.dev/cmd/cgo> · <https://github.com/gen2brain/malgo/issues/23>

### [minor] WASAPI: запрос 48k при другой нативной частоте отключает low-latency shared mode; есть обход через NoAutoConvertSRC

miniaudio §15.1 WASAPI: "Low-latency shared mode will be disabled when using an application-defined sample rate which is different to the device's native sample rate. To work around this, set `wasapi.noAutoConvertSRC` to true in the device config. This is due to IAudioClient3_InitializeSharedAudioStream() failing when the AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM flag is specified. Setting wasapi.noAutoConvertSRC will result in miniaudio's internal resampler being used instead which will in turn enable the use of low-latency shared mode."

В malgo экспонировано полностью: `DeviceConfig.Wasapi.NoAutoConvertSRC`, `.NoDefaultQualitySRC`, `.NoAutoStreamRouting`, `.NoHardwareOffloading` (тип `WasapiDeviceConfig`, поля `uint32`; PR #47 чинил как раз то, что флаг раньше не доходил до C-структуры). Для 10-мс/480 бюджета на Windows этот флаг может оказаться решающим, если устройство нативно работает не на 48k.

Исторически по duplex на WASAPI: исправление "WASAPI: Fix a bug in duplex mode when the capture and playback devices have different native sample rates" в miniaudio v0.11.5 (2022-01-16), "WASAPI: Some minor improvements to overrun recovery for capture and duplex modes" в v0.11.11.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.h> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device_config.go> · <https://github.com/gen2brain/malgo/pull/47> · <https://github.com/mackron/miniaudio/blob/master/CHANGES.md>

### [minor] Linux/ALSA: shared mode на default-устройстве уходит в dmix/dsnoop, что навязывает свою частоту и программный ресемплинг

Документация miniaudio по `ma_device_init`: "ALSA Specific: When initializing the default device, requesting shared mode will try using the 'dmix' device for playback and the 'dsnoop' device for capture. If these fail it will try falling back to the 'hw' device." Также: "Some backends do not have a practical way of choosing whether or not the device should be exclusive or not (ALSA, for example) in which case it just acts as a hint."

malgo это документирует в device.go у геттера `CaptureInternalSampleRate()`: "This may differ from SampleRate() when the backend cannot honor the requested rate, for example when ALSA's dsnoop plugin locks the hardware at a fixed rate and resamples in software." То есть возможен двойной ресемплинг (ALSA plugin + miniaudio) — лишняя задержка и джиттер для AEC.

Экспонированные рычаги: `DeviceConfig.Alsa.NoMMap`, `.NoAutoFormat`, `.NoAutoChannels`, `.NoAutoResample` (последний включает SND_PCM_NO_AUTO_RESAMPLE). В issue malgo #56 видно и вторую линуксовую проблему: в списке capture-устройств PulseAudio отдаёт monitor-источники ("Monitor of ..."), которые выглядят как микрофоны, но ими не являются — список нужно фильтровать.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.h> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device_config.go> · <https://github.com/gen2brain/malgo/issues/56>

### [minor] malgo пробрасывает в Go только data и stop callback — уведомлений о reroute/disconnect нет

Вся C→Go обвязка — в miniaudio.c:
```c
void goSetDeviceConfigCallbacks(ma_device_config* pConfig) {
    pConfig->dataCallback = goDataCallbackWrapper;
    pConfig->stopCallback = goStopCallback;
}
```
`notificationCallback` в `DeviceConfig` объявлен как `*[0]byte` (сырой C-указатель) и не заполняется. Значит, из Go нельзя узнать о смене дефолтного устройства, отключении наушников, rerouting'е — только косвенно через `Stop` callback и `dev.IsStarted()`. Для WASAPI-дедлока (miniaudio #1149) рекомендованный обход требует именно своего notification client — в malgo его сделать нельзя, остаётся только выставить `Wasapi.NoAutoStreamRouting = 1` и обрабатывать смену устройства самостоятельно (переоткрытие device).

Отдельно про реалтайм: `goDataCallback` на каждом вызове берёт глобальный `deviceMutex` (`var deviceMutex sync.Mutex`, общий на все устройства процесса) для поиска колбэка в map, плюс каждый вызов — это C→Go переход из аудиопотока. При 10 мс это 100 вызовов/с — терпимо, но GC-паузы и cgo-оверхед остаются риском для жёсткого реалтайма.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.c> · <https://raw.githubusercontent.com/gen2brain/malgo/master/malgo.h> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device_config.go> · <https://github.com/mackron/miniaudio/issues/1149>

### [info] Репозиторий активен, актуальная версия v0.11.26 (2026-08-18), вендорится miniaudio 0.11.25

Последний тег — v0.11.26 (sha 4de8979), он же последний коммит master от 2026-08-18 ("Update README.md", перед ним PR #72 "enable avx2 / optimize miniaudio build"). pkg.go.dev показывает v0.11.26, published Aug 18, 2026. Более ранняя значимая активность: 2026-05-13 — обновление miniaudio.h до v0.11.25 (PR #68) и экспорт internal device properties (PR #69); 2025-11-20 — PR #67; 2025-09-19 — PR #66 (aaudio DeviceConfig). Темп: несколько содержательных коммитов в год, поддержка есть, но не быстрая. GitHub Releases пустые — версионирование только тегами. Вендоренная копия: MA_VERSION_MAJOR 0 / MINOR 11 / REVISION 25, шапка файла "miniaudio - v0.11.25 - 2026-03-04". go.mod: `module github.com/gen2brain/malgo`, `go 1.21`, внешних зависимостей нет; требуется cgo, на Linux линкуется `-ldl -lpthread -lm`.

Источники: <https://github.com/gen2brain/malgo> · <https://api.github.com/repos/gen2brain/malgo/tags> · <https://api.github.com/repos/gen2brain/malgo/commits> · <https://pkg.go.dev/github.com/gen2brain/malgo> · <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.h> · <https://raw.githubusercontent.com/gen2brain/malgo/master/go.mod> · <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.go>

### [info] Full-duplex поддерживается: один device, один callback с input и output одновременно

`DeviceType` в enumerations.go: `Playback DeviceType = iota + 1`, `Capture`, `Duplex`, `Loopback` — то есть Duplex == 3 == ma_device_type_duplex. Открытие: `cfg := malgo.DefaultDeviceConfig(malgo.Duplex)` → внутри вызывается `C.ma_device_config_init(ma_device_type_duplex)`.

Сигнатура callback (device.go):
```go
type DataProc func(pOutputSample, pInputSamples []byte, framecount uint32)
type StopProc func()
type DeviceCallbacks struct {
	// Data is called for the full duplex IO.
	Data DataProc
	// Stop is called when the device stopped.
	Stop StopProc
}
func InitDevice(context Context, deviceConfig DeviceConfig, deviceCallbacks DeviceCallbacks) (*Device, error)
```
Да, input и output приходят в ОДНОМ вызове. Порядок аргументов — сначала OUTPUT, потом INPUT (легко перепутать: в примерах malgo capture-callback назван `onRecvFrames(pSample2, pSample []byte, ...)`).

В `goDataCallback` размеры слайсов считаются раздельно: output = `frameCount * pDevice.playback.channels * ma_get_bytes_per_sample(playback.format)`, input = `frameCount * pDevice.capture.channels * ma_get_bytes_per_sample(capture.format)`. Слайсы построены через `unsafe.Slice` поверх C-памяти — валидны только на время вызова, данные надо копировать.

Важно: в `_examples` НЕТ duplex-примера (только capture, enumeration, io_api, playback) — эталонного кода в репозитории нет, ориентироваться на miniaudio `examples/simple_duplex.c`.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/enumerations.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device_config.go> · <https://api.github.com/repos/gen2brain/malgo/contents/_examples> · <https://github.com/mackron/miniaudio/blob/master/examples/simple_duplex.c>

### [info] 480 сэмплов на callback гарантируются: PeriodSizeInFrames=480 + NoFixedSizedCallback=0 (дефолт)

В miniaudio `noFixedSizedCallback` по умолчанию false (ma_device_config_init делает MA_ZERO_OBJECT), и тогда используется промежуточный буфер: в `ma_device_init` `intermediaryBufferCap = pConfig->periodSizeInFrames` (если 0 — считается из `periodSizeInMilliseconds` и клиентского sampleRate). Для duplex явно прописано: `pDevice->playback.intermediaryBufferCap = pDevice->capture.intermediaryBufferCap; /* In duplex mode, make sure the intermediary buffer is always the same size as the capture side. */`. Функция `ma_device__on_data` при `noFixedSizedCallback == false` нарезает поток ровно по этому буферу и в duplex-ветке вызывает `ma_device__on_data_inner(pDevice, playback.pIntermediaryBuffer, capture.pIntermediaryBuffer, capture.intermediaryBufferCap)`.

Вывод: `deviceConfig.PeriodSizeInFrames = 480` при `SampleRate = 48000` даёт framecount == 480 в каждом вызове независимо от того, какой период реально выдал бэкенд. Документация про "the period size you request is actually just a hint" относится к бэкенду, а не к размеру блока в callback.

Поля в malgo экспонированы: `DeviceConfig.PeriodSizeInFrames`, `.PeriodSizeInMilliseconds`, `.Periods`, `.NoFixedSizedCallback`, `.PerformanceProfile`, `.NoClip`, `.NoPreSilencedOutputBuffer`. `PeriodSizeInFrames` имеет приоритет над `PeriodSizeInMilliseconds`. Дефолты miniaudio: MA_DEFAULT_PERIODS = 3, период low-latency = 10 мс.

КРИТИЧНО для AEC: НЕ выставлять `NoFixedSizedCallback = 1` — иначе framecount станет произвольным и speex AEC (фиксированный frame_size 480) сломается.

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.h> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device_config.go>

### [info] f32 / 48000 Hz / mono запрашивается напрямую; miniaudio сам ресемплит при несовпадении

`FormatF32` есть в enumerations.go (FormatUnknown, FormatU8, FormatS16, FormatS24, FormatS32, FormatF32). Конфигурация:
```go
cfg := malgo.DefaultDeviceConfig(malgo.Duplex)
cfg.SampleRate = 48000
cfg.Capture.Format = malgo.FormatF32;  cfg.Capture.Channels = 1
cfg.Playback.Format = malgo.FormatF32; cfg.Playback.Channels = 1
cfg.PeriodSizeInFrames = 480
```
SampleRate — общий для обеих сторон: в доке miniaudio прямо сказано, что он "must be the same for both playback and capture in full-duplex configurations". Форматы и число каналов задаются раздельно для capture и playback.

Ресемплинг: "When sending or receiving data to/from a device, miniaudio will internally perform a format conversion to convert between the format specified by the config and the format used internally by the backend". Алгоритм по умолчанию — `ma_resample_algorithm_linear` с ФНЧ, `linear.lpfOrder` по умолчанию `min(4, MA_MAX_FILTER_ORDER)`. В malgo экспонировано как `DeviceConfig.Resampling.Algorithm` и `.Resampling.Linear.LpfOrder`.

Проверить, что реально получилось, можно новыми геттерами (добавлены в v0.11.25, май 2026): `dev.SampleRate()`, `dev.CaptureInternalSampleRate()`, `dev.PlaybackInternalSampleRate()`, `dev.CaptureInternalFormat()/Channels()`, `dev.PlaybackInternalFormat()/Channels()`. Комментарий в device.go прямо про этот кейс: "This may differ from SampleRate() when the backend cannot honor the requested rate, for example when ALSA's dsnoop plugin locks the hardware at a fixed rate and resamples in software."

Побочный эффект: при f32-playback miniaudio по умолчанию клиппит выход после возврата из callback (`noClip = false`).

Источники: <https://raw.githubusercontent.com/gen2brain/malgo/master/enumerations.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/miniaudio.h> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device.go> · <https://raw.githubusercontent.com/gen2brain/malgo/master/device_config.go>

## Рекомендации

- M2: собрать минимальный duplex-прототип (в репозитории malgo примера нет — брать за основу miniaudio examples/simple_duplex.c) с конфигом `DefaultDeviceConfig(malgo.Duplex)` + `SampleRate=48000`, `Capture/Playback.Format=FormatF32`, `Channels=1`, `PeriodSizeInFrames=480`, `NoFixedSizedCallback=0` и залогировать гистограмму реального `framecount` за 10+ минут на Windows/macOS/Linux. Ожидание: ровно 480 всегда; любое отклонение сразу ломает speex AEC.
- M2: после `InitDevice` обязательно снять фактические параметры новыми геттерами v0.11.25 — `dev.SampleRate()`, `dev.CaptureInternalSampleRate()`, `dev.PlaybackInternalSampleRate()`, `dev.CaptureInternalFormat()/Channels()`, `dev.PlaybackInternalFormat()/Channels()` — и зафиксировать, где включается внутренний ресемплер. Если internal != 48000, оценить добавленную задержку и решить, не проще ли форсировать нативные 48k на уровне ОС/устройства.
- M2: измерить и промониторить СТАБИЛЬНОСТЬ задержки far-end→near-end (а не только её величину) на macOS и PulseAudio, где duplex идёт через `ma_duplex_rb`. Конкретный тест: проиграть импульс/чирп в playback, найти его во входном сигнале, повторить 1000 раз за 30+ минут и построить распределение задержки. Скачки = подстановка тишины/дроп во внутреннем ring buffer, и AEC будет расходиться.
- M2: на Linux явно передавать список бэкендов в `InitContext`, а не полагаться на дефолт — по приоритету miniaudio первым берётся PulseAudio (асинхронный, duplex через ring buffer), тогда как ALSA даёт синхронный цикл read→callback→write. Сравнить AEC-метрики (ERLE, стабильность задержки) на `BackendAlsa` против `BackendPulseaudio` и принять решение по умолчанию для продукта.
- M2: на Windows проверить два варианта — с `Wasapi.NoAutoConvertSRC = 0` и `= 1` — на устройстве с нативной частотой, отличной от 48 кГц. Замерить реальный период и джиттер: по доке low-latency shared mode отключается при несовпадении частот, и флаг может быть обязательным для попадания в 10 мс.
- M2: на macOS проверить сценарий «один физический девайс на вход и выход» против «разные девайсы» (встроенный микрофон + внешние колонки/BT). CoreAudio открывает два независимых AudioUnit, компенсации дрейфа часов нет — на разных устройствах дрейф гарантирован, и нужно заранее решить, поддерживаем ли мы такую конфигурацию или запрещаем её в UI.
- M2: подтвердить/опровергнуть воспроизводимость miniaudio #81 (гэпы 10–30 мс после ~10 минут duplex на WASAPI) — гонять прогон минимум 30–60 минут с подсчётом xrun/подстановок тишины. Если воспроизводится, заложить в дизайн детектор рассинхрона и принудительный reset фильтра speex AEC.
- M2: реализовать корректный выбор пары устройств: вызывать `Devices(malgo.Playback)` и `Devices(malgo.Capture)` РАЗДЕЛЬНО (`Devices(malgo.Duplex)` молча вернёт playback-список), фильтровать на Linux monitor-источники (см. malgo #56), и по `DeviceInfo.Formats` заранее проверять поддержку 48000 Hz перед открытием устройства.
- M2: обойти утечку `DeviceID.Pointer()` (C.CBytes → malloc без free): вызывать её ровно один раз на устройство, кэшировать `unsafe.Pointer` и не дёргать в цикле реконнекта; либо завести свой тонкий хелпер с `C.free` в коде проекта. Проверить рост RSS в тесте с 1000 переоткрытий устройства.
- M2: спроектировать обработку смены/отключения устройства своими силами — malgo не пробрасывает `notificationCallback`, доступен только `Stop`. Реализовать watchdog (`dev.IsStarted()` + периодическая переперечислялка устройств) и на Windows рассмотреть `Wasapi.NoAutoStreamRouting = 1` как защиту от открытого дедлока miniaudio #1149 (v0.11.25, воспроизводится и в dev).
- M2: проверить реалтайм-поведение Go-стороны callback: копировать `pInputSamples`/писать `pOutputSample` без аллокаций и без блокирующих операций (слайсы `unsafe.Slice` валидны только внутри вызова), исключить логирование и обращения к каналам с блокировкой. Дополнительно замерить влияние GC-пауз на пропуски callback'ов при 10 мс периоде.
- M2: зафиксировать версии — `github.com/gen2brain/malgo v0.11.26` (miniaudio 0.11.25) — и завести в проекте прогон при `-tags ma_debug` (`-DMA_DEBUG_OUTPUT=1`) плюс `SetLogProc` на контексте, чтобы логи miniaudio (в т.ч. "Failed to commit capture PCM frames to ring buffer", реальные периоды PulseAudio/ALSA) попадали в диагностику M2.
