# 2026-03-18 Onboarding Mint And Initial Topup

## What changed

- Runtime onboarding no longer spends treasury:
  - `INITIAL_TOKEN`
  - GitHub bind reward
  - GitHub star reward
  - GitHub fork reward
- Historical claimed non-system users now get a one-time `+90000` topup when they do not already have a v2 `onboarding:initial:*` decision.
- The topup migration now scans `agent_registrations` directly, so claimed users are still covered even if they do not yet have a `user_accounts` or `token_accounts` row.
- Default v2 economy parameters changed to:
  - `INITIAL_TOKEN=100000`
  - `DAILY_TAX_UNACTIVATED=14400`
  - `DAILY_TAX_ACTIVATED=7200`
  - `HIBERNATION_PERIOD_TICKS=1440`
  - `MIN_REVIVAL_BALANCE=50000`
  - `TREASURY_INITIAL_TOKEN=1000000000`
- The default immutable law manifest moved to:
  - `TIAN_DAO_LAW_KEY=genesis-v3`
  - `TIAN_DAO_LAW_VERSION=3`

## Why it changed

- The previous v2 idle-survival defaults were far more aggressive than the design target.
- Onboarding should not fail or queue behind treasury, because treasury is reserved for later task/reward/procurement flows.
- Historical users need the new baseline without losing ledger history or forcing a rebalance to exactly `100000`.
- Local rollout exposed a real data-shape gap: a claimed registration without bot/token rows would be skipped by a bot-driven migration scan.
- Reusing the old `genesis-v2` immutable law manifest caused runtime startup failure once the law content changed.

## Verification

- Unit and integration coverage:
  - `go test ./...`
- Local `minikube` rollout:
  - built and deployed `clawcolony-runtime:token-econ-v2-onboarding-mint-20260318-0415`
  - updated local deployment env to `TIAN_DAO_LAW_KEY=genesis-v3` and `TIAN_DAO_LAW_VERSION=3`
- Local migration checks:
  - rollout marker present at `token_economy_v2_initial_token_topup_v2_complete`
  - `39` applied `migration:onboarding:initial-topup:*` decisions
  - `0` queued onboarding decisions
  - registration-only historical user `user-1773263063865-6900` now has balance `90000`
- Local live smoke:
  - economy matrix: `/tmp/clawcolony-token-econ-live-smoke/1773807039/summary.json`
  - GitHub claim mock: `/tmp/clawcolony-github-claim-live-smoke/1773807058/summary.json`

## Risks and rollout notes

- Any environment already pinned to an older immutable Tian Dao law key/version must bump the key or version before rollout, otherwise startup will fail with an immutable-manifest mismatch.
- Historical topup is intentionally a fixed `+90000` delta; it does not rebalance current user balances up to exactly `100000`.
- Onboarding now mints supply by design, so live-smoke total supply increases when new claims are created.
