# LiveKit и VLESS + REALITY для Gul

Актуальный клиент находится в `desktop/` и использует Electron, LiveKit JS и
встроенный официальный Xray v26.3.27. Go в `server/` обслуживает только
аутентификацию, дерево каналов и выдачу media grants. Голос, видео и звук
демонстрации обрабатывает Chromium в приложении.

## Схема и порты

На публичном TCP 443 HAProxy направляет camouflage SNI к Xray на loopback 8443.
Xray разрешает только TCP к `127.0.0.1:443`; остальные назначения блокируются.
Внутренний TLS на том же адресе разделяет HTTPS broker, WSS LiveKit и TURN/TLS.
Клиент проверяет внутренний сертификат и использует локальный TURN/TCP мост
через REALITY. Произвольный SOCKS proxy и прямой ICE fallback отсутствуют.

| Внешний порт | Назначение |
|---|---|
| TCP 443 | REALITY, HTTPS, WSS, TURN/TLS |
| TCP 80 | HTTP-01 для выпуска/продления сертификата |
| TCP 22 | SSH |
| TCP 7881, UDP 7882 | Прямой media transport SFU; Electron использует relay |

`firewall.sh` закрывает TCP 5349, 7880, 8080, 8443, 8787 и 9443 от внешних
подключений. LiveKit работает в host network, поэтому firewall включают
**до** запуска контейнеров. TURN разрешает relay только к IP этого SFU;
UDP TURN выключен. Compose закрепляет LiveKit v1.13.8 по digest.

## Подготовка production

Нужны Debian/Linux, Docker Compose, HAProxy 2.6, iptables, OpenSSL и доверенный
TLS-сертификат для публичного IPv4. Генератор сертификат не выпускает.
Действующая конфигурация Certbot использует имя `gul-ip` и каталоги
`/opt/gul-livekit/{acme,acme-work,acme-logs}`. Certbot обновляет тот же
`/etc/haproxy/gul.pem` и делает graceful reload HAProxy.

Сохраните случайный пароль длиной 16–256 символов без пробелов в файле 0600.
Не передавайте пароль аргументом процесса и не включайте его в URL.

```sh
python3 deploy/livekit/prepare.py SERVER_IPV4 \
  --password-file /private/path/join-password --output /private/path/generated
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go -C server build -trimpath -o ../bin/gul-livekit-server ./cmd/gul-livekit-server
```

Генератор создаёт приватные `broker.json`, `livekit.yaml`, `haproxy.cfg` и
данные подключения. Существующий каталог не перезаписывается. Go toolchain
закреплён в `server/go.mod`; Go и DSP библиотеки клиенту не требуются.

1. Создайте `/opt/gul-livekit/private` с режимом 0700. Скопируйте туда созданные
   конфиги с режимом 0600, а compose/shell/unit файлы — в `/opt/gul-livekit`.
2. Установите binary как `/usr/local/libexec/gul-livekit-server` (0755),
   shell scripts как 0755, unit files в `/etc/systemd/system/`,
   `docker-firewall.conf` в `/etc/systemd/system/docker.service.d/gul-livekit.conf`.
   Примените `99-gul-livekit.conf` через `/etc/sysctl.d/`.
3. Включите `gul-livekit-firewall.service`, затем запустите Compose и
   `gul-livekit-broker.service`. Broker использует systemd credential с 0400/0600,
   loopback listener и ограничение памяти 128 МБ.
4. Объедините TLS fullchain и private key в `/etc/haproxy/gul.pem` с режимом 0600.
   Проверьте конфиг `haproxy -c -f private/haproxy.cfg` перед установкой в
   `/etc/haproxy/haproxy.cfg`. Включите HAProxy и certbot timer.

## Xray и REALITY

Официальный Xray v26.3.27 скачивается и проверяется по закреплённому SHA-256:

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
`GUL_LIVEKIT_REALITY_IMAGE=sha256:...`; получите его через `docker image inspect`.
Выберите camouflage DNS имя с TLS 1.3/HTTP2, доступное с VPS.

```sh
python3 deploy/livekit/prepare_reality.py SERVER_IPV4 DOMAIN \
  --password-file /private/path/join-password --xray "$xray_dir/xray" \
  --output /opt/gul-livekit/private/reality
"$xray_dir/xray" run -test -config /opt/gul-livekit/private/reality/server.json
haproxy -c -f /opt/gul-livekit/private/reality/haproxy.cfg
```

Скопируйте `compose.reality.yaml` рядом с основным Compose. Из `/opt/gul-livekit`
запустите `docker compose -f compose.yaml -f compose.reality.yaml up -d --no-deps reality`,
установите проверенный proxy config и выполните `systemctl reload haproxy`.
Xray UUID выводится из прежнего пароля domain-separated SHA-256; формат совпадает
с Electron transport. Приватный файл `Gul-LiveKit-Reality-server.txt` передают
пользователям отдельно от Git. Перезапуск broker обрывает его логические сессии;
graceful reload HAProxy сохраняет текущие соединения.

## Постоянные каналы и владелец

Для новой установки добавьте `--managed-channels` к `prepare.py` и
`prepare_reality.py`. Broker получит `statePath`, все HTTP signaling маршруты
будут направлены через admission. Для обновления существующего сервера
сохраните его пароль, TLS и REALITY ключи; замените только broker, его unit,
добавьте statePath и проверенную маршрутизацию admission.

Unit задаёт `StateDirectory=gul-livekit` и режим 0700. Используется
`/var/lib/private/gul-livekit/catalog.json`: systemd создаёт симлинк
`/var/lib/gul-livekit`, который отвергает строгая проверка приватного каталога.
Это поведение проверено с DynamicUser на Debian 12/systemd 252.

До первого запуска managed broker администратор один раз выполняет bootstrap
в приватном каталоге с `-bootstrap-owner -config FILE -owner-output FILE`.
При использовании systemd можно выполнить bootstrap отдельным transient
unit с такими же DynamicUser/StateDirectory ограничениями и записать экспорт
внутри этого каталога. Если bootstrap выполнялся от root после создания
StateDirectory, назначьте state-файлам владельца родительского каталога:

```sh
chown --reference=/var/lib/private/gul-livekit \
  /var/lib/private/gul-livekit/catalog.json \
  /var/lib/private/gul-livekit/catalog.json.lock
```

Не задавайте DynamicUser UID вручную. Systemd может повторно выбрать тот же
UID и тогда не менять владельца дочерних файлов, созданных root позднее.
Режимы 0700/0600 сохраняются. Этот сценарий и изолированный запуск managed
broker под настоящим systemd sandbox проверены на Debian 12.
Импортируйте экспорт в Gul; не публикуйте его в Git.
Полная модель прав и ограничения — [CHANNELS.md](../../docs/CHANNELS.md).
Managed server требует новый клиент с протоколом 2; переключение broker
требует повторного входа. Проверяйте отсутствие участников перед обновлением.

Дополнительный локальный stand:

```sh
python3 deploy/livekit/stand_reality.py --managed --output bin/managed-fixture \
  --xray "$xray_dir/xray" --broker bin/gul-livekit-server
cd desktop
GUL_MANAGED_STAND_DIR="$PWD/../bin/managed-fixture" \
  npm run test:integration -- e2e/managed-channels.live.spec.ts
```

Он создаёт отдельный private owner export. Реальная проверка encrypted
identity restart — `member-key-restart.live.spec.ts`, с защищённым OS store.

## Тесты и локальный стенд

```sh
go -C server test -race -count=1 -coverprofile=coverage.out ./...
go -C server vet ./...
python3 -m unittest discover -s deploy/livekit -p 'test_*.py'
```

Изолированный стенд использует отдельные контейнеры, случайный порт только на
`127.0.0.1`, локальный TLS 1.3 decoy и тестовый CA. Разрешение private TURN peer
ограничено точным `/32` адресом контейнера SFU. Production ACL не изменяется.

```sh
docker build --platform linux/amd64 -f deploy/livekit/Containerfile.smoke \
  -t gul-livekit-reality-smoke:local deploy/livekit
python3 deploy/livekit/stand_reality.py --output bin/livekit-reality-fixture \
  --xray "$xray_dir/xray" --broker bin/gul-livekit-server
python3 deploy/livekit/smoke_reality.py bin/livekit-reality-fixture
```

Smoke проверяет HTTPS и STUN Binding через REALITY, запрет другого IP/порта
и неправильного пароля. Полный Electron E2E проверяет голос в обе стороны,
чат, декодированное видео, звук демонстрации, смену каналов и relay TCP:

```sh
cd desktop
npm run build
GUL_ELECTRON_STAND_DIR="$PWD/../bin/livekit-reality-fixture" \
  npm run test:integration
```

Тестовый CA используется только тестовым приложением с `NODE_ENV=test`;
проверка TLS не отключается. Остановить исключительно контейнеры стенда:

```sh
python3 deploy/livekit/stand_reality.py --remove bin/livekit-reality-fixture
```

Приватные файлы после остановки сохраняются. Удалите их вручную, когда
тестирование закончено. Не включайте access logs с JWT query/headers.

## Сессии

Broker допускает 32 сессии; лимит входа — 20 попыток в минуту на IP и 120 суммарно.
REALITY клиенты делят loopback IP и его login limit. Lease — 60 секунд,
продлевается polling. Logout, смена канала и истечение lease удаляют активные
voice/screen participants; неудачные удаления повторяются maintenance циклом.
Первичный JWT действует 90 секунд, SFU может его обновлять. RemoveParticipant
не отзывает ранее выданный JWT криптографически. В managed режиме broker
admission запрещает его повторное использование после logout, смены канала
или отзыва ACL; LiveKit refresh сохраняет обязательные nonce/attributes.
Broker bearer отзывается сразу при logout. Без statePath сохраняется прежний
режим доверенной компании с общим паролем.
