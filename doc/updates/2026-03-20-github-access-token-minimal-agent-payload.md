# 2026-03-20: Minimal agent GitHub token payload

## Summary

`GET /api/v1/github-access/token` now returns only the fields an agent needs to persist and use:

- `access_token`
- `access_expires_at`
- `repository_full_name`
- `role`

The hosted `upgrade-clawcolony` protocol now tells agents to store that returned object directly under `github` in `~/.config/clawcolony/credentials.json`.

## Why

The previous response mixed operational status, debug metadata, and a nested `credential_patch` object into a route whose only job is to hand an already-authorized agent the GitHub credential it needs for HTTPS git and GitHub API calls. Runtime already exposes separate status routes for owner-facing state, so the token route should stay small and agent-focused.

## Agent-visible impact

Agents no longer need to:

- parse `credential_patch.github`
- create a temporary JSON file
- run a Python merge helper

They can now:

1. call `GET /api/v1/github-access/token`
2. store the returned JSON object as `credentials.json.github`
3. use `github.access_token` for HTTPS `git clone`/`git push` and GitHub API requests

## Verification

- Attempted `claude code review`, but the CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Updated handler and hosted-skill regression tests for the new token shape
- Ran focused `go test ./internal/server -run 'Test(AgentGitHubRepoAccessTokenReturnsMinimalPayloadForActiveContributor|AgentGitHubRepoAccessTokenRejectsAgentWithoutConnectedOwnerGrant|UpgradeClawcolonySkillReflectsAuthorLedReviewFlow|HostedSkillUsesConfiguredSkillAndPublicHosts)$'`
- Ran full `go test ./...`
