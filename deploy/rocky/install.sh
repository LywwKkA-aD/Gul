#!/usr/bin/env bash
#
# Gul server provisioning for Rocky Linux 9 and 10.
#
# One script, one fresh box, one voice server: Murmur behind an authenticated
# relay, plus the hardening the box needs before it is worth putting either on
# it. Interactive throughout (charmbracelet/gum), and safe to run again - every
# phase checks the state it is about to create and skips what is already there.
#
#   curl -fsSLO https://raw.githubusercontent.com/LywwKkA-aD/Gul/main/deploy/rocky/install.sh
#   sudo bash install.sh
#
# The one thing this script will not do quietly is lock you out. SSH hardening
# stops and makes you prove, from a second terminal, that the new way in works
# before the old one is closed. Answer honestly; there is nobody else to ask.
#
# Every answer can be preset in the environment for an unattended run (see
# defaults below). GUL_ASSUME_YES=1 accepts every confirmation, which is only
# safe when every value is preset too.

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults. Every one is overridable from the environment.
# ---------------------------------------------------------------------------

GUL_REPO_URL=${GUL_REPO_URL:-https://github.com/LywwKkA-aD/Gul.git}
GUL_REPO_REF=${GUL_REPO_REF:-main}
GUL_BUILDER_IMAGE=${GUL_BUILDER_IMAGE:-docker.io/library/golang:1.26}
GUL_MUMBLE_IMAGE=${GUL_MUMBLE_IMAGE:-docker.io/mumblevoip/mumble-server:v1.5.915}

# The unprivileged account that owns the containers. Rootless: nothing here
# runs as root once the box is provisioned.
SERVICE_USER=${SERVICE_USER:-murmur}
SERVICE_HOME=${SERVICE_HOME:-/var/lib/murmur}

# Ports. 443 and 80 are redirected by firewalld to unprivileged listeners, so
# no container ever needs a capability to bind them.
RELAY_PORT=${RELAY_PORT:-8443}
ACME_PORT=${ACME_PORT:-8080}
MUMBLE_PORT=${MUMBLE_PORT:-64738}

STATE_DIR=/var/lib/gul-install
LOG_FILE=${GUL_LOG_FILE:-/var/log/gul-install.log}

# ---------------------------------------------------------------------------
# Output. Everything the operator sees goes through here; everything that
# happens goes to the log. Secrets go to neither.
# ---------------------------------------------------------------------------

# have_gum is true only when gum is installed AND there is a terminal to draw
# on. Without the second test gum still emits its cursor and clear sequences
# when stdout is a pipe, which turns an install log into unreadable escape
# soup - and an unattended run into one long line of it.
have_gum() {
  command -v gum >/dev/null 2>&1 && [[ -t 0 && -t 1 ]]
}

log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >>"$LOG_FILE"; }

say() {
  if have_gum; then gum style --foreground 250 "$*"; else printf '  %s\n' "$*"; fi
  log "SAY $*"
}

good() {
  if have_gum; then gum style --foreground 42 "  ok  $*"; else printf '  ok  %s\n' "$*"; fi
  log "OK $*"
}

warn() {
  if have_gum; then gum style --foreground 214 "  !!  $*"; else printf '  !!  %s\n' "$*"; fi
  log "WARN $*"
}

die() {
  if have_gum; then gum style --foreground 196 --bold "  стоп: $*"; else printf '  стоп: %s\n' "$*"; fi
  log "FATAL $*"
  exit 1
}

banner() {
  if have_gum; then
    gum style --border rounded --border-foreground 63 --padding "0 2" --margin "1 0" --bold "$*"
  else
    printf '\n=== %s ===\n' "$*"
  fi
  log "PHASE $*"
}

# confirm asks yes/no. GUL_ASSUME_YES makes every answer yes, which is for
# unattended runs and nothing else.
confirm() {
  if [[ ${GUL_ASSUME_YES:-0} == 1 ]]; then log "CONFIRM(auto-yes) $*"; return 0; fi
  if have_gum; then gum confirm "$*"; else
    read -r -p "$* [y/N] " reply
    [[ $reply == [yY]* ]]
  fi
}

# ask prompts for a value with a default. $1 prompt, $2 default, $3 preset.
ask() {
  local prompt=$1 default=${2:-} preset=${3:-}
  if [[ -n $preset ]]; then printf '%s' "$preset"; return; fi
  if [[ ${GUL_ASSUME_YES:-0} == 1 ]]; then printf '%s' "$default"; return; fi
  if have_gum; then
    gum input --prompt "$prompt " --placeholder "$default" --value "$default"
  else
    read -r -p "$prompt [$default] " reply
    printf '%s' "${reply:-$default}"
  fi
}

# ask_secret prompts without echoing. The value is never logged.
ask_secret() {
  local prompt=$1 preset=${2:-}
  if [[ -n $preset ]]; then printf '%s' "$preset"; return; fi
  if have_gum; then gum input --password --prompt "$prompt "; else
    read -r -s -p "$prompt " reply; printf '\n' >&2; printf '%s' "$reply"
  fi
}

choose() {
  local prompt=$1; shift
  if [[ ${GUL_ASSUME_YES:-0} == 1 ]]; then printf '%s' "$1"; return; fi
  if have_gum; then gum choose --header "$prompt" "$@"; else
    printf '%s\n' "$prompt" >&2
    select reply in "$@"; do printf '%s' "$reply"; return; done
  fi
}

# run executes a command, logs it, and reports whether it worked.
#
# The status is captured on the command itself and not after an `if`: `$?`
# following a completed if-statement is the status of the if, which succeeds
# even when its condition failed. Written the obvious way this function
# returned 0 for everything, which quietly turned every `|| die` in this
# script into dead code - found by running the script, not by reading it.
run() {
  log "RUN $*"
  local rc=0
  "$@" >>"$LOG_FILE" 2>&1 || rc=$?
  [[ $rc -eq 0 ]] || warn "команда не прошла: $* (см. $LOG_FILE)"
  return "$rc"
}

# spin runs a command with a spinner over it.
#
# The command runs in a subshell of THIS shell rather than under `gum spin --`,
# because gum execs a fresh process and the helpers here are shell functions:
# `spin "..." as_service podman build ...` under the obvious implementation
# fails with "as_service: command not found", and fails inside a spinner where
# nobody reads the error. The spinner watches the pid instead.
spin() {
  local title=$1; shift
  log "SPIN $title :: $*"
  if have_gum; then
    ( "$@" >>"$LOG_FILE" 2>&1 ) &
    local pid=$!
    gum spin --spinner dot --title "$title" -- \
      bash -c "while kill -0 $pid 2>/dev/null; do sleep 0.2; done" || true
    wait "$pid"
  else
    printf '  ... %s\n' "$title"
    "$@" >>"$LOG_FILE" 2>&1
  fi
}

# done_marker records a completed phase so a second run can skip it.
done_marker() { printf '%s\n' "$(date -u +%FT%TZ)" >"$STATE_DIR/$1.done"; }
is_done() { [[ -f $STATE_DIR/$1.done ]]; }

as_service() {
  runuser -u "$SERVICE_USER" -- env XDG_RUNTIME_DIR="/run/user/$(id -u "$SERVICE_USER")" \
    DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$(id -u "$SERVICE_USER")/bus" "$@"
}

# ---------------------------------------------------------------------------
# Phase 0: preflight
# ---------------------------------------------------------------------------

phase_preflight() {
  banner "Проверка машины"

  [[ $EUID -eq 0 ]] || die "запускать надо от root: sudo bash $0"

  local id version
  id=$(. /etc/os-release && printf '%s' "$ID")
  version=$(. /etc/os-release && printf '%s' "${VERSION_ID%%.*}")
  [[ $id == rocky ]] || die "этот скрипт только для Rocky Linux, а здесь $id"
  [[ $version -ge 9 ]] || die "нужна Rocky 9 или новее, здесь $version"
  good "Rocky Linux $version, $(uname -m)"

  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
  touch "$LOG_FILE"
  chmod 600 "$LOG_FILE"

  getent hosts github.com >/dev/null 2>&1 || die "нет DNS или интернета - без них ставить нечего"
  good "сеть на месте"

  local mem_mb cores
  mem_mb=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
  cores=$(nproc)
  say "память ${mem_mb} МБ, ядер ${cores}"
  if [[ $mem_mb -lt 900 ]]; then
    warn "меньше гигабайта памяти: сборка релея может не пройти, а Murmur настроен на 768 МБ"
    confirm "Всё равно продолжать?" || die "остановлено оператором"
  fi
  if [[ $cores -lt 2 ]]; then
    warn "одно ядро. При сотне слушателей это 20 тысяч пакетов в секунду - впритык"
  fi
}

# ---------------------------------------------------------------------------
# Phase 1: gum, so the rest of the script can talk properly
# ---------------------------------------------------------------------------

phase_gum() {
  if have_gum; then good "gum уже стоит"; return; fi
  banner "Ставлю gum"
  cat >/etc/yum.repos.d/charm.repo <<'EOF'
[charm]
name=Charm
baseurl=https://repo.charm.sh/yum/
enabled=1
gpgcheck=1
gpgkey=https://repo.charm.sh/yum/gpg.key
EOF
  if ! run dnf install -y gum; then
    rm -f /etc/yum.repos.d/charm.repo
    warn "репозиторий Charm недоступен, дальше без gum - вопросы будут простым текстом"
    return
  fi
  good "gum поставлен"
}

# ---------------------------------------------------------------------------
# Phase 2: base packages
# ---------------------------------------------------------------------------

phase_packages() {
  banner "Базовые пакеты"
  if is_done packages; then good "уже сделано"; return; fi

  # A tolerated failure: a mirror having a bad minute is not a reason to
  # abandon an install. The packages below are not optional and do abort.
  spin "обновляю систему" dnf -y upgrade --refresh \
    || warn "обновление не прошло целиком, продолжаю"

  # fail2ban is not in Rocky's own repositories, only in EPEL. Found by
  # running this script on a clean Rocky 9, where the whole install died on
  # "Unable to find a match: fail2ban" - so EPEL comes first, and its absence
  # is survivable rather than fatal: with password login off and keys only,
  # fail2ban trims the log noise, it does not hold the door.
  spin "подключаю EPEL" dnf install -y epel-release \
    || warn "EPEL не подключился - поставлю всё, кроме fail2ban"

  # curl, tar and openssl are deliberately absent from this list. A minimal
  # Rocky carries curl-minimal, which provides the same /usr/bin/curl, and
  # asking for the full package makes dnf refuse the whole transaction over a
  # conflict it cannot resolve on its own. What matters is the binary, so the
  # binary is what gets checked, below.
  if ! spin "ставлю пакеты" dnf install -y \
    podman container-selinux \
    firewalld openssh-server \
    chrony dnf-automatic \
    policycoreutils-python-utils \
    git jq; then
    die "не поставились обязательные пакеты - подробности в $LOG_FILE"
  fi

  local missing=()
  for tool in podman firewall-cmd sshd git jq curl tar openssl; do
    command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
  done
  [[ ${#missing[@]} -eq 0 ]] || die "не хватает программ: ${missing[*]}"
  good "обязательные пакеты на месте"

  if spin "ставлю fail2ban" dnf install -y fail2ban; then
    good "fail2ban поставлен"
  else
    warn "fail2ban недоступен - обойдёмся без него, вход и так только по ключу"
  fi
  done_marker packages
}

# ---------------------------------------------------------------------------
# Phase 3: the human's account and key
#
# Done before sshd is touched, because the next phase closes the door this one
# opens.
# ---------------------------------------------------------------------------

phase_admin() {
  banner "Учётная запись администратора"
  if is_done admin; then good "уже сделано"; return; fi

  local admin
  admin=$(ask "Имя администратора:" "gul" "${GUL_ADMIN_USER:-}")
  [[ -n $admin ]] || die "имя администратора пустое"

  if id "$admin" >/dev/null 2>&1; then
    good "пользователь $admin уже есть"
  else
    run useradd -m -s /bin/bash "$admin" || die "не смог создать $admin"
    good "создан $admin"
  fi
  run usermod -aG wheel "$admin" || warn "не смог добавить в wheel - sudo придётся настроить вручную"

  local keyfile="/home/$admin/.ssh/authorized_keys"
  mkdir -p "/home/$admin/.ssh"
  chmod 700 "/home/$admin/.ssh"
  touch "$keyfile"

  if [[ -s $keyfile ]]; then
    good "ключи уже есть: $(wc -l <"$keyfile") шт."
    if ! confirm "Добавить ещё один?"; then
      chown -R "$admin:$admin" "/home/$admin/.ssh"
      printf '%s\n' "$admin" >"$STATE_DIR/admin.user"
      done_marker admin
      return
    fi
  fi

  say "Вставьте публичный ключ целиком (ssh-ed25519 AAAA... comment)."
  say "Секретный ключ остаётся у вас - сюда идёт только публичный."
  local pubkey
  if [[ -n ${GUL_ADMIN_PUBKEY:-} ]]; then
    pubkey=$GUL_ADMIN_PUBKEY
  elif have_gum; then
    pubkey=$(gum input --prompt "Публичный ключ: " --width 100)
  else
    read -r -p "Публичный ключ: " pubkey
  fi

  [[ $pubkey =~ ^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp[0-9]+|sk-ssh-ed25519) ]] \
    || die "это не похоже на публичный ключ SSH"

  if grep -qF "$pubkey" "$keyfile" 2>/dev/null; then
    good "такой ключ уже записан"
  else
    printf '%s\n' "$pubkey" >>"$keyfile"
    good "ключ добавлен"
  fi

  chmod 600 "$keyfile"
  chown -R "$admin:$admin" "/home/$admin/.ssh"

  GUL_ADMIN_USER=$admin
  printf '%s\n' "$admin" >"$STATE_DIR/admin.user"
  done_marker admin
}

# ---------------------------------------------------------------------------
# Phase 4: sshd
#
# The lockout gate lives here. The order is deliberate: open the new port and
# accept the key first, prove it from a second terminal, and only then close
# password login and the old port. A script that reverses those two steps is a
# script that strands its operator on a box in another country.
# ---------------------------------------------------------------------------

phase_sshd() {
  banner "SSH"
  if is_done sshd; then good "уже сделано"; return; fi

  if ! command -v sshd >/dev/null 2>&1; then
    warn "sshd не установлен - настраивать нечего, пропускаю"
    return
  fi

  local admin current_port new_port
  admin=$(cat "$STATE_DIR/admin.user" 2>/dev/null || printf 'gul')
  # pipefail makes a failing sshd -T fail the whole assignment, and set -e
  # then ends the script without a word - which is exactly how this phase
  # died silently on a box whose sshd could not read its config.
  current_port=$(sshd -T 2>/dev/null | awk '/^port /{print $2; exit}' || true)
  current_port=${current_port:-22}

  say "Сейчас sshd слушает порт $current_port."
  say "Другой порт не защита, но он убирает из логов почти весь шум перебора."
  new_port=$(ask "Порт для SSH:" "22022" "${GUL_SSH_PORT:-}")
  [[ $new_port =~ ^[0-9]+$ ]] && [[ $new_port -ge 1 && $new_port -le 65535 ]] \
    || die "порт $new_port - не порт"

  # Step one: let the new port in, keep the old one, keep passwords.
  run systemctl enable --now firewalld \
    || die "firewalld не запустился, а на нём держится вход на 443 - дальше идти нельзя"
  run firewall-cmd --permanent --add-port="$new_port/tcp" || true
  run firewall-cmd --reload || die "правила фаервола не применились"

  local sshd_drop=/etc/ssh/sshd_config.d/50-gul.conf
  mkdir -p /etc/ssh/sshd_config.d
  {
    printf '# Written by the Gul installer. Password login is still on at this\n'
    printf '# point; it goes off only after you confirm the key works.\n'
    printf 'Port %s\n' "$new_port"
    # Only when it is a different port: sshd refuses a duplicate Port line.
    [[ $current_port == "$new_port" ]] || printf 'Port %s\n' "$current_port"
  } >"$sshd_drop"
  cat >>"$sshd_drop" <<EOF
PubkeyAuthentication yes
PermitRootLogin prohibit-password
X11Forwarding no
MaxAuthTries 3
LoginGraceTime 30
AllowTcpForwarding no
ClientAliveInterval 300
ClientAliveCountMax 2
EOF
  run sshd -t || die "конфиг sshd не проходит проверку - ничего не менял"
  run systemctl restart sshd || die "sshd не перезапустился - проверьте journalctl -u sshd"
  good "sshd слушает $new_port и $current_port, пароль пока разрешён"

  # Step two: make the operator prove the key works.
  local host_ip
  host_ip=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
  say ""
  say "Теперь откройте ВТОРОЙ терминал и проверьте вход по ключу:"
  if have_gum; then
    gum style --border normal --padding "0 2" --foreground 45 \
      "ssh -p $new_port $admin@${host_ip:-<адрес сервера>}"
  else
    printf '\n    ssh -p %s %s@%s\n\n' "$new_port" "$admin" "${host_ip:-<адрес>}"
  fi
  say "Не закрывайте эту сессию, пока вторая не откроется."
  say ""

  if ! confirm "Вход по ключу на порт $new_port работает?"; then
    warn "Оставляю пароль и старый порт включёнными - лучше так, чем запереть вас снаружи."
    warn "Разберитесь с ключом и запустите скрипт ещё раз."
    return
  fi

  # Step three: now it is safe to close.
  mkdir -p /etc/ssh/sshd_config.d
  cat >/etc/ssh/sshd_config.d/50-gul.conf <<EOF
# Written by the Gul installer after the key was confirmed working.
Port $new_port
PermitRootLogin prohibit-password
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
PermitEmptyPasswords no
X11Forwarding no
MaxAuthTries 3
LoginGraceTime 30
AllowTcpForwarding no
ClientAliveInterval 300
ClientAliveCountMax 2
EOF
  run sshd -t || die "конфиг sshd сломался - старый файл на месте, sshd не трогал"
  run systemctl restart sshd || die "sshd не перезапустился - проверьте journalctl -u sshd"
  if [[ $current_port != "$new_port" ]]; then
    run firewall-cmd --permanent --remove-port="$current_port/tcp" || true
    run firewall-cmd --permanent --remove-service=ssh || true
    run firewall-cmd --reload || die "правила фаервола не применились"
  fi
  good "пароль выключен, остался только ключ на порту $new_port"

  printf '%s\n' "$new_port" >"$STATE_DIR/ssh.port"
  done_marker sshd
}

# ---------------------------------------------------------------------------
# Phase 5: fail2ban
# ---------------------------------------------------------------------------

phase_fail2ban() {
  banner "fail2ban"
  if is_done fail2ban; then good "уже сделано"; return; fi
  if ! command -v fail2ban-server >/dev/null 2>&1; then
    warn "fail2ban не установлен, пропускаю"
    return
  fi

  local ssh_port
  ssh_port=$(cat "$STATE_DIR/ssh.port" 2>/dev/null || printf '22')

  mkdir -p /etc/fail2ban/jail.d
  cat >/etc/fail2ban/jail.d/gul.local <<EOF
[DEFAULT]
backend = systemd
banaction = firewallcmd-rich-rules[actiontype=<multiport>]
bantime = 1h
findtime = 10m
maxretry = 4

[sshd]
enabled = true
port = $ssh_port
EOF
  run systemctl enable --now fail2ban || warn "fail2ban не стартовал - вход по ключу от этого не страдает"
  good "fail2ban следит за sshd на порту $ssh_port"
  done_marker fail2ban
}

# ---------------------------------------------------------------------------
# Phase 6: unattended updates and clock
#
# The clock matters more than it looks: certificates and the tunnel's own
# handshake both care, and a box whose time has drifted fails in ways that
# read as network trouble.
# ---------------------------------------------------------------------------

phase_maintenance() {
  banner "Обновления и часы"
  if is_done maintenance; then good "уже сделано"; return; fi

  run systemctl enable --now chronyd || warn "chronyd не стартовал - следите за временем сами, от него зависят сертификаты"
  good "часы синхронизируются"

  if confirm "Ставить обновления безопасности автоматически?"; then
    sed -i 's/^upgrade_type =.*/upgrade_type = security/; s/^apply_updates =.*/apply_updates = yes/' \
      /etc/dnf/automatic.conf
    run systemctl enable --now dnf-automatic.timer || warn "таймер обновлений не включился"
    good "обновления безопасности ставятся сами"
  else
    say "тогда не забывайте про dnf upgrade --security"
  fi
  done_marker maintenance
}

# ---------------------------------------------------------------------------
# Phase 7: kernel knobs
#
# Only what this workload actually needs. The shaped tunnel sends one packet
# per 10 ms per talking stream, so a busy box is packet-bound long before it is
# byte-bound: a hundred listeners with two speakers is twenty thousand packets
# a second outbound, and the default socket buffers are sized for neither that
# rate nor the UDP road's bursts.
# ---------------------------------------------------------------------------

phase_sysctl() {
  banner "Ядро"
  if is_done sysctl; then good "уже сделано"; return; fi

  mkdir -p /etc/sysctl.d
  cat >/etc/sysctl.d/90-gul.conf <<'EOF'
# Written by the Gul installer.

# Reverse-path filtering and no source routing: ordinary hygiene for a box
# with one public interface.
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
net.ipv4.conf.all.accept_source_route = 0
net.ipv6.conf.all.accept_source_route = 0
net.ipv4.conf.all.accept_redirects = 0
net.ipv6.conf.all.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0

# SYN flood handling for a public 443.
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_max_syn_backlog = 4096
net.core.somaxconn = 4096

# The QUIC road is UDP, and quic-go wants room for bursts; the defaults on
# Rocky are 208 KB and drop datagrams under load rather than queueing them.
net.core.rmem_max = 8388608
net.core.wmem_max = 8388608
net.core.netdev_max_backlog = 8192

# BBR keeps the shaped stream's fixed cadence honest on a lossy path, where
# cubic reads steady small packets as congestion and paces them apart.
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF
  if ! run sysctl --system; then
    warn "часть параметров не применилась (ядро без bbr?) - смотрите $LOG_FILE"
  fi
  good "параметры ядра применены"
  done_marker sysctl
}

# ---------------------------------------------------------------------------
# Phase 8: the rootless service account
# ---------------------------------------------------------------------------

phase_service_user() {
  banner "Служебная учётная запись"
  if is_done serviceuser; then good "уже сделано"; return; fi

  if id "$SERVICE_USER" >/dev/null 2>&1; then
    good "пользователь $SERVICE_USER уже есть"
  else
    run useradd --system --create-home --home-dir "$SERVICE_HOME" \
      --shell /usr/sbin/nologin --comment "Gul Murmur service" "$SERVICE_USER" \
      || die "не смог создать $SERVICE_USER"
    good "создан $SERVICE_USER (без входа в систему)"
  fi

  # Rootless podman needs a subordinate id range and a session that survives
  # logout - without lingering the containers stop the moment nobody is logged
  # in, which on a headless box means always.
  if ! grep -q "^$SERVICE_USER:" /etc/subuid 2>/dev/null; then
    run usermod --add-subuids 200000-265535 --add-subgids 200000-265535 "$SERVICE_USER" \
      || die "без диапазона subuid rootless podman не заработает"
    good "выделен диапазон subuid/subgid"
  fi
  run loginctl enable-linger "$SERVICE_USER" \
    || die "без lingering контейнеры остановятся, как только никто не залогинен"
  good "сессия $SERVICE_USER живёт без входа в систему"

  mkdir -p "$SERVICE_HOME/.config/containers/systemd"
  chown -R "$SERVICE_USER:$SERVICE_USER" "$SERVICE_HOME/.config"
  done_marker serviceuser
}

# ---------------------------------------------------------------------------
# Phase 9: what this server is called
# ---------------------------------------------------------------------------

phase_identity() {
  banner "Имя сервера"

  if [[ -f $STATE_DIR/domain ]] && ! confirm "Домен уже задан ($(cat "$STATE_DIR/domain")). Поменять?"; then
    DOMAIN=$(cat "$STATE_DIR/domain")
    ACME_MAIL=$(cat "$STATE_DIR/acme.mail" 2>/dev/null || printf 'admin@%s' "$DOMAIN")
    return
  fi

  say "Домен нужен по-настоящему: по нему выписывается сертификат,"
  say "и его же клиент требует в заголовке Host - иначе релей отвечает как обычный сайт."
  DOMAIN=$(ask "Домен этого сервера:" "murmur.example.com" "${GUL_DOMAIN:-}")
  [[ $DOMAIN =~ ^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]] || die "«$DOMAIN» не похож на домен"

  local resolved myip
  resolved=$(getent hosts "$DOMAIN" | awk '{print $1; exit}' || true)
  myip=$(curl -fsS --max-time 10 https://api.ipify.org 2>/dev/null || hostname -I 2>/dev/null | awk '{print $1}' || true)
  if [[ -z $resolved ]]; then
    warn "$DOMAIN сейчас никуда не резолвится - ACME не выпишет сертификат"
    confirm "Продолжать? (A-запись можно добавить и потом)" || die "остановлено оператором"
  elif [[ $resolved != "$myip" ]]; then
    warn "$DOMAIN указывает на $resolved, а этот сервер - $myip"
    confirm "Всё равно продолжать?" || die "остановлено оператором"
  else
    good "$DOMAIN указывает сюда ($myip)"
  fi

  ACME_MAIL=$(ask "Почта для Let's Encrypt:" "admin@$DOMAIN" "${GUL_ACME_MAIL:-}")
  printf '%s\n' "$DOMAIN" >"$STATE_DIR/domain"
  printf '%s\n' "$ACME_MAIL" >"$STATE_DIR/acme.mail"
}

# ---------------------------------------------------------------------------
# Phase 10: build the relay image
#
# Built here rather than pulled, because there is no public registry for it and
# because the box then holds a digest it produced itself. The relay has no cgo
# in its dependency graph, so a static build in a throwaway toolchain container
# is enough and the host needs no Go.
# ---------------------------------------------------------------------------

phase_build_relay() {
  banner "Сборка релея"

  local existing
  existing=$(as_service podman images --noheading --format '{{.Repository}}:{{.Tag}}' \
    localhost/gul-wss-relay 2>/dev/null | head -1 || true)
  if [[ -n $existing ]] && ! confirm "Образ релея уже собран ($existing). Пересобрать?"; then
    good "оставляю собранный образ"
    return
  fi

  local ref
  ref=$(ask "Какую версию собирать (ветка или тег):" "$GUL_REPO_REF" "${GUL_REPO_REF_ANSWER:-}")

  local work="$SERVICE_HOME/build"
  rm -rf "$work"
  mkdir -p "$work"
  chown "$SERVICE_USER:$SERVICE_USER" "$work"

  spin "клонирую $ref" runuser -u "$SERVICE_USER" -- \
    git clone --depth 1 --branch "$ref" "$GUL_REPO_URL" "$work/src" \
    || die "не смог склонировать $GUL_REPO_URL на $ref"

  # CGO off on purpose: the relay's dependency graph is identity, relayproto
  # and relay, none of which touch C. The client's audio stack does, and does
  # not belong on a server.
  # Binary and legal bundle in one pass. The Containerfile copies `legal` from
  # the build context and there is no such directory in the repository: it is
  # generated, and generating it needs the Go toolchain to walk the module
  # graph. Both therefore happen in the throwaway builder rather than on the
  # host, which is why the host needs no Go at all.
  spin "собираю релей и лицензии" as_service podman run --rm \
    -v "$work/src:/src:z" -w /src \
    -e CGO_ENABLED=0 -e GOFLAGS=-trimpath \
    "$GUL_BUILDER_IMAGE" \
    bash -c "set -e
      go build -ldflags '-s -w -X main.version=$ref' -o deploy/relay/gul-relay ./cmd/gul-relay
      bash scripts/collect-licenses.sh deploy/relay/legal" \
    || die "сборка не прошла, смотрите $LOG_FILE"

  [[ -f $work/src/deploy/relay/gul-relay ]] || die "бинарь релея не появился"
  [[ -d $work/src/deploy/relay/legal ]] || die "каталог лицензий не собрался"
  chown -R "$SERVICE_USER:$SERVICE_USER" "$work"

  spin "собираю образ" as_service podman build \
    --build-arg "VERSION=$ref" \
    -t "localhost/gul-wss-relay:$ref" \
    -f "$work/src/deploy/relay/Containerfile" "$work/src/deploy/relay" \
    || die "podman build не прошёл"

  RELAY_DIGEST=$(as_service podman inspect --format '{{.Digest}}' "localhost/gul-wss-relay:$ref")
  [[ -n $RELAY_DIGEST ]] || die "образ собрался, но digest не читается"
  printf '%s\n' "$RELAY_DIGEST" >"$STATE_DIR/relay.digest"
  good "образ собран: $RELAY_DIGEST"

  rm -rf "$work"
}

# ---------------------------------------------------------------------------
# Phase 11: secrets
#
# Two of them, and they are different things that people conflate. The server
# password is what a person types into the client. The relay credential is
# derived from it and is what the relay checks at the front door, so the relay
# never stores the password itself.
# ---------------------------------------------------------------------------

phase_secrets() {
  banner "Секреты"

  local existing
  existing=$(as_service podman secret ls --noheading --format '{{.Name}}' 2>/dev/null || true)
  if grep -q '^GUL_RELAY_BEARER$' <<<"$existing" && ! confirm "Секреты уже заведены. Перевыпустить?"; then
    good "оставляю существующие секреты"
    return
  fi

  # Three secrets, and they are three different things people conflate:
  #
  #   MUMBLE_CONFIG_SERVER_PASSWORD  what a person types into the client
  #   GUL_RELAY_BEARER               derived from it; what the relay checks
  #   MUMBLE_SUPERUSER_PASSWORD      the Murmur admin account, unrelated
  #
  # Leaving the first one out does not fail loudly - it produces a server that
  # lets in anybody who finds it. It is set here first, and everything else
  # follows from it.
  local password
  password=$(ask_secret "Пароль сервера (его вводят в клиенте, пусто = сгенерировать):" "${GUL_SERVER_PASSWORD:-}")
  if [[ -z $password ]]; then
    password=$(openssl rand -base64 24 | tr -d '/+=' | head -c 24 || true)
    say ""
    if have_gum; then
      gum style --border double --border-foreground 214 --padding "1 3" --bold \
        "Пароль сервера: $password" "" "Запишите его СЕЙЧАС - больше он нигде не покажется."
    else
      printf '\n  Пароль сервера: %s\n  Запишите его сейчас.\n\n' "$password"
    fi
    confirm "Записали?" || die "остановлено: без пароля сервер бесполезен"
  fi

  local superuser
  superuser=$(openssl rand -base64 24 | tr -d '/+=' | head -c 24 || true)

  for name in MUMBLE_CONFIG_SERVER_PASSWORD MUMBLE_SUPERUSER_PASSWORD GUL_RELAY_BEARER; do
    as_service podman secret rm "$name" >/dev/null 2>&1 || true
  done

  printf '%s' "$password" | as_service podman secret create MUMBLE_CONFIG_SERVER_PASSWORD - >/dev/null \
    || die "не смог создать секрет с паролем сервера"
  printf '%s' "$superuser" | as_service podman secret create MUMBLE_SUPERUSER_PASSWORD - >/dev/null \
    || die "не смог создать секрет SuperUser"
  unset password

  # The bearer is derived by the relay's own binary, inside a throwaway
  # container, reading the password from the mounted secret. Neither value ever
  # reaches a command line, a file on the host, or this script's variables
  # after this point - which is the procedure deploy/relay/README.md documents.
  local digest
  digest=$(cat "$STATE_DIR/relay.digest")
  as_service podman run --rm \
    --secret MUMBLE_CONFIG_SERVER_PASSWORD,type=mount,target=MUMBLE_CONFIG_SERVER_PASSWORD,uid=10000,gid=10000,mode=0400 \
    "localhost/gul-wss-relay@$digest" \
    derive-credential --secret-file /run/secrets/MUMBLE_CONFIG_SERVER_PASSWORD \
    | as_service podman secret create GUL_RELAY_BEARER - >/dev/null \
    || die "не смог вывести учётку релея из пароля сервера"

  good "три секрета заведены в podman, на диск в открытом виде ничего не легло"

  say ""
  if have_gum; then
    gum style --border double --border-foreground 214 --padding "1 3" \
      "Пароль SuperUser (админ Murmur): $superuser" "" "Тоже запишите - он больше не покажется."
  else
    printf '  Пароль SuperUser: %s\n\n' "$superuser"
  fi
  confirm "Записали?" || warn "пароль SuperUser потерян; сбросить можно через mumble-server -supw"
  unset superuser
}

# ---------------------------------------------------------------------------
# Phase 12: quadlets
# ---------------------------------------------------------------------------

phase_quadlets() {
  banner "Юниты Murmur и релея"

  local users bandwidth welcome quic
  users=$(ask "Максимум одновременных пользователей:" "32" "${GUL_MAX_USERS:-}")
  bandwidth=$(ask "Потолок битрейта на клиента, бит/с:" "128000" "${GUL_BANDWIDTH:-}")
  welcome=$(ask "Приветствие:" "Welcome" "${GUL_WELCOME:-}")
  if confirm "Включить вторую дорогу QUIC (UDP 443)?"; then quic=true; else quic=false; fi

  local digest unit_dir
  digest=$(cat "$STATE_DIR/relay.digest")
  unit_dir="$SERVICE_HOME/.config/containers/systemd"
  mkdir -p "$unit_dir"

  # Back up anything already here, the way an operator would want it done.
  local stamp; stamp=$(date -u +%Y%m%d-%H%M%S)
  for f in "$unit_dir"/*.container "$unit_dir"/*.volume; do
    [[ -e $f ]] || continue
    cp -a "$f" "$f.bak-$stamp"
  done

  cat >"$unit_dir/gul-murmur-data.volume" <<'EOF'
[Volume]
VolumeName=gul-murmur-data
Label=app=gul-murmur
EOF

  cat >"$unit_dir/gul-murmur-acme.volume" <<'EOF'
[Volume]
VolumeName=gul-murmur-acme
Label=app=gul-murmur
Label=purpose=acme
EOF

  cat >"$unit_dir/gul-murmur.container" <<EOF
[Unit]
Description=Gul public Mumble server
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=10min
StartLimitBurst=3

[Container]
ContainerName=gul-murmur
HostName=$DOMAIN
Image=$GUL_MUMBLE_IMAGE
Network=pasta

Volume=gul-murmur-data.volume:/data
Volume=gul-murmur-acme.volume:/etc/acme
Secret=MUMBLE_SUPERUSER_PASSWORD,type=env,target=MUMBLE_SUPERUSER_PASSWORD
Secret=MUMBLE_CONFIG_SERVER_PASSWORD,type=env,target=MUMBLE_CONFIG_SERVER_PASSWORD

# 80 and 443 are redirected here by firewalld, so nothing binds a low port.
PublishPort=0.0.0.0:$ACME_PORT:80/tcp
PublishPort=0.0.0.0:$RELAY_PORT:$RELAY_PORT/tcp
PublishPort=0.0.0.0:$RELAY_PORT:$RELAY_PORT/udp
PublishPort=127.0.0.1:$MUMBLE_PORT:$MUMBLE_PORT/tcp

Environment=PUID=10000
Environment=PGID=10000
Environment=MUMBLE_CHOWN_DATA=true
Environment=MUMBLE_VERBOSE=false
Environment=ACME_DOMAIN=$DOMAIN
Environment=ACME_ACCOUNT_MAIL=$ACME_MAIL
Environment=ACME_HTTP=1

Environment="MUMBLE_CONFIG_WELCOMETEXT=$welcome"
Environment=MUMBLE_CONFIG_USERS=$users
Environment=MUMBLE_CONFIG_BANDWIDTH=$bandwidth
Environment=MUMBLE_CONFIG_OPUSTHRESHOLD=0
Environment=MUMBLE_CONFIG_TIMEOUT=30
Environment=MUMBLE_CONFIG_REMEMBERCHANNEL=true
Environment=MUMBLE_CONFIG_ALLOWHTML=false
# Answering pings would let anyone enumerate the server from outside; the
# cover site exists so that a stranger on 443 finds nothing to identify.
Environment=MUMBLE_CONFIG_ALLOWPING=false
Environment=MUMBLE_CONFIG_SENDVERSION=false
Environment=MUMBLE_CONFIG_BONJOUR=false
Environment=MUMBLE_CONFIG_ICE=
Environment=MUMBLE_CONFIG_LOGDAYS=31
Environment=MUMBLE_CONFIG_AUTOBANATTEMPTS=10
Environment=MUMBLE_CONFIG_AUTOBANTIMEFRAME=120
Environment=MUMBLE_CONFIG_AUTOBANTIME=300

NoNewPrivileges=true
PidsLimit=256
Memory=768M
LogDriver=journald
PodmanArgs=--umask=0077
HealthCmd=pidof mumble-server
HealthInterval=30s
HealthTimeout=5s
HealthRetries=3
HealthStartPeriod=30s

[Service]
Restart=on-failure
RestartSec=10s
TimeoutStartSec=3min

[Install]
WantedBy=default.target
EOF

  cat >"$unit_dir/gul-relay.container" <<EOF
[Unit]
Description=Gul authenticated relay
Requires=gul-murmur.service
After=network-online.target gul-murmur.service
PartOf=gul-murmur.service
StartLimitIntervalSec=10min
StartLimitBurst=3

[Container]
ContainerName=gul-wss-relay
Image=localhost/gul-wss-relay@$digest
Pull=never
# Share Murmur's network namespace only: the relay reaches Murmur on loopback
# and gains nothing else from the host.
Network=gul-murmur.container
User=10000:10000

Mount=type=volume,source=gul-murmur-data.volume,destination=/run/relay-tls,subpath=acme,ro=true
Secret=GUL_RELAY_BEARER,type=mount,target=GUL_RELAY_BEARER,uid=10000,gid=10000,mode=0400

Exec=--listen 0.0.0.0:$RELAY_PORT --host $DOMAIN --upstream 127.0.0.1:$MUMBLE_PORT --cert /run/relay-tls/mumble.crt --key /run/relay-tls/mumble.key --credential-file /run/secrets/GUL_RELAY_BEARER --quic=$quic --log-level info

ReadOnly=true
NoNewPrivileges=true
DropCapability=ALL
PidsLimit=128
Memory=128M
LogDriver=journald
HealthCmd=["/usr/local/bin/gul-relay","healthcheck"]
HealthInterval=30s
HealthTimeout=5s
HealthRetries=3
HealthStartPeriod=15s
HealthOnFailure=kill
StopTimeout=15

[Service]
Restart=on-failure
RestartSec=10s
TimeoutStartSec=2min
LimitNOFILE=8192

[Install]
WantedBy=default.target
EOF

  chown -R "$SERVICE_USER:$SERVICE_USER" "$unit_dir"
  good "юниты записаны в $unit_dir"
}

# ---------------------------------------------------------------------------
# Phase 13: firewall
#
# 443 and 80 are redirected rather than bound. A rootless container cannot take
# a privileged port without a capability, and handing it one to save a redirect
# rule is the wrong trade.
# ---------------------------------------------------------------------------

phase_firewall() {
  banner "Фаервол"

  local ssh_port
  ssh_port=$(cat "$STATE_DIR/ssh.port" 2>/dev/null || sshd -T 2>/dev/null | awk '/^port /{print $2; exit}' || true)
  ssh_port=${ssh_port:-22}

  run firewall-cmd --permanent --add-port="$ssh_port/tcp" || true
  run firewall-cmd --permanent --add-port=443/tcp || true
  run firewall-cmd --permanent --add-port=443/udp || true
  run firewall-cmd --permanent --add-port=80/tcp || true

  run firewall-cmd --permanent --add-forward-port=port=443:proto=tcp:toport="$RELAY_PORT" || true
  run firewall-cmd --permanent --add-forward-port=port=443:proto=udp:toport="$RELAY_PORT" || true
  run firewall-cmd --permanent --add-forward-port=port=80:proto=tcp:toport="$ACME_PORT" || true

  if confirm "Открыть прямой порт Mumble $MUMBLE_PORT наружу (для официального клиента)?"; then
    run firewall-cmd --permanent --add-port="$MUMBLE_PORT/tcp" || true
    run firewall-cmd --permanent --add-port="$MUMBLE_PORT/udp" || true
    warn "прямой порт виден снаружи и опознаётся как Mumble - маскировку он не переживает"
  else
    run firewall-cmd --permanent --remove-port="$MUMBLE_PORT/tcp" 2>/dev/null || true
    run firewall-cmd --permanent --remove-port="$MUMBLE_PORT/udp" 2>/dev/null || true
    good "снаружи открыты только 443 и 80"
  fi

  run firewall-cmd --reload || die "правила фаервола не применились"
  good "правила применены"
}

# ---------------------------------------------------------------------------
# Phase 14: start and verify
# ---------------------------------------------------------------------------

phase_start() {
  banner "Запуск"

  as_service systemctl --user daemon-reload \
    || die "systemd пользователя $SERVICE_USER не отвечает - проверьте loginctl enable-linger"
  spin "поднимаю Murmur" as_service systemctl --user start gul-murmur.service \
    || warn "Murmur не поднялся с первого раза, смотрим ниже"

  say "жду сертификат от Let's Encrypt (до двух минут)"
  local waited=0
  while [[ $waited -lt 120 ]]; do
    if as_service podman exec gul-murmur test -s /data/acme/mumble.crt >/dev/null 2>&1; then
      good "сертификат на месте"
      break
    fi
    sleep 5; waited=$((waited + 5))
  done
  [[ $waited -lt 120 ]] || warn "сертификата пока нет - релей не стартует, пока он не появится"

  spin "поднимаю релей" as_service systemctl --user start gul-relay.service \
    || warn "релей не поднялся"

  as_service systemctl --user enable gul-murmur.service gul-relay.service >/dev/null 2>&1 || true
}

phase_verify() {
  banner "Проверка"

  local ok=0 fail=0
  check() {
    if eval "$2" >>"$LOG_FILE" 2>&1; then good "$1"; ok=$((ok + 1)); else warn "$1 - НЕТ"; fail=$((fail + 1)); fi
  }

  check "Murmur запущен" "as_service systemctl --user is-active --quiet gul-murmur.service"
  check "релей запущен" "as_service systemctl --user is-active --quiet gul-relay.service"
  check "порт $RELAY_PORT слушается" "ss -tln | grep -q ':$RELAY_PORT'"
  # Not a loopback connect to 443: the firewalld redirect acts on traffic
  # arriving from outside, so a local dial reaches nothing and the check would
  # fail on a perfectly good box. The rule itself is what to assert.
  check "редирект 443 на релей заведён" "firewall-cmd --list-forward-ports | grep -q 'port=443:proto=tcp:toport=$RELAY_PORT'"
  check "sshd жив" "systemctl is-active --quiet sshd"
  check "firewalld жив" "systemctl is-active --quiet firewalld"
  if command -v fail2ban-server >/dev/null 2>&1; then
    check "fail2ban жив" "systemctl is-active --quiet fail2ban"
  fi
  check "SELinux в enforcing" "[[ \$(getenforce) == Enforcing ]]"

  # The cover site is the whole disguise: a stranger on 443 must get a plain
  # nginx page, not something that says Gul.
  local cover
  cover=$(curl -fsS --max-time 10 -o /dev/null -w '%{http_code}' "https://$DOMAIN/" 2>/dev/null || printf '000')
  if [[ $cover == 404 || $cover == 200 ]]; then
    good "прикрытие отвечает снаружи (HTTP $cover)"
    ok=$((ok + 1))
  else
    warn "прикрытие не ответило (HTTP $cover) - проверьте DNS и сертификат"
    fail=$((fail + 1))
  fi

  say ""
  if [[ $fail -eq 0 ]]; then
    if have_gum; then
      gum style --border double --border-foreground 42 --padding "1 3" --bold \
        "Сервер готов." "" "Адрес для клиента: wss://$DOMAIN" \
        "Проверок пройдено: $ok"
    else
      printf '\n  Сервер готов. Адрес: wss://%s\n\n' "$DOMAIN"
    fi
  else
    warn "проверок не прошло: $fail. Журнал установки: $LOG_FILE"
    say "логи сервисов:  journalctl --user -u gul-murmur -u gul-relay  (от $SERVICE_USER)"
  fi
}

# ---------------------------------------------------------------------------

main() {
  if have_gum; then
    gum style --border double --border-foreground 63 --padding "1 4" --margin "1 0" --bold \
      "Установка сервера Gul" "Rocky Linux, rootless podman"
  else
    printf '\n=== Установка сервера Gul ===\n\n'
  fi

  phase_preflight
  phase_gum
  phase_packages
  phase_admin
  phase_sshd
  phase_fail2ban
  phase_maintenance
  phase_sysctl
  phase_service_user
  phase_identity
  phase_build_relay
  phase_secrets
  phase_quadlets
  phase_firewall
  phase_start
  phase_verify
}

main "$@"
