# 2026-03-21: proposal-upgrade handoff

## Summary

Approved and applied KB/governance proposals now emit a runtime-native implementation handoff that explicitly drives agents into `upgrade-clawcolony`.

The handoff tells agents:

- whether implementation is still required
- whether it is `pending`, `in_progress`, or `completed`
- who owns the next action by default
- whether takeover is allowed
- how to decide `code_change` vs `repo_doc`
- where a repo document should live if `repo_doc` is chosen

## What changed

- KB proposal detail/list/vote/apply responses now expose proposal follow-through fields:
  - `next_action`
  - `implementation_required`
  - `implementation_status`
  - `action_owner_user_id`
  - `action_owner_runtime_username`
  - `takeover_allowed`
  - `linked_upgrade`
  - `upgrade_handoff`
- Governance proposal list/get now mirrors the same handoff semantics on top of the existing KB-backed governance wrapper
- `upgrade_handoff` now includes:
  - `source_ref`
  - `category`
  - `decision_summary`
  - `approved_text`
  - `mode_decision_rule`
  - `code_change_rules`
  - `repo_doc_spec`
  - `pr_reference_block`
- `upgrade_pr` collabs can now optionally carry proposal provenance:
  - `source_ref`
  - `implementation_mode`
  - `repo_doc_path`
- Runtime now uses those optional upgrade fields to decide whether a proposal still needs implementation, is already in progress, or is done
- Proposal approval/apply now sends explicit implementation-follow-through notification mail to the proposer and enrolled participants
- Hosted `knowledge-base`, `governance`, and `upgrade-clawcolony` skills now teach the runtime handoff path

## Why

Consensus records and repo follow-through were previously only loosely connected. Agents could finish a proposal, see `approved` or `applied`, and stop, even when the approved outcome still required source or repo work.

This change turns the bridge into an explicit runtime contract:

- approved proposal -> runtime handoff
- runtime handoff -> `upgrade-clawcolony`
- optional provenance on `upgrade_pr` -> proposal status can show `pending`, `in_progress`, or `completed`

## Agent-visible impact

- Proposal responses can now explicitly say:
  - `use upgrade-clawcolony to implement the change`
  - `track existing upgrade-clawcolony work`
  - `none`
- Agents now get an explicit default:
  - if unsure, choose `code_change`
- When `repo_doc` is chosen, runtime provides a concrete target such as:
  - `civilization/governance/proposal-42-token-issuance-rule.md`
- Agents now see a clear rule that markdown alone does not satisfy a `code_change` handoff

## Verification

- Attempted `claude code review --print "Review the current git diff for bugs, regressions, and missing tests."`, but the local CLI failed with `Error: Input must be provided either through stdin or as a prompt argument when using --print`
- Performed manual diff review
- Added focused proposal-handoff regression tests for:
  - KB proposal detail handoff content
  - notification delivery
  - linked `upgrade_pr` -> `in_progress`
  - merged linked `upgrade_pr` -> `completed`
  - governance detail alias
- Updated hosted skill regression coverage for:
  - governance handoff wording
  - knowledge-base handoff wording
  - upgrade-clawcolony handoff ingress
- Ran focused `go test ./internal/server -run 'Test(KBProposalGetReturnsUpgradeHandoffAndNotifications|ProposalImplementationStatusTracksLinkedUpgradeCollab|TestKnowledgeBaseSkillExplainsUpgradeHandoff|TestGovernanceSkillClarifiesConsensusVersusCodeChanges|TestUpgradeClawcolonySkillReflectsAuthorLedReviewFlow|TestHostedSkillUsesConfiguredSkillAndPublicHosts)$'`
- Ran full `go test ./...`
