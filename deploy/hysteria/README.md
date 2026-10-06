# Gul: официальный Hysteria 2 + Mumble

Клиентское ядро Hysteria **v2.13.0 встроено в Gul**. Участники устанавливают
только Gul, вводят адрес сервера и пароль. Отдельные Hysteria, VPN, TUN,
локальный SOCKS-прокси и права администратора на их компьютерах не нужны.

```text
Gul (Hysteria core + extras v2.13.0)
  -> QUIC/UDP 443 -> официальный Hysteria server
  -> TCP 127.0.0.1:64738 -> Mumble v1.5.915
       внутри потока: Mumble TLS, голос UDPTunnel, чат и управление
```

TLS Mumble заканчивается на Mumble-сервере. Gul проверяет его сертификат по
TOFU и предъявляет собственную устойчивую клиентскую идентичность. Внешний
TLS Hysteria проверяется через системные CA; самоподписанный внешний сертификат
этот стенд не использует. `gul-relay` в новой схеме не участвует.

## Первый запуск на Linux VPS

Стартовая конфигурация рассчитана на эксперимент с **1 vCPU / 1 ГБ RAM** и
группой до 16 участников. Это ограничение стенда, а не подтверждённая нагрузочным
тестом ёмкость: после живого разговора проверьте CPU, память и задержку.
Нужны Python 3, Docker Engine с Compose **2.17+** и публичный IP. Для DNS-имени
используйте этот раздел; запуск без домена описан ниже в разделе сертификата на IP.

1. Создайте DNS A-запись, например `voice.example.org`, прямо на IP VPS.
   AAAA добавляйте только при рабочем IPv6. Если DNS находится в Cloudflare,
   используйте DNS only: обычный HTTP CDN не передаёт этот транспорт.
2. Разрешите входящие **UDP 443**, **TCP 443** и **TCP 80** в firewall хостера
   и хоста. TCP 80 нужен для выдачи и продления ACME-сертификата; TCP 443 —
   обычная HTTPS-страница. Голос идёт по UDP 443. Порт 64738 не публикуется.
3. Скопируйте этот каталог на VPS и выполните в нём, заменив домен и email:

   ```sh
   python3 prepare.py voice.example.org admin@example.org
   docker compose config --quiet
   docker compose pull
   docker compose up -d
   docker compose ps
   docker compose logs --tail=80 hysteria
   ```

   Используйте `sudo docker`, если ваш пользователь не имеет доступа к Docker.
   Порты 80/443 должны быть свободны. Скрипт создаёт каталог `private` с правами
   `0700`, файлы с `0600` и два независимых случайных пароля. Повторный запуск
   отказывается заменять существующий каталог. Конфигурация Hysteria хранится
   в `private/server.json`; JSON поддерживается официальным приложением.
4. В Gul укажите адрес из `private/client-address` и пароль из
   `private/join-password`. Передайте друзьям эти два значения отдельно.
   `private/admin-password` предназначен только владельцу Mumble-сервера.
   Скрипт не выводит пароли; читайте их локально на VPS и не прикладывайте
   каталог `private` к диагностике или репозиторию.

Подготовка использует ACME Let's Encrypt с HTTP challenge. ACME выдаёт подходящий
ECDSA/RSA-сертификат. При переходе на собственные сертификаты сохраняйте цепочку
доверия публичного CA и используйте ECDSA или RSA: Chrome QUIC fingerprint
parroting не поддерживает внешний Ed25519-сертификат.

Для существующего сервера сначала сохраните его данные и сертификат. Этот
Compose создаёт **новый** том Mumble и не переносит каналы, регистрации и
сертификат из старого сервера автоматически. Сохранение старого Mumble-сертификата
важно для уже записанных TOFU-пинов. Старые адреса `wss://...` нужно явно заменить
на новый адрес Hysteria: клиент не выводит новый адрес из старого и не переключает
пользователя на другой сервер молча.

## Запуск без домена: сертификат на IP

Let's Encrypt выдаёт публично доверенные сертификаты на IPv4/IPv6 с профилем
`shortlived`: срок действия — **160 часов**. Нужен доступный извне TCP 80 для
HTTP challenge и автоматическое продление. Hysteria 2.13.0 не предоставляет
настройку ACME-профиля, поэтому здесь сертификатом управляет внешний Certbot.
Проверка доверия в Gul остаётся обычной, через системные CA.
[Поддержка IP в Let's Encrypt](https://letsencrypt.org/2026/01/15/6day-and-ip-general-availability),
[поддержка в Certbot](https://letsencrypt.org/2026/03/11/shorter-certs-certbot).

Скопируйте этот каталог в `/opt/gul-hysteria` на VPS. Если выбрали другой путь,
замените его также в systemd unit ниже. Выполните из этого каталога, подставив
**свой публичный IP** вместо `YOUR_PUBLIC_IP`:

```sh
cd /opt/gul-hysteria
GUL_PUBLIC_IP='YOUR_PUBLIC_IP'
GUL_CERTBOT_IMAGE='certbot/certbot:v5.8.0@sha256:f70ad0adbb7e117f0fe42a63c553f28ea451edabc0148757b6efcd9735acaa20'
sudo install -d -m 0700 acme acme-work

gul_certbot() {
  sudo docker run --rm --publish 80:80/tcp \
    --mount "type=bind,source=$PWD/acme,target=/etc/letsencrypt" \
    --mount "type=bind,source=$PWD/acme-work,target=/var/lib/letsencrypt" \
    "$GUL_CERTBOT_IMAGE" "$@"
}

gul_certbot certonly --standalone --non-interactive --agree-tos \
  --register-unsafely-without-email --required-profile shortlived \
  --ip-address "$GUL_PUBLIC_IP" --cert-name gul-ip --key-type ecdsa --dry-run

gul_certbot certonly --standalone --non-interactive --agree-tos \
  --register-unsafely-without-email --required-profile shortlived \
  --ip-address "$GUL_PUBLIC_IP" --cert-name gul-ip --key-type ecdsa

gul_certbot renew --cert-name gul-ip --non-interactive \
  --no-random-sleep-on-renew --dry-run

python3 prepare.py "$GUL_PUBLIC_IP" --ip-certificate gul-ip --obfs salamander
docker compose -f compose.ip.yaml config --quiet
docker compose -f compose.ip.yaml pull
docker compose -f compose.ip.yaml up -d
```

Запускайте каждую следующую команду только после успеха предыдущей. Первый
`--dry-run` проверяет выдачу без сохранения тестового сертификата. Для временного
стенда команды регистрируют ACME-аккаунт без контактного email; можно заменить
`--register-unsafely-without-email` на `--email admin@example.org` в обеих командах
выдачи. В `prepare.py` email необязателен только для этого внешнего IP-режима.
При наличии своего адреса указывайте его в Certbot, который управляет аккаунтом.

`compose.ip.yaml` — самостоятельный файл: используйте **только**
`docker compose -f compose.ip.yaml`, без объединения с `compose.yaml`.
TCP 80 он не занимает, Mumble остаётся закрытым на loopback. Hysteria получает
только `acme/live/gul-ip` и `acme/archive/gul-ip` read-only, сохраняя относительные
ссылки Certbot. Ключ ACME-аккаунта ей недоступен. Имя `gul-ip` фиксировано в
скрипте и Compose. Не заменяйте эти directory mounts отдельными PEM-файлами:
обновление ссылки после продления должно быть видно контейнеру.

Участники используют адрес из `private/client-address`, например
`hysteria2://PUBLIC_IP?obfs=salamander`, и пароль из `private/join-password`.
Для IPv6 скрипт добавляет квадратные скобки. Смена публичного IP потребует нового
сертификата и обновления адреса у участников.

Для продления создайте `/etc/systemd/system/gul-certbot-renew.service`:

```ini
[Unit]
Description=Renew the Gul IP certificate
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
TimeoutStartSec=20min
ExecStart=/usr/bin/docker run --rm --name gul-certbot-renew --publish 80:80/tcp \
  --mount type=bind,source=/opt/gul-hysteria/acme,target=/etc/letsencrypt \
  --mount type=bind,source=/opt/gul-hysteria/acme-work,target=/var/lib/letsencrypt \
  certbot/certbot:v5.8.0@sha256:f70ad0adbb7e117f0fe42a63c553f28ea451edabc0148757b6efcd9735acaa20 \
  renew --cert-name gul-ip --non-interactive --quiet --no-random-sleep-on-renew
ExecStopPost=-/usr/bin/docker rm -f gul-certbot-renew
```

И `/etc/systemd/system/gul-certbot-renew.timer`:

```ini
[Unit]
Description=Check the Gul IP certificate every six hours

[Timer]
OnCalendar=*-*-* 00,06,12,18:00:00
RandomizedDelaySec=30m
Persistent=true

[Install]
WantedBy=timers.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now gul-certbot-renew.timer
sudo systemctl start gul-certbot-renew.service
sudo systemctl list-timers gul-certbot-renew.timer
sudo journalctl -u gul-certbot-renew.service --since today
```

Certbot сам выбирает момент продления; таймер проверяет его каждые шесть часов.
TCP 80 должен оставаться доступным. Следите за ошибками unit и сроком сертификата;
архивируйте `acme`, `private` и том Mumble, сохраняя права доступа. Каталог `acme`
содержит секретный ключ аккаунта и не должен попадать в репозиторий или диагностику.
Hysteria автоматически перечитывает изменившиеся сертификат и ключ при новом
TLS handshake, поэтому deploy-hook и перезапуск разговоров не нужны.
[Docker Certbot](https://eff-certbot.readthedocs.io/en/stable/install.html#alternative-1-docker),
[правила продления](https://eff-certbot.readthedocs.io/en/stable/using.html#renewing-certificates),
[загрузчик Hysteria 2.13.0](https://github.com/HyNetworks/hysteria/blob/app/v2.13.0/app/internal/utils/certloader.go).

## Обфускация

По умолчанию используется обычный Hysteria QUIC. При подготовке нового стенда
можно выбрать официальный Salamander или Gecko:

```sh
python3 prepare.py voice.example.org admin@example.org --obfs salamander
# Либо --obfs gecko; выполняйте только один вариант первичной подготовки.
```

Полученный адрес будет `hysteria2://voice.example.org?obfs=salamander`
или `hysteria2://voice.example.org?obfs=gecko`. Настройка сервера и адрес клиента
должны совпадать. Для смены режима уже работающего стенда отредактируйте `obfs`
в `private/server.json`, сохраните прежний join-password, перезапустите Hysteria
и обновите адрес у участников. Пароль один и тот же для Hysteria auth,
Mumble serverPassword и выбранной обфускации.

Обфускация не делает заблокированный IP доступным и не решает полную блокировку
UDP. Этот клиент не включает Mimic, Realms, ECH или смену портов. **TCP Brutal
v2.0.1 — отдельный модуль ядра Linux**, для данного стенда он не нужен.
TUN и изменения маршрутов операционной системы также не используются.

## Ограничения сервера и эксплуатация

- Два официальных образа закреплены версиями **и OCI index digest**, проверенными
  2026-10-06; образ Hysteria опубликован для Linux amd64 и arm64.
- Hysteria делит сетевое пространство только с контейнером Mumble. Mumble
  слушает `127.0.0.1:64738` внутри него; сетевое пространство хоста не используется.
- ACL разрешает только `direct(127.0.0.1, tcp/64738, 127.0.0.1)`, затем
  `reject(all)`. Последний аргумент принудительно фиксирует фактическую цель:
  домен с loopback A-записью и внешней AAAA-записью не обходит ограничение.
  Проксирование UDP отключено (`disableUDP`); это не отключает внешний QUIC.
  Пароль нельзя использовать для проксирования произвольных сайтов и портов.
- HTTP sniffing отключён; внутренний Mumble TLS не разбирается посредником.
  Административный Ice listener Mumble отключён.
- Лимиты памяти: Mumble 256 МиБ, Hysteria 384 МиБ, Go soft limit 256 МиБ.
  Журналы ограничены по размеру. При OOM сначала оцените нагрузку и память VPS.
- Hysteria не получает `NET_ADMIN`, host network или TUN-устройство. В DNS-варианте
  ACME и база Mumble живут в отдельных постоянных томах. В IP-варианте сертификаты
  хранятся в каталоге `acme`. Архивируйте их вместе с `private` и томом Mumble.
- Все участники видны Mumble с loopback; autoban оставлен, но порог увеличен
  до 60 попыток, бан сокращён до 30 секунд для переподключений группы.

Проверка работы: дождитесь успешной выдачи сертификата в логах, подключитесь
двумя Gul из разных сетей, проверьте чат и двусторонний голос 20–30 минут,
паузы и переподключение. Повторите из проблемного российского домашнего и
мобильного интернета. Успешный HTTPS или ping не подтверждает работу голоса.

```sh
docker stats --no-stream
docker compose logs --tail=100
docker compose restart hysteria
# При пересоздании контейнера Mumble пересоздавайте весь стек с общим namespace:
docker compose up -d --force-recreate
```

Для IP-варианта добавляйте `-f compose.ip.yaml` после `docker compose` во всех
командах эксплуатации, включая остановку и пересоздание контейнеров.

Остановка с сохранением данных: `docker compose down` (без `--volumes`).
Изменение join-password требует синхронно обновить Hysteria `auth.password`,
`obfs.<mode>.password`, если есть, и `private/join-password`, затем пересоздать
оба контейнера. На клиентских компьютерах нужно ввести новый пароль.

## Локальная проверка файлов

```sh
python3 -m unittest discover -s deploy/hysteria -p 'test_*.py'
```

Тесты проверяют совпадение паролей и режимов, права файлов, защиту от перезаписи
и symlink, границы ACL, валидацию публичных IP, доступ только к ключам нужного
сертификата, свободный TCP 80 и обе схемы Compose через настоящий `docker compose config`
при наличии CLI. Docker daemon для них не нужен. Это не проверка доступности
из российских сетей и не запуск публичного сервера.

Замечания security scan и сопоставление старого GO-2026-5288 с исправлением
upstream описаны в [журнале решений](../../docs/DECISIONS.md).

Первоисточники: [официальная установка и образ](https://hysteria.network/docs/getting-started/Installation/),
[схема сервера](https://hysteria.network/docs/advanced/Full-Server-Config/),
[ACL](https://hysteria.network/docs/advanced/ACL/),
[Chrome parroting и сертификаты](https://hysteria.network/docs/advanced/Full-Client-Config/),
[официальный Mumble Docker и secrets](https://github.com/mumble-voip/mumble-docker),
[TCP Brutal](https://github.com/apernet/tcp-brutal).
