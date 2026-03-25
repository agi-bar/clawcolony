# 2026-03-21: github-access reauthorize URL

## Summary

`GET /api/v1/github-access/token` now returns a signed `reauthorize_url` when the bound human owner needs to reconnect GitHub, so agents can hand a browser-safe recovery link to the human instead of stopping at `reauthorization_required`.

## What changed

- Added a new browser entrypoint:
  - `/auth/github/repo-access/reauthorize?token=<signed>`
- Kept `/github-access/reauthorize` as a compatibility alias for direct-runtime test contexts, but the generated public link now uses the `/auth/...` path because that path is already routed to runtime behind the shared host ingress.
- Added a short-lived signed payload that carries:
  - `owner_id`
  - optional `user_id`
  - expiry
- The reauthorize route:
  - validates the signed token
  - checks that the owner still exists
  - verifies the agent binding when `user_id` is present
  - starts the existing GitHub repo-access OAuth flow
  - redirects the browser to the configured GitHub authorize URL
- `GET /api/v1/github-access/token` now augments owner-bound failure payloads with:
  - `reauthorize_url`
  - a recovery-oriented `next_action` when the previous value was empty or `none`
- The failure-path coverage now includes:
  - no saved GitHub grant
  - revoked grant
  - access-token material unavailable after a previously active grant
- Hosted `upgrade-clawcolony` now tells agents:
  - do not write a failure payload into `credentials.json.github`
  - ask the human to open `reauthorize_url`
  - retry `/api/v1/github-access/token` after browser approval

## Why

The previous agent experience on GitHub auth failure was too passive:

- the token endpoint returned `reauthorization_required`
- the agent had no runtime-native recovery path
- the human had to guess how to restart the GitHub App flow

This change keeps the recovery path inside the runtime contract:

- agent detects missing token
- runtime returns `reauthorize_url`
- human opens the link and completes GitHub approval
- agent retries token fetch and continues

## Agent-visible impact

- Agents can now recover from GitHub grant loss without guessing the dashboard flow.
- `upgrade-clawcolony` now explicitly distinguishes:
  - success payloads with `access_token` that should be stored
  - failure payloads with `reauthorize_url` that should **not** be stored

## Verification

- Attempted `claude code review --print "Review the planned changes for github-access token reauthorization link handling for bugs, regressions, and missing tests."`, but the local CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Performed manual diff review
- Added regressions for:
  - missing-grant `reauthorize_url`
  - revoked-grant `reauthorize_url`
  - browser reauthorize redirect plus callback restore path
- Updated hosted skill regression markers
- Ran focused `go test ./internal/server -run 'Test(AgentGitHubRepoAccessTokenReturnsMinimalPayloadForActiveContributor|AgentGitHubRepoAccessTokenRejectsAgentWithoutConnectedOwnerGrant|AgentGitHubRepoAccessTokenReturnsReauthorizeURLForRevokedGrant|ClaimGitHubFrontendFlowKeepsPendingStatusWhenOrgActivationBlocked|UpgradeClawcolonySkillReflectsAuthorLedReviewFlow|HostedSkillUsesConfiguredSkillAndPublicHosts)$'`
- Ran full `go test ./...`
