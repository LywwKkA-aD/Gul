# VLESS + REALITY для Gul

Дополнение к [Hysteria + Mumble](../hysteria/README.md) для Gul **0.6.0-alpha.3**.
Клиент встроен в Gul; пользователю не нужны отдельный Xray, VPN, TUN или права
администратора. Внешнее соединение — TCP 443, внутри VLESS проходит прежний
Mumble TLS с голосом UDPTunnel. Hysteria продолжает работать на UDP 443.

Используется plain VLESS TCP **без Vision**. Профиль Gul имеет обязательное
`flow=none`; это значение профиля отображается в пустой `clients.flow` Xray.
Универсальные VLESS-ссылки с UUID в userinfo не принимаются. Полное Xray-ядро
в клиент не линкуется: REALITY handshake адаптирован из MPL-2.0 исходника
v26.3.27; provenance и лицензия находятся в `internal/reality/`.

## Установка на существующий Debian amd64 VPS

Сначала разверните IP-вариант `deploy/hysteria/compose.ip.yaml`. Он публикует
TCP/UDP 443 из namespace Mumble, а сам Mumble слушает только 127.0.0.1:64738.
Дальнейшие команды выполняются из корня репозитория на VPS. Нужны `curl`,
`unzip`, Python 3 и Docker Compose. Не публикуйте содержимое `private/`.

Получите закреплённый официальный Xray:

```sh
xray_dir=$(mktemp -d)
curl --fail --location --output "$xray_dir/Xray-linux-64.zip" \
  https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-64.zip
(cd "$xray_dir" && printf '%s  %s\n' \
  23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae \
  Xray-linux-64.zip | sha256sum --check -)
unzip -q "$xray_dir/Xray-linux-64.zip" xray LICENSE -d "$xray_dir"
chmod 755 "$xray_dir/xray"
docker build -f deploy/reality/Containerfile -t gul-xray:26.3.27 "$xray_dir"
export GUL_REALITY_IMAGE=$(docker image inspect gul-xray:26.3.27 --format '{{.Id}}')
```

Это SHA-256 Linux amd64 release asset, не хеш образа. `GUL_REALITY_IMAGE`
содержит неизменяемый ID собранного локального образа; сохраните его в приватном
`.env` или передавайте через `--env-file` при последующих командах Compose.
Контейнер имеет лимит 128 МиБ, `GOMEMLIMIT=96MiB`, read-only rootfs и только
`NET_BIND_SERVICE`. Он читает принадлежащий root конфиг 0600; host namespace,
Docker socket и приватные файлы других сервисов не подключаются.

Выберите доступный **с VPS** сайт для SNI/target с TLS 1.3 и HTTP/2. Например,
проверьте `openssl s_client -connect SITE:443 -servername SITE -tls1_3 -alpn h2`.
Это не домен Mumble: он нужен внешнему REALITY рукопожатию. В примере ниже
замените IP и SNI реальными значениями:

```sh
umask 077
python3 deploy/reality/prepare.py 203.0.113.10 www.example.org \
  --join-password-file deploy/hysteria/private/join-password \
  --xray "$xray_dir/xray" --output deploy/hysteria/private/reality
"$xray_dir/xray" run -test -config deploy/hysteria/private/reality/server.json
```

Генератор не заменяет существующие ключи и не выводит реквизиты. Он создаёт
`server.json`, `address`, `Gul-Reality-server.txt`, все с режимом 0600. Не запускайте
его повторно для обычного перезапуска. Старый join password должен быть
случайным, не короче 16 символов; пароль Mumble не меняется. VLESS UUIDv8
выводится из domain-separated SHA-256 исходных байт пароля; это не усиление
слабого пароля и не замена password KDF.

Освободите TCP 443 от Hysteria masquerade, сохранив UDP-сервис и сертификаты:

```sh
cp deploy/hysteria/private/server.json deploy/hysteria/private/server.before-reality.json
python3 - <<'PY'
import json
from pathlib import Path
p = Path('deploy/hysteria/private/server.json')
config = json.loads(p.read_text())
config.get('masquerade', {}).pop('listenHTTPS', None)
p.write_text(json.dumps(config, indent=2) + '\n')
PY
docker compose -f deploy/hysteria/compose.ip.yaml restart hysteria
docker compose -f deploy/hysteria/compose.ip.yaml \
  -f deploy/reality/compose.yaml up -d
```

Все относительные пути overlay разрешаются относительно первого Compose-файла.
Xray допускает только TCP 127.0.0.1:64738 и закрепляет этот destination через
`freedom.redirect`; остальные назначения и UDP отправляются в blackhole.
Конфигурация не является общедоступным прокси. Hysteria ACME/Certbot timer
остаётся нужен для его сертификата и не меняется этой установкой.

## Подключение и проверка

Передайте участнику приватный `Gul-Reality-server.txt`. В Gul он вставляет
**полный адрес**, вводит прежний пароль сервера отдельно и выбирает ник.
Формат ссылки: `vless://host?flow=none&pbk=KEY&security=reality&sid=HEX&sni=DOMAIN&type=tcp`.
Параметры ключа и short ID также считайте приватными. Они не должны попадать в
публичные issues, логи или скриншоты. Голый IP продолжает выбирать Hysteria;
для REALITY необходим полный профиль.

REALITY проверяет временный сертификат по ключу из профиля перед передачей
VLESS UUID. Обычный сертификат сайта-маскировки не принимается. Mumble TLS
дополнительно проверяет прежний TOFU-пин и клиентскую идентичность по хосту VPS.
Не подтверждайте неожиданную смену отпечатка без проверки владельцем сервера.

Автоматические проверки из рабочего дерева клиента:

```sh
python3 -m unittest discover -s deploy/reality -p 'test_*.py'
GUL_REALITY_LIVE_ADDRESS_FILE=/private/path/address \
GUL_REALITY_LIVE_PASSWORD_FILE=/private/path/join-password \
  go test -race -tags live ./internal/mumble \
  -run '^TestPublicReality(ChatAndVoice|StaysConnected)$' -count=1 -v
```

Live-тест проверяет две независимые идентичности, чат, по 100 audible Opus кадров
в обе стороны и 90 секунд соединения. Он не включает микрофон. Отдельно нужен
разговор из проблемной сети; успех из другой страны его не заменяет. TCP может
увеличивать задержку звука при потере пакетов. Блокировку IP транспорт не устраняет.

Для отката остановите только `reality`, восстановите `server.before-reality.json`,
перезапустите `hysteria` и вернитесь в Gul к прежнему адресу `hysteria2://...`.
Том Mumble, пароли, клиентские идентичности и база сервера сохраняются.
