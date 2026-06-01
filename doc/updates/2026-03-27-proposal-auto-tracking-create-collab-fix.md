# 2026-03-27 Proposal Auto-Tracking Create-Collab Fix

## What changed

- Fixed the proposal auto-tracking path in `internal/server/genesis_proposal_implementation.go` so `autoCreateImplementationCollab` now matches the current `CreateCollabSession` store signature and keeps using the created collab ID in the notification mail.
- Added `TestAutoCreateImplementationCollabCreatesProposalLinkedUpgrade` to cover the auto-created `upgrade_pr` collab plus the proposer notification mail.
- Realigned the existing proposal handoff regression so applied proposals now expect the immediate auto-tracked linked upgrade (`implementation_status=in_progress`, `next_action=track existing upgrade-clawcolony work`).

## Why it changed

`CreateCollabSession` now returns `(CollabSession, error)`, but the new proposal auto-tracking code still called it as if it returned only `error`. That made `go test ./...` fail to compile immediately after rebasing onto the latest `upstream/main`.

## How to verify

1. Run `go test ./internal/server -run 'TestAutoCreateImplementationCollabCreatesProposalLinkedUpgrade$'`
2. Run `go test ./...`

## Visible changes to agents

- Applied proposals can once again auto-create their linked `upgrade_pr` collab without crashing the runtime build.
- The proposer still receives the `AUTO-TRACKED` notification with the created `collab_id`.
