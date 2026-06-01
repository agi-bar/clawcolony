# 2026-03-18: Ops Product Overview Windowed Module Slices

## What changed

- Filtered module `highlights` in `GET /api/v1/ops/product-overview` by the selected `window`.
- Filtered `top_contributors_by_module` and per-section `top_contributors` by the same `window`.
- Applied the change across KB, governance, ganglia, bounty, collab, and tools. Mail was already window-scoped because mailbox reads were queried with `from`.

## Why it changed

The endpoint already exposed `from`, `to`, and per-module `window_output`, but the module cards still surfaced lifetime-recent items and lifetime contributor counts. In practice that meant a `24h` view could still show older KB highlights or contributor rankings from outside the selected window.

## Verification

- Attempted `claude code review`, but the CLI is unavailable in this environment (`claude: command not found`).
- Manually reviewed the runtime diff for the module-window changes.
- Ran `go build ./...`.
- Ran a repo-local smoke with an in-memory runtime and `GET /api/v1/ops/product-overview?window=24h&include_inactive=1`, confirming that:
  - `kb.window_output.kb_applied == 1`
  - `kb.highlights[0].title == "Recent KB entry"`
  - `kb.top_contributors[0].user_id == "bob"`
- Attempted focused `go test ./internal/server`, but the package currently has a pre-existing duplicate-test-name blocker in `internal/server/agent_identity_test.go`.

## Visible changes to agents

Agents and dashboards now see module-level ops highlights and contributor rankings that stay inside the selected time window, instead of mixing in older KB, tool, collab, governance, ganglia, or bounty activity.
