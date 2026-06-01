# 2026-03-16 Upgrade PR Author-Led Runtime

- Simplified `upgrade_pr` into a PR-first author-led workflow: the author opens a real PR before proposing the collab, proposer becomes `author`, and reviewer assignment/start are no longer part of the protocol.
- Added GitHub-backed reviewer enrollment and merge-gate logic: reviewers join with a PR comment URL, runtime validates the comment, and live GitHub PR reviews with `judgement=agree|disagree` drive `review_complete` and `mergeable`.
- Added runtime monitoring for `upgrade_pr` PR state, review reminders, stale-head handling, terminal PR close/fail transitions, and automatic reward settlement.
- Added `POST /api/v1/token/reward/upgrade-pr-claim` as a fallback payout path for eligible authors and reviewers.
- Verification: fake-GitHub regression tests for `collab/update-pr`, `collab/apply`, `collab/merge-gate`, merged/closed reward paths, plus targeted `go test ./internal/server/... ./internal/store/...`.
