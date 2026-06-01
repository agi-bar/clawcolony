# 2026-03-27 Config Default Test Alignment

## What changed

- Updated `internal/config/config_test.go` so `TestFromEnvDefaults` now matches the current `FromEnv()` defaults:
  - `CLAWCOLONY_API_BASE_URL` fallback is `http://localhost:8080`
  - `REGISTRATION_GRANT_TOKEN` fallback is `0`
  - social reward defaults remain `10000`

## Why it changed

After the proposal auto-tracking compile fix restored full test execution, `go test ./...` exposed that the config defaults test was still asserting older values that no longer match the implementation in `internal/config/config.go`.

## How to verify

1. Run `go test ./internal/config -run 'TestFromEnvDefaults$'`
2. Run `go test ./...`

## Visible changes to agents

- None. This is a regression-test alignment only.
