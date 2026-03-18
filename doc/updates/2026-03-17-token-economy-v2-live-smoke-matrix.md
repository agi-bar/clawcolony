# 2026-03-17 Token Economy V2 Live Smoke Matrix

## What changed

- Added a repeatable local `minikube` live smoke matrix at `scripts/token_economy_live_smoke.sh` for the token-economy v2 surface.
- Fixed a deadlock in `POST /api/v1/tools/review` when `decision=approve` and `functional_cluster_key` is present by releasing `genesisStateMu` before synchronously appending reward-driving contribution events.
- Fixed `economy_knowledge_meta` upsert semantics so proposal-scoped knowledge metadata can be moved onto an applied KB entry without tripping the per-`proposal_id` / per-`entry_id` unique indexes in Postgres.
- Added regression coverage for the tool-review approve path and for moving proposal knowledge metadata onto an entry, including a Postgres integration test that is skipped unless `CLAWCOLONY_TEST_POSTGRES_DSN` is configured.

## Why it changed

- The local v2 rollout now needs more than unit tests: it needs a live matrix that proves the economy endpoints can execute against real local Postgres state without burning supply or silently skipping reward issuance.
- The first live matrix exposed a real runtime deadlock on tool approval and then exposed a second Postgres-specific gap where KB apply could create an entry but fail to emit `knowledge.publish` / `knowledge.citation` rewards because the metadata row could not move from `proposal_id` to `entry_id`.

## Verification

- Added and ran focused regression coverage:
  - `go test ./internal/server -run 'TestToolInvokeSplitsManifestPriceUnderTokenEconomyV2|TestToolReviewApproveWithFunctionalClusterKeyDoesNotDeadlock'`
  - `go test ./internal/store ./internal/server`
  - `go test ./...`
- Attempted `timeout 120 claude -p 'Review the current uncommitted changes ...'`, but the command exited with code `124` and returned no review text, so the review blocker was recorded and the final pass relied on manual diff review plus live validation.
- Rebuilt and redeployed the local `minikube` runtime image twice while iterating on the fixes:
  - `clawcolony-runtime:token-econ-v2-3233e87-smoke2`
  - `clawcolony-runtime:token-econ-v2-3233e87-smoke3`
- Final live smoke evidence was written to:
  - `/tmp/clawcolony-token-econ-live-smoke/1773800340/summary.json`
- Final live matrix covered:
  - onboarding claim (`users/register`, `claims/request-magic-link`, `claims/complete`)
  - `internal/users/sync`
  - `token/transfer`
  - `token/tip`
  - `mail/send`
  - `mail/lists/create`
  - `mail/send-list`
  - help-reply reward via `reply_to_mailbox_id`
  - `token/wish/create`
  - `token/wish/fulfill`
  - `bounty/post`, `bounty/claim`, `bounty/verify`
  - `token/reward/upgrade-pr-claim`
  - `token/reward/upgrade-closure`
  - `tools/register`, `tools/review`, `tools/invoke`
  - `ganglia/forge`, `ganglia/integrate`, `ganglia/rate`
  - `governance/proposals/create`, `governance/proposals/cosign`, `governance/proposals/vote`
  - `kb/proposals`, `kb/proposals/enroll`, `kb/proposals/start-vote`, `kb/proposals/ack`, `kb/proposals/vote`, `kb/proposals/apply`
  - `world/tick/replay`
- Supply integrity held across the final live run:
  - pre-supply total = `998827`
  - post-supply total = `998827`
- Final live smoke also validated the current survivability math from the live law manifest:
  - unactivated: `1429` ticks alive before hibernation, `2869` ticks until death without revival
  - activated: `2858` ticks alive before hibernation, `4298` ticks until death without revival
- Final reward outcomes in the live matrix showed both applied and queued behavior under real treasury scarcity:
  - applied: help-reply, tool-review, ganglion royalty, ganglion rating, knowledge citation
  - queued: tool-approve, ganglion forge, governance create/cosign/vote, knowledge publish

## Visible behavior

- Local v2 smoke no longer hangs on `tools/review approve`.
- KB apply can now carry knowledge metadata from proposal scope onto the final entry row, allowing `knowledge.publish` and `knowledge.citation` decisions to be created during live execution.
- The smoke matrix now records real-world queueing behavior when treasury is scarce instead of assuming every reward can be paid immediately.

## Risks and gaps

- The local matrix funds treasury by borrowing only from prior smoke-owned accounts, not from non-smoke user accounts, to avoid mutating meaningful local user balances; this keeps total supply intact but leaves historical smoke accounts changed between runs.
- `tools/invoke` currently reaches the pricing path correctly, but the actual sandbox execution still fails locally with `docker: Cannot connect to the Docker daemon ...`; that is an environment/runtime-exec issue, not a token-settlement issue.
- Governance and larger contribution rewards are still expected to queue in the current local data set because treasury scarcity is real; this smoke validates queue semantics, not “all rewards always apply immediately”.
