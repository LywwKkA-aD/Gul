#!/usr/bin/env bash
# Local-only experiment. Never modifies the existing Mumble or remote services.
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE="$ROOT/bin/livekit-local"
BINARY="$STATE/livekit-lab"
CONTAINER="gul-livekit-local"
LABEL="org.gul.livekit-lab"
IMAGE="livekit/livekit-server:v1.13.8@sha256:ad961c9b4b064298cc08d618d772671f7c783e036001388079fa813929cc357e"

fail() { printf '%s\n' "$1" >&2; exit 1; }

owned_container() {
  local owner
  owner="$(docker inspect --format "{{index .Config.Labels \"$LABEL\"}}" "$CONTAINER" 2>/dev/null)" || return 1
  [[ "$owner" == "$ROOT" ]] || fail "Container name belongs to another project; nothing changed."
}

broker_pid() {
  [[ -f "$STATE/broker.pid" ]] || return 1
  local pid command
  pid="$(cat "$STATE/broker.pid")"
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  command="$(ps -p "$pid" -o command=)"
  [[ "$command" == "$BINARY -config "* ]] || return 1
  printf '%s\n' "$pid"
}

start_server() {
  docker info >/dev/null 2>&1 || fail "Start Docker or OrbStack first."
  if owned_container; then
    docker start "$CONTAINER" >/dev/null
  else
    docker run -d --name "$CONTAINER" --label "$LABEL=$ROOT" \
      --restart no --read-only --tmpfs /tmp:rw,noexec,nosuid,size=16m \
      --cap-drop ALL --security-opt no-new-privileges:true \
      --pids-limit 128 --memory 512m \
      -p 127.0.0.1:7880:7880/tcp \
      -p 127.0.0.1:7881:7881/tcp \
      -p 127.0.0.1:7882:7882/udp \
      --mount "type=bind,source=$STATE/server.yaml,target=/etc/livekit.yaml,readonly" \
      "$IMAGE" --config /etc/livekit.yaml >/dev/null
  fi
  local _attempt
  for _attempt in {1..30}; do
    if curl --noproxy '*' --silent --fail --max-time 1 http://127.0.0.1:7880/ >/dev/null; then
      return
    fi
    sleep 0.2
  done
  fail "Local LiveKit did not become ready; inspect container $CONTAINER."
}

up() {
  mkdir -p "$STATE"
  chmod 700 "$STATE"
  if ! broker_pid >/dev/null; then
    (cd "$ROOT" && GOTOOLCHAIN=go1.26.7 go build -o "$BINARY" ./cmd/livekit-lab)
    "$BINARY" -init "$STATE"
  fi
  start_server
  if ! broker_pid >/dev/null; then
    [[ -f "$ROOT/frontend/dist/index.html" ]] || fail "Run npm --prefix frontend run build first."
    if [[ "${1:-}" == "--foreground" ]]; then
      printf '%s\n' "$$" >"$STATE/broker.pid"
      exec "$BINARY" -config "$STATE/broker.json" -web-dir "$ROOT/frontend/dist"
    fi
    nohup "$BINARY" -config "$STATE/broker.json" -web-dir "$ROOT/frontend/dist" \
      >"$STATE/broker.log" 2>&1 </dev/null &
    printf '%s\n' "$!" >"$STATE/broker.pid"
  fi
  local _attempt
  for _attempt in {1..30}; do
    if broker_pid >/dev/null && curl --noproxy '*' --silent --fail --max-time 1 http://127.0.0.1:8787/healthz >/dev/null; then
      printf '%s\n' "Local lab ready: http://127.0.0.1:8787/#livekit" "Room: gul-local. All published ports are bound to 127.0.0.1."
      return
    fi
    sleep 0.2
  done
  fail "Local broker did not become ready; inspect bin/livekit-local/broker.log."
}

down() {
  local pid
  if pid="$(broker_pid)"; then
    kill "$pid"
    rm -f "$STATE/broker.pid"
  fi
  if owned_container; then
    docker stop --time 5 "$CONTAINER" >/dev/null
    docker rm "$CONTAINER" >/dev/null
  fi
  printf '%s\n' "Local LiveKit lab stopped. Private credentials retained for the next run."
}

status() {
  if broker_pid >/dev/null && curl --noproxy '*' --silent --fail --max-time 1 http://127.0.0.1:8787/healthz >/dev/null; then
    printf '%s\n' "Broker: running at http://127.0.0.1:8787/#livekit"
  else
    printf '%s\n' "Broker: stopped"
  fi
  if owned_container; then
    docker inspect --format 'LiveKit: {{.State.Status}} ({{.Config.Image}})' "$CONTAINER"
    docker port "$CONTAINER"
  else
    printf '%s\n' "LiveKit: stopped"
  fi
}

case "${1:-}" in
  up)
    [[ "${2:-}" == "" || "${2:-}" == "--foreground" ]] || fail "Unknown option."
    up "${2:-}"
    ;;
  down|stop) down ;;
  status) status ;;
  *) printf '%s\n' "Usage: scripts/livekit-local.sh {up [--foreground]|status|down}"; exit 2 ;;
esac
