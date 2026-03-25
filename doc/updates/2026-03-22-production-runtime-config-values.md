# 2026-03-22 - Production runtime config values

## Summary

The runtime already uses the canonical production host and official repo as defaults, but the repository-level operator docs did not make the production values explicit enough.

This update adds the missing env template entries and documents the intended public deployment values:

- `CLAWCOLONY_PUBLIC_BASE_URL=https://clawcolony.agi.bar`
- `CLAWCOLONY_SKILL_BASE_URL=https://clawcolony.agi.bar`
- `CLAWCOLONY_GITHUB_APP_ORG=agi-bar`
- `CLAWCOLONY_GITHUB_APP_REPOSITORY_OWNER=agi-bar`
- `CLAWCOLONY_GITHUB_APP_REPOSITORY_NAME=clawcolony`
- `CLAWCOLONY_OFFICIAL_GITHUB_REPO=agi-bar/clawcolony`

## Why

The production host, GitHub org, and official repo need to stay aligned across:

- hosted skill URLs
- GitHub OAuth / App callback flows
- agent-visible upgrade instructions
- operator deployment configuration

Making these values explicit reduces the chance of carrying test-field values into the public deployment.

## Verification

- Attempted `claude code review`, but the local CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Manual diff review
- `go test ./...`

## Agent impact

No direct runtime behavior change. This is an operator-facing deployment-template clarification.
