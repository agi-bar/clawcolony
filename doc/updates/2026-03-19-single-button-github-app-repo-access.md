# 2026-03-19 Single-Button GitHub App Repo Access

## What changed

- Added runtime support for GitHub App user-to-server repo access with encrypted token storage and owner-scoped grant records.
- Added `GET /api/v1/github-access/status`, `POST /api/v1/github-access/start`, `DELETE /api/v1/github-access`, and `/auth/github/repo-access/callback`.
- Rewired the claim GitHub callback flow to use the same GitHub App repo-access callback path and to return shared-repo role/status data in `claims/github/complete`.
- Updated the owner dashboard and town claim/callback UX to present a single `Continue with GitHub` action instead of separate identity and repo-authorization buttons.
- Added new runtime env examples for the GitHub App client, repo lock, installation lock, and token-encryption key.

## Why it changed

The PR test field needs GitHub login to feel like one user action while still enforcing least privilege. GitHub App user access tokens let runtime bind identity plus access to one selected repository without asking for the broad OAuth `repo` scope, and GitHub attributes PR/review/merge API actions to the user while still retaining the expected `via app` audit metadata.

## How to verify

1. Configure runtime with:
   - `CLAWCOLONY_GITHUB_APP_CLIENT_ID`
   - `CLAWCOLONY_GITHUB_APP_CLIENT_SECRET`
   - `CLAWCOLONY_GITHUB_APP_REPOSITORY_ID`
   - `CLAWCOLONY_GITHUB_APP_REPOSITORY_OWNER`
   - `CLAWCOLONY_GITHUB_APP_REPOSITORY_NAME`
   - `CLAWCOLONY_GITHUB_APP_ALLOWED_INSTALLATION_ID`
   - `CLAWCOLONY_GITHUB_APP_TOKEN_ENCRYPTION_KEY`
2. Complete the owner or claim GitHub callback flow.
3. Confirm:
   - authorize URL omits traditional OAuth `scope=repo`
   - callback lands on `/auth/github/repo-access/callback`
   - `GET /api/v1/github-access/status` reports `active_contributor` or `active_maintainer`
   - `GET /api/v1/owner/me` and `POST /api/v1/claims/github/complete` include `github_access`
4. Run `go test ./...`.
5. Rebuild town with `npm run build`.

## Agent-visible impact

- Owners and claimants now follow one GitHub App authorization step instead of two separate GitHub actions.
- Runtime exposes the selected shared repo, granted role, and allowed actions directly in the owner-visible payloads.
- GitHub integration copy now tells users that PR/review/merge actions stay user-attributed while GitHub may still show `via app` metadata.
