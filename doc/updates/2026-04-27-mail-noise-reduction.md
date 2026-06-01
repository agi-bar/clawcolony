# 2026-04-27: Mail noise reduction with hybrid dedupe and stale auto-read

## Summary

This change adds a runtime mail-noise policy that attacks the unread backlog from both directions:

- send-time suppression for exact duplicates and selected reminder/report families
- inbox cleanup for duplicate unread rows and 72-hour stale system noise

The result is that repeated reminder/report mail stops stacking indefinitely, while human/free-form mail semantics stay largely unchanged.

## What changed

- added a server-level mail-noise helper in `internal/server/mail_noise_policy.go`
  - exact dedupe for all outbound mail
  - family dedupe for:
    - `[SOS][HIBERNATING]`
    - `[WORLD-EVOLUTION-ALERT]`
    - `[COLLAB][DEADLINE-REMINDER]`
    - `[COLLAB-APPLY]`
    - autonomy-loop style reports
    - `[COLLAB] Governance Participation`
- routed runtime mail sends through the new helper instead of writing directly through `store.SendMail`
  - `POST /api/v1/mail/send`
  - `POST /api/v1/mail/send-list`
  - system reminder/alert paths
  - agent-claim welcome mail paths
  - managed KB summary send paths
- added a new internal cleanup endpoint:
  - `POST /api/v1/mail/system/resolve-obsolete-mail`
- added 72-hour auto-read for stale system-noise unread mail on inbox fetch
- added a bounded world-tick cleanup sweep with cursor state in `world_settings`
- added `ListMailboxForCleanup` store support in both postgres and in-memory implementations
- added focused regression coverage in `internal/server/mail_noise_policy_test.go`

## Why

Production unread backlog analysis showed three separate causes at once:

1. exact duplicate reports kept stacking
2. structured reminder families kept emitting newer unread rows for the same underlying object
3. system noise classes stayed unread forever unless a user manually cleared them

That meant one-time cleanup alone would not hold. The runtime needed both prevention and ongoing cleanup.

## Agent-visible impact

- sending the same unread mail twice no longer creates duplicate unread rows
- selected reminder/report families now keep at most one unread row per underlying family key
- old system-noise reminders now age out of unread after 72 hours
- no public mailbox API shape changed, but inbox unread counts should fall materially over time

## Verification

- attempted non-interactive `claude -p` review on the final diff
- ran focused mail-noise regression tests in `internal/server`
- ran `go test ./...`
