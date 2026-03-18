#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME_NS="${RUNTIME_NS:-freewill}"
RUNTIME_TARGET="${RUNTIME_TARGET:-deploy/clawcolony-runtime}"
POSTGRES_POD="${POSTGRES_POD:-clawcolony-postgres-0}"
POSTGRES_DB="${POSTGRES_DB:-clawcolony_runtime}"
POSTGRES_USER="${POSTGRES_USER:-clawcolony}"
LOCAL_PORT="${LOCAL_PORT:-38080}"
OUTPUT_ROOT="${OUTPUT_ROOT:-/tmp/clawcolony-token-econ-live-smoke}"
RUN_ID="${RUN_ID:-$(date +%s)}"
OUTPUT_DIR="${OUTPUT_ROOT}/${RUN_ID}"
SMOKE_TREASURY_TARGET_BALANCE="${SMOKE_TREASURY_TARGET_BALANCE:-200000}"
SMOKE_TREASURY_MIN_REQUIRED="${SMOKE_TREASURY_MIN_REQUIRED:-125000}"

mkdir -p "${OUTPUT_DIR}"
TMP_DIR="$(mktemp -d)"
STEPS_FILE="${OUTPUT_DIR}/steps.ndjson"
SUMMARY_FILE="${OUTPUT_DIR}/summary.json"
PORT_FORWARD_LOG="${OUTPUT_DIR}/port-forward.log"
LAST_BODY="${TMP_DIR}/last-response.json"
TREASURY_TOPUP_FILE="${TMP_DIR}/treasury-topups.tsv"

PF_PID=""

cleanup() {
  if [[ -n "${PF_PID}" ]] && kill -0 "${PF_PID}" >/dev/null 2>&1; then
    kill "${PF_PID}" >/dev/null 2>&1 || true
    wait "${PF_PID}" >/dev/null 2>&1 || true
  fi
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "[FAIL] missing required command: $1" >&2
    exit 1
  }
}

for bin in jq curl kubectl base64; do
  require_cmd "${bin}"
done

BASE_URL="http://127.0.0.1:${LOCAL_PORT}"

record_step() {
  printf '%s\n' "$1" >>"${STEPS_FILE}"
}

fail_http() {
  local method="$1"
  local path="$2"
  local code="$3"
  echo "[FAIL] ${method} ${path} -> HTTP ${code}" >&2
  if [[ -f "${LAST_BODY}" ]] && [[ -s "${LAST_BODY}" ]]; then
    echo "[FAIL] response body:" >&2
    cat "${LAST_BODY}" >&2
    echo >&2
  fi
  exit 1
}

request() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  shift 3 || true
  local -a headers=()
  if (($# > 0)); then
    headers=("$@")
  fi
  local -a curl_args=(
    -sS
    -o "${LAST_BODY}"
    -w "%{http_code}"
    -X "${method}"
    "${BASE_URL}${path}"
  )
  if [[ "${method}" != "GET" && "${method}" != "HEAD" ]]; then
    curl_args+=(-H "Content-Type: application/json")
  fi
  local h
  if [[ ${#headers[@]} -gt 0 ]]; then
    for h in "${headers[@]}"; do
      curl_args+=(-H "${h}")
    done
  fi
  if [[ -n "${body}" ]]; then
    curl_args+=(--data "${body}")
  fi
  curl "${curl_args[@]}"
}

api_get() {
  local api_key="$1"
  local path="$2"
  request GET "${path}" "" "Authorization: Bearer ${api_key}"
}

api_post() {
  local api_key="$1"
  local path="$2"
  local body="$3"
  request POST "${path}" "${body}" "Authorization: Bearer ${api_key}"
}

public_post() {
  local path="$1"
  local body="$2"
  request POST "${path}" "${body}"
}

internal_post() {
  local path="$1"
  local body="$2"
  request POST "${path}" "${body}" "X-Clawcolony-Internal-Token: ${INTERNAL_SYNC_TOKEN}"
}

db_query() {
  kubectl exec -i -n "${RUNTIME_NS}" "${POSTGRES_POD}" -- \
    psql -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -v ON_ERROR_STOP=1 -At -F $'\t' "$@"
}

sql_scalar() {
  db_query -c "$1" | tr -d '\n'
}

sql_json() {
  db_query -c "$1" | tr -d '\n'
}

balance_of() {
  local user_id="$1"
  sql_scalar "SELECT COALESCE((SELECT balance FROM token_accounts WHERE user_id = '${user_id}'), 0);"
}

supply_json() {
  sql_json "
    SELECT json_build_object(
      'token_accounts', count(*),
      'total_supply', COALESCE(sum(balance), 0),
      'treasury_balance', COALESCE(MAX(CASE WHEN user_id = 'clawcolony-treasury' THEN balance END), 0)
    )
    FROM token_accounts;
  "
}

decision_json() {
  local decision_key="$1"
  sql_json "
    SELECT COALESCE(
      (
        SELECT row_to_json(t)
        FROM (
          SELECT decision_key, rule_key, recipient_user_id, amount, status,
                 COALESCE(queue_reason, '') AS queue_reason,
                 COALESCE(ledger_id, 0) AS ledger_id,
                 COALESCE(balance_after, 0) AS balance_after,
                 created_at
          FROM economy_reward_decisions
          WHERE decision_key = '${decision_key}'
        ) t
      ),
      'null'::json
    );
  "
}

decision_prefix_json() {
  local prefix="$1"
  sql_json "
    SELECT COALESCE(
      json_agg(row_to_json(t) ORDER BY t.created_at, t.decision_key),
      '[]'::json
    )
    FROM (
      SELECT decision_key, rule_key, recipient_user_id, amount, status,
             COALESCE(queue_reason, '') AS queue_reason,
             COALESCE(ledger_id, 0) AS ledger_id,
             COALESCE(balance_after, 0) AS balance_after,
             created_at
      FROM economy_reward_decisions
      WHERE decision_key LIKE '${prefix}%'
    ) t;
  "
}

treasury_transfer() {
  local user_id="$1"
  local amount="$2"
  db_query <<SQL
DO \$\$
DECLARE treasury_after BIGINT;
DECLARE user_after BIGINT;
BEGIN
  UPDATE token_accounts
     SET balance = balance - ${amount}, updated_at = NOW()
   WHERE user_id = 'clawcolony-treasury'
     AND balance >= ${amount}
  RETURNING balance INTO treasury_after;
  IF treasury_after IS NULL THEN
    RAISE EXCEPTION 'treasury_insufficient';
  END IF;

  UPDATE token_accounts
     SET balance = balance + ${amount}, updated_at = NOW()
   WHERE user_id = '${user_id}'
  RETURNING balance INTO user_after;
  IF user_after IS NULL THEN
    RAISE EXCEPTION 'user_not_found';
  END IF;

  INSERT INTO token_ledger(user_id, op_type, amount, balance_after, created_at)
  VALUES
    ('clawcolony-treasury', 'smoke_seed_out', ${amount}, treasury_after, NOW()),
    ('${user_id}', 'smoke_seed_in', ${amount}, user_after, NOW());
END
\$\$;
SQL
}

user_to_treasury_transfer() {
  local user_id="$1"
  local amount="$2"
  db_query <<SQL
DO \$\$
DECLARE treasury_after BIGINT;
DECLARE user_after BIGINT;
BEGIN
  UPDATE token_accounts
     SET balance = balance - ${amount}, updated_at = NOW()
   WHERE user_id = '${user_id}'
     AND balance >= ${amount}
  RETURNING balance INTO user_after;
  IF user_after IS NULL THEN
    RAISE EXCEPTION 'user_insufficient';
  END IF;

  UPDATE token_accounts
     SET balance = balance + ${amount}, updated_at = NOW()
   WHERE user_id = 'clawcolony-treasury'
  RETURNING balance INTO treasury_after;
  IF treasury_after IS NULL THEN
    RAISE EXCEPTION 'treasury_not_found';
  END IF;

  INSERT INTO token_ledger(user_id, op_type, amount, balance_after, created_at)
  VALUES
    ('${user_id}', 'smoke_pool_loan_out', ${amount}, user_after, NOW()),
    ('clawcolony-treasury', 'smoke_pool_loan_in', ${amount}, treasury_after, NOW());
END
\$\$;
SQL
}

top_up_treasury_from_smoke_donors() {
  local target_balance="$1"
  local min_required="$2"
  : >"${TREASURY_TOPUP_FILE}"
  local current_treasury
  current_treasury="$(balance_of "clawcolony-treasury")"
  if (( current_treasury >= target_balance )); then
    return 0
  fi
  while IFS=$'\t' read -r donor_user_id donor_balance; do
    [[ -n "${donor_user_id}" ]] || continue
    [[ "${donor_user_id}" == "clawcolony-treasury" ]] && continue
    if (( current_treasury >= target_balance )); then
      break
    fi
    local needed transferable
    needed=$(( target_balance - current_treasury ))
    transferable="${donor_balance}"
    if (( transferable > needed )); then
      transferable="${needed}"
    fi
    if (( transferable <= 0 )); then
      continue
    fi
    user_to_treasury_transfer "${donor_user_id}" "${transferable}"
    current_treasury=$(( current_treasury + transferable ))
    printf '%s\t%s\n' "${donor_user_id}" "${transferable}" >>"${TREASURY_TOPUP_FILE}"
  done < <(
    db_query -c "
      SELECT ta.user_id, ta.balance
      FROM token_accounts ta
      LEFT JOIN agent_human_bindings ahb ON ahb.user_id = ta.user_id
      LEFT JOIN human_owners ho ON ho.owner_id = ahb.owner_id
      WHERE ta.user_id <> 'clawcolony-treasury'
        AND (
          ta.user_id LIKE 'smoke-%'
          OR ta.user_id LIKE '%smoke%'
          OR COALESCE(ho.human_username, '') LIKE 'smoke%'
        )
      ORDER BY balance DESC, user_id;
    "
  )
  if (( current_treasury < min_required )); then
    echo "[FAIL] treasury funding pool only reached ${current_treasury}, below required ${min_required}" >&2
    exit 1
  fi
}

upsert_internal_user() {
  local user_id="$1"
  local username="$2"
  local api_key="$3"
  local payload
  payload="$(jq -nc \
    --arg user_id "${user_id}" \
    --arg username "${username}" \
    --arg api_key "${api_key}" \
    '{
      op: "upsert",
      user: {
        user_id: $user_id,
        name: $username,
        provider: "agent",
        status: "running",
        initialized: true,
        username: $username,
        good_at: "token economy smoke",
        api_key: $api_key
      }
    }')"
  local code
  code="$(internal_post "/api/v1/internal/users/sync" "${payload}")"
  [[ "${code}" =~ ^2 ]] || fail_http POST "/api/v1/internal/users/sync" "${code}"
}

wait_for_port_forward() {
  local attempts=50
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl -sf "${BASE_URL}/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  echo "[FAIL] runtime port-forward did not become ready" >&2
  exit 1
}

start_port_forward() {
  kubectl port-forward -n "${RUNTIME_NS}" "${RUNTIME_TARGET}" "${LOCAL_PORT}:8080" >"${PORT_FORWARD_LOG}" 2>&1 &
  PF_PID="$!"
  wait_for_port_forward
}

long_ascii() {
  local count="$1"
  head -c "${count}" < /dev/zero | tr '\0' 'a'
}

copy_last_body() {
  local dest="$1"
  cp "${LAST_BODY}" "${dest}"
}

INTERNAL_SYNC_TOKEN="$(kubectl get secret -n "${RUNTIME_NS}" clawcolony-runtime -o jsonpath='{.data.CLAWCOLONY_INTERNAL_SYNC_TOKEN}' | base64 --decode)"
start_port_forward

LAW_CODE="$(request GET "/api/v1/tian-dao/law")"
[[ "${LAW_CODE}" =~ ^2 ]] || fail_http GET "/api/v1/tian-dao/law" "${LAW_CODE}"
copy_last_body "${TMP_DIR}/law.json"

INITIAL_TOKEN="$(jq -r '.manifest.initial_token // 100000' "${TMP_DIR}/law.json")"
DAILY_TAX_UNACTIVATED="$(jq -r '.manifest.daily_tax_unactivated // 100000' "${TMP_DIR}/law.json")"
DAILY_TAX_ACTIVATED="$(jq -r '.manifest.daily_tax_activated // 50000' "${TMP_DIR}/law.json")"
HIBERNATION_TICKS="$(jq -r '.manifest.hibernation_ticks // 1440' "${TMP_DIR}/law.json")"
MIN_REVIVAL_BALANCE="$(jq -r '.manifest.min_revival_balance // 50000' "${TMP_DIR}/law.json")"
TICK_INTERVAL_SECONDS="$(jq -r '.manifest.tick_interval_seconds // 60' "${TMP_DIR}/law.json")"
CONSTITUTION_PASSED="$(sql_scalar "SELECT CASE WHEN EXISTS (SELECT 1 FROM kb_entries WHERE section = 'governance/constitution' AND is_deleted = FALSE) THEN 'true' ELSE 'false' END;")"

TICKS_PER_DAY=1440
TAX_PER_TICK_UNACTIVATED="$(( (DAILY_TAX_UNACTIVATED + TICKS_PER_DAY - 1) / TICKS_PER_DAY ))"
TAX_PER_TICK_ACTIVATED="$(( (DAILY_TAX_ACTIVATED + TICKS_PER_DAY - 1) / TICKS_PER_DAY ))"
ALIVE_TICKS_UNACTIVATED="$(( (INITIAL_TOKEN + TAX_PER_TICK_UNACTIVATED - 1) / TAX_PER_TICK_UNACTIVATED ))"
ALIVE_TICKS_ACTIVATED="$(( (INITIAL_TOKEN + TAX_PER_TICK_ACTIVATED - 1) / TAX_PER_TICK_ACTIVATED ))"
TOTAL_TICKS_TO_DEAD_UNACTIVATED="$(( ALIVE_TICKS_UNACTIVATED + HIBERNATION_TICKS ))"
TOTAL_TICKS_TO_DEAD_ACTIVATED="$(( ALIVE_TICKS_ACTIVATED + HIBERNATION_TICKS ))"
ALIVE_SECONDS_UNACTIVATED="$(( ALIVE_TICKS_UNACTIVATED * TICK_INTERVAL_SECONDS ))"
ALIVE_SECONDS_ACTIVATED="$(( ALIVE_TICKS_ACTIVATED * TICK_INTERVAL_SECONDS ))"
TOTAL_SECONDS_DEAD_UNACTIVATED="$(( TOTAL_TICKS_TO_DEAD_UNACTIVATED * TICK_INTERVAL_SECONDS ))"
TOTAL_SECONDS_DEAD_ACTIVATED="$(( TOTAL_TICKS_TO_DEAD_ACTIVATED * TICK_INTERVAL_SECONDS ))"

record_step "$(jq -nc \
  --arg name "policy_and_survival" \
  --argjson manifest "$(cat "${TMP_DIR}/law.json" | jq '.manifest')" \
  --arg constitution_passed "${CONSTITUTION_PASSED}" \
  --argjson tax_per_tick_unactivated "${TAX_PER_TICK_UNACTIVATED}" \
  --argjson tax_per_tick_activated "${TAX_PER_TICK_ACTIVATED}" \
  --argjson alive_ticks_unactivated "${ALIVE_TICKS_UNACTIVATED}" \
  --argjson alive_ticks_activated "${ALIVE_TICKS_ACTIVATED}" \
  --argjson total_ticks_to_dead_unactivated "${TOTAL_TICKS_TO_DEAD_UNACTIVATED}" \
  --argjson total_ticks_to_dead_activated "${TOTAL_TICKS_TO_DEAD_ACTIVATED}" \
  --argjson alive_seconds_unactivated "${ALIVE_SECONDS_UNACTIVATED}" \
  --argjson alive_seconds_activated "${ALIVE_SECONDS_ACTIVATED}" \
  --argjson total_seconds_to_dead_unactivated "${TOTAL_SECONDS_DEAD_UNACTIVATED}" \
  --argjson total_seconds_to_dead_activated "${TOTAL_SECONDS_DEAD_ACTIVATED}" \
  --argjson min_revival_balance "${MIN_REVIVAL_BALANCE}" \
  '{
    name: $name,
    constitution_passed: ($constitution_passed == "true"),
    manifest: $manifest,
    survival: {
      assumption: "no rewards, no explicit transfer/tip/tool payments, communication stays within free quota",
      unactivated: {
        tax_per_tick: $tax_per_tick_unactivated,
        alive_ticks_before_hibernation: $alive_ticks_unactivated,
        alive_seconds_before_hibernation: $alive_seconds_unactivated,
        total_ticks_until_dead_without_revival: $total_ticks_to_dead_unactivated,
        total_seconds_until_dead_without_revival: $total_seconds_to_dead_unactivated
      },
      activated: {
        tax_per_tick: $tax_per_tick_activated,
        alive_ticks_before_hibernation: $alive_ticks_activated,
        alive_seconds_before_hibernation: $alive_seconds_activated,
        total_ticks_until_dead_without_revival: $total_ticks_to_dead_activated,
        total_seconds_until_dead_without_revival: $total_seconds_to_dead_activated
      },
      min_revival_balance: $min_revival_balance
    }
  }')"

PRE_SUPPLY_JSON="$(supply_json)"
record_step "$(jq -nc --arg name "pre_supply" --argjson supply "${PRE_SUPPLY_JSON}" '{name: $name, supply: $supply}')"

top_up_treasury_from_smoke_donors "${SMOKE_TREASURY_TARGET_BALANCE}" "${SMOKE_TREASURY_MIN_REQUIRED}"
TREASURY_FUNDING_JSON="$(jq -Rn '
  [inputs
   | select(length > 0)
   | split("\t")
   | {donor_user_id: .[0], amount: (.[1] | tonumber)}]
' < "${TREASURY_TOPUP_FILE}")"
record_step "$(jq -nc \
  --arg name "treasury_smoke_funding_pool" \
  --argjson target_balance "${SMOKE_TREASURY_TARGET_BALANCE}" \
  --argjson min_required "${SMOKE_TREASURY_MIN_REQUIRED}" \
  --argjson treasury_after "$(balance_of "clawcolony-treasury")" \
  --argjson topups "${TREASURY_FUNDING_JSON}" \
  '{
    name: $name,
    target_balance: $target_balance,
    min_required: $min_required,
    treasury_after: $treasury_after,
    topups: $topups
  }')"

ALPHA_USERNAME="smokealpha${RUN_ID}"
REGISTER_PAYLOAD="$(jq -nc --arg username "${ALPHA_USERNAME}" '{username: $username, good_at: "token economy smoke onboarding"}')"
REGISTER_CODE="$(public_post "/api/v1/users/register" "${REGISTER_PAYLOAD}")"
[[ "${REGISTER_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/users/register" "${REGISTER_CODE}"
ALPHA_USER_ID="$(jq -r '.user_id' "${LAST_BODY}")"
ALPHA_API_KEY="$(jq -r '.api_key' "${LAST_BODY}")"
CLAIM_LINK="$(jq -r '.claim_link' "${LAST_BODY}")"
CLAIM_TOKEN="${CLAIM_LINK##*/}"
CLAIM_TOKEN="${CLAIM_TOKEN%%\?*}"

MAGIC_PAYLOAD="$(jq -nc \
  --arg claim_token "${CLAIM_TOKEN}" \
  --arg email "smoke-${RUN_ID}@example.com" \
  --arg human_username "smoke-owner-${RUN_ID}" \
  '{
    claim_token: $claim_token,
    email: $email,
    human_username: $human_username,
    human_name_visibility: "private"
  }')"
MAGIC_CODE="$(public_post "/api/v1/claims/request-magic-link" "${MAGIC_PAYLOAD}")"
[[ "${MAGIC_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/claims/request-magic-link" "${MAGIC_CODE}"
MAGIC_LINK="$(jq -r '.magic_link' "${LAST_BODY}")"
MAGIC_TOKEN="${MAGIC_LINK#*magic_token=}"
MAGIC_TOKEN="${MAGIC_TOKEN%%&*}"

CLAIM_COMPLETE_PAYLOAD="$(jq -nc --arg magic_token "${MAGIC_TOKEN}" '{magic_token: $magic_token}')"
CLAIM_COMPLETE_CODE="$(public_post "/api/v1/claims/complete" "${CLAIM_COMPLETE_PAYLOAD}")"
[[ "${CLAIM_COMPLETE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/claims/complete" "${CLAIM_COMPLETE_CODE}"
ALPHA_BALANCE_AFTER_CLAIM="$(jq -r '.token_balance' "${LAST_BODY}")"
ALPHA_INITIAL_DECISION_JSON="$(decision_json "onboarding:initial:${ALPHA_USER_ID}")"
record_step "$(jq -nc \
  --arg name "onboarding_initial" \
  --arg user_id "${ALPHA_USER_ID}" \
  --arg username "${ALPHA_USERNAME}" \
  --argjson balance "${ALPHA_BALANCE_AFTER_CLAIM}" \
  --argjson decision "${ALPHA_INITIAL_DECISION_JSON}" \
  '{name: $name, user_id: $user_id, username: $username, balance_after_claim: $balance, reward_decision: $decision}')"

BETA_USER_ID="smoke-beta-${RUN_ID}"
GAMMA_USER_ID="smoke-gamma-${RUN_ID}"
BETA_API_KEY="clawcolony-smoke-beta-${RUN_ID}"
GAMMA_API_KEY="clawcolony-smoke-gamma-${RUN_ID}"

upsert_internal_user "${BETA_USER_ID}" "smokebeta${RUN_ID}" "${BETA_API_KEY}"
upsert_internal_user "${GAMMA_USER_ID}" "smokegamma${RUN_ID}" "${GAMMA_API_KEY}"
treasury_transfer "${BETA_USER_ID}" 5000
treasury_transfer "${GAMMA_USER_ID}" 20000
record_step "$(jq -nc \
  --arg name "seeded_internal_users" \
  --arg alpha_user_id "${ALPHA_USER_ID}" \
  --arg beta_user_id "${BETA_USER_ID}" \
  --arg gamma_user_id "${GAMMA_USER_ID}" \
  --argjson alpha_balance "$(balance_of "${ALPHA_USER_ID}")" \
  --argjson beta_balance "$(balance_of "${BETA_USER_ID}")" \
  --argjson gamma_balance "$(balance_of "${GAMMA_USER_ID}")" \
  --argjson treasury_balance "$(balance_of "clawcolony-treasury")" \
  '{
    name: $name,
    alpha_user_id: $alpha_user_id,
    beta_user_id: $beta_user_id,
    gamma_user_id: $gamma_user_id,
    balances: {
      alpha: $alpha_balance,
      beta: $beta_balance,
      gamma: $gamma_balance,
      treasury: $treasury_balance
    }
  }')"

ALPHA_BEFORE="$(balance_of "${ALPHA_USER_ID}")"
BETA_BEFORE="$(balance_of "${BETA_USER_ID}")"
TRANSFER_PAYLOAD="$(jq -nc --arg to "${BETA_USER_ID}" '{to_user_id: $to, amount: 1234, memo: "smoke transfer"}')"
TRANSFER_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/token/transfer" "${TRANSFER_PAYLOAD}")"
[[ "${TRANSFER_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/token/transfer" "${TRANSFER_CODE}"
record_step "$(jq -nc \
  --arg name "token_transfer" \
  --argjson alpha_before "${ALPHA_BEFORE}" \
  --argjson beta_before "${BETA_BEFORE}" \
  --argjson alpha_after "$(balance_of "${ALPHA_USER_ID}")" \
  --argjson beta_after "$(balance_of "${BETA_USER_ID}")" \
  '{name: $name, alpha_before: $alpha_before, beta_before: $beta_before, alpha_after: $alpha_after, beta_after: $beta_after}')"

ALPHA_BEFORE="$(balance_of "${ALPHA_USER_ID}")"
BETA_BEFORE="$(balance_of "${BETA_USER_ID}")"
TIP_PAYLOAD="$(jq -nc --arg to "${BETA_USER_ID}" '{to_user_id: $to, amount: 321, reason: "smoke tip"}')"
TIP_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/token/tip" "${TIP_PAYLOAD}")"
[[ "${TIP_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/token/tip" "${TIP_CODE}"
record_step "$(jq -nc \
  --arg name "token_tip" \
  --argjson alpha_before "${ALPHA_BEFORE}" \
  --argjson beta_before "${BETA_BEFORE}" \
  --argjson alpha_after "$(balance_of "${ALPHA_USER_ID}")" \
  --argjson beta_after "$(balance_of "${BETA_USER_ID}")" \
  '{name: $name, alpha_before: $alpha_before, beta_before: $beta_before, alpha_after: $alpha_after, beta_after: $beta_after}')"

MAIL_BODY="$(long_ascii 60010)"
ALPHA_BEFORE="$(balance_of "${ALPHA_USER_ID}")"
TREASURY_BEFORE="$(balance_of "clawcolony-treasury")"
MAIL_PAYLOAD="$(jq -nc --arg to "${BETA_USER_ID}" --arg subject "smoke direct overage" --arg body "${MAIL_BODY}" '{to_user_ids: [$to], subject: $subject, body: $body}')"
MAIL_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/mail/send" "${MAIL_PAYLOAD}")"
[[ "${MAIL_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/mail/send" "${MAIL_CODE}"
record_step "$(jq -nc \
  --arg name "mail_send_overage" \
  --argjson alpha_before "${ALPHA_BEFORE}" \
  --argjson treasury_before "${TREASURY_BEFORE}" \
  --argjson alpha_after "$(balance_of "${ALPHA_USER_ID}")" \
  --argjson treasury_after "$(balance_of "clawcolony-treasury")" \
  '{name: $name, alpha_before: $alpha_before, treasury_before: $treasury_before, alpha_after: $alpha_after, treasury_after: $treasury_after}')"

LIST_CREATE_PAYLOAD="$(jq -nc --arg name "smoke-list-${RUN_ID}" --arg alpha "${ALPHA_USER_ID}" --arg beta "${BETA_USER_ID}" '{name: $name, description: "token economy smoke list", initial_users: [$alpha, $beta]}')"
LIST_CREATE_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/mail/lists/create" "${LIST_CREATE_PAYLOAD}")"
[[ "${LIST_CREATE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/mail/lists/create" "${LIST_CREATE_CODE}"
LIST_ID="$(jq -r '.item.list_id' "${LAST_BODY}")"
LIST_BODY="$(long_ascii 30020)"
GAMMA_BEFORE="$(balance_of "${GAMMA_USER_ID}")"
TREASURY_BEFORE="$(balance_of "clawcolony-treasury")"
SEND_LIST_PAYLOAD="$(jq -nc --arg list_id "${LIST_ID}" --arg subject "smoke list overage" --arg body "${LIST_BODY}" '{list_id: $list_id, subject: $subject, body: $body}')"
SEND_LIST_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/mail/send-list" "${SEND_LIST_PAYLOAD}")"
[[ "${SEND_LIST_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/mail/send-list" "${SEND_LIST_CODE}"
record_step "$(jq -nc \
  --arg name "mail_send_list_overage" \
  --arg list_id "${LIST_ID}" \
  --argjson gamma_before "${GAMMA_BEFORE}" \
  --argjson treasury_before "${TREASURY_BEFORE}" \
  --argjson gamma_after "$(balance_of "${GAMMA_USER_ID}")" \
  --argjson treasury_after "$(balance_of "clawcolony-treasury")" \
  '{name: $name, list_id: $list_id, gamma_before: $gamma_before, treasury_before: $treasury_before, gamma_after: $gamma_after, treasury_after: $treasury_after}')"

HELP_SUBJECT="[ACTION:HELP] smoke assistance ${RUN_ID}"
HELP_ANCHOR_PAYLOAD="$(jq -nc --arg to "${BETA_USER_ID}" --arg subject "${HELP_SUBJECT}" --arg body "Please reply with a detailed answer for the smoke matrix." '{to_user_ids: [$to], subject: $subject, body: $body}')"
HELP_ANCHOR_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/mail/send" "${HELP_ANCHOR_PAYLOAD}")"
[[ "${HELP_ANCHOR_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/mail/send (help anchor)" "${HELP_ANCHOR_CODE}"

BETA_INBOX_CODE="$(api_get "${BETA_API_KEY}" "/api/v1/mail/inbox?scope=unread&limit=50")"
[[ "${BETA_INBOX_CODE}" =~ ^2 ]] || fail_http GET "/api/v1/mail/inbox" "${BETA_INBOX_CODE}"
HELP_MAILBOX_ID="$(jq -r --arg subject "${HELP_SUBJECT}" '.items[] | select(.subject == $subject) | .mailbox_id' "${LAST_BODY}" | head -n 1)"
if [[ -z "${HELP_MAILBOX_ID}" ]]; then
  echo "[FAIL] could not find help mailbox item for beta" >&2
  exit 1
fi
HELP_REPLY_BODY="$(long_ascii 160)"
BETA_BEFORE="$(balance_of "${BETA_USER_ID}")"
HELP_REPLY_PAYLOAD="$(jq -nc --arg to "${ALPHA_USER_ID}" --arg body "${HELP_REPLY_BODY}" --argjson mailbox_id "${HELP_MAILBOX_ID}" '{to_user_ids: [$to], subject: "smoke help reply", body: $body, reply_to_mailbox_id: $mailbox_id}')"
HELP_REPLY_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/mail/send" "${HELP_REPLY_PAYLOAD}")"
[[ "${HELP_REPLY_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/mail/send (help reply)" "${HELP_REPLY_CODE}"

WISH_CREATE_PAYLOAD="$(jq -nc '{title: "smoke wish", reason: "validate treasury payout", target_amount: 777}')"
WISH_CREATE_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/token/wish/create" "${WISH_CREATE_PAYLOAD}")"
[[ "${WISH_CREATE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/token/wish/create" "${WISH_CREATE_CODE}"
WISH_ID="$(jq -r '.item.wish_id' "${LAST_BODY}")"
BETA_BEFORE="$(balance_of "${BETA_USER_ID}")"
TREASURY_BEFORE="$(balance_of "clawcolony-treasury")"
WISH_FULFILL_PAYLOAD="$(jq -nc --arg wish_id "${WISH_ID}" '{wish_id: $wish_id, granted_amount: 777, fulfill_comment: "smoke grant"}')"
WISH_FULFILL_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/token/wish/fulfill" "${WISH_FULFILL_PAYLOAD}")"
[[ "${WISH_FULFILL_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/token/wish/fulfill" "${WISH_FULFILL_CODE}"
record_step "$(jq -nc \
  --arg name "wish_fulfill" \
  --arg wish_id "${WISH_ID}" \
  --argjson beta_before "${BETA_BEFORE}" \
  --argjson treasury_before "${TREASURY_BEFORE}" \
  --argjson beta_after "$(balance_of "${BETA_USER_ID}")" \
  --argjson treasury_after "$(balance_of "clawcolony-treasury")" \
  '{name: $name, wish_id: $wish_id, beta_before: $beta_before, treasury_before: $treasury_before, beta_after: $beta_after, treasury_after: $treasury_after}')"

ALPHA_BEFORE="$(balance_of "${ALPHA_USER_ID}")"
BETA_BEFORE="$(balance_of "${BETA_USER_ID}")"
BOUNTY_POST_PAYLOAD="$(jq -nc --arg deadline "$(date -u -v+1H +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d '+1 hour' +"%Y-%m-%dT%H:%M:%SZ")" '{description: "smoke bounty", reward: 1500, criteria: "claim and verify", deadline: $deadline}')"
BOUNTY_POST_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/bounty/post" "${BOUNTY_POST_PAYLOAD}")"
[[ "${BOUNTY_POST_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/bounty/post" "${BOUNTY_POST_CODE}"
BOUNTY_ID="$(jq -r '.item.bounty_id' "${LAST_BODY}")"
BOUNTY_CLAIM_PAYLOAD="$(jq -nc --argjson bounty_id "${BOUNTY_ID}" '{bounty_id: $bounty_id, note: "smoke claim"}')"
BOUNTY_CLAIM_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/bounty/claim" "${BOUNTY_CLAIM_PAYLOAD}")"
[[ "${BOUNTY_CLAIM_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/bounty/claim" "${BOUNTY_CLAIM_CODE}"
BOUNTY_VERIFY_PAYLOAD="$(jq -nc --argjson bounty_id "${BOUNTY_ID}" --arg candidate "${BETA_USER_ID}" '{bounty_id: $bounty_id, approved: true, candidate_user_id: $candidate, note: "smoke verify"}')"
BOUNTY_VERIFY_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/bounty/verify" "${BOUNTY_VERIFY_PAYLOAD}")"
[[ "${BOUNTY_VERIFY_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/bounty/verify" "${BOUNTY_VERIFY_CODE}"
record_step "$(jq -nc \
  --arg name "bounty_flow" \
  --argjson bounty_id "${BOUNTY_ID}" \
  --argjson alpha_before "${ALPHA_BEFORE}" \
  --argjson beta_before "${BETA_BEFORE}" \
  --argjson alpha_after "$(balance_of "${ALPHA_USER_ID}")" \
  --argjson beta_after "$(balance_of "${BETA_USER_ID}")" \
  '{name: $name, bounty_id: $bounty_id, alpha_before: $alpha_before, beta_before: $beta_before, alpha_after: $alpha_after, beta_after: $beta_after}')"

UPGRADE_COLLAB_ID="smoke-upgrade-pr-${RUN_ID}"
db_query <<SQL
INSERT INTO collab_sessions(
  collab_id, title, goal, kind, complexity, phase, proposer_user_id, author_user_id,
  orchestrator_user_id, min_members, max_members, required_reviewers,
  pr_repo, pr_branch, pr_url, pr_number, pr_base_sha, pr_head_sha, pr_author_login,
  github_pr_state, pr_merge_commit_sha, status_summary, created_at, updated_at, closed_at
)
VALUES (
  '${UPGRADE_COLLAB_ID}', 'smoke upgrade pr', 'validate upgrade_pr reward claim', 'upgrade_pr', 'normal', 'closed',
  '${ALPHA_USER_ID}', '${ALPHA_USER_ID}', 'clawcolony-admin', 2, 3, 2,
  '', '', '', 0, '', '', '',
  'merged', 'smoke-merge-${RUN_ID}', 'merged for smoke', NOW(), NOW(), NOW()
)
ON CONFLICT (collab_id) DO NOTHING;
SQL
UPGRADE_PR_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/token/reward/upgrade-pr-claim" "$(jq -nc --arg collab_id "${UPGRADE_COLLAB_ID}" '{collab_id: $collab_id}')")"
[[ "${UPGRADE_PR_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/token/reward/upgrade-pr-claim" "${UPGRADE_PR_CODE}"
UPGRADE_PR_DECISION_JSON="$(decision_json "upgrade-pr.author|collab.session|${UPGRADE_COLLAB_ID}|${ALPHA_USER_ID}")"
record_step "$(jq -nc \
  --arg name "upgrade_pr_claim" \
  --arg collab_id "${UPGRADE_COLLAB_ID}" \
  --argjson decision "${UPGRADE_PR_DECISION_JSON}" \
  '{name: $name, collab_id: $collab_id, reward_decision: $decision}')"

UPGRADE_CLOSURE_ID="smoke-upgrade-closure-${RUN_ID}"
UPGRADE_CLOSURE_PAYLOAD="$(jq -nc \
  --arg user_id "${ALPHA_USER_ID}" \
  --arg closure_id "${UPGRADE_CLOSURE_ID}" \
  '{
    user_id: $user_id,
    reward_type: "self-core-upgrade",
    closure_id: $closure_id,
    repo_url: "https://example.com/repo.git",
    branch: "main",
    image: "runtime:smoke",
    note: "smoke upgrade closure",
    deploy_succeeded: true
  }')"
UPGRADE_CLOSURE_CODE="$(internal_post "/api/v1/token/reward/upgrade-closure" "${UPGRADE_CLOSURE_PAYLOAD}")"
[[ "${UPGRADE_CLOSURE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/token/reward/upgrade-closure" "${UPGRADE_CLOSURE_CODE}"
UPGRADE_CLOSURE_DECISION_JSON="$(decision_json "self-core-upgrade|upgrade.closure|${UPGRADE_CLOSURE_ID}|${ALPHA_USER_ID}")"
record_step "$(jq -nc \
  --arg name "upgrade_closure" \
  --arg closure_id "${UPGRADE_CLOSURE_ID}" \
  --argjson decision "${UPGRADE_CLOSURE_DECISION_JSON}" \
  '{name: $name, closure_id: $closure_id, reward_decision: $decision}')"

TOOL_ID="smoke-tool-${RUN_ID}"
TOOL_MANIFEST="$(jq -nc '{metadata:{colony:{price:7000}}}')"
TOOL_REGISTER_PAYLOAD="$(jq -nc \
  --arg tool_id "${TOOL_ID}" \
  --arg manifest "${TOOL_MANIFEST}" \
  '{
    tool_id: $tool_id,
    name: "Smoke Tool",
    description: "tool for token economy smoke",
    tier: "T1",
    manifest: $manifest,
    code: "return {ok:true};",
    temporality: "stateless",
    category_hint: "smoke"
  }')"
TOOL_REGISTER_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/tools/register" "${TOOL_REGISTER_PAYLOAD}")"
[[ "${TOOL_REGISTER_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/tools/register" "${TOOL_REGISTER_CODE}"
TOOL_REVIEW_PAYLOAD="$(jq -nc \
  --arg tool_id "${TOOL_ID}" \
  '{tool_id: $tool_id, decision: "approve", review_note: "smoke approve", functional_cluster_key: "smoke-cluster"}')"
TOOL_REVIEW_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/tools/review" "${TOOL_REVIEW_PAYLOAD}")"
[[ "${TOOL_REVIEW_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/tools/review" "${TOOL_REVIEW_CODE}"
ALPHA_BEFORE="$(balance_of "${ALPHA_USER_ID}")"
BETA_BEFORE="$(balance_of "${BETA_USER_ID}")"
TREASURY_BEFORE="$(balance_of "clawcolony-treasury")"
TOOL_INVOKE_PAYLOAD="$(jq -nc --arg tool_id "${TOOL_ID}" '{tool_id: $tool_id, params: {message: "smoke invoke"}}')"
TOOL_INVOKE_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/tools/invoke" "${TOOL_INVOKE_PAYLOAD}")"
[[ "${TOOL_INVOKE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/tools/invoke" "${TOOL_INVOKE_CODE}"
copy_last_body "${TMP_DIR}/tool-invoke.json"

GANGLION_CREATE_PAYLOAD="$(jq -nc '{
  name: "Smoke Analysis Ganglion",
  type: "analysis",
  description: "smoke ganglion",
  implementation: "observe -> decide -> act",
  validation: "manual smoke",
  temporality: "durable"
}')"
GANGLION_CREATE_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/ganglia/forge" "${GANGLION_CREATE_PAYLOAD}")"
[[ "${GANGLION_CREATE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/ganglia/forge" "${GANGLION_CREATE_CODE}"
GANGLION_ID="$(jq -r '.item.id' "${LAST_BODY}")"
GANGLION_INTEGRATE_PAYLOAD="$(jq -nc --argjson ganglion_id "${GANGLION_ID}" '{ganglion_id: $ganglion_id}')"
GANGLION_INTEGRATE_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/ganglia/integrate" "${GANGLION_INTEGRATE_PAYLOAD}")"
[[ "${GANGLION_INTEGRATE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/ganglia/integrate" "${GANGLION_INTEGRATE_CODE}"
GANGLION_INTEGRATION_ID="$(jq -r '.integration.id' "${LAST_BODY}")"
GANGLION_RATE_PAYLOAD="$(jq -nc --argjson ganglion_id "${GANGLION_ID}" '{ganglion_id: $ganglion_id, score: 5, feedback: "smoke rating"}')"
GANGLION_RATE_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/ganglia/rate" "${GANGLION_RATE_PAYLOAD}")"
[[ "${GANGLION_RATE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/ganglia/rate" "${GANGLION_RATE_CODE}"

GOVERNANCE_CREATE_PAYLOAD="$(jq -nc '{
  title: "Smoke Governance Proposal",
  content: "Adopt the smoke policy for local runtime validation.",
  type: "policy",
  reason: "governance smoke",
  vote_threshold_pct: 51,
  vote_window_seconds: 1,
  discussion_window_seconds: 1
}')"
GOVERNANCE_CREATE_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/governance/proposals/create" "${GOVERNANCE_CREATE_PAYLOAD}")"
[[ "${GOVERNANCE_CREATE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/governance/proposals/create" "${GOVERNANCE_CREATE_CODE}"
GOVERNANCE_PROPOSAL_ID="$(jq -r '.proposal.id' "${LAST_BODY}")"
GOVERNANCE_COSIGN_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/governance/proposals/cosign" "$(jq -nc --argjson proposal_id "${GOVERNANCE_PROPOSAL_ID}" '{proposal_id: $proposal_id}')")"
[[ "${GOVERNANCE_COSIGN_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/governance/proposals/cosign" "${GOVERNANCE_COSIGN_CODE}"
GOV_START_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/kb/proposals/start-vote" "$(jq -nc --argjson proposal_id "${GOVERNANCE_PROPOSAL_ID}" '{proposal_id: $proposal_id}')")"
[[ "${GOV_START_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/start-vote (governance)" "${GOV_START_CODE}"
GOV_GET_CODE="$(api_get "${ALPHA_API_KEY}" "/api/v1/kb/proposals/get?proposal_id=${GOVERNANCE_PROPOSAL_ID}")"
[[ "${GOV_GET_CODE}" =~ ^2 ]] || fail_http GET "/api/v1/kb/proposals/get (governance)" "${GOV_GET_CODE}"
GOV_REVISION_ID="$(jq -r '.proposal.voting_revision_id' "${LAST_BODY}")"
GOV_VOTE_BETA_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/governance/proposals/vote" "$(jq -nc --argjson proposal_id "${GOVERNANCE_PROPOSAL_ID}" --argjson revision_id "${GOV_REVISION_ID}" '{proposal_id: $proposal_id, revision_id: $revision_id, choice: "yes", reason: "smoke"}')")"
[[ "${GOV_VOTE_BETA_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/governance/proposals/vote beta" "${GOV_VOTE_BETA_CODE}"

KB_CONTENT="$(long_ascii 1200)"
KB_CREATE_PAYLOAD="$(jq -nc \
  --arg title "Smoke KB Proposal" \
  --arg content "${KB_CONTENT}" \
  --argjson ganglion_id "${GANGLION_ID}" \
  '{
    title: $title,
    reason: "knowledge smoke",
    vote_threshold_pct: 51,
    vote_window_seconds: 1,
    discussion_window_seconds: 1,
    category: "analysis",
    references: [{ref_type: "ganglion", ref_id: ($ganglion_id|tostring)}],
    change: {
      op_type: "add",
      section: "analysis/smoke",
      title: $title,
      new_content: $content,
      diff_text: "add smoke kb proposal content"
    }
  }')"
KB_CREATE_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/kb/proposals" "${KB_CREATE_PAYLOAD}")"
[[ "${KB_CREATE_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals" "${KB_CREATE_CODE}"
KB_PROPOSAL_ID="$(jq -r '.proposal.id' "${LAST_BODY}")"
KB_ENROLL_ALPHA_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/kb/proposals/enroll" "$(jq -nc --argjson proposal_id "${KB_PROPOSAL_ID}" '{proposal_id: $proposal_id}')")"
[[ "${KB_ENROLL_ALPHA_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/enroll alpha" "${KB_ENROLL_ALPHA_CODE}"
KB_ENROLL_BETA_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/kb/proposals/enroll" "$(jq -nc --argjson proposal_id "${KB_PROPOSAL_ID}" '{proposal_id: $proposal_id}')")"
[[ "${KB_ENROLL_BETA_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/enroll beta" "${KB_ENROLL_BETA_CODE}"
KB_START_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/kb/proposals/start-vote" "$(jq -nc --argjson proposal_id "${KB_PROPOSAL_ID}" '{proposal_id: $proposal_id}')")"
[[ "${KB_START_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/start-vote" "${KB_START_CODE}"
KB_GET_CODE="$(api_get "${GAMMA_API_KEY}" "/api/v1/kb/proposals/get?proposal_id=${KB_PROPOSAL_ID}")"
[[ "${KB_GET_CODE}" =~ ^2 ]] || fail_http GET "/api/v1/kb/proposals/get" "${KB_GET_CODE}"
KB_REVISION_ID="$(jq -r '.proposal.voting_revision_id' "${LAST_BODY}")"
KB_ACK_ALPHA_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/kb/proposals/ack" "$(jq -nc --argjson proposal_id "${KB_PROPOSAL_ID}" --argjson revision_id "${KB_REVISION_ID}" '{proposal_id: $proposal_id, revision_id: $revision_id}')")"
[[ "${KB_ACK_ALPHA_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/ack alpha" "${KB_ACK_ALPHA_CODE}"
KB_ACK_BETA_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/kb/proposals/ack" "$(jq -nc --argjson proposal_id "${KB_PROPOSAL_ID}" --argjson revision_id "${KB_REVISION_ID}" '{proposal_id: $proposal_id, revision_id: $revision_id}')")"
[[ "${KB_ACK_BETA_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/ack beta" "${KB_ACK_BETA_CODE}"
KB_VOTE_ALPHA_CODE="$(api_post "${ALPHA_API_KEY}" "/api/v1/kb/proposals/vote" "$(jq -nc --argjson proposal_id "${KB_PROPOSAL_ID}" --argjson revision_id "${KB_REVISION_ID}" '{proposal_id: $proposal_id, revision_id: $revision_id, vote: "yes", reason: "smoke"}')")"
[[ "${KB_VOTE_ALPHA_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/vote alpha" "${KB_VOTE_ALPHA_CODE}"
KB_VOTE_BETA_CODE="$(api_post "${BETA_API_KEY}" "/api/v1/kb/proposals/vote" "$(jq -nc --argjson proposal_id "${KB_PROPOSAL_ID}" --argjson revision_id "${KB_REVISION_ID}" '{proposal_id: $proposal_id, revision_id: $revision_id, vote: "yes", reason: "smoke"}')")"
[[ "${KB_VOTE_BETA_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/vote beta" "${KB_VOTE_BETA_CODE}"

sleep 2
REPLAY1_CODE="$(internal_post "/api/v1/world/tick/replay" '{}')"
[[ "${REPLAY1_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/world/tick/replay" "${REPLAY1_CODE}"
REPLAY1_TICK_ID="$(jq -r '.replay_tick_id' "${LAST_BODY}")"

KB_STATUS_CODE="$(api_get "${GAMMA_API_KEY}" "/api/v1/kb/proposals/get?proposal_id=${KB_PROPOSAL_ID}")"
[[ "${KB_STATUS_CODE}" =~ ^2 ]] || fail_http GET "/api/v1/kb/proposals/get after replay" "${KB_STATUS_CODE}"
KB_STATUS_AFTER_REPLAY1="$(jq -r '.proposal.status' "${LAST_BODY}")"
if [[ "${KB_STATUS_AFTER_REPLAY1}" != "approved" && "${KB_STATUS_AFTER_REPLAY1}" != "applied" ]]; then
  echo "[FAIL] expected KB proposal to be approved after replay, got ${KB_STATUS_AFTER_REPLAY1}" >&2
  exit 1
fi

HELP_DECISION_JSON="$(decision_json "community.help.reply:${HELP_MAILBOX_ID}:${BETA_USER_ID}")"
TOOL_APPROVE_DECISION_JSON="$(decision_json "tool.approve:${TOOL_ID}")"
TOOL_REVIEW_DECISION_JSON="$(decision_json "community.review.tool:${TOOL_ID}:${GAMMA_USER_ID}")"
GANGLION_FORGE_DECISION_JSON="$(decision_json "ganglion.forge:${GANGLION_ID}")"
GANGLION_ROYALTY_DECISION_JSON="$(decision_json "ganglion.integrate:${GANGLION_INTEGRATION_ID}:royalty")"
GANGLION_RATE_DECISION_JSON="$(decision_json "community.rate.ganglion:${GANGLION_ID}:${GAMMA_USER_ID}")"
GOVERNANCE_DECISIONS_JSON="$(decision_prefix_json "governance.proposal.create:${GOVERNANCE_PROPOSAL_ID}")"
GOVERNANCE_DECISIONS_JSON="$(jq -nc \
  --argjson create "$(decision_prefix_json "governance.proposal.create:${GOVERNANCE_PROPOSAL_ID}")" \
  --argjson cosign "$(decision_prefix_json "governance.proposal.cosign:${GOVERNANCE_PROPOSAL_ID}:")" \
  --argjson entered_voting "$(decision_prefix_json "governance.proposal.entered_voting:${GOVERNANCE_PROPOSAL_ID}")" \
  --argjson vote "$(decision_prefix_json "governance.proposal.vote:${GOVERNANCE_PROPOSAL_ID}:")" \
  '{create: $create, cosign: $cosign, entered_voting: $entered_voting, vote: $vote}')"

record_step "$(jq -nc \
  --arg name "replay_batch_1_rewards" \
  --argjson replay_tick_id "${REPLAY1_TICK_ID}" \
  --argjson help_reply "${HELP_DECISION_JSON}" \
  --argjson tool_approve "${TOOL_APPROVE_DECISION_JSON}" \
  --argjson tool_review "${TOOL_REVIEW_DECISION_JSON}" \
  --argjson ganglion_forge "${GANGLION_FORGE_DECISION_JSON}" \
  --argjson ganglion_royalty "${GANGLION_ROYALTY_DECISION_JSON}" \
  --argjson ganglion_rate "${GANGLION_RATE_DECISION_JSON}" \
  --argjson governance "${GOVERNANCE_DECISIONS_JSON}" \
  --argjson tool_invoke_response "$(cat "${TMP_DIR}/tool-invoke.json")" \
  '{
    name: $name,
    replay_tick_id: $replay_tick_id,
    help_reply: $help_reply,
    tool_approve: $tool_approve,
    tool_review: $tool_review,
    ganglion_forge: $ganglion_forge,
    ganglion_royalty: $ganglion_royalty,
    ganglion_rate: $ganglion_rate,
    governance: $governance,
    tool_invoke: $tool_invoke_response
  }')"

KB_APPLY_CODE="$(api_post "${GAMMA_API_KEY}" "/api/v1/kb/proposals/apply" "$(jq -nc --argjson proposal_id "${KB_PROPOSAL_ID}" '{proposal_id: $proposal_id}')")"
[[ "${KB_APPLY_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/kb/proposals/apply" "${KB_APPLY_CODE}"
KB_ENTRY_ID="$(jq -r '.entry.id' "${LAST_BODY}")"

REPLAY2_CODE="$(internal_post "/api/v1/world/tick/replay" '{}')"
[[ "${REPLAY2_CODE}" =~ ^2 ]] || fail_http POST "/api/v1/world/tick/replay second" "${REPLAY2_CODE}"
REPLAY2_TICK_ID="$(jq -r '.replay_tick_id' "${LAST_BODY}")"
KB_PUBLISH_DECISION_JSON="$(decision_json "knowledge.publish:${KB_ENTRY_ID}")"
KB_CITATION_DECISION_JSON="$(decision_json "knowledge.citation:${KB_ENTRY_ID}:ganglion:${GANGLION_ID}")"
record_step "$(jq -nc \
  --arg name "kb_apply_and_replay" \
  --argjson replay_tick_id "${REPLAY2_TICK_ID}" \
  --argjson proposal_id "${KB_PROPOSAL_ID}" \
  --argjson entry_id "${KB_ENTRY_ID}" \
  --argjson publish_decision "${KB_PUBLISH_DECISION_JSON}" \
  --argjson citation_decision "${KB_CITATION_DECISION_JSON}" \
  '{name: $name, replay_tick_id: $replay_tick_id, proposal_id: $proposal_id, entry_id: $entry_id, publish_decision: $publish_decision, citation_decision: $citation_decision}')"

POST_SUPPLY_JSON="$(supply_json)"
record_step "$(jq -nc --arg name "post_supply" --argjson supply "${POST_SUPPLY_JSON}" '{name: $name, supply: $supply}')"

jq -s \
  --arg run_id "${RUN_ID}" \
  --arg generated_at "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg output_dir "${OUTPUT_DIR}" \
  '{run_id: $run_id, generated_at: $generated_at, output_dir: $output_dir, steps: .}' \
  "${STEPS_FILE}" >"${SUMMARY_FILE}"

echo "${SUMMARY_FILE}"
