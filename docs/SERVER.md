# Сервер Gul

Клиент 0.8.0-alpha.7 работает с LiveKit через встроенный VLESS + REALITY.
Go broker выделен в самостоятельный `server/`; старые голосовые серверы и
транспортные эксперименты текущей версии не нужны. Подробная установка,
конфиги systemd/firewall и локальный fixture: [deploy/livekit](../deploy/livekit/README.md).

## Состав

- HAProxy принимает TCP 443, разделяет REALITY и внутренний TLS.
- Серверный Xray v26.3.27 принимает REALITY на loopback и разрешает только
  TCP к локальному TLS 443; прочие назначения блокируются.
- TLS ветка направляет Gul HTTPS API к broker, TURN/TLS к SFU. В managed
  режиме WSS проходит через broker admission перед LiveKit.
- LiveKit v1.13.8 закреплён по image digest. TURN relay ограничен адресом SFU.
- Go broker выдаёт сессионный bearer и отдельные voice/screen grants,
  обслуживает каналы/roster и удаление медиа при logout/смене/истечении lease.

Изначальный ориентир для небольшой компании — 1 vCPU/1 ГБ RAM. Число участников,
демонстраций и доступная полоса влияют на нагрузку; synthetic тест пары клиентов
не подтверждает предельную вместимость. Сервер пересылает media без перекодирования.

## Сборка broker

```sh
go -C server test -race -count=1 ./...
go -C server vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go -C server build -trimpath -o ../bin/gul-livekit-server ./cmd/gul-livekit-server
python3 -m unittest discover -s deploy/livekit -p 'test_*.py'
```

CLI принимает `-config` с приватным `broker.json`. Формат JSON сохранён при
переносе. Broker слушает loopback, проверяет trusted proxy headers, Host/Origin,
bearer и ограничения входа. Config должен быть regular file с 0400/0600;
systemd credential подходит. Серверные API ключи не выдаются клиентам.

Необязательный `statePath` включает постоянный каталог, владельца, приглашения
и закрытые каналы. Для systemd DynamicUser используется канонический путь
`/var/lib/private/gul-livekit/catalog.json`; `StateDirectory=gul-livekit`
создаёт родительский каталог 0700. Публичный `/var/lib/gul-livekit` — симлинк
и не подходит строгому no-symlink guard хранилища.
Bootstrap владельца и модель доступа описаны в [CHANNELS.md](CHANNELS.md).
Перезапуск не теряет каталог или личные ключи, но требует нового входа сессий.

## Данные подключения

Владелец передаёт полный `livekit+vless://…` профиль и отдельно случайный пароль
длиной 16–256 символов без пробелов. REALITY UUID выводится из пароля
существующим domain-separated SHA-256; профиль не содержит пароль/UUID.
Профили и конфиги не публикуются в Git, release notes или диагностике.
Внутренний TLS проверяется клиентом по hostname/IP назначению профиля;
REALITY key не заменяет проверку сертификата broker/TURN.

## Сессии и эксплуатация

Broker хранит до 32 сессий в памяти. Lease 60 секунд продлевается polling.
Login limit — 20 попыток в минуту на IP, 120 суммарно; REALITY клиенты делят
loopback источник. Initial JWT действует 90 секунд, LiveKit может его обновлять.
Logout отзывает broker bearer сразу и удаляет активные media participants;
В legacy режиме уже выданный JWT не становится отозванным криптографически.
В managed режиме admission проверяет живую сессию, nonce, revision и актуальный
ACL перед каждым join/reconnect/validation, включая обновлённые SFU токены.
Отзыв доступа закрывает signaling и удаляет voice/screen participants;
при недоступном cleanup возвращается ошибка с повтором maintenance.

Перезапуск broker теряет логические сессии и требует нового входа.
Graceful reload HAProxy сохраняет текущие соединения; его используют для
сертификата и proxy config после проверки. Не включайте access logs с JWT URL.
Firewall защищает внутренние plaintext/admin/TURN порты до запуска контейнеров.

## Проверка

Локальный stand использует отдельные контейнеры и private directory в `bin/`,
случайный loopback порт, тестовый CA и локальный TLS 1.3 decoy. Production
конфиг не меняется. Smoke проверяет доверенный TLS, HTTPS/TURN через REALITY,
отказ с неправильным паролем и запрет другого назначения.

В предыдущей версии alpha.5 на действующем VPS проверены два Electron клиента через REALITY/TURN:
двусторонний synthetic voice, чат, видео и stereo audio с 20 циклами демонстрации.
В alpha.6 managed broker установлен на VPS после проверки отсутствия активных
участников, с сохранением профиля, общего пароля и REALITY/TLS. Проверены
личный доступ владельца, создание закрытого канала, отказ гостю, переименование
и удаление пустого канала через публичный TLS. Два Electron клиента прошли
20 циклов voice/video/stereo; первые кадры в этом прогоне приходили примерно
за 1,9–4,9 секунды. Серверные службы active, новых перезапусков и OOM нет.
Alpha.7 совместима с этим сервером и не требует его перезапуска.
Проверки реализации каталога:
45 Go tests с race detector, покрытие модуля 82.8%, vet чистый.
Настоящий локальный LiveKit подтвердил refresh nonce, удаление обоих
участников при отзыве, отказ старых JWT и сохранение ACL после restart.
Native Linux capture
на DISPLAY/PulseAudio стенде подтвердил movingframes в пределах 720p и stereo
PCM 440 Гц L / 660 Гц R через TURN/TCP. Physical mic, игра Windows 10 ↔ Ubuntu 26,
физическое PTT и часовой soak остаются отдельными проверками.
Контейнеры локального stand останавливаются только по его inventory;
приватные файлы после остановки сохраняются.
