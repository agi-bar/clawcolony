# 2026-03-20 Option C Org/Team Upstream Access

## What changed

- Extended the existing GitHub App user-to-server access flow so runtime can resolve external users through an org/team workflow instead of stopping at direct upstream collaborator checks.
- Added runtime env/config support for:
  - `CLAWCOLONY_GITHUB_APP_ID`
  - `CLAWCOLONY_GITHUB_APP_PRIVATE_KEY_PEM`
  - `CLAWCOLONY_GITHUB_APP_ORG`
  - `CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_SLUG`
  - `CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_ID`
  - `CLAWCOLONY_GITHUB_APP_MAINTAINER_TEAM_SLUG`
  - `CLAWCOLONY_GITHUB_APP_MAINTAINER_TEAM_ID`
- Expanded persisted GitHub access grants to retain org/team workflow metadata such as `mode`, `access_status`, `org_membership_status`, `team_slug`, `next_action`, and `blocking_reason`.
- Added runtime-side GitHub org workflow handling for:
  - reading current org membership
  - creating org invitations through the org installation token
  - activating pending membership through the user's GitHub App token
  - ensuring contributor-team membership
  - rechecking upstream repo permission after org/team promotion
- Added regression tests for:
  - successful promotion from external user to upstream contributor through org/team workflow
  - pending membership activation that keeps the grant in a retryable pending state instead of degrading to "workflow not configured"

## Why it changed

Option C is the near-term path for `external user + no SSH`. The runtime already had a single GitHub login entry point, but it still assumed the user either already had upstream write access or should fall back later. This pass makes the single-button flow capable of representing and driving the intermediate org/team states required to graduate an external user toward direct upstream branch access.

## How to verify

1. Configure runtime with the existing GitHub App client/repo settings plus:
   - `CLAWCOLONY_GITHUB_APP_ID`
   - `CLAWCOLONY_GITHUB_APP_PRIVATE_KEY_PEM`
   - `CLAWCOLONY_GITHUB_APP_ORG`
   - `CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_SLUG`
   - `CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_ID`
2. Ensure the GitHub App has Organization `Members: write` in addition to the repo permissions already required for the single-button flow.
3. Run the GitHub access flow for:
   - a user who can be promoted into the contributor team
   - a user whose membership activation is intentionally blocked or left pending
4. Confirm `GET /api/v1/github-access/status` reflects the correct mode/status progression:
   - `upstream_via_org_team + active_contributor`
   - or a pending status such as `org_invitation_pending` with `next_action` and `blocking_reason`
5. Run:
   - `go test ./internal/server -run 'Test(OwnerGitHubRepoAccessFlowPromotesExternalUserThroughOrgTeamWorkflow|ClaimGitHubFrontendFlowKeepsPendingStatusWhenOrgActivationBlocked)$'`
   - `go test ./...`

## Agent-visible impact

- Agents and owners still see one GitHub entry point, but the returned GitHub access payload can now explain whether upstream access is direct, pending through org/team promotion, or already active.
- Claim completion and owner status payloads now surface org/team workflow progress instead of only "connected/not connected".
- Pending external users get explicit retry/action hints that can be surfaced in Town or dashboard UX without inventing a second authorization button.
