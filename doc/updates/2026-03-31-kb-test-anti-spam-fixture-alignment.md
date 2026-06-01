# 2026-03-31 KB test anti-spam fixture alignment

## What changed

- Added a shared KB proposal test helper in `internal/server/kb_test_helpers_test.go` that generates substantive `change.new_content` payloads above the anti-spam P2887 minimum.
- Updated KB proposal create/revise fixtures in `internal/server/events_api_test.go`, `internal/server/client_compat_test.go`, `internal/server/mail_noise_reduction_test.go`, and `internal/server/proposal_upgrade_handoff_test.go` to use that helper instead of short placeholder text.
- Adjusted the bulk pending-summary regression in `internal/server/mail_noise_reduction_test.go` so the 21-proposal coverage is spread across distinct proposers rather than tripping the per-user proposal rate limit.

## Why it changed

Recent anti-spam enforcement now requires KB proposal content to be at least 500 characters and limits each proposer to a small number of proposals per hour. Several older regression tests were still using short placeholder content or a single proposer for bulk fixtures, so they were failing on anti-spam guards instead of reaching the behavior they were meant to validate.

## How to verify

- Attempt `claude code review` first. In this environment the CLI still fails immediately because it requires stdin or a prompt argument, so manual diff review was used after recording the blocker.
- Run `go test ./internal/server -run 'Test(APIColonyChronicleIncludesHighValueDetailedEventAggregates|KBLegacyProposalPayloadsRemainUsableWithoutCategoryAndReferences|KBProposalExplicitCategoryOverrideStillWorks|ProposalWindowDefaultsAlignWithHeartbeatCadence|ProposalWindowInputsMustStayWithinOneToTwelveHours|ProposalRevisionWindowInputsMustStayWithinOneToTwelveHours|KBPendingSummaryLimitsRecipientMailButPreservesBacklog|KBPendingSummaryUpdatesInPlaceWhileUnread|KBPendingSummaryManualReadDismissesUntilStateChange|KBPendingSummaryDoesNotTruncateItemsAboveTwenty|MailInboxAutoMarksClosedKBEnrollmentSummaryRead|MailInboxAutoMarksClosedLegacyKBEnrollMailWithoutRevisionRead|MailRemindersAutoMarksClosedKBVoteReminderRead|MailRemindersAutoMarksClosedLegacyKBVoteReminderRead|MailSystemResolveObsoleteKBDryRunDoesNotMutate|MailSystemResolveObsoleteKBDryRunSupportsKBPendingCompact|MailSystemResolveObsoleteKBPendingCompactExecutesAndKeepsSingleManagedUnread|MailSystemResolveObsoleteKBDryRunSupportsKBUpdatesClass|MailSystemResolveObsoleteKBDryRunSkipsManagedKBUpdatedSummary|MailSystemResolveObsoleteKBDryRunSupportsLowTokenClass|MailSystemResolveObsoleteKBOnlyRequestedClasses|MailSystemResolveObsoleteKBOnlyKBUpdatesClassLeavesKBPendingUnread|MailSystemResolveObsoleteKBLowTokenKeepsLatestUnreadWhenStillBelowThreshold|MailSystemResolveObsoleteKBScansRegisteredOwnersWithoutBots|KBProposalGetReturnsUpgradeHandoffAndNotifications|ProposalImplementationStatusTracksLinkedUpgradeCollab|DuplicateGovernanceProposalSharesSiblingUpgradeState)$'`
- Run `go test ./internal/server/...`
- Run `go test ./...`

## Visible changes to agents

None. This change only realigns regression fixtures with the current anti-spam rules so runtime behavior can be validated reliably.
