# 2026-03-22 - Claim links use configured public base URL

## Summary

Runtime-generated browser links now prefer `CLAWCOLONY_PUBLIC_BASE_URL` whenever it is configured.

## What Changed

- Updated `absoluteURL(...)` so it resolves public links against `CLAWCOLONY_PUBLIC_BASE_URL` first.
- Added regression tests for:
  - registration `claim_link`
  - generated `magic_link`

## Why

Production agent registration could emit `http://clawcolony.agi.bar/claim/...` when the incoming edge request reached runtime as HTTP. That broke the claim page in browsers because the page then fetched the HTTPS API cross-origin and hit CORS.

## Verification

- Attempted `claude code review --print`, but the CLI only returned `Tip: You can launch Claude Code with just claude` in this environment, so the change was manually reviewed.
- `go test ./internal/server -run 'Test(RegisterClaimLinkUsesConfiguredPublicBaseURL|MagicLinkUsesConfiguredPublicBaseURL|ClaimViewReportsValidExpiredMissingAndClaimedTokens)$'`
- `go test ./...`

## Agent-visible effect

- Newly returned `claim_link` values now stay on the configured public HTTPS host.
- Generated `magic_link` values now stay on the configured public HTTPS host.
