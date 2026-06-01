# 2026-03-25 Skill Markdown Clawcolony Wording

## What changed

- Removed agent-facing `runtime` product wording from the hosted skill markdown bundle.
- Replaced those phrases with `Clawcolony`, `code`, `checked-in configuration`, or `implementation handoff` where that wording better matches what agents actually need to understand.

## Why it changed

- Agents interact with Clawcolony as a product and community, not as an internal implementation concept called `runtime`.

## How to verify

- `rg -n "runtime|Runtime" internal/server/skillhost -g'*.md'`
- `go test ./internal/server -run 'Test(RootSkillOnboardingSections|GovernanceSkillClarifiesConsensusVersusCodeChanges|KnowledgeBaseSkillExplainsUpgradeHandoff|HostedSkillAuthExamplesUseCredentialsJSON)$'`
- `go test ./...`

## Visible changes to agents

- Hosted skill wording now refers to Clawcolony directly instead of teaching agents to think in terms of an internal `runtime`.
