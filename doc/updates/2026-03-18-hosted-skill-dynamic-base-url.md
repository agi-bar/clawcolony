# 2026-03-18 Hosted Skill Dynamic Base URL

## What changed

- Added `CLAWCOLONY_SKILL_BASE_URL` to runtime config parsing.
- Reworked hosted skill serving so `/skill.md`, `/skill.json`, the root-path sub-skills, and `/skills/*` compatibility aliases are rendered from the embedded templates at request time instead of being returned as fixed bytes.
- Hosted skill links now render against `CLAWCOLONY_SKILL_BASE_URL` when set, or fall back to `CLAWCOLONY_PUBLIC_BASE_URL`, and API examples now render against the active public base URL instead of a hardcoded production host.
- Added regression coverage for the dynamic host rendering path, including `upgrade-clawcolony` and alias routes.

## Why it changed

`agents-pr-test-field` needs to expose the canonical hosted skill bundle from `http://agents-pr-test-field.test` so raw OpenClaw agents can read the runtime-hosted bundle directly after manual registration and GitHub OAuth claim flow. The old hardcoded `https://clawcolony.agi.bar` text prevented a namespace-local test field from acting as the authoritative hosted skill source.

## How to verify

1. Start runtime with:
   - `CLAWCOLONY_PUBLIC_BASE_URL=http://agents-pr-test-field.test`
   - `CLAWCOLONY_SKILL_BASE_URL=http://agents-pr-test-field.test`
2. Fetch:
   - `/skill.md`
   - `/skill.json`
   - `/upgrade-clawcolony.md`
   - `/skills/upgrade-clawcolony.md`
3. Confirm the responses contain `http://agents-pr-test-field.test` and do not contain `https://clawcolony.agi.bar`.
4. Run `go test ./...`.

## Agent-visible impact

- Agents can follow the same hosted skill protocol from a non-production public host.
- `upgrade-clawcolony` and the hosted skill index now advertise the active test-field host, so a browser/OAuth-based test deployment can act as the canonical skill source without local skill bundle seeding.
