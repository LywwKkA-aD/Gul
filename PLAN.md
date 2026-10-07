# Архитектура и план Gul

Текущая версия — **0.8.0-alpha.1**. Клиент находится в `desktop/` и полностью
написан на TypeScript/React для Electron. Go сохранён только как отдельный
серверный модуль `server/`. Legacy клиент, DSP, упаковка и транспортные
эксперименты удалены; история доступна в Git.

## Компоненты

| Компонент | Ответственность |
|---|---|
| Electron main | Полномочия сессии, пароль, broker bearer, сохранение серверов, native dialogs, транспорт, диагностика, update/tray/PTT |
| Preload | Узкий проверяемый IPC API для собственного renderer |
| React renderer + LiveKit JS | Интерфейс, микрофон, экран, кодеки, playback, VAD/gain, статистика |
| Встроенный Xray v26.3.27 | VLESS + REALITY TCP, отдельный дочерний процесс |
| Go broker | Аутентификация, roster/каналы, role grants, lease и удаление участников |
| HAProxy + LiveKit + Xray на сервере | Внешний TCP вход, TLS, SFU и TURN relay |

```mermaid
flowchart LR
  UI[React / LiveKit JS] -->|узкий IPC| Main[Electron main]
  UI -->|WSS и TURN/TCP на loopback| Bridge[Локальные мосты main]
  Main --> Bridge
  Bridge --> Xray[Встроенный Xray]
  Xray -->|REALITY TCP 443| Remote[HAProxy / Xray / TLS]
  Remote --> Broker[Go broker]
  Remote --> SFU[LiveKit / TURN]
```

`server/go.mod` независим от клиента. Сервер не захватывает, не декодирует и
не перекодирует медиа. UI загружается локально с `gul://app`, не с VPS.

## Владение медиа

Chromium владеет всем захватом, кодированием, декодированием и воспроизведением.
Кадры и PCM не гоняются через IPC. Voice и screen имеют отдельные LiveKit Rooms
и peer connections; Xray mux выключен. Это разделяет TCP потоки, но не устраняет
TCP head-of-line задержки и конкуренцию за общую пропускную способность.

Микрофон запрашивает mono/48 kHz с WebRTC AEC/NS/AGC. AudioWorklet применяет
усиление и RMS gate для активации голосом; gate не является отдельной нейросетью.
При PTT/mute/deafen эффективная громкость задаётся сразу, отдельно от сохранённых
пользовательских настроек. Playback использует WebAudio GainNode, без двойного
вывода одновременно через HTML audio и WebAudio.

Экран ограничен 1280×720/30 FPS и 2 Мбит/с для видео, один слой без simulcast.
По умолчанию VP8; H264 выбирается только через проверяемую capability политику.
Демонстрация автоматически запрашивает системный звук вместе с видео.
Стереозвук экрана не проходит голосовые AEC/NS/AGC. Подписка на чужой экран
независима от возможности собственного захвата. Не выбранные экраны
не подписываются для декодирования.

## Транспорт и границы доверия

Renderer получает только media grants текущей эпохи. Пароль и broker bearer
после подключения принадлежат main. Main проверяет профиль, hostname внутреннего
TLS, broker response и JWT назначения; redirect не выбирает новый сервер.
Local SOCKS аутентифицирован, UDP/mux выключены, назначение фиксировано.
WSS/TURN мосты проверяют Origin/Host, grant, эпоху и лимиты сообщений.
Новый канал/выход закрывает прежние медиапотоки. Прямого fallback нет.

Electron renderer: sandbox, contextIsolation, без Node integration и произвольной
навигации. Сохранение пароля требует явного согласия и защищённого keyring.
Диагностика собирается по списку разрешённых полей, а не редакцией сырых логов.
Update check сообщает о релизе и открывает проверенную GitHub страницу;
установщики автоматически не запускаются.

## Состояние и следующий тест

Миграция исходников завершена. Пройденные проверки:

- 186 unit tests; на macOS 184 passed/2 platform skips. TS line coverage —
  93.10%, Go module coverage — 87.3%.
- Windows/Linux/macOS arm64/x64 packaged smoke с sandbox. Linux DEB установлен.
- Windows native helper: регистрация и освобождение F8 hook; Linux portal:
  семь DBus сценариев. Физическое удержание клавиши ещё не проверено.
- Два Electron клиента через обновлённый VPS REALITY/TURN: синтетические
  голос/видео/stereo audio, чат и 20 циклов демонстрации.

Linux native capture прошёл реальный DISPLAY/PulseAudio стенд: движущиеся кадры,
лимит 720p и stereo PCM 440 Гц L / 880 Гц R через TURN/TCP. Перед стабильной версией нужны:

1. Физический микрофон на целевых компьютерах, качество AEC/NS и выбор устройств.
2. Реальный захват игры Windows 10 ↔ Ubuntu 26 в обе стороны и проверка
   общего loopback без возврата голосов Gul. Громкость просмотра отдельно 0–200%.
3. Физическое удержание PTT, отпускание клавиши и смена фокуса на целевых ОС;
   отсутствие залипшего микрофона. Toggle остаётся доступным режимом.
4. Час игры с голосом и демонстрацией, измерение CPU/RAM, задержки и восстановления
   после потери сети. Численного преимущества перед alpha.6 пока не заявляем.

Проверки и команды — [README](README.md), deployment —
[deploy/livekit](deploy/livekit/README.md), правила работы — [CLAUDE.md](CLAUDE.md).
