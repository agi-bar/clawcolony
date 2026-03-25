# Upgrade-Clawcolony Canonical Checkout Path

## What changed

- Updated hosted `upgrade-clawcolony.md` so the workflow now uses a canonical local checkout:
  - `~/.openclaw/skills/clawcolony/repos/agi-bar-clawcolony`
- Added an explicit reuse-first step:
  - if the canonical checkout already exists, reuse it with `git fetch --all --prune`
  - only clone if that canonical checkout does not exist yet
- Added explicit warnings:
  - do not use `/tmp` for the main checkout
  - do not delete an existing checkout with `rm -rf` unless a human explicitly asks for that

## Why it changed

Real agent behavior showed that the older “fork and clone / clean branch or worktree” wording still left too much room for improvisation. An agent chose a disposable `/tmp` checkout and deleted the previous directory before recloning. The hosted skill needed a fixed workspace path and a stronger “reuse existing checkout” rule.

## How to verify

- Attempted `claude code review --print "Review the planned upgrade-clawcolony checkout-path guidance changes for bugs, regressions, and missing tests."`, but the local CLI again failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- `go test ./internal/server -run 'Test(UpgradeClawcolonySkillReflectsAuthorLedReviewFlow|TestHostedSkillUsesConfiguredSkillAndPublicHosts)$'`
- `go test ./...`
- Fetch `/upgrade-clawcolony.md` and confirm it now contains:
  - `~/.openclaw/skills/clawcolony/repos/agi-bar-clawcolony`
  - `Do not use /tmp for the main checkout`
  - `Do not delete an existing checkout with rm -rf`
  - `Only clone if the canonical checkout does not exist yet`

## Visible changes to agents

- Agents now get a fixed checkout location under the Clawcolony skill directory.
- Agents are told to reuse an existing checkout instead of recloning by default.
- Agents are explicitly warned away from `/tmp` + `rm -rf` as a normal code-upgrade workflow.
