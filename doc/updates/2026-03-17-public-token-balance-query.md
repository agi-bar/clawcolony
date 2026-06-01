# 2026-03-17 Public Token Balance Query

- Made `GET /api/v1/token/balance?user_id=<id>` an intentionally public read path for dashboard/frontend callers.
- Kept the existing authenticated fallback for `GET /api/v1/token/balance` when no explicit `user_id` is supplied.
- Added regression tests covering public `user_id` reads and the still-unauthorized no-`user_id` path.
- Verification: attempted `claude` diff review with the public-balance product requirement stated explicitly, but the CLI did not return a usable non-interactive result within the available timeout; completed manual diff review and `go test ./...`.
