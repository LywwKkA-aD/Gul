#!/usr/bin/env bash
#
# Gul server provisioning for Debian.
#
# One script, one fresh box, one voice server: Murmur behind an authenticated
# relay, plus the hardening the box needs before it is worth putting either on
# it. Interactive throughout (charmbracelet/gum), and safe to run again - every
# phase checks the state it is about to create and skips what is already there.
#
#   curl -fsSLO https://raw.githubusercontent.com/LywwKkA-aD/Gul/main/deploy/debian/install.sh
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

# apt_refresh and the DEBIAN_FRONTEND below exist so that no phase can stop on
# a debconf prompt: this script asks its questions through gum, and a package
# deciding to open a dialog of its own would hang an unattended run forever.
export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

apt_refresh() {
  run apt-get update -qq || warn "список пакетов обновился не полностью"
}

# done_marker records a completed phase so a second run can skip it.
done_marker() { printf '%s\n' "$(date -u +%FT%TZ)" >"$STATE_DIR/$1.done"; }
is_done() { [[ -f $STATE_DIR/$1.done ]]; }

# as_service runs a command as the unprivileged container owner.
#
# The cd is not decoration. runuser keeps the caller's working directory, and
# this script runs as root - usually from /root, which is mode 700 and which
# the service user cannot enter. git survived that because it was handed an
# absolute path; podman did not, and failed with "cannot chdir to /root:
# Permission denied" followed by "Error: setting up the process", which is
# not a message that names its own cause. Found on a real box, after the same
# line had appeared in an earlier session and been read past.
#
# The subshell keeps the directory change from leaking into the caller.
as_service() {
  local uid
  uid=$(id -u "$SERVICE_USER")
  (
    cd "$SERVICE_HOME" 2>/dev/null || cd /
    runuser -u "$SERVICE_USER" -- env XDG_RUNTIME_DIR="/run/user/$uid" \
      DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$uid/bus" "$@"
  )
}

# ---------------------------------------------------------------------------
# Phase 0: preflight
# ---------------------------------------------------------------------------

phase_preflight() {
  banner "Проверка машины"

  [[ $EUID -eq 0 ]] || die "запускать надо от root: sudo bash $0"

  local id version pretty
  id=$(. /etc/os-release && printf '%s' "$ID")
  version=$(. /etc/os-release && printf '%s' "${VERSION_ID%%.*}")
  pretty=$(. /etc/os-release && printf '%s' "$PRETTY_NAME")
  [[ $id == debian ]] || die "этот скрипт только для Debian, а здесь $id"

  case $version in
  12 | 13) good "$pretty, $(uname -m)" ;;
  11)
    # Bullseye LTS ended 2026-08-31. A new server put on it gets no security
    # updates from the day it is built, which is not a trade worth taking for
    # a box that will be on a public address.
    warn "$pretty снят с поддержки 31 августа 2026 - обновлений безопасности больше нет"
    confirm "Всё равно ставить на него?" || die "остановлено: возьмите Debian 12 или 13"
    ;;
  *) die "нужен Debian 12 или 13, здесь $pretty" ;;
  esac

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
  apt_refresh
  run apt-get install -y --no-install-recommends curl ca-certificates gnupg \
    || die "без curl и gnupg дальше никак"

  mkdir -p /etc/apt/keyrings
  if ! curl -fsSL --max-time 30 https://repo.charm.sh/apt/gpg.key \
    | gpg --dearmor -o /etc/apt/keyrings/charm.gpg 2>>"$LOG_FILE"; then
    warn "ключ Charm не скачался, дальше без gum - вопросы будут простым текстом"
    return
  fi
  # A flat repository: the "* *" is the documented form and the only one that
  # resolves - repo.charm.sh serves no dists/stable path.
  printf 'deb [signed-by=/etc/apt/keyrings/charm.gpg] https://repo.charm.sh/apt/ * *\n' \
    >/etc/apt/sources.list.d/charm.list
  apt_refresh
  if ! run apt-get install -y --no-install-recommends gum; then
    rm -f /etc/apt/sources.list.d/charm.list
    apt_refresh
    warn "gum не поставился, дальше вопросы будут простым текстом"
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

  apt_refresh
  # A tolerated failure: a mirror having a bad minute is not a reason to
  # abandon an install. The packages below are not optional and do abort.
  spin "обновляю систему" apt-get -y upgrade \
    || warn "обновление не прошло целиком, продолжаю"

  # uidmap, slirp4netns and dbus-user-session are what make rootless podman
  # work at all on Debian: the subordinate id ranges, the network, and the
  # user session bus the units live in. Debian does not pull them in with
  # podman, and without them the containers fail late and confusingly.
  if ! spin "ставлю пакеты" apt-get install -y --no-install-recommends \
    podman uidmap slirp4netns dbus-user-session \
    firewalld openssh-server \
    chrony unattended-upgrades \
    git jq curl ca-certificates openssl sudo; then
    die "не поставились обязательные пакеты - подробности в $LOG_FILE"
  fi

  local missing=()
  for tool in podman firewall-cmd sshd git jq curl tar openssl; do
    command -v "$tool" >/dev/null 2>&1 || missing+=("$tool")
  done
  [[ ${#missing[@]} -eq 0 ]] || die "не хватает программ: ${missing[*]}"
  good "обязательные пакеты на месте"

  # podman 4.3 in bookworm has no quadlet - this script writes plain systemd
  # units instead, which is why no version is demanded here beyond what the
  # container flags need.
  say "podman $(podman --version | awk '{print $3}')"

  if spin "ставлю fail2ban" apt-get install -y --no-install-recommends fail2ban; then
    good "fail2ban поставлен"
  else
    warn "fail2ban не поставился - обойдёмся, вход и так только по ключу"
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
  # Debian's administrator group is "sudo"; "wheel" is the Red Hat name and
  # does not exist here, so asking for it fails and leaves the account unable
  # to become root - found by running this on a clean bookworm.
  run usermod -aG sudo "$admin" \
    || warn "не смог добавить в группу sudo - права придётся выдать вручную"

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
  if run systemctl enable --now fail2ban; then
    good "fail2ban следит за sshd на порту $ssh_port"
  else
    warn "fail2ban не стартовал - вход по ключу от этого не страдает"
  fi
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

  # The unit is chrony.service on Debian; chronyd.service is an alias symlink
  # and systemctl refuses to enable one ("Refusing to operate on alias name or
  # linked unit file"). The name that works is the one shipped.
  if run systemctl enable --now chrony; then
    good "часы синхронизируются"
  else
    warn "chrony не стартовал - следите за временем сами, от него зависят сертификаты"
  fi

  if confirm "Ставить обновления безопасности автоматически?"; then
    # Both files are needed: 50unattended-upgrades says what may be installed,
    # 20auto-upgrades says whether the timer runs at all. Debian ships the
    # first and omits the second, so the package alone does nothing.
    cat >/etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
    if run systemctl enable --now unattended-upgrades; then
      good "обновления безопасности ставятся сами"
    else
      warn "unattended-upgrades не запустился - обновления придётся ставить руками"
    fi
  else
    rm -f /etc/apt/apt.conf.d/20auto-upgrades
    say "тогда не забывайте про apt update && apt upgrade"
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
# Debian are 208 KB and drop datagrams under load rather than queueing them.
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

  spin "клонирую $ref" as_service \
    git clone --depth 1 --branch "$ref" "$GUL_REPO_URL" "$work/src" \
    || die "не смог склонировать $GUL_REPO_URL на $ref"

  # CGO off on purpose: the relay's dependency graph is identity, relayproto
  # and relay, none of which touch C. The client's audio stack does, and does
  # not belong on a server.
  # The build recipe is written to a file rather than inlined, because it is
  # about to be quoted through a heredoc, a shell -c and a container - three
  # layers in which a stray backslash is invisible until it fails on someone
  # else's server.
  #
  # scripts/collect-licenses.sh is deliberately NOT used. It walks the whole
  # release matrix - desktop targets that need a built frontend and cgo, plus
  # npm packages - because that is what a release ships. On a bare clone it
  # fails at the first target ("pattern all:frontend/dist: no matching files
  # found"), which is exactly what happened on the first real install. What
  # the relay image needs is narrower and is gathered here: this repository's
  # own licence and notice, plus the licence of every module that actually
  # ends up inside the relay binary.
  cat >"$work/build-relay.sh" <<'RECIPE'
#!/usr/bin/env bash
set -eu
go build -ldflags "-s -w -X main.version=${GUL_BUILD_REF}" \
  -o deploy/relay/gul-relay ./cmd/gul-relay

mkdir -p deploy/relay/legal
cp LICENSE NOTICE deploy/relay/legal/
go list -deps -f '{{with .Module}}{{.Path}}|{{.Dir}}{{end}}' ./cmd/gul-relay |
  sort -u |
  while IFS='|' read -r module_path module_dir; do
    [ -n "$module_dir" ] || continue
    case "$module_path" in github.com/LywwKkA-aD/Gul) continue ;; esac
    for licence in "$module_dir"/LICENSE* "$module_dir"/LICENCE* \
      "$module_dir"/COPYING* "$module_dir"/NOTICE*; do
      [ -f "$licence" ] || continue
      target="deploy/relay/legal/$module_path"
      mkdir -p "$target"
      cp "$licence" "$target/"
    done
  done
RECIPE
  chown "$SERVICE_USER:$SERVICE_USER" "$work/build-relay.sh"

  spin "собираю релей и лицензии" as_service podman run --rm \
    -v "$work/src:/src:z" -v "$work/build-relay.sh:/build-relay.sh:ro,z" -w /src \
    -e CGO_ENABLED=0 -e GOFLAGS=-trimpath -e "GUL_BUILD_REF=$ref" \
    "$GUL_BUILDER_IMAGE" \
    bash /build-relay.sh \
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
# Phase 12: systemd units
# ---------------------------------------------------------------------------

# Phase 12 writes plain systemd user units rather than Quadlet files.
#
# Quadlet would be the tidier form, but it arrived in podman 4.4 and Debian 12
# ships 4.3.1 with no generator at all - verified, not assumed. Rather than
# carry two orchestration paths, this writes the unit podman would have been
# asked to generate. It works from podman 3.0 upward, so the same file serves
# whatever Debian the next box runs.
phase_units() {
  banner "Юниты Murmur и релея"

  local users bandwidth welcome quic
  users=$(ask "Максимум одновременных пользователей:" "100" "${GUL_MAX_USERS:-}")
  bandwidth=$(ask "Потолок битрейта на клиента, бит/с:" "128000" "${GUL_BANDWIDTH:-}")
  welcome=$(ask "Приветствие:" "Welcome" "${GUL_WELCOME:-}")
  if confirm "Включить вторую дорогу QUIC (UDP 443)?"; then quic=true; else quic=false; fi

  local digest unit_dir
  digest=$(cat "$STATE_DIR/relay.digest")
  unit_dir="$SERVICE_HOME/.config/systemd/user"
  mkdir -p "$unit_dir"

  local stamp; stamp=$(date -u +%Y%m%d-%H%M%S)
  for f in "$unit_dir"/gul-*.service; do
    [[ -e $f ]] || continue
    cp -a "$f" "$f.bak-$stamp"
  done

  # Three volumes, and the middle one is the reason there are three. The image
  # writes its certificate to /data/acme and the relay has to read it; podman
  # 4.3 cannot mount a subdirectory of a volume (no --mount subpath), so the
  # certificate gets a volume of its own mounted at that exact path. /etc/acme
  # is lego's own state and stays private to Murmur.
  for v in gul-murmur-data gul-murmur-certs gul-murmur-acme; do
    as_service podman volume exists "$v" 2>/dev/null || {
      as_service podman volume create "$v" >/dev/null 2>&1 \
        || die "не смог создать том $v"
      good "создан том $v"
    }
  done

  # port_handler=slirp4netns preserves the client's real address. The default
  # rootlesskit handler rewrites every source to 127.0.0.1, which would blind
  # the relay's per-address admission control and make every log line say the
  # same thing. It costs some throughput and buys back the one field the
  # relay's defences are keyed on.
  cat >"$unit_dir/gul-murmur.service" <<EOF
[Unit]
Description=Gul public Mumble server
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=10min
StartLimitBurst=3

[Service]
Type=notify
NotifyAccess=all
Restart=on-failure
RestartSec=10s
# First start waits for a certificate from Let's Encrypt: the image blocks
# until /data/acme/mumble.crt exists, and that is a round trip to the outside.
TimeoutStartSec=10min
TimeoutStopSec=45s
ExecStartPre=-/usr/bin/podman rm -f gul-murmur
ExecStart=/usr/bin/podman run \\
  --rm --name gul-murmur --hostname $DOMAIN \\
  --sdnotify=conmon --cgroups=no-conmon \\
  --network slirp4netns:port_handler=slirp4netns \\
  --publish 0.0.0.0:$ACME_PORT:80/tcp \\
  --publish 0.0.0.0:$RELAY_PORT:$RELAY_PORT/tcp \\
  --publish 0.0.0.0:$RELAY_PORT:$RELAY_PORT/udp \\
  --publish 127.0.0.1:$MUMBLE_PORT:$MUMBLE_PORT/tcp \\
  --volume gul-murmur-data:/data \\
  --volume gul-murmur-certs:/data/acme \\
  --volume gul-murmur-acme:/etc/acme \\
  --secret MUMBLE_SUPERUSER_PASSWORD,type=env,target=MUMBLE_SUPERUSER_PASSWORD \\
  --secret MUMBLE_CONFIG_SERVER_PASSWORD,type=env,target=MUMBLE_CONFIG_SERVER_PASSWORD \\
  --env PUID=10000 --env PGID=10000 \\
  --env MUMBLE_CHOWN_DATA=true \\
  --env MUMBLE_VERBOSE=false \\
  --env ACME_DOMAIN=$DOMAIN \\
  --env ACME_ACCOUNT_MAIL=$ACME_MAIL \\
  --env ACME_HTTP=1 \\
  --env "MUMBLE_CONFIG_WELCOMETEXT=$welcome" \\
  --env MUMBLE_CONFIG_USERS=$users \\
  --env MUMBLE_CONFIG_BANDWIDTH=$bandwidth \\
  --env MUMBLE_CONFIG_OPUSTHRESHOLD=0 \\
  --env MUMBLE_CONFIG_TIMEOUT=30 \\
  --env MUMBLE_CONFIG_REMEMBERCHANNEL=true \\
  --env MUMBLE_CONFIG_ALLOWHTML=false \\
  --env MUMBLE_CONFIG_ALLOWPING=false \\
  --env MUMBLE_CONFIG_SENDVERSION=false \\
  --env MUMBLE_CONFIG_BONJOUR=false \\
  --env MUMBLE_CONFIG_LOGDAYS=31 \\
  --env MUMBLE_CONFIG_AUTOBANATTEMPTS=10 \\
  --env MUMBLE_CONFIG_AUTOBANTIMEFRAME=120 \\
  --env MUMBLE_CONFIG_AUTOBANTIME=300 \\
  --memory 768m --pids-limit 256 \\
  --security-opt no-new-privileges \\
  --log-driver journald \\
  $GUL_MUMBLE_IMAGE
ExecStop=/usr/bin/podman stop --ignore -t 30 gul-murmur

[Install]
WantedBy=default.target
EOF

  cat >"$unit_dir/gul-relay.service" <<EOF
[Unit]
Description=Gul authenticated relay
Requires=gul-murmur.service
After=gul-murmur.service
PartOf=gul-murmur.service
StartLimitIntervalSec=10min
StartLimitBurst=3

[Service]
Type=notify
NotifyAccess=all
Restart=on-failure
RestartSec=10s
TimeoutStartSec=2min
LimitNOFILE=8192
ExecStartPre=-/usr/bin/podman rm -f gul-wss-relay
# The relay joins Murmur's network namespace: it reaches Murmur on loopback
# and gains nothing else of the host. Murmur publishes the listener port.
ExecStart=/usr/bin/podman run \\
  --rm --name gul-wss-relay \\
  --sdnotify=conmon --cgroups=no-conmon \\
  --network container:gul-murmur \\
  --user 10000:10000 \\
  --read-only \\
  --cap-drop ALL \\
  --security-opt no-new-privileges \\
  --memory 128m --pids-limit 128 \\
  --log-driver journald \\
  --volume gul-murmur-certs:/run/relay-tls:ro \\
  --secret GUL_RELAY_BEARER,type=mount,target=GUL_RELAY_BEARER,uid=10000,gid=10000,mode=0400 \\
  localhost/gul-wss-relay@$digest \\
  --listen 0.0.0.0:$RELAY_PORT \\
  --host $DOMAIN \\
  --upstream 127.0.0.1:$MUMBLE_PORT \\
  --cert /run/relay-tls/mumble.crt \\
  --key /run/relay-tls/mumble.key \\
  --credential-file /run/secrets/GUL_RELAY_BEARER \\
  --quic=$quic \\
  --log-level info
ExecStop=/usr/bin/podman stop --ignore -t 15 gul-wss-relay

[Install]
WantedBy=default.target
EOF

  chown -R "$SERVICE_USER:$SERVICE_USER" "$SERVICE_HOME/.config"
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
  as_service systemctl --user enable gul-murmur.service gul-relay.service >/dev/null 2>&1 || true
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
  # Debian has no SELinux; AppArmor is the default and needs nothing from us.
  if command -v aa-status >/dev/null 2>&1; then
    check "AppArmor загружен" "aa-status --enabled"
  fi

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
      "Установка сервера Gul" "Debian, rootless podman"
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
  phase_units
  phase_firewall
  phase_start
  phase_verify
}

main "$@"
