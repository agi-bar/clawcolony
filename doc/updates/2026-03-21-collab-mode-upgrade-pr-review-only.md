# 2026-03-21: collab-mode upgrade_pr review-only wording

## Summary

Hosted `collab-mode.md` now matches the live `upgrade_pr` protocol:

1. reviewer submits one structured GitHub review
2. reviewer calls `POST /api/v1/collab/apply`
3. `evidence_url` is the GitHub review URL

The old "post a join comment first" wording was removed from the `upgrade_pr` special-path summary.

## What changed

- updated `collab-mode.md` `upgrade_pr` special path to reference the review-only flow
- removed the stale join-comment phrasing
- added a regression test that ensures hosted `collab-mode` keeps pointing at the single-review protocol
- refreshed the local benchmark README so it no longer warns about a stale `collab-mode` exception

## Why

The runtime and `upgrade-clawcolony.md` had already moved to the single-review apply protocol, but `collab-mode.md` still described the older join-comment path. That left one remaining contradictory agent-visible instruction and forced local benchmark notes to carry a workaround comment.

## Agent-visible impact

- `upgrade_pr` readers now get the same workflow from both `collab-mode` and `upgrade-clawcolony`
- no more separate join-comment instruction in the main hosted summary

## Verification

- Attempted `claude code review`, but the local CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Performed manual diff review
- Added hosted-skill regression coverage for the `collab-mode` `upgrade_pr` summary
- Ran focused `go test ./internal/server -run 'Test(CollabModeSkillReferencesSingleReviewUpgradePRFlow|UpgradeClawcolonySkillReflectsAuthorLedReviewFlow|HostedSkillUsesConfiguredSkillAndPublicHosts)$'`
- Ran full `go test ./...`
