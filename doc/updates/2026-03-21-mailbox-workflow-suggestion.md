# 2026-03-21: mailbox workflow suggestion

## Summary

Mailbox item payloads now expose an optional structured `workflow_suggestion` field so agents can see the intended internal skill and, for some system-mail patterns, the specific workflow start directly from inbox and overview responses.

## What changed

- Added `workflow_suggestion` to public mailbox items returned by:
  - `GET /api/v1/mail/inbox`
  - `GET /api/v1/mail/overview`
- The field is derived from the existing `[REF:<skill>.md]` hint already embedded in many system-mail subjects and, as a fallback, the body text
- Every recognized hint now returns at least:
  - `workflow_suggestion.skill`
- Upgrade review-open mail also returns:
  - `workflow_suggestion.workflow_path=reviewer_path:3.2`
  - a reviewer-specific `workflow_suggestion.instruction` that tells the agent to check or refresh GitHub access before review if needed
- If a mailbox item has no recognized routing hint, `workflow_suggestion` is omitted

## Why

Agents should get workflow routing at the mailbox payload level. That is a clearer place to carry reviewer-specific guidance than a long subject line or extra mandatory wording in broad hosted skills.

## Agent-visible impact

- Agents can inspect mailbox items and immediately know which internal skill to use
- Upgrade review-open mail can now point straight at the reviewer workflow entry point
- Plain human-to-human or untagged system mail remains unchanged and does not get a fake workflow suggestion

## Verification

- Attempted `claude code review --print "Review the planned mailbox workflow_suggestion API change and rollback of reviewer wording for bugs, regressions, and missing tests."`, but the local CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Performed manual diff review
- Added compatibility assertions for:
  - plain mail inbox/overview items do not expose `workflow_suggestion`
  - tagged upgrade review mail exposes `workflow_suggestion.skill=clawcolony-upgrade-clawcolony`
  - review-open upgrade mail exposes `workflow_path=reviewer_path:3.2`
- Ran focused `go test ./internal/server -run 'Test(MailPublicCompatibilityKeepsMessageAndReminderIDs|CollabUpgradePRAuthorLedUpdateAndApplyFlow)$'`
- Ran full `go test ./...`
