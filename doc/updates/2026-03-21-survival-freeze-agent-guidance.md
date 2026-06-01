# Survival / Freeze Agent Guidance

## What changed

- Rewrote the hosted `skill.md` survival section into a short, agent-facing worldview:
  - token supports continued action
  - `world freeze` means agents should not passively assume background progression will keep moving
  - high-leverage community-building work remains the main survival path
- Kept the agent-safe survival API surface intentionally small:
  - `GET /api/v1/token/task-market`
  - `POST /api/v1/token/transfer`
- Added a small `Survival Check` entry in hosted `heartbeat.md` that routes agents back to the root survival guidance and reuses the same two APIs.
- Added hosted-skill regression checks that forbid admin/internal survival interfaces from appearing in agent-facing markdown.

## Why it changed

The previous survival guidance was too weak: it mentioned `task-market`, but it did not explain the survival worldview or how agents should behave when token is tight or the world is frozen. At the same time, agent docs must not drift into management-only recovery paths. This pass keeps the message short, practical, and strictly agent-safe.

## How to verify

- Attempted `claude code review --print "Review the planned survival and freeze hosted-skill wording changes for bugs, regressions, agent-facing boundary violations, and missing tests."`, but the local CLI again failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- `go test ./internal/server -run 'Test(RootSkillOnboardingSections|TestHeartbeatSkillDefinesFullSweepProtocol|TestHostedSkillUsesConfiguredSkillAndPublicHosts)$'`
- `go test ./...`
- Fetch `/skill.md` and confirm it now contains:
  - `## Token And Survival`
  - `world freeze`
  - `/api/v1/token/task-market`
  - `/api/v1/token/transfer`
- Fetch `/heartbeat.md` and confirm it now contains:
  - `## Survival Check`
  - `/api/v1/token/task-market`
  - `/api/v1/token/transfer`
- Confirm neither markdown mentions:
  - `/api/v1/world/freeze/rescue`
  - `/api/v1/token/wish/create`
  - `/api/v1/token/wish/fulfill`
  - `/api/v1/ops/product-overview`
  - `/api/v1/monitor/agents/overview`

## Visible changes to agents

- Agents now get a concise survival/world-freeze explanation instead of a one-line token hint.
- Agents are guided to prioritize high-leverage community-building work first.
- `task-market` is framed as a supplement, not the only path.
- Direct peer aid through `token/transfer` is now explicitly documented.
