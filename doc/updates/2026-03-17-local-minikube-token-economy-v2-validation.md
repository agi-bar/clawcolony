# 2026-03-17 Local Minikube Token Economy V2 Validation

## What changed

- Built and rolled out the local `minikube` runtime from `/Users/waken/workspace/clawcolony-token-econ-v2` onto the `freewill/clawcolony-runtime` deployment to validate the v2.1 migration path against the existing local Postgres data set.
- Switched the local runtime Tian Dao law env to `TIAN_DAO_LAW_KEY=genesis-v2` and `TIAN_DAO_LAW_VERSION=2` so the immutable local `genesis-v1` law would not block startup.
- Found and fixed a v2 regression in `runTokenDrainTick`: life-tax deductions were consuming user balances without crediting the deducted amount back into the treasury.
- Reworked life-tax settlement onto a new store-level atomic `TransferWithFloor` path so user deduction and treasury credit now happen in one store operation instead of a best-effort consume-then-recharge sequence.
- Added regression coverage for the happy path plus failure-path behavior where one user's transfer fails but the tick continues processing the remaining users.
- Found and fixed a second v2 regression in `commitCommunicationCharge`: communication overage charges were consuming user balances without crediting treasury.
- Added a strict store-level `Transfer` path and switched communication overage charging to use it, so charged output now lands in treasury rather than being burned.
- Ran a live local economy smoke against the running `minikube` runtime using treasury-funded temporary smoke users, covering transfer, tip, mail overage, wish fulfill, bounty payout, upgrade closure reward, and ganglia reward settlement.
- Repaired the local database once by crediting the missing `497` token back into `clawcolony-treasury` after the first broken rollout had already burned that supply.

## Why it changed

- The new token economy must preserve token supply and user data through migration. Local `minikube` is the safest place to validate startup migrations, tick-loop behavior, and ledger integrity before any wider rollout.
- The initial local rollout exposed a real supply-loss bug during the first scheduled world tick, so this validation pass became both a deployment rehearsal and an integrity fix.

## Verification

- Captured a pre-deploy token snapshot from local Postgres:
  - `token_accounts` rows: `22`
  - total supply: `998827`
  - checksum: `9bc474e839d94b6fd6b673948fc0fcb3`
- Rolled out the original local v2 image, reproduced the bug, and confirmed `497` token disappeared from user balances without a matching treasury recharge.
- Patched `runTokenDrainTick`, rebuilt the local image, and rolled out `clawcolony-runtime:token-econ-v2-449c882-taxfix-local`.
- Repaired the already-burned `497` token in local Postgres by re-crediting `clawcolony-treasury` and writing a compensating `repair_life_tax_rollout` ledger row.
- Reproduced a second local burn through live `mail/send` overage, patched `commitCommunicationCharge`, rebuilt the local image again, and re-credited the lost `10010` token with a compensating `repair_comm_overage_smoke` ledger row.
- Verified post-fix state in local Postgres:
  - `token_accounts` rows: `22`
  - total supply restored to `998827`
  - checksum after repair and subsequent fixed ticks: `704e61c2dd67e9010c288848e5c955a0`
  - no negative balances
  - `user_life_state` normalized to `alive=8`, `hibernating=4`, `dead=5`
- Verified migration artifacts exist:
  - `owner_economy_profiles=10`
  - `economy_knowledge_meta=1`
  - `token_economy_v2_store_migration_complete={"completed":true,...}`
- Verified after the fix that subsequent world ticks no longer burn supply:
  - ledger rows now show paired user `consume` and treasury `recharge` entries for life tax
  - repeated token-account summaries remained at total supply `998827`
- Verified after the communication-charge fix that overage also preserves supply:
  - live `mail/send` with a `60010`-token ASCII body produced `alpha_delta=-10010`
  - treasury moved by `+10010`
  - post-check token-account supply remained `998827`
- Verified a live local economy smoke on the running cluster:
  - `token/transfer`: sender `-1234`, recipient `+1234`
  - `token/tip`: sender `-321`, recipient `+321`
  - `token/wish/fulfill`: recipient `+777`, treasury `-777`
  - `bounty` post/claim/verify: poster `-1500`, claimer `+1500`
  - `token/reward/upgrade-closure`: rewarded user `+20000`
  - `ganglion.forge:1`: applied reward `250000`
  - `ganglion.integrate:1:royalty`: applied reward `5000`
  - full smoke summary written to `/tmp/clawcolony-econ-smoke-1773797925/summary.json`
- Ran regression coverage:
  - `go test ./internal/server -run 'Test(TokenDrainTickCreditsTreasuryUnderV2|TokenDrainTickContinuesAfterAtomicTransferFailure|MailSendOverageCreditsTreasuryUnderV2|MailSendOverageRejectsWhenSenderCannotCoverCharge)$'`
  - `go test ./internal/store ./internal/server`
  - `go test ./...`
- Ran `timeout 120 claude -p ...` review multiple times:
  - first round flagged failure-path handling and the non-atomic life-tax consume/recharge window
  - second round flagged the communication overage burn plus missing strict-transfer error-path coverage
  - final round returned no blocking findings after moving both life tax and communication overage onto treasury-preserving transfer paths and adding the missing tests

## Visible behavior

- v2.1 store migration now succeeds against the local persisted data set.
- Life-tax drain ticks preserve total token supply by atomically moving the deducted amount from the user into treasury.
- If one user's atomic life-tax transfer fails, the drain tick now logs the failure and continues processing the remaining bots instead of aborting the whole cycle.
- Communication overage now preserves total token supply by moving charged output from the sender into treasury instead of burning it.
- The local `minikube` runtime has now been smoke-tested across both major consumption flows and immediate/deferred reward flows without reducing total token supply.

## Risks and rollback

- Local rollout still starts the scheduled world tick loop shortly after boot. That means user balances can legitimately change during deployment because real life tax is still running, even though supply is preserved. If deployment requires strict "no balance drift during maintenance", runtime still needs an explicit maintenance freeze/pause path before wider rollout.
- This validation changed the local cluster env for Tian Dao law selection:
  - `TIAN_DAO_LAW_KEY=genesis-v2`
  - `TIAN_DAO_LAW_VERSION=2`
- Local rollback options:
  - runtime image rollback to the previous local image if needed
  - database rollback is not automatic; the compensating treasury repair was recorded as a ledger entry so the local history remains auditable
