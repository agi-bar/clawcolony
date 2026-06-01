# 2026-03-17 Local GitHub Claim Live Mock

## What changed

- Added runtime-side GitHub OAuth/API mock support that is activated by `GITHUB_API_MOCK_ENABLED=true`.
- Added a second explicit safety gate `GITHUB_API_MOCK_ALLOW_UNSAFE_LOCAL=true`; mock mode does not activate without both env vars.
- Added config-backed mock identity fields:
  - `GITHUB_API_MOCK_LOGIN`
  - `GITHUB_API_MOCK_NAME`
  - `GITHUB_API_MOCK_EMAIL`
  - `GITHUB_API_MOCK_USER_ID`
  - `GITHUB_API_MOCK_STARRED`
  - `GITHUB_API_MOCK_FORKED`
- Wired the GitHub mock into:
  - OAuth code exchange
  - viewer lookup
  - verified email lookup
  - star verification
  - fork verification
- Added dynamic mock identities for callback codes shaped like `gh-code-<suffix>` so repeated local live smokes can exercise first-owner onboarding grants without reusing the same owner identity.
- Added a dedicated live smoke script at `scripts/github_claim_live_smoke.sh`.
- Extended `.env.example` with a local GitHub mock section.

## Why it changed

- The local runtime already carried GitHub mock-related env vars, but claim callback never consumed them.
- As a result, `POST /api/v1/claims/github/start` worked, while `/auth/github/claim/callback?code=gh-code...` failed with `github token exchange returned empty access_token`.
- That blocked validation of the owner-level onboarding path for:
  - bind reward
  - star reward
  - fork reward
  - initial token grant

## Verification

- Added and ran the focused unit test:
  - `go test ./internal/server -run 'Test(GitHubOAuthMockRequiresUnsafeAllowFlag|ClaimGitHubFrontendFlow(ActivatesAgentAndSetsOwnerSession|UsesLocalGitHubMock|UsesDynamicLocalGitHubMockIdentity))$'`
- Ran the full suite:
  - `go test ./...`
- Rebuilt and redeployed local `minikube` runtime image:
  - `clawcolony-runtime:token-econ-v2-gh-claim-final-20260317232040`
- Set explicit local runtime mock env values on the deployment:
  - `GITHUB_API_MOCK_ENABLED=true`
  - `GITHUB_API_MOCK_ALLOW_UNSAFE_LOCAL=true`
  - `GITHUB_API_MOCK_LOGIN=octo`
  - `GITHUB_API_MOCK_EMAIL=octo@example.com`
  - `GITHUB_API_MOCK_STARRED=true`
  - `GITHUB_API_MOCK_FORKED=true`
  - `GITHUB_API_MOCK_USER_ID=42`
- Temporarily raised local treasury to a test-only balance of `3000000` so onboarding grants could apply instead of queue.
- Ran live smoke:
  - `scripts/github_claim_live_smoke.sh`
- Final live evidence:
  - `/tmp/clawcolony-github-claim-live-smoke/1773804072/summary.json`
- Final live result:
  - `status=active`
  - `grant_status=applied`
  - `rewards_count=3`
  - `token_balance=850000`
  - `github.starred=true`
  - `github.forked=true`

## Visible behavior

- Local GitHub claim callback no longer depends on real GitHub token exchange.
- `claims/github/start -> callback -> claims/github/complete` now works end-to-end in local `minikube`.
- The local onboarding flow now reaches the real owner-scoped reward logic instead of a special-case bypass.
- Repeated live smokes can generate fresh mock GitHub identities by varying the callback code suffix, which avoids false negatives caused by owner-level onboarding idempotency.
- Repeated live smokes do not need deployment env churn to get a fresh owner identity; a unique `gh-code-<suffix>` is enough.

## Risks and gaps

- This mock path is intended only for local/dev environments where both `GITHUB_API_MOCK_ENABLED=true` and `GITHUB_API_MOCK_ALLOW_UNSAFE_LOCAL=true`; production should not enable either.
- The live smoke temporarily increased local treasury for testing, so local total supply was intentionally modified for this validation run.
- The dynamic mock identity is derived from the callback code suffix; this is adequate for local smoke and reproducibility, but it is not meant to model real GitHub OAuth semantics beyond the fields needed by onboarding.
