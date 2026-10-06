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

## Дополнительный транспорт VLESS + REALITY

`prepare_reality.py` добавляет транспорт к существующему LiveKit, сохраняя
обычный `https://IP`, сертификат, API credentials и комнаты. Голос и демонстрации
остаются LiveKit. Встроенный клиент REALITY устанавливает внешний TCP 443;
внутри него проходят прежние HTTPS/WSS и TURN/TLS с проверкой сертификата IP.
Профиль имеет вид `livekit+vless://IP?flow=none&pbk=KEY&security=reality&sid=HEX&sni=DOMAIN&type=tcp`.
Пароль вводится отдельно. VLESS UUIDv8 выводится из исходных байт случайного
пароля тем же domain-separated SHA-256, что и в `internal/reality`; слабый пароль
эта схема не усиливает.

HAProxy сначала читает только TLS ClientHello. Указанный REALITY SNI направляется
в Xray на `127.0.0.1:8443`; остальные HTTPS/TURN соединения — в TLS frontend на
`127.0.0.1:9443`. На этой ветке PROXY v2 сохраняет IP прямого клиента. Xray
разрешает только VLESS TCP к `127.0.0.1:443` и дополнительно закрепляет этот адрес
через `freedom.redirect`; остальные IP, порты и UDP блокируются. Внутренний TLS
по IP не передаёт camouflage SNI, поэтому возвращается в обычную TLS-ветку,
а не зацикливается в REALITY. У клиентов REALITY broker видит loopback как
источник, поэтому они разделяют его лимит попыток входа с одного IP.

Используется закреплённый официальный Xray **v26.3.27**:

```sh
xray_dir=$(mktemp -d)
curl --fail --location --output "$xray_dir/Xray-linux-64.zip" \
  https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-64.zip
(cd "$xray_dir" && printf '%s  %s\n' \
  23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae \
  Xray-linux-64.zip | sha256sum --check -)
unzip -q "$xray_dir/Xray-linux-64.zip" xray LICENSE -d "$xray_dir"
chmod 755 "$xray_dir/xray"
docker build -f deploy/livekit/Containerfile.reality \
  -t gul-livekit-xray:26.3.27 "$xray_dir"
```

Сохраните immutable image ID в приватном `/opt/gul-livekit/.env` как
`GUL_LIVEKIT_REALITY_IMAGE=sha256:...`; значение получает
`docker image inspect gul-livekit-xray:26.3.27 --format '{{.Id}}'`.
Compose не скачивает изменившийся тег. Xray имеет read-only rootfs, лимит 128 МиБ,
`GOMEMLIMIT=96MiB` и нулевые capabilities; единственный bind — loopback 8443.

Выберите SNI сайта, доступного **с VPS**, с TLS 1.3 и HTTP/2. Проверьте его через
`openssl s_client -connect DOMAIN:443 -servername DOMAIN -tls1_3 -alpn h2`.
Затем подготовьте отдельный каталог; существующие ключи не перезаписываются:

```sh
python3 deploy/livekit/prepare_reality.py SERVER_IPV4 DOMAIN \
  --password-file /private/path/join-password --xray "$xray_dir/xray" \
  --output /opt/gul-livekit/private/reality
"$xray_dir/xray" run -test -config /opt/gul-livekit/private/reality/server.json
haproxy -c -f /opt/gul-livekit/private/reality/haproxy.cfg
```

Скопируйте `compose.reality.yaml` рядом с действующим `compose.yaml`. Обновите
`firewall.sh`: внутренние 8443 и 9443 тоже должны оставаться закрыты снаружи.
Из `/opt/gul-livekit` запустите **только** новый контейнер, затем примените
проверенный proxy config:

```sh
/opt/gul-livekit/firewall.sh
docker compose -f compose.yaml -f compose.reality.yaml up -d --no-deps reality
install -m 0644 private/reality/haproxy.cfg /etc/haproxy/haproxy.cfg
systemctl reload haproxy
```

Graceful reload оставляет установленные соединения старому worker; перезапуск
SFU, broker или Docker не требуется. Certbot по-прежнему обновляет тот же
`/etc/haproxy/gul.pem`, проверяет текущий конфиг и делает reload. Передайте
пользователям приватный `Gul-LiveKit-Reality-server.txt`, не публикуя его в Git.

## Локальная проверка REALITY без изменения VPS

`stand_reality.py` создаёт отдельную Docker-сеть namespace с HAProxy **2.6**,
официальным Xray и закреплённым LiveKit. Внешний порт публикуется только на
`127.0.0.1` со случайным номером. Внутренний сертификат тестовый: тестовый клиент
явно доверяет `ca.pem`; отключение проверки TLS не используется. TLS 1.3 decoy
также локальный, поэтому camouflage-сайт не нужен для этой проверки.
TURN разрешает приватный IP SFU внутри Docker отдельным `/32` в
`allow_restricted_peer_cidrs`; остальные адреса остаются запрещены. Это
исключение относится только к стенду: production использует публичный IP SFU.

```sh
docker build --platform linux/amd64 -f deploy/livekit/Containerfile.smoke \
  -t gul-livekit-reality-smoke:local deploy/livekit
GOTOOLCHAIN=go1.26.7 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -o bin/gul-livekit-server ./cmd/gul-livekit-server
python3 deploy/livekit/stand_reality.py --output bin/livekit-reality-fixture \
  --xray "$xray_dir/xray" --broker bin/gul-livekit-server
python3 deploy/livekit/smoke_reality.py bin/livekit-reality-fixture
```

Smoke проверяет HTTPS broker через REALITY с доверенным сертификатом, настоящий
STUN Binding ответ встроенного TURN через обе ветки TCP 443, запрет обращения
к другому IP/порту и отказ с неверным паролем. Приватные `address`, `join-password`
и `ca.pem` можно передать native integration tests. Для остановки только
созданных этим стендом контейнеров выполните `--remove` после проверки.
Полная проверка двух клиентов включает голос в обе стороны, звук демонстрации,
чат, смену канала и выбор только локального TURN/TCP через REALITY:

```sh
GOTOOLCHAIN=go1.26.7 GUL_LIVEKIT_REALITY=1 \
  GUL_LIVEKIT_ADDRESS_FILE="$PWD/bin/livekit-reality-fixture/address" \
  GUL_LIVEKIT_PASSWORD_FILE="$PWD/bin/livekit-reality-fixture/join-password" \
  GUL_LIVEKIT_CA_FILE="$PWD/bin/livekit-reality-fixture/ca.pem" \
  go test -race -tags live ./internal/livekit \
  -run '^TestRealitySFUTwoNativeManagers$' -count=1 -timeout=90s
```

Остановка локального стенда:

```sh
python3 deploy/livekit/stand_reality.py --remove bin/livekit-reality-fixture
```

После завершения тестов удалите приватный каталог стенда. Отдельный скрипт
`scripts/probe-linux-webrtc.py` проверяет реальный WebKitGTK Ubuntu: наличие
JavaScript WebRTC API нельзя вывести из успешной сборки DEB или наличия
GStreamer-плагинов.
