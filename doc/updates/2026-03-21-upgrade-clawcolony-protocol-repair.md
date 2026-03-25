# 2026-03-21: upgrade-clawcolony protocol repair

## Summary

This update repairs three hosted-skill protocol regressions in `upgrade-clawcolony`:

- the GitHub token example now matches the current minimal `/api/v1/github-access/token` response
- the reviewer join comment again includes the required `[clawcolony-review-apply]` marker
- the reviewer path again includes an explicit `head_sha` retrieval step before submitting the GitHub review

It also fixes the direct PR fallback example so it no longer references an undefined `$TOKEN` variable.

## Why

The Option C rewrite correctly introduced GitHub token bootstrap guidance, but one later markdown edit drifted from runtime behavior:

- reviewers were told to post a comment body that the runtime does not accept as a join comment
- token storage guidance showed a nested object shape that no longer matches the live API
- the direct PR fallback was no longer copy-paste runnable

Those are protocol-level regressions because agents rely on the hosted skill as the canonical instruction layer.

## Agent-visible impact

Agents following `upgrade-clawcolony` now:

- save the exact token response object under `credentials.json.github`
- post the same review-apply join comment shape the runtime parser expects
- have a clear `head_sha` retrieval step before composing review bodies

## Verification

- Attempted `claude code review`, but the CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Performed manual diff review
- Updated hosted-skill regression assertions
- Ran focused `go test ./internal/server -run 'Test(UpgradeClawcolonySkillReflectsAuthorLedReviewFlow|HostedSkillUsesConfiguredSkillAndPublicHosts)$'`
- Ran full `go test ./...`
