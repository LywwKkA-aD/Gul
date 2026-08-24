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
