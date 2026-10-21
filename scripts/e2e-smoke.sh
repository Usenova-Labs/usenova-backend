#!/usr/bin/env bash
# End-to-end smoke test for the local Usenova stack.
#
# Exercises the full local path in one go:
#
#   control plane -> agent -> nova CLI -> deploy with a published port
#   -> kill/restart -> logs -> cleanup
#
# Every step prints a pass/fail line; the summary lists every check together
# with the hint for the checks that failed.
#
# Usage:
#   scripts/e2e-smoke.sh [options]
#
# Options:
#   --keep              keep the agent/control plane and the deployed service
#   --skip-build        reuse the binaries in bin/ instead of rebuilding them
#   --port N            host port to publish for the smoke service (default 18080)
#   --image IMAGE       container image to deploy (default nginx:alpine)
#   --service NAME      service name to deploy (default smoke-web)
#   --timeout SECONDS   how long to wait for each step (default 60)
#   -h, --help          show this help
#
# Environment overrides: SMOKE_IMAGE, SMOKE_SERVICE, SMOKE_PORT,
# SMOKE_CONTAINER_PORT, SMOKE_TIMEOUT, SMOKE_DEPLOY_TIMEOUT,
# SMOKE_CONTROL_PLANE_URL, SMOKE_AGENT_URL, SMOKE_AGENT_TOKEN, SMOKE_API_TOKEN.
#
# DOCKER_HOST is honoured when set; otherwise the script pins the agent to the
# endpoint of the active docker context.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --- configuration ----------------------------------------------------------

IMAGE="${SMOKE_IMAGE:-nginx:alpine}"
SERVICE="${SMOKE_SERVICE:-smoke-web}"
PORT="${SMOKE_PORT:-18080}"
CONTAINER_PORT="${SMOKE_CONTAINER_PORT:-80}"
TIMEOUT="${SMOKE_TIMEOUT:-60}"
DEPLOY_TIMEOUT="${SMOKE_DEPLOY_TIMEOUT:-180}"
CONTROL_PLANE_URL="${SMOKE_CONTROL_PLANE_URL:-http://127.0.0.1:8080}"
AGENT_URL="${SMOKE_AGENT_URL:-http://127.0.0.1:9090}"
AGENT_TOKEN="${SMOKE_AGENT_TOKEN:-local-smoke-token}"
API_TOKEN="${SMOKE_API_TOKEN:-local-smoke-token}"
SKIP_BUILD=0
KEEP=0

BIN_DIR="$REPO_ROOT/bin"
LOG_DIR="$BIN_DIR/smoke"
NOVA="$BIN_DIR/nova"
AGENT_BIN="$BIN_DIR/usenova-agent"
CONTROL_BIN="$BIN_DIR/nova-control"

CONTROL_PLANE_PID=""
AGENT_PID=""

# --- result bookkeeping -----------------------------------------------------

PASSED=0
FAILED=0
declare -a CHECK_NAMES=()
declare -a CHECK_STATES=()
declare -a CHECK_HINTS=()

log() { printf '%s\n' "$*"; }
info() { printf '  → %s\n' "$*"; }

pass() {
  PASSED=$((PASSED + 1))
  CHECK_NAMES+=("$1")
  CHECK_STATES+=("pass")
  CHECK_HINTS+=("${2:-}")
  printf '  ✓ %s%s\n' "$1" "${2:+ — $2}"
}

fail() {
  FAILED=$((FAILED + 1))
  CHECK_NAMES+=("$1")
  CHECK_STATES+=("fail")
  CHECK_HINTS+=("${2:-}")
  printf '  ✗ %s%s\n' "$1" "${2:+ — $2}"
}

# record <ok?> <name> <detail> <hint>
record() {
  if [ "$1" = "0" ]; then
    pass "$2" "$3"
  else
    fail "$2" "$4"
  fi
}

usage() {
  sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'
}

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --keep)
        KEEP=1
        ;;
      --skip-build)
        SKIP_BUILD=1
        ;;
      --port)
        PORT="${2:?--port needs a value}"
        shift
        ;;
      --image)
        IMAGE="${2:?--image needs a value}"
        shift
        ;;
      --service)
        SERVICE="${2:?--service needs a value}"
        shift
        ;;
      --timeout)
        TIMEOUT="${2:?--timeout needs a value}"
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        echo "unknown option: $1" >&2
        usage >&2
        exit 2
        ;;
    esac
    shift
  done
}

# --- helpers ----------------------------------------------------------------

# port_in_use <host> <port>
port_in_use() {
  (exec 3<>"/dev/tcp/$1/$2") 2>/dev/null && {
    exec 3<&- 3>&-
    return 0
  } || return 1
}

# wait_for <seconds> <description> <command...>
# Runs the command until it succeeds or the timeout expires.
wait_for() {
  local deadline=$((SECONDS + $1))
  shift
  local what="$1"
  shift
  while [ "$SECONDS" -lt "$deadline" ]; do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  log "  ! timed out after ${TIMEOUT}s waiting for ${what}"
  return 1
}

# http_code prints the HTTP status the published port answers with ("000" when
# nothing answers at all).
http_code() {
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$1/" 2>/dev/null)" || code="000"
  printf '%s' "${code:-000}"
}

# http_responds reports whether the published port answers with any HTTP
# status. The status itself does not matter: the image may not serve "/".
http_responds() {
  [ "$(http_code "$1")" != "000" ]
}

# service_state prints the state the agent reports for the smoke service.
service_state() {
  "$NOVA" --agent-url "$AGENT_URL" --token "$API_TOKEN" status "$SERVICE" 2>/dev/null |
    awk '$1=="STATE" {print $2; exit}'
}

# container_id prints the docker id of the container backing the service.
container_id() {
  "$NOVA" --agent-url "$AGENT_URL" --token "$API_TOKEN" status "$SERVICE" 2>/dev/null |
    awk '$1=="CONTAINER" {print $2; exit}'
}

cleanup() {
  local code=$?
  if [ "$KEEP" -eq 0 ]; then
    # Remove the service while the agent is still up, then stop the processes.
    "$NOVA" --agent-url "$AGENT_URL" --token "$API_TOKEN" remove "$SERVICE" >/dev/null 2>&1 || true
    if [ -n "$AGENT_PID" ] && kill -0 "$AGENT_PID" 2>/dev/null; then
      kill "$AGENT_PID" 2>/dev/null || true
      wait "$AGENT_PID" 2>/dev/null || true
    fi
    if [ -n "$CONTROL_PLANE_PID" ] && kill -0 "$CONTROL_PLANE_PID" 2>/dev/null; then
      kill "$CONTROL_PLANE_PID" 2>/dev/null || true
      wait "$CONTROL_PLANE_PID" 2>/dev/null || true
    fi
    docker rm -f "usenova-$SERVICE" >/dev/null 2>&1 || true
  else
    log ""
    log "Kept running (--keep):"
    log "  control plane pid : ${CONTROL_PLANE_PID:-none}"
    log "  agent pid         : ${AGENT_PID:-none}"
    log "  service           : $SERVICE (container usenova-$SERVICE)"
    log "  logs              : $LOG_DIR"
  fi
  exit "$code"
}

# --- steps ------------------------------------------------------------------

preflight() {
  log "Preflight"

  mkdir -p "$LOG_DIR"

  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    record 0 "docker daemon reachable" "" ""
  else
    record 1 "docker daemon reachable" "" \
      "Is Docker installed and running? Check with: docker info"
    exit 1
  fi

  if command -v go >/dev/null 2>&1; then
    record 0 "go toolchain available" "$(go version | awk '{print $3}')" ""
  else
    record 1 "go toolchain available" "" \
      "Install Go 1.21+ (https://go.dev/dl/) and re-run the script"
    exit 1
  fi

  # The agent reaches Docker through DOCKER_HOST while the docker CLI uses the
  # active context. Pin both to the same endpoint so the script inspects the
  # containers the agent actually runs.
  if [ -z "${DOCKER_HOST:-}" ]; then
    endpoint="$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)"
    if [ -n "$endpoint" ]; then
      export DOCKER_HOST="$endpoint"
      info "DOCKER_HOST=$endpoint (from the active docker context)"
    fi
  fi

  # The agent pulls the image on every deploy. Fetch it up front so a slow
  # registry cannot turn into a deploy timeout later on.
  if docker image inspect "$IMAGE" >/dev/null 2>&1; then
    record 0 "image available locally" "$IMAGE" ""
  elif timeout "$DEPLOY_TIMEOUT" docker pull "$IMAGE" >"$LOG_DIR/pull.log" 2>&1; then
    record 0 "image pulled" "$IMAGE" ""
  else
    record 1 "image available locally" "" \
      "The agent pulls the image on every deploy and the registry was unreachable. See $LOG_DIR/pull.log"
    exit 1
  fi

  if [ "$PORT" = "8080" ] || [ "$PORT" = "9090" ]; then
    record 1 "published port is free" "" \
      "Port $PORT is used by the control plane (8080) or the agent API (9090). Pass --port with a different host port."
    exit 1
  fi

  local busy=""
  port_in_use 127.0.0.1 8080 && busy="8080 (control plane)"
  port_in_use 127.0.0.1 9090 && busy="$busy 9090 (agent API)"
  port_in_use 127.0.0.1 "$PORT" && busy="$busy $PORT (published port)"
  if [ -z "$busy" ]; then
    record 0 "ports free" "8080, 9090, $PORT" ""
  else
    record 1 "ports free" "" \
      "Already in use: $busy. The control plane port is fixed at 8080; free it or pass --port for the published port."
    exit 1
  fi
}

build() {
  log ""
  log "Build"

  if [ "$SKIP_BUILD" -eq 1 ]; then
    record 0 "binaries present (--skip-build)" "" ""
    return
  fi

  mkdir -p "$BIN_DIR" "$LOG_DIR"
  if (cd apps/cli && go build -o "$NOVA") &&
    (cd apps/agent && go build -o "$AGENT_BIN") &&
    (cd apps/control-plane && go build -o "$CONTROL_BIN"); then
    record 0 "binaries built" "nova, usenova-agent, nova-control" ""
  else
    record 1 "binaries built" "" "The build failed; run scripts/build.sh to see the compiler output"
    exit 1
  fi
}

start_control_plane() {
  log ""
  log "Control plane"

  mkdir -p "$LOG_DIR"
  "$CONTROL_BIN" >"$LOG_DIR/control-plane.log" 2>&1 &
  CONTROL_PLANE_PID=$!

  if wait_for "$TIMEOUT" "the control plane" curl -fsS "$CONTROL_PLANE_URL/health"; then
    record 0 "control plane healthy" "$CONTROL_PLANE_URL/health" ""
  else
    record 1 "control plane healthy" "" \
      "The control plane listens on :8080 and needs no configuration. See $LOG_DIR/control-plane.log"
    exit 1
  fi
}

start_agent() {
  log ""
  log "Agent"

  CONTROL_PLANE_URL="$CONTROL_PLANE_URL" \
    AGENT_TOKEN="$AGENT_TOKEN" \
    API_ADDR="127.0.0.1:9090" \
    API_TOKEN="$API_TOKEN" \
    HEARTBEAT_INTERVAL=5s \
    "$AGENT_BIN" >"$LOG_DIR/agent.log" 2>&1 &
  AGENT_PID=$!

  if wait_for "$TIMEOUT" "the agent local API" curl -fsS \
    -H "Authorization: Bearer $API_TOKEN" "$AGENT_URL/services"; then
    record 0 "agent local API reachable" "$AGENT_URL" ""
  else
    record 1 "agent local API reachable" "" \
      "The local API is loopback-only: API_ADDR must be 127.0.0.1:9090 and API_TOKEN must match the CLI token. See $LOG_DIR/agent.log"
    exit 1
  fi

  if wait_for "$TIMEOUT" "the agent registration" bash -c \
    "curl -fsS '$CONTROL_PLANE_URL/api/v1/agents' | grep -q '\"agent_id\"'"; then
    record 0 "agent registered with control plane" "" ""
  else
    record 1 "agent registered with control plane" "" \
      "Registration is fatal: the agent retries 3 times (5s apart) and exits when the control plane is unreachable. Check CONTROL_PLANE_URL and $LOG_DIR/agent.log"
  fi
}

deploy() {
  log ""
  log "Deploy"

  # A leftover service from an earlier run would block the deploy.
  "$NOVA" --agent-url "$AGENT_URL" --token "$API_TOKEN" remove "$SERVICE" >/dev/null 2>&1 || true

  # The agent pulls the image on every deploy, so a flaky registry fails the
  # deploy even when the image is cached. Retry once before giving up.
  local attempt=1
  local deployed=1
  while [ "$attempt" -le 2 ]; do
    if "$NOVA" --agent-url "$AGENT_URL" --token "$API_TOKEN" --timeout "${DEPLOY_TIMEOUT}s" \
      deploy --name "$SERVICE" --image "$IMAGE" \
      --port "$PORT:$CONTAINER_PORT" \
      --health-port "$CONTAINER_PORT" --health-path / \
      --restart on-failure >"$LOG_DIR/deploy.log" 2>&1; then
      deployed=0
      break
    fi
    attempt=$((attempt + 1))
    info "deploy failed, retrying once (see $LOG_DIR/deploy.log)"
    sleep 5
  done

  if [ "$deployed" -eq 0 ]; then
    record 0 "service deployed" "$SERVICE ($IMAGE)" ""
  else
    record 1 "service deployed" "" \
      "nova deploy needs --name and --image. The agent pulls the image on every deploy, so an unreachable registry fails the deploy even for a cached image. See $LOG_DIR/deploy.log"
    return
  fi

  if wait_for "$TIMEOUT" "the service to report running" bash -c \
    "[ \"\$('$NOVA' --agent-url '$AGENT_URL' --token '$API_TOKEN' status '$SERVICE' 2>/dev/null | awk '\$1==\"STATE\" {print \$2; exit}')\" = 'running' ]"; then
    record 0 "service running" "$SERVICE" ""
  else
    record 1 "service running" "" \
      "The agent deploys asynchronously. Check 'nova status $SERVICE' and $LOG_DIR/agent.log for the deploy error"
  fi
}

check_published_port() {
  log ""
  log "Published port"

  if wait_for "$TIMEOUT" "the published port to answer" http_responds "$PORT"; then
    record 0 "published port answers" "http://127.0.0.1:$PORT/ -> container $CONTAINER_PORT (HTTP $(http_code "$PORT"))" ""
  else
    record 1 "published port answers" "" \
      "The mapping is host:container (--port $PORT:$CONTAINER_PORT). A wrong container port or a slow start shows up as a refused connection."
  fi

  if docker port "usenova-$SERVICE" "$CONTAINER_PORT/tcp" 2>/dev/null | grep -q ":$PORT"; then
    record 0 "container publishes the port" "$PORT -> $CONTAINER_PORT" ""
  else
    record 1 "container publishes the port" "" \
      "Expected docker port to map $PORT onto $CONTAINER_PORT/tcp. Check the --port mapping and that the container is running."
  fi
}

check_kill_restart() {
  log ""
  log "Kill / restart"

  local id
  id="$(container_id || true)"
  if [ -z "$id" ]; then
    id="$(docker ps -q --filter "name=usenova-$SERVICE" 2>/dev/null | head -n1 || true)"
  fi
  if [ -z "$id" ]; then
    record 1 "container found" "" "Could not find the container for service $SERVICE"
    return
  fi
  record 0 "container found" "$id" ""

  if docker kill "$id" >/dev/null 2>&1; then
    record 0 "container killed" "$id" ""
  else
    record 1 "container killed" "" "docker kill failed; is the container still there?"
    return
  fi

  if wait_for "$TIMEOUT" "the container to come back" bash -c \
    "[ \"\$(docker inspect -f '{{.State.Running}}' '$id' 2>/dev/null)\" = 'true' ]"; then
    record 0 "container restarted" "$id" ""
  else
    record 1 "container restarted" "" \
      "Docker restarts the container through its restart policy (on-failure). The agent re-checks every 60s and bounds its own restarts (see RESTART_MAX_RESTARTS)."
    return
  fi

  if wait_for "$TIMEOUT" "the published port to answer again" http_responds "$PORT"; then
    record 0 "published port answers after restart" "http://127.0.0.1:$PORT/" ""
  else
    record 1 "published port answers after restart" "" \
      "The container came back but does not serve yet; the image may need more time to start."
  fi

  if [ "$(service_state || true)" = "running" ]; then
    record 0 "agent still tracks the service" "state=running" ""
  else
    record 1 "agent still tracks the service" "" \
      "The agent reconciles every 60s. If the service is 'failed', the restart budget may be exhausted (RESTART_MAX_RESTARTS)."
  fi
}

check_logs() {
  log ""
  log "Logs"

  if "$NOVA" --agent-url "$AGENT_URL" --token "$API_TOKEN" logs "$SERVICE" --tail 20 \
    >"$LOG_DIR/service.log" 2>&1 && [ -s "$LOG_DIR/service.log" ]; then
    record 0 "service logs retrievable" "$LOG_DIR/service.log" ""
  else
    record 1 "service logs retrievable" "" \
      "nova logs reads the container log through the agent API. Check that the container is still running."
  fi
}

remove_service() {
  log ""
  log "Cleanup"

  "$NOVA" --agent-url "$AGENT_URL" --token "$API_TOKEN" stop "$SERVICE" >/dev/null 2>&1 || true
  if "$NOVA" --agent-url "$AGENT_URL" --token "$API_TOKEN" remove "$SERVICE" >/dev/null 2>&1; then
    record 0 "service removed" "$SERVICE" ""
  else
    record 1 "service removed" "" "nova remove deletes the container and forgets the service"
  fi
}

summary() {
  log ""
  log "Summary"
  for i in "${!CHECK_NAMES[@]}"; do
    if [ "${CHECK_STATES[$i]}" = "pass" ]; then
      printf '  ✓ %s\n' "${CHECK_NAMES[$i]}"
    else
      printf '  ✗ %s\n' "${CHECK_NAMES[$i]}"
    fi
  done

  if [ "$FAILED" -gt 0 ]; then
    log ""
    log "Common failures"
    for i in "${!CHECK_NAMES[@]}"; do
      if [ "${CHECK_STATES[$i]}" = "fail" ] && [ -n "${CHECK_HINTS[$i]}" ]; then
        printf '  ✗ %s\n      %s\n' "${CHECK_NAMES[$i]}" "${CHECK_HINTS[$i]}"
      fi
    done
  fi

  log ""
  if [ "$FAILED" -eq 0 ]; then
    log "✓ All $PASSED checks passed"
  else
    log "✗ $FAILED of $((PASSED + FAILED)) checks failed"
    log "  Logs: $LOG_DIR"
  fi
}

main() {
  trap cleanup EXIT

  parse_args "$@"

  log "Usenova end-to-end smoke test"
  log "  control plane : $CONTROL_PLANE_URL"
  log "  agent API     : $AGENT_URL"
  log "  service       : $SERVICE ($IMAGE, published on port $PORT)"
  log "  logs          : $LOG_DIR"

  preflight
  build
  start_control_plane
  start_agent
  deploy
  check_published_port
  check_kill_restart
  check_logs
  remove_service
  summary

  [ "$FAILED" -eq 0 ]
}

main "$@"
