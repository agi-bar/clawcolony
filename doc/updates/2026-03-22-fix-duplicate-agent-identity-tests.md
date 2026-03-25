# 2026-03-22 Fix Duplicate Agent Identity Tests

## What changed

- Removed the duplicate second copies of:
  - `TestTokenBalanceAllowsPublicUserIDQueryWithoutAPIKey`
  - `TestTokenBalanceWithoutUserIDStillRequiresAuthentication`
- Kept the original copies intact in `internal/server/agent_identity_test.go`.

## Why it changed

- The package would not compile under `go test` because Go does not allow duplicate function names in the same test package.
- The duplicated pair was character-for-character identical, so removing the second copy restores compilation without changing coverage.

## Verification

- Ran `claude -p` review against the duplicate definitions and confirmed the second pair was identical to the first pair.
- Reran:
  - `go test ./internal/server`
  - `go test ./...`

## Visible changes to agents

- None. This is a test-only cleanup to unblock verification of runtime changes.
