#!/usr/bin/env bash
set -euo pipefail

RUN_ID="$(date +%s)"
RUN_DIR="/tmp/clawcolony-github-claim-live-smoke/${RUN_ID}"
mkdir -p "${RUN_DIR}"

BASE_URL="${BASE_URL:-http://127.0.0.1:38081}"
PF_PID=""
COOKIE_JAR="${RUN_DIR}/cookies.txt"

cleanup() {
  if [[ -n "${PF_PID}" ]]; then
    kill "${PF_PID}" >/dev/null 2>&1 || true
    wait "${PF_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

if [[ "${BASE_URL}" == "http://127.0.0.1:38081" ]]; then
  kubectl port-forward -n freewill deploy/clawcolony-runtime 38081:8080 >"${RUN_DIR}/port-forward.log" 2>&1 &
  PF_PID="$!"
  for _ in $(seq 1 30); do
    if curl -fsS "${BASE_URL}/healthz" >"${RUN_DIR}/healthz.json" 2>/dev/null; then
      break
    fi
    sleep 1
  done
fi

curl_json() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  shift 3 || true
  local curl_args=(
    -fsS
    -X "${method}"
    "${BASE_URL}${path}"
    "$@"
    -H 'Content-Type: application/json'
  )
  if [[ -n "${body}" ]]; then
    curl_args+=(--data "${body}")
  fi
  curl "${curl_args[@]}"
}

USERNAME="gh-claim-smoke-${RUN_ID}"
HUMAN_USERNAME="gh-human-${RUN_ID}"
GITHUB_CODE="gh-code-${RUN_ID}"

REGISTER_JSON="$(curl_json POST /api/v1/users/register "{\"username\":\"${USERNAME}\",\"good_at\":\"github claim smoke\"}")"
printf '%s\n' "${REGISTER_JSON}" >"${RUN_DIR}/register.json"

USER_ID="$(python3 - <<'PY' "${RUN_DIR}/register.json"
import json, sys
print(json.load(open(sys.argv[1]))["user_id"])
PY
)"
API_KEY="$(python3 - <<'PY' "${RUN_DIR}/register.json"
import json, sys
print(json.load(open(sys.argv[1]))["api_key"])
PY
)"
CLAIM_LINK="$(python3 - <<'PY' "${RUN_DIR}/register.json"
import json, sys
print(json.load(open(sys.argv[1]))["claim_link"])
PY
)"
CLAIM_TOKEN="${CLAIM_LINK##*/}"

START_JSON="$(curl_json POST /api/v1/claims/github/start "{\"claim_token\":\"${CLAIM_TOKEN}\"}" -c "${COOKIE_JAR}" -b "${COOKIE_JAR}")"
printf '%s\n' "${START_JSON}" >"${RUN_DIR}/start.json"
AUTHORIZE_URL="$(python3 - <<'PY' "${RUN_DIR}/start.json"
import json, sys
print(json.load(open(sys.argv[1]))["authorize_url"])
PY
)"
STATE="$(python3 - <<'PY' "${AUTHORIZE_URL}"
import sys, urllib.parse
u=urllib.parse.urlparse(sys.argv[1])
print(urllib.parse.parse_qs(u.query)["state"][0])
PY
)"

curl -sS -D "${RUN_DIR}/callback.headers" -o "${RUN_DIR}/callback.body" \
  -c "${COOKIE_JAR}" -b "${COOKIE_JAR}" \
  "${BASE_URL}/auth/github/claim/callback?code=${GITHUB_CODE}&state=$(python3 - <<'PY' "${STATE}"
import sys, urllib.parse
print(urllib.parse.quote(sys.argv[1], safe=''))
PY
)"

CALLBACK_LOCATION="$(python3 - <<'PY' "${RUN_DIR}/callback.headers"
import sys
for line in open(sys.argv[1]):
    if line.lower().startswith("location:"):
        print(line.split(":",1)[1].strip())
        break
PY
)"

COMPLETE_JSON="$(curl_json POST /api/v1/claims/github/complete "{\"human_username\":\"${HUMAN_USERNAME}\"}" -c "${COOKIE_JAR}" -b "${COOKIE_JAR}")"
printf '%s\n' "${COMPLETE_JSON}" >"${RUN_DIR}/complete.json"

BALANCE_JSON="$(curl -fsS "${BASE_URL}/api/v1/token/balance" -H "Authorization: Bearer ${API_KEY}")"
printf '%s\n' "${BALANCE_JSON}" >"${RUN_DIR}/balance.json"

python3 - <<'PY' "${RUN_DIR}/complete.json" "${RUN_DIR}/balance.json" "${RUN_DIR}/summary.json" "${USER_ID}" "${CALLBACK_LOCATION}" "${RUN_DIR}"
import json, sys
complete=json.load(open(sys.argv[1]))
balance=json.load(open(sys.argv[2]))
summary_path=sys.argv[3]
user_id=sys.argv[4]
callback_location=sys.argv[5]
run_dir=sys.argv[6]
item=balance["item"]
summary={
  "user_id": user_id,
  "status": complete["status"],
  "github": complete["github"],
  "rewards_count": len(complete.get("rewards", [])),
  "grant_status": complete.get("grant_status"),
  "token_balance": item["balance"],
  "callback_location": callback_location,
  "run_dir": run_dir,
}
json.dump(summary, open(summary_path, "w"), indent=2, sort_keys=True)
print(json.dumps(summary, indent=2, sort_keys=True))
if summary["status"] != "active":
    raise SystemExit("claim did not activate")
if summary["github"].get("starred") is not True or summary["github"].get("forked") is not True:
    raise SystemExit("github mock did not exercise star+fork rewards")
if int(summary["token_balance"]) != 850000:
    raise SystemExit(f"unexpected token balance {summary['token_balance']}, want 850000")
PY

printf '\nsummary: %s\n' "${RUN_DIR}/summary.json"
