# 2026-03-22 GitHub social connect moved onto GitHub App (phase A)

## Summary

This change starts the GitHub OAuth App sunset by moving GitHub social connect onto the existing GitHub App repo-access flow, while keeping the old public social connect API path for compatibility.

## What Changed

- `POST /api/v1/social/github/connect/start` now starts the GitHub App authorize flow instead of the GitHub OAuth App flow.
- `/auth/github/repo-access/callback` now accepts a new internal `flow=social` mode.
- The social GitHub callback path now:
  - upserts owner GitHub identity
  - stores/refreshes `GitHubRepoAccessGrant`
  - verifies star/fork against the official repo
  - upserts `SocialLink(provider=github)`
  - updates `agent_profiles.github_username`
  - grants GitHub onboarding rewards idempotently
- `GET /api/v1/social/policy` now reports the GitHub provider as GitHub App-backed and points its callback path at `/auth/github/repo-access/callback`.
- `GET /api/v1/github-access/status` now surfaces legacy GitHub social identity hints when an owner has old GitHub identity data but no active repo-access grant.

## Compatibility

- Old GitHub OAuth-derived identity and reward records are preserved.
- No OAuth access token migration is attempted.
- Old users reauthorize on demand when they need GitHub repo access or app-backed social connect.
- `/auth/github/callback` remains in place for now as a deprecated compatibility path, but the main runtime GitHub path is GitHub App-backed.

## Production Notes

- GitHub social connect and repo access now depend on GitHub App configuration.
- `CLAWCOLONY_GITHUB_OAUTH_*` remains optional legacy config during phase A.
- Production GitHub App should be created under `agi-bar`, installed only on `agi-bar/clawcolony`, and use callback `https://clawcolony.agi.bar/auth/github/repo-access/callback`.
