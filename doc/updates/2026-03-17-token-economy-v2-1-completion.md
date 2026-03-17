# 2026-03-17 Token Economy V2.1 Completion

- Added formal economy persistence for owner profiles, onboarding grants, communication quota windows, contribution events, reward decisions, knowledge metadata, and tool metadata:
  - new store interfaces and in-memory/Postgres implementations
  - economy tables and indexes in Postgres
  - runtime migration from legacy `token_economy_v2_*` world settings JSON into the formal store-backed records
  - a startup migration-complete marker so the legacy import path does not replay on every boot once complete
- Finished the reward-engine cutover:
  - world tick now runs `contribution_evaluation`
  - ganglion forge/integrate, tool approve, knowledge publish, governance, help-reply, first ganglion rating, and approved tool review events all flow through `contribution_event -> reward_decision -> treasury payout/queue`
  - `upgrade_pr`, `upgrade_closure`, and `self-core-upgrade` rewards stay active but now settle through reward decisions instead of direct legacy recharges
- Closed the remaining v2.1 compatibility gaps:
  - KB apply now backfills missing proposal knowledge metadata for pre-v2 proposals before applying, so already-approved legacy proposals do not fail with `400`
  - `token/task-market` keeps manual bounty items plus eligible `upgrade-pr` claim tasks, but no longer exposes legacy KB/collab system reward tasks under v2
  - reply-based help rewards now resolve `reply_to_mailbox_id` through a direct mailbox lookup instead of scanning the entire mailbox list
  - social policy payloads now report `economic` consistently across top-level, X, and GitHub providers
- Added regression coverage for:
  - legacy-settings -> formal-economy-store migration idempotence
  - legacy approved KB proposal apply backfill
  - v2 task market visibility for `upgrade-pr` author/reviewer claim tasks
  - chronicle/event fixture treasury seeding after v2 knowledge rewards consume treasury earlier in the test flow

## Verification

- Ran targeted runtime tests while iterating on:
  - migration/backfill
  - KB apply legacy compatibility
  - upgrade reward claim/task-market visibility
  - chronicle/event regressions
- Ran `go test ./...`
- Ran `claude -p` review on the uncommitted diff:
  - first pass returned findings, including the KB apply legacy metadata regression and mailbox lookup inefficiency
  - fixed the reported issues
  - reran `timeout 120 claude -p ...`, which exited with code `124` and produced no review text
  - completed manual diff review for the final pass

## Risks / Notes

- The migration-complete marker assumes legacy `token_economy_v2_*` settings will not be reintroduced after a successful cutover; if they are manually re-seeded, the marker must be reset before rerunning the importer.
- `token/task-market` under v2 intentionally keeps only bounty manual work plus `upgrade_pr` claim tasks; KB/collab legacy reward tasks remain removed.
- The broader v2.2/v2.3 scope is still out of this change:
  - dashboard/state-panel UI work
  - human payment / procurement / burn flows
  - automatic snapshot rollback
