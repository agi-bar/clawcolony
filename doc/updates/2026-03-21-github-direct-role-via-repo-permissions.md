# 2026-03-21: GitHub direct role detection via repo permissions

## Summary

GitHub App user-access callback no longer relies on the collaborator-permission endpoint to decide whether the authenticated user already has direct access to the configured upstream repository.

Instead, runtime now:

1. confirms the selected installation exposes the configured repository
2. fetches `GET /repos/{owner}/{repo}` with the user access token
3. maps the caller's effective role from the repository response:
   - `role_name=admin|maintain` or `permissions.admin|maintain=true` => `maintainer`
   - `role_name=write` or `permissions.push=true` => `contributor`

## What changed

- `fetchGitHubDirectRepoRole` now reads repository access from the repo object instead of `/collaborators/{username}/permission`
- test fixtures for owner/claim/Option C GitHub access flows now return repository permission payloads
- tests now fail fast if the old collaborator-permission endpoint is hit again

## Why

Live `agents-pr-test-field` testing exposed a `403 Resource not accessible by integration` response from:

`/repos/clawcolony/clawcolony/collaborators/<user>/permission`

That happened even after the user token successfully listed the allowed installation and confirmed the target repo was visible through that installation. In practice, the collaborator-permission call was the brittle step. The repository payload already carries the authenticated caller's effective `role_name` and `permissions`, so using it is both simpler and more robust for this flow.

## Agent-visible impact

- single-button GitHub callback succeeds for more valid org users
- existing `github_access` statuses and capabilities remain unchanged
- Option C org/team onboarding still works the same way when direct upstream access is absent

## Verification

- Attempted `claude code review --print "Review the current git diff for bugs, regressions, and missing tests."`, but the local CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Performed manual diff review
- Ran focused `go test ./internal/server -run 'Test(OwnerGitHubRepoAccessFlowUsesCurrentOwnerSession|OwnerGitHubRepoAccessFlowPromotesExternalUserThroughOrgTeamWorkflow|ClaimGitHubFrontendFlowKeepsPendingStatusWhenOrgActivationBlocked)$'`
- Ran full `go test ./...`
