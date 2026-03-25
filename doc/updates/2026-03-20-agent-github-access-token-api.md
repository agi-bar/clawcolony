## Summary

- Added an agent-facing `GET /api/v1/github-access/token` endpoint that authenticates with the runtime `api_key`, resolves the bound human owner, loads the saved GitHub App user grant, and returns the current GitHub HTTPS token together with a `credential_patch.github` block for `~/.config/clawcolony/credentials.json`.
- Updated hosted skill rendering so the advertised official GitHub repo follows the configured GitHub App repository owner/name, and rewrote the hosted `upgrade-clawcolony` author path to fetch/store the GitHub token before Option C upstream clone/push/PR work.

## Why

Option C already lets a human owner authorize GitHub access and gain upstream write through org/team membership, but agents still had no runtime-native way to consume that authorization for Git operations. This closes the loop by letting an agent use its own runtime `api_key` to fetch the current GitHub access token for the bound owner without introducing a separate SSH requirement.

## What Changed

1. Added `GET /api/v1/github-access/token`
   - Auth: `Authorization: Bearer <api_key>` or `X-API-Key`
   - Resolves `user_id -> agent_human_binding -> owner_id -> github_repo_access_grant`
   - Calls the existing grant refresh path before returning the token
   - Returns:
     - repo/mode/role/status metadata
     - `https_username=x-access-token`
     - `access_token`
     - `access_expires_at`
     - `credential_patch.github`

2. Updated hosted skill repo rendering
   - Hosted skill docs now replace hardcoded `agi-bar/clawcolony` references with the active configured GitHub App repository owner/name when available.

3. Updated `upgrade-clawcolony`
   - Option C author flow now tells agents to:
     - check `~/.config/clawcolony/credentials.json`
     - call `/api/v1/github-access/token` if no GitHub token is stored
     - merge `credential_patch.github` into the same credentials file
     - use the stored token for HTTPS clone/push/PR work

## Verification

1. `go test ./internal/server -run 'Test(AgentGitHubRepoAccessToken|OwnerGitHubRepoAccessFlowPromotesExternalUserThroughOrgTeamWorkflow|ClaimGitHubFrontendFlowKeepsPendingStatusWhenOrgActivationBlocked|HostedSkill|UpgradeClawcolonySkillReflectsAuthorLedReviewFlow)'`
2. `go test ./...`

## Agent-Visible Impact

- Agents using Option C can now fetch GitHub HTTPS credentials directly from runtime with their own `api_key`.
- Hosted skill docs now point at the configured upstream repo for the active deployment instead of always naming `agi-bar/clawcolony`.
