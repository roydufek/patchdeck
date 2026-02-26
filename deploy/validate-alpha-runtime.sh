#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${PATCHDECK_BASE_URL:-http://localhost:6070}"
COMPOSE_FILE="${PATCHDECK_COMPOSE_FILE:-docker-compose.yml}"
ADMIN_USER="${PATCHDECK_ADMIN_USER:-}"
ADMIN_PASS="${PATCHDECK_ADMIN_PASS:-}"
TEST_APPRISE_URL="${PATCHDECK_TEST_APPRISE_URL:-mailto://alpha-test@example.invalid}"

fail() {
  echo "[FAIL] $*" >&2
  exit 1
}

pass() {
  echo "[PASS] $*"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "required command missing: $1"
}

need_cmd curl
need_cmd docker
need_cmd jq

if [[ ! -f "$COMPOSE_FILE" ]]; then
  fail "docker compose file not found: $COMPOSE_FILE"
fi

pass "validating docker compose config"
docker compose -f "$COMPOSE_FILE" config >/dev/null

pass "checking API and web containers are running"
for service in api web; do
  cid="$(docker compose -f "$COMPOSE_FILE" ps -q "$service")"
  [[ -n "$cid" ]] || fail "service '$service' is not created"
  state="$(docker inspect -f '{{.State.Status}}' "$cid")"
  [[ "$state" == "running" ]] || fail "service '$service' is not running (state=$state)"
done

pass "checking API health endpoint"
health_json="$(curl -fsS "$BASE_URL/healthz")"
echo "$health_json" | jq -e '.ok == true' >/dev/null || fail "/healthz did not return ok=true"

pass "checking setup wizard status endpoint"
setup_json="$(curl -fsS "$BASE_URL/api/setup")"
bootstrap_required="$(echo "$setup_json" | jq -r '.bootstrap_required')"

if [[ -z "$ADMIN_USER" || -z "$ADMIN_PASS" ]]; then
  echo "[WARN] PATCHDECK_ADMIN_USER/PATCHDECK_ADMIN_PASS not set; skipping authenticated host ops, scheduler, and notification-path checks"
  exit 0
fi

pass "logging in as admin"
login_json="$(curl -fsS -X POST "$BASE_URL/api/login" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg u "$ADMIN_USER" --arg p "$ADMIN_PASS" '{username:$u,password:$p}')")" || fail "login request failed"

token="$(echo "$login_json" | jq -r '.token // empty')"
[[ -n "$token" ]] || fail "login succeeded but no token returned"
AUTH_HEADER=( -H "Authorization: Bearer $token" )

pass "checking authenticated host listing"
curl -fsS "$BASE_URL/api/hosts" "${AUTH_HEADER[@]}" >/dev/null

tmp_key="$(mktemp)"
trap 'rm -f "$tmp_key"' EXIT
ssh-keygen -q -t ed25519 -N '' -f "$tmp_key" >/dev/null
private_key="$(cat "$tmp_key")"

host_name="alpha-runtime-check-$(date +%s)"
pass "creating temporary host for host-ops + scheduler path checks"
create_host_payload="$(jq -nc \
  --arg name "$host_name" \
  --arg key "$private_key" \
  '{name:$name,address:"127.0.0.1",port:22,ssh_user:"root",auth_type:"key",private_key_pem:$key,host_key_required:true,host_key_trust_mode:"tofu"}')"
create_host_json="$(curl -fsS -X POST "$BASE_URL/api/hosts" "${AUTH_HEADER[@]}" -H 'Content-Type: application/json' -d "$create_host_payload")"
[[ "$(echo "$create_host_json" | jq -r '.message // empty')" == "host added" ]] || fail "failed to create temporary host"

hosts_json="$(curl -fsS "$BASE_URL/api/hosts" "${AUTH_HEADER[@]}")"
host_id="$(echo "$hosts_json" | jq -r --arg n "$host_name" '.[] | select(.name==$n) | .id' | head -n1)"
[[ -n "$host_id" ]] || fail "temporary host not found after creation"

cleanup() {
  local job_id="${1:-}"
  if [[ -n "$job_id" ]]; then
    curl -fsS -X DELETE "$BASE_URL/api/jobs/$job_id" "${AUTH_HEADER[@]}" >/dev/null || true
  fi
  if [[ -n "$host_id" ]]; then
    curl -fsS -X DELETE "$BASE_URL/api/hosts/$host_id" "${AUTH_HEADER[@]}" >/dev/null || true
  fi
}

job_id=""
trap 'cleanup "$job_id"; rm -f "$tmp_key"' EXIT

pass "validating host operational controls update path"
ops_payload='{"checks_enabled":true,"auto_update_policy":"manual"}'
ops_json="$(curl -fsS -X PUT "$BASE_URL/api/hosts/$host_id/operations" "${AUTH_HEADER[@]}" -H 'Content-Type: application/json' -d "$ops_payload")"
[[ "$(echo "$ops_json" | jq -r '.message // empty')" == "host operational controls updated" ]] || fail "host operations update path failed"

pass "validating scheduler create/list/delete path"
job_payload="$(jq -nc --arg host_id "$host_id" '{host_id:$host_id,name:"alpha-runtime-check",cron_expr:"*/30 * * * *",mode:"scan",enabled:true}')"
create_job_json="$(curl -fsS -X POST "$BASE_URL/api/jobs" "${AUTH_HEADER[@]}" -H 'Content-Type: application/json' -d "$job_payload")"
[[ "$(echo "$create_job_json" | jq -r '.message // empty')" == "scheduled" ]] || fail "job create path failed"

jobs_json="$(curl -fsS "$BASE_URL/api/jobs" "${AUTH_HEADER[@]}")"
job_id="$(echo "$jobs_json" | jq -r --arg host_id "$host_id" '.[] | select(.host_id==$host_id) | .id' | head -n1)"
[[ -n "$job_id" ]] || fail "created job not found in scheduler list"

pass "checking notification runtime + test endpoint path"
curl -fsS "$BASE_URL/api/settings/notifications/runtime" "${AUTH_HEADER[@]}" | jq -e '.available != null' >/dev/null || fail "notification runtime endpoint missing expected shape"

notif_test_payload="$(jq -nc --arg u "$TEST_APPRISE_URL" '{apprise_url:$u}')"
notif_code="$(curl -sS -o /tmp/patchdeck-alpha-notif.json -w '%{http_code}' -X POST "$BASE_URL/api/settings/notifications/test" "${AUTH_HEADER[@]}" -H 'Content-Type: application/json' -d "$notif_test_payload")"
case "$notif_code" in
  200|409|502)
    pass "notification test path reached (http $notif_code)"
    ;;
  *)
    cat /tmp/patchdeck-alpha-notif.json >&2 || true
    fail "unexpected notification test response code: $notif_code"
    ;;
esac

cleanup "$job_id"
host_id=""
job_id=""

pass "alpha runtime validation complete"
echo "bootstrap_required=$bootstrap_required"