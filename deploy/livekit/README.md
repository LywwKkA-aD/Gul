# LiveKit на VPS

Стенд для Gul 0.7.0-alpha.3: Debian 12, Docker Compose, HAProxy 2.6 и публичный
IPv4. Проверен на 1 vCPU / 1 ГБ RAM с двумя голосовыми клиентами и демонстрацией
720p. Это проверка небольшой компании, а не оценка предельной нагрузки.

## Схема и порты

HAProxy завершает TLS на TCP 443. HTTP/WebSocket идут к broker и LiveKit;
TURN/STUN внутри того же TLS-входа идут к встроенному TURN LiveKit.
PROXY v2 сохраняет исходный IP. TURN принимает его только с loopback.

| Внешний порт | Назначение |
|---|---|
| TCP 443 | HTTPS API, WSS, TURN/TLS |
| TCP 7881 | Прямой WebRTC TCP |
| UDP 7882 | Прямой WebRTC UDP |
| TCP 80 | Только HTTP-01 при выпуске/продлении сертификата |
| TCP 22 | Администрирование SSH |

Порты 5349, 7880, 8080 и 8787 закрыты от внешних подключений собственной
INPUT-цепочкой firewall. LiveKit работает в host network, поэтому эту защиту
нужно включить **до** запуска контейнера. Drop-in Docker сохраняет порядок при
перезагрузке. Глобальная политика firewall и посторонние службы не меняются.
TURN разрешает relay только к IPv4 этого SFU, исключая произвольные назначения.
UDP TURN выключен; в режиме relay клиент использует `turns:IP:443?transport=tcp`.

## Подготовка

Нужен действительный TLS-сертификат для публичного IP, доверенный клиентскими ОС.
В проверенном развёртывании используется краткоживущий IP-сертификат Let's Encrypt.
Каталог Certbot с его `live`, `archive`, `renewal` и `accounts` хранится целиком в
`/opt/gul-livekit/acme`; имя сертификата — `gul-ip`. Соседние `acme-work` и
`acme-logs` используются Certbot. Существующий сертификат можно перенести вместе
с этими каталогами. Новый сертификат нужно выпустить до запуска TLS-входа;
генератор не выполняет ACME-регистрацию.

Сохраните случайный пароль (не менее 16 символов, без пробелов) в файле 0600.
Не передавайте пароль аргументом процесса и не включайте его в адрес сервера.

```sh
python3 deploy/livekit/prepare.py SERVER_IPV4 \
  --password-file /private/path/join-password --output /private/path/generated
GOTOOLCHAIN=go1.26.7 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -o bin/gul-livekit-server ./cmd/gul-livekit-server
```

Генератор создаёт новые API credentials, парольный hash, конфиги broker/SFU,
HAProxy и приватный файл подключения. Существующий каталог не перезаписывается.
Секреты находятся только в приватных файлах; в Git их добавлять нельзя.

## Установка на Debian

1. Установите Docker с Compose, `haproxy`, `iptables` и `openssl`.
2. Создайте `/opt/gul-livekit` и `/opt/gul-livekit/private` с режимом 0700.
   Скопируйте `compose.yaml`, shell-скрипты и unit-файлы из этого каталога в
   `/opt/gul-livekit`, а созданные `broker.json`, `livekit.yaml`, `haproxy.cfg`
   в его `private` с режимом 0600.
3. Установите бинарник как `/usr/local/libexec/gul-livekit-server` (0755),
   shell-скриптам задайте 0755. Unit-файлы установите в `/etc/systemd/system/`.
   `docker-firewall.conf` установите как
   `/etc/systemd/system/docker.service.d/gul-livekit.conf`.
   `99-gul-livekit.conf` установите в `/etc/sysctl.d/` и примените `sysctl -p`
   к этому файлу. Затем `systemctl daemon-reload`.
4. Выполните `systemctl enable --now gul-livekit-firewall.service`;
   из `/opt/gul-livekit` выполните `docker compose up -d`, затем
   `systemctl enable --now gul-livekit-broker.service`.
5. С режимом 0600 объедините `acme/live/gul-ip/fullchain.pem` и `privkey.pem`
   в `/etc/haproxy/gul.pem`. Проверьте созданный proxy config через
   `haproxy -c -f /opt/gul-livekit/private/haproxy.cfg`, установите его как
   `/etc/haproxy/haproxy.cfg` (0644), освободите TCP 443 от старой службы и
   выполните `systemctl enable --now haproxy`, `systemctl restart haproxy`.
6. Включите `systemctl enable --now gul-livekit-certbot.timer` и проверьте
   `/opt/gul-livekit/renew-certificate.sh --dry-run`. Продление запускается
   каждые 6 часов; reload HAProxy сохраняет действующие соединения.

Клиенту выдаются `https://SERVER_IPV4` и пароль. Самому клиенту отдельный
LiveKit/прокси устанавливать не требуется. Сертификат проверяется системными
центрами доверия; отключать проверку TLS не нужно.

## Проверка

`https://SERVER_IPV4/healthz` должен вернуть `status: ok`, а
`/api/livekit/token` и `/twirp/...` — 404. Внешний доступ к внутренним портам
должен отсутствовать. Без правильного пароля `/api/gul/login` возвращает 401.

Для теста реального голоса используйте приватные файлы с адресом и паролем:

```sh
GUL_LIVEKIT_PUBLIC=1 GUL_LIVEKIT_FORCE_RELAY=1 \
GUL_LIVEKIT_ADDRESS_FILE=/private/path/address \
GUL_LIVEKIT_PASSWORD_FILE=/private/path/join-password \
GOTOOLCHAIN=go1.26.7 go test -race -tags live ./internal/livekit \
  -run '^TestPublicSFUTwoNativeManagers$' -count=1 -v
```

Без `GUL_LIVEKIT_FORCE_RELAY` проверяется обычный ICE. Браузерный тест запускается на macOS/Linux (он проверяет POSIX-права
приватного файла). Для него создайте файл 0600 с `GUL_REMOTE_URL` и `GUL_REMOTE_PASSWORD`, затем из `frontend`:

```sh
GUL_REMOTE_E2E=1 GUL_REMOTE_E2E_ENV=/private/path/remote.env \
  npx playwright test --config playwright.remote.config.ts
```

Он проверяет декодированные кадры 720p и спектр аудиосигнала в обычном режиме
и при принудительном TURN/TLS. Артефакты с токенами не записываются.

## Границы alpha

Комнаты предназначены для доверенной компании с общим случайным паролем.
Сессии broker ограничены 32, попытки входа — 20 в минуту с IP и 120 суммарно.
Lease сессии — 60 секунд, клиент продлевает его polling-запросами. При выходе,
смене канала и истечении lease broker удаляет активные voice/screen подключения;
сбой удаления повторяется обслуживающим циклом.

Начальный LiveKit JWT выдаётся на 90 секунд, но SFU может обновить его на более
долгий срок. `RemoveParticipant` не отзывает уже выданный JWT: он может быть
повторно использован до истечения срока. Это не система изолированных арендаторов
и не модель мгновенной блокировки недоверенных пользователей. Broker bearer
отзывается при logout сразу. Сессии находятся в памяти, перезапуск broker
потребует повторного подключения клиентов.

HAProxy access logs выключены: URL сигналинга может содержать JWT. Не включайте
их без редактирования query/headers. TURN/TLS помогает при недоступном UDP,
но не гарантирует прохождение DPI или блокировки IP; проверяйте реальное
подключение из нужной сети. Захват игрового/системного звука зависит от ОС.
