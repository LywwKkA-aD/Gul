# Прод-сервер Gul

Сервер — готовый `mumblevoip/mumble-server` (murmur) v1.5.915, своего кода на
сервере нет, кроме WSS-релея. Этот документ — операторская памятка: топология,
порты, секреты, бэкап, обновление, где смотреть логи. Детали каждого
компонента — в `deploy/murmur/` и `deploy/relay/README.md`.

## Топология

```text
клиент Gul ──wss://murmur.gulvox.com/mumble:443──► firewalld redirect 443→8443
                                                   └─► gul-relay (rootless podman, сетевой
                                                        namespace murmur) ──► 127.0.0.1:64738 murmur
клиент Gul / официальный Mumble ──host:64738 (tcp)──────────────────────────► murmur
```

- Релей — непрозрачный байтовый насос с фиксированной целью (loopback-murmur).
  Внутри WSS идёт полный Mumble TLS с тем же TOFU-отпечатком, что и напрямую:
  релей доверие не терминирует и пароль сервера не хранит (DECISIONS 2026-08-23).
- Прямой порт 64738 остаётся для сетей, где его не режут, и для официального
  клиента Mumble (он же использует UDP 64738 для голоса; Gul голос гонит только
  по TCP-туннелю).

## Порты

| Порт | Кто | Зачем |
|---|---|---|
| 443/tcp (публичный) | firewalld redirect → 8443 | WSS-вход клиентов Gul |
| 8443/tcp (только localhost/namespace) | gul-relay | слушатель релея; в публичной зоне не открывать |
| 64738/tcp | murmur | прямой Mumble (Gul и официальный клиент) |
| 64738/udp | murmur | голос официального клиента; Gul не использует |

Правила host-фаерволла с лимитом соединений на источник (nftables и firewalld,
IPv6 маскируется до /64) — в `deploy/relay/README.md`, раздел «Host firewall».
Это второй эшелон: у самого релея есть per-source лимит до TLS, но на
пользовательском уровне connlimit дешевле и срабатывает раньше.

## Секреты

- `MUMBLE_SUPERUSER_PASSWORD` — задаётся явно при первом запуске murmur
  (иначе он сгенерирует свой и утопит в логах). Нужен только для админских
  действий через официальный клиент (регистрация, ACL, каналы).
- `MUMBLE_CONFIG_SERVER_PASSWORD` — пароль входа на сервер; его знают все
  участники. Из него клиент выводит bearer релея.
- `GUL_RELAY_BEARER` — podman-секрет с предвычисленными bearer-строками
  (`gul-relay derive-credential`, v2 первой строкой, legacy второй). Релей
  читает только его; сырой пароль в контейнер релея не монтируется.
  **При обновлении релея до версии с bearer v2 секрет надо пересоздать** —
  релей без `v2.`-строки не стартует. Процедура и жизненный цикл legacy —
  в README релея.

Ротация пароля сервера = ротация `GUL_RELAY_BEARER` (они связаны по построению).

## Данные и бэкап

Всё состояние murmur — том `/data` (владелец UID/GID 10000:10000): SQLite-база
(регистрации, каналы, ACL, баны), сертификат сервера (`mumble.crt`/`mumble.key` —
это и есть TOFU-идентичность, которую пинят клиенты) и подкаталог `acme`
с цепочкой для публичного имени (её же читает релей, только чтение).

Бэкап: `podman volume export` тома (или tar из `/data` при остановленном
murmur) по расписанию, хранить минимум две копии вне хоста. Потеря
`mumble.key` = смена отпечатка у всех клиентов (TOFU-предупреждение и
переподтверждение у каждого) — это самый дорогой файл на сервере.

## Незаметность: релей отвечает как обычный сайт

Сделано 2026-08-26. До этого хост опознавался одним запросом: `GET /mumble`
отдавал `WWW-Authenticate: Bearer realm="gul-relay"`, а корень — голый
`404 page not found` без заголовка `Server`. Различимых ответов было десять,
и по ним читалось всё дерево решений сервиса.

Теперь у хоста одна личность (`internal/relay/cover.go`):

- корень отдаёт страницу стокового nginx (200, `Server: nginx`, `ETag`,
  `Last-Modified`);
- **любой** отказ — не тот путь, не тот метод, не тот хост, нет ключа, не тот
  ключ, нет подпротокола — отдаёт байт в байт ту же страницу 404, что и
  несуществующий адрес. Причина уходит в лог, а не в ответ;
- `/healthz` вне loopback неотличим от несуществующего адреса;
- 429 и 503 сохранены: занятый сайт так и отвечает, а клиент по ним ждёт.

Свои страницы подставляются через `Config.ServerHeader`, `CoverIndex`,
`CoverNotFound`.

Следствие для клиента: неверный пароль теперь неотличим от неверного адреса —
это и есть цель. Клиент говорит «неверный адрес сервера или пароль».

h2 пока НЕ анонсируется. Если добавить его в ALPN, существующие клиенты
договорятся на HTTP/2 и WebSocket-апгрейд сломается: это идёт вместе с
браузерным транспортом в M7.5.

### Прямой порт Mumble закрыт снаружи

Открытый 64738 сообщал сканеру, что за трафик идёт на 443 — то самое, что
релей и прячет. За семь дней логов на этом порту были только сканеры (пробы
разными версиями TLS, HTTP-запросы на Mumble-порт); все реальные сессии идут
через релей, а он ходит к murmur по loopback внутри своего namespace и
правилом не задет.

Правило — в `/etc/nftables/gul-relay-guard.nft`, рядом с connlimit:
`iif != "lo" tcp dport 64738 counter drop` и то же для udp. Откат: убрать две
строки и `nft -f /etc/nftables/gul-relay-guard.nft`, либо вернуть
`.bak-20260826-cover`.

## Обновление релея: пошагово

Проверено на проде 2026-08-24 (переход alpha.2 → hardened). Всё выполняется
под пользователем `murmur`; `XDG_RUNTIME_DIR=/run/user/1001` обязателен, иначе
podman не найдёт сокет. Заходить лучше через `runuser -u murmur --`, при этом
`podman exec` и `podman healthcheck run` вручную падают на cgroup (нет
systemd-сессии) — это не поломка, штатные проверки контейнера идут своим
путём и возвращают 0.

1. Собрать статический бинарь и лицензионный бандл на рабочей машине:
   `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X main.version=$(bash scripts/check-version.sh --print app)" -o gul-relay ./cmd/gul-relay`
   и `bash scripts/collect-licenses.sh legal`.
2. Перенести `gul-relay`, `legal/` и `deploy/relay/Containerfile` на сервер,
   собрать образ: `podman build -t gul-wss-relay:<tag> -f Containerfile .`
3. Взять digest: `podman inspect --format "{{.Digest}}" localhost/gul-wss-relay:<tag>`.
4. **Пересоздать секрет** (обязательно при переходе на bearer v2 — без строки
   `v2.` релей не стартует). Значение не должно попадать в терминал:
   ```sh
   podman run --rm \
     --secret MUMBLE_CONFIG_SERVER_PASSWORD,type=mount,target=MUMBLE_CONFIG_SERVER_PASSWORD,uid=10000,gid=10000,mode=0400 \
     --entrypoint /usr/local/bin/gul-relay localhost/gul-wss-relay:<tag> \
     derive-credential --secret-file /run/secrets/MUMBLE_CONFIG_SERVER_PASSWORD > /tmp/cred.txt
   podman secret rm GUL_RELAY_BEARER; podman secret create GUL_RELAY_BEARER /tmp/cred.txt; shred -u /tmp/cred.txt
   ```
   Проверка без раскрытия значения: `wc -l` = 2 и `head -c3` = `v2.`.
5. В `~/.config/containers/systemd/gul-relay.container` заменить `Image=` на
   новый digest и дописать в `Exec=` флаги `--accept-legacy-bearer=true
   --log-level info`. Старый файл сохранить рядом как `.bak-<дата>` — это и
   есть путь отката (вернуть файл, `daemon-reload`, `restart`).
6. `systemctl --user daemon-reload && systemctl --user restart gul-relay.service`
7. Проверить: `podman ps` показывает `(healthy)`, а в журнале появляется
   `relay ready` с `accept_legacy_bearer=true`. Подключиться клиентом; в
   журнале должны быть `relay session opened` / `closed` с байтами и причиной.
   Клиент версии alpha.2 даст дополнительную строку `relay accepted legacy
   bearer credential` с адресом источника — по ней видно, кому ещё обновляться.

**Состояние на 2026-08-24: legacy отключён.** Секрет пересоздан с
`derive-credential --v2-only` (одна строка), в `Exec=` стоит
`--accept-legacy-bearer=false`. Клиенты сборки v0.3.0-alpha.2 и старше
получают 401, в журнале — `relay authorization rejected` с
`credential=legacy`. Чтобы временно вернуть совместимость: пересоздать секрет
без `--v2-only` и переставить флаг в `true`.

## Обновление

- Образ murmur запинен тегом `v1.5.915` (PLAN §2); ветка 1.6 — RC, не брать.
- Релей: собрать Linux amd64 с `CGO_ENABLED=0` (CI делает это статически и
  проверяет), собрать scratch-образ из `deploy/relay`, подставить digest в
  quadlet (`Pull=never`, образ только локальный), `systemctl --user daemon-reload`
  и перезапуск юнита. Quadlet объявляет `PartOf=gul-murmur.service`: рестарт
  murmur перезапускает релей; релей дренирует сессии close-фреймом до 5 с,
  после чего клиенты переподключаются сами.
- Порядок при несовместимых изменениях протокола релея: сначала релей с
  `--accept-legacy-bearer=true`, затем клиенты, затем отключение legacy.

## Ограничение соединений на фаерволе

Развёрнуто 2026-08-24 и проверено нагрузкой. У релея есть собственный лимит на
источник, но это внешний, более дешёвый рубеж: флуд гасится ядром до того, как
стоить TLS-рукопожатия. Порог 32 новых соединения с адреса (IPv4 /32, IPv6 /64)
против 16 у самого релея, так что обычная работа его не достаёт.

Файлы: `/etc/nftables/gul-relay-guard.nft` (правила) и
`/etc/systemd/system/gul-relay-guard.service` (загрузка при старте, `enabled`).

Размещение правила — несущая деталь. firewalld редиректит 443 → 8443 в nat
prerouting с приоритетом −100, поэтому правило после него увидит уже 8443, а не
443; conntrack работает на −200, и `ct state`/`ct count` без него недоступны.
Приоритет **−110** — единственное окно, где виден публичный порт И уже есть
conntrack. Действие `drop`, а не `reject`: в prerouting reject недоступен, да и
флуд не должен оплачиваться ответным пакетом на каждый отброшенный SYN.

Таблица своя (`inet gul_relay_guard`), поэтому firewalld — владелец таблицы
`inet firewalld` — её не трогает, и `firewall-cmd --reload` её не сотрёт.

Проверка (45 одновременных соединений с одного адреса): установилось 32,
остальные отброшены, счётчик правила вырос. Текущее состояние:

```sh
nft list table inet gul_relay_guard   # счётчики packets/bytes у обоих правил
systemctl status gul-relay-guard
```

Известное ограничение: редирект 443 → 8443 у firewalld объявлен только для
IPv4 (`meta nfproto ipv4`), поэтому IPv6-клиенты до релея пока не доходят —
правило relay6 написано на будущее и сейчас всегда нулевое.

## Наблюдаемость

- Релей пишет JSON-логи в journald (`journalctl --user -u gul-relay`):
  открытие/закрытие сессий с байтами и причиной, 401 с классом credential,
  активация бана, отказ по ёмкости, недоступность murmur, ошибки перезагрузки
  сертификата. Таблица «класс отказа → что видно в логе» — в README релея.
- `gul-relay healthcheck` (HealthCmd quadlet) проверяет TLS и `/healthz`
  (доступен только с loopback); при проблемах с перезагрузкой сертификата
  в теле ответа появляется `certificate-reload: failing`, но юнит не убивается.
- murmur: `journalctl --user -u gul-murmur`; autoban в проде **включён**
  (в дев-стенде выключен ради smoke-тестов). Релей подставляет источникам
  псевдонимные loopback-адреса (по /32 или /64), так что автобан murmur
  продолжает работать по блокам источников, а не по одному адресу релея.

## Дев-стенд

`task murmur:up` поднимает тот же образ локально на 127.0.0.1:64738 с
выключенным autoban; для LAN — переменные `MUMBLE_BIND_ADDRESS` и
`MUMBLE_SUPERUSER_PASSWORD` (README проекта).
