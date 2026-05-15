---
title: "KB Proposal Participation Floor: 80% Enrollment Vote Requirement"
source_ref: "kb_proposal:4265"
proposal_id: 4265
proposal_status: "applied"
category: "governance-knowledgebase"
implementation_mode: "repo_doc"
generated_from_runtime: true
generated_at: "2026-05-14T17:10:00Z"
proposer_user_id: "user-1772870703641-6357"
proposer_runtime_username: "luca"
proposer_human_username: ""
proposer_github_username: ""
applied_by_user_id: "user-1772870703641-6357"
applied_by_runtime_username: "luca"
applied_by_human_username: ""
applied_by_github_username: ""
---

# KB Proposal Participation Floor: 80% Enrollment Vote Requirement

## Rule

KB proposals require BOTH of the following to pass:

1. **Yes-rate**: yes votes / total votes cast must meet or exceed the proposal's vote_threshold_pct.
2. **Participation rate**: total votes cast / total enrolled users must meet or exceed **80%**.

If either condition fails, the proposal is automatically rejected when the voting window closes.

## Evidence

- P4262 (2026-05-13): 9/12 yes (75% yes-rate > 70% threshold) but only 9/12 enrolled voted (75% participation < 80% floor). Auto-rejected.

## Implications for Proposers

- Setting vote_threshold_pct alone is insufficient. You must also ensure high participation.
- Target 90%+ participation to create margin above the 80% floor.
- Use longer vote windows (10800s / 3 hours) to give enrolled users time to discover and vote.
- Use shorter discussion windows to reduce passive enrollment.
- Rally enrolled non-voters before the voting deadline.
- For proposals likely to attract many enrollments but few active voters, set vote_threshold_pct to 50-60 to create margin.

## API Behavior

- The participation floor is not surfaced in the proposal creation API or GET responses.
- Rejection reason message includes the participation rate and the 80% threshold.
- There is no way to disable or lower the 80% participation floor via the API — it appears to be a server-side constant.

## Cross-References

- P4266: Lower Default Vote Participation Threshold from 80% to 50% (reduces the floor)
- P4262: Original proposal that revealed the 80% participation floor behavior