package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"clawcolony/internal/economy"
	"clawcolony/internal/store"
)

const (
	communityRewardStateKey = "community_reward_state_v1"

	tokenTaskMarketSourceManual = "manual"
	tokenTaskMarketSourceSystem = "system"

	communityRewardRuleKBApply                   = "kb.apply"
	communityRewardRuleCollabClose               = "collab.close"
	communityRewardRuleBountyPaid                = "bounty.paid"
	communityRewardRuleGangliaIntegrate          = "ganglia.integrate"
	communityRewardRuleUpgradeClawcolony         = "upgrade-clawcolony"
	communityRewardRuleUpgradePRAuthor           = "upgrade-pr.author"
	communityRewardRuleUpgradePRReviewer         = "upgrade-pr.reviewer"
	communityRewardRuleSelfCoreUpgrade           = "self-core-upgrade"
	communityRewardAmountBountyPaid        int64 = 5000
	communityRewardAmountGanglia                 = 5000
	communityRewardAmountKBApply                 = 5000
	communityRewardAmountCollabClose             = 5000
	communityRewardAmountUpgradeClosure          = 20000
	communityRewardAmountUpgradePRAuthor         = communityRewardAmountUpgradeClosure
	communityRewardAmountUpgradePRReviewer       = 2000
	taskClaimPolicyExclusiveLease                = "exclusive_lease"
	taskClaimPolicySharedOpen                    = "shared_open"
	taskClaimPolicyViewerOnly                    = "viewer_only"
	proposalImplementationTaskLeaseKind          = "proposal_implementation"
	taskMarketAcceptRateLimitMaxClaims           = 2
)

const proposalImplementationTaskLeaseDuration = 6 * time.Hour
const taskMarketAcceptRateLimitWindow = 30 * time.Minute
const proposalImplementationTaskOpenDelay = time.Hour

type communityRewardGrant struct {
	GrantKey      string         `json:"grant_key"`
	RuleKey       string         `json:"rule_key"`
	ResourceType  string         `json:"resource_type"`
	ResourceID    string         `json:"resource_id"`
	RecipientUser string         `json:"recipient_user_id"`
	Amount        int64          `json:"amount"`
	LedgerID      int64          `json:"ledger_id"`
	BalanceAfter  int64          `json:"balance_after"`
	CreatedAt     time.Time      `json:"created_at"`
	Meta          map[string]any `json:"meta,omitempty"`
}

type communityRewardState struct {
	Grants map[string]communityRewardGrant `json:"grants"`
}

type communityRewardResult struct {
	GrantKey        string         `json:"grant_key"`
	RuleKey         string         `json:"rule_key"`
	ResourceType    string         `json:"resource_type"`
	ResourceID      string         `json:"resource_id"`
	RecipientUserID string         `json:"recipient_user_id"`
	Amount          int64          `json:"amount"`
	Applied         bool           `json:"applied"`
	LedgerID        int64          `json:"ledger_id,omitempty"`
	BalanceAfter    int64          `json:"balance_after,omitempty"`
	CreatedAt       time.Time      `json:"created_at,omitempty"`
	Meta            map[string]any `json:"meta,omitempty"`
}

type communityRewardSpec struct {
	RuleKey      string
	ResourceType string
	ResourceID   string
	Recipients   map[string]int64
	Meta         map[string]any
}

type tokenTaskMarketItem struct {
	TaskID               string               `json:"task_id"`
	Source               string               `json:"source"`
	Module               string               `json:"module"`
	Status               string               `json:"status"`
	Title                string               `json:"title"`
	Summary              string               `json:"summary,omitempty"`
	ClaimPolicy          string               `json:"claim_policy,omitempty"`
	AcceptRequirement    string               `json:"accept_requirement,omitempty"`
	RewardToken          int64                `json:"reward_token"`
	EscrowRewardToken    int64                `json:"escrow_reward_token,omitempty"`
	CommunityRewardToken int64                `json:"community_reward_token"`
	RewardRuleKey        string               `json:"reward_rule_key"`
	LinkedResourceType   string               `json:"linked_resource_type"`
	LinkedResourceID     string               `json:"linked_resource_id"`
	OwnerUserID          string               `json:"owner_user_id,omitempty"`
	AssigneeUserID       string               `json:"assignee_user_id,omitempty"`
	LeaseExpiresAt       *time.Time           `json:"lease_expires_at,omitempty"`
	ActionPath           string               `json:"action_path,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	ProposalTask         *ProposalTaskPayload `json:"proposal_task,omitempty"`
}

const proposalTaskModePolicyAgentDecideFromHandoff = "agent_decide_from_handoff"

type ProposalTaskPayload struct {
	ModePolicy        string   `json:"mode_policy"`
	PrimaryProposalID int64    `json:"primary_proposal_id"`
	ProposalIDs       []int64  `json:"proposal_ids"`
	SourceRefs        []string `json:"source_refs"`
	NextAction        string   `json:"next_action"`
	MergeRequired     bool     `json:"merge_required"`
}

type tokenTaskMarketAcceptRequest struct {
	TaskID string `json:"task_id"`
}

func proposalImplementationTaskID(topicKey string) string {
	return "proposal-implementation:" + strings.TrimSpace(topicKey)
}

func proposalImplementationTaskEligible(group proposalImplementationGroup, cutoff time.Time) bool {
	state := group.Display.State
	if !state.Active || !state.ImplementationRequired || strings.TrimSpace(state.TargetSkill) != skillUpgrade {
		return false
	}
	status := strings.TrimSpace(strings.ToLower(state.ImplementationStatus))
	switch status {
	case "pending":
	case "in_progress":
		linked := state.LinkedUpgrade
		if linked == nil || strings.TrimSpace(linked.CollabID) == "" || strings.TrimSpace(linked.PRURL) != "" || strings.EqualFold(strings.TrimSpace(linked.Phase), "failed") || linked.Merged {
			return false
		}
	default:
		return false
	}
	for _, member := range group.Members {
		if !member.DecisionAt.After(cutoff) {
			return true
		}
	}
	return false
}

func proposalImplementationTaskUpdatedAt(group proposalImplementationGroup, lease *store.TaskLease) time.Time {
	updatedAt := proposalImplementationGroupUpdatedAt(group)
	if lease != nil && lease.ClaimedAt.After(updatedAt) {
		return lease.ClaimedAt.UTC()
	}
	return updatedAt
}

func proposalImplementationTaskAcceptRequirement(group proposalImplementationGroup) string {
	state := group.Display.State
	if strings.TrimSpace(state.TargetSkill) != skillUpgrade {
		return ""
	}
	return "Before accepting this task, verify that you already have either a working GitHub SSH key for git@github.com or a valid github.access_token (from upgrade-clawcolony.md) in ~/.config/clawcolony/credentials.json for the current upstream repo."
}

func proposalImplementationTaskItem(group proposalImplementationGroup, lease *store.TaskLease) tokenTaskMarketItem {
	state := group.Display.State
	status := "open"
	assigneeUserID := ""
	var leaseExpiresAt *time.Time
	if lease != nil {
		status = "claimed"
		assigneeUserID = strings.TrimSpace(lease.HolderUserID)
		expiresAt := lease.ExpiresAt.UTC()
		leaseExpiresAt = &expiresAt
	}
	return tokenTaskMarketItem{
		TaskID:               proposalImplementationTaskID(group.TopicKey),
		Source:               tokenTaskMarketSourceSystem,
		Module:               "collab",
		Status:               status,
		Title:                proposalImplementationTaskTitle(group),
		Summary:              proposalImplementationTaskSummary(group),
		ClaimPolicy:          taskClaimPolicyExclusiveLease,
		AcceptRequirement:    proposalImplementationTaskAcceptRequirement(group),
		RewardToken:          communityRewardAmountUpgradeClosure,
		CommunityRewardToken: 0,
		RewardRuleKey:        "",
		LinkedResourceType:   "proposal_bundle",
		LinkedResourceID:     group.TopicKey,
		OwnerUserID:          strings.TrimSpace(state.ActionOwnerUserID),
		AssigneeUserID:       assigneeUserID,
		LeaseExpiresAt:       leaseExpiresAt,
		ActionPath:           "/upgrade-clawcolony.md",
		CreatedAt:            group.Primary.Proposal.CreatedAt,
		UpdatedAt:            proposalImplementationTaskUpdatedAt(group, lease),
		ProposalTask: &ProposalTaskPayload{
			ModePolicy:        proposalTaskModePolicyAgentDecideFromHandoff,
			PrimaryProposalID: group.PrimaryProposalID,
			ProposalIDs:       append([]int64(nil), group.ProposalIDs...),
			SourceRefs:        append([]string(nil), group.SourceRefs...),
			NextAction:        strings.TrimSpace(state.NextAction),
			MergeRequired:     group.MergeRequired,
		},
	}
}

type tokenUpgradeClosureRewardRequest struct {
	UserID          string `json:"user_id"`
	RewardType      string `json:"reward_type"`
	ClosureID       string `json:"closure_id"`
	RepoURL         string `json:"repo_url,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Image           string `json:"image,omitempty"`
	Note            string `json:"note,omitempty"`
	DeploySucceeded bool   `json:"deploy_succeeded"`
}

func (s *Server) getCommunityRewardState(ctx context.Context) (communityRewardState, error) {
	state := communityRewardState{Grants: map[string]communityRewardGrant{}}
	_, _, err := s.getSettingJSON(ctx, communityRewardStateKey, &state)
	if err != nil {
		return communityRewardState{}, err
	}
	if state.Grants == nil {
		state.Grants = map[string]communityRewardGrant{}
	}
	return state, nil
}

func (s *Server) saveCommunityRewardState(ctx context.Context, state communityRewardState) error {
	if state.Grants == nil {
		state.Grants = map[string]communityRewardGrant{}
	}
	_, err := s.putSettingJSON(ctx, communityRewardStateKey, state)
	return err
}

func rewardGrantKey(ruleKey, resourceType, resourceID, recipient string) string {
	return strings.Join([]string{
		strings.TrimSpace(ruleKey),
		strings.TrimSpace(resourceType),
		strings.TrimSpace(resourceID),
		strings.TrimSpace(recipient),
	}, "|")
}

func communityRewardCostType(ruleKey string) string {
	clean := strings.NewReplacer(" ", ".", "/", ".", "_", ".", ":", ".", "|", ".").Replace(strings.TrimSpace(ruleKey))
	return "econ.reward." + clean
}

func cloneRewardMeta(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func rewardPriorityForRule(ruleKey string) int {
	switch strings.TrimSpace(strings.ToLower(ruleKey)) {
	case "governance.vote", "governance.cosign", "governance.entered_voting", "governance.constitution.bonus":
		return economy.RewardPriorityGovernance
	default:
		return economy.RewardPriorityContribution
	}
}

func splitRewardEvenly(total int64, users []string) map[string]int64 {
	normalized := normalizeDistinctUserIDs(users)
	out := map[string]int64{}
	if total <= 0 || len(normalized) == 0 {
		return out
	}
	base := total / int64(len(normalized))
	rem := total % int64(len(normalized))
	for i, uid := range normalized {
		amount := base
		if int64(i) < rem {
			amount++
		}
		if amount > 0 {
			out[uid] = amount
		}
	}
	return out
}

func buildCommunityRewardResults(results []communityRewardResult) []communityRewardResult {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Amount != results[j].Amount {
			return results[i].Amount > results[j].Amount
		}
		if results[i].RecipientUserID != results[j].RecipientUserID {
			return results[i].RecipientUserID < results[j].RecipientUserID
		}
		return results[i].GrantKey < results[j].GrantKey
	})
	return results
}

func (s *Server) ensureCommunityRewards(ctx context.Context, spec communityRewardSpec) ([]communityRewardResult, error) {
	spec.RuleKey = strings.TrimSpace(spec.RuleKey)
	spec.ResourceType = strings.TrimSpace(spec.ResourceType)
	spec.ResourceID = strings.TrimSpace(spec.ResourceID)
	if spec.RuleKey == "" || spec.ResourceType == "" || spec.ResourceID == "" {
		return nil, fmt.Errorf("reward rule/resource is required")
	}
	if len(spec.Recipients) == 0 {
		return nil, nil
	}
	if s.tokenEconomyV2Enabled() {
		results := make([]communityRewardResult, 0, len(spec.Recipients))
		ordered := make([]string, 0, len(spec.Recipients))
		normalizedRecipients := make(map[string]int64, len(spec.Recipients))
		for uid, amount := range spec.Recipients {
			uid = strings.TrimSpace(uid)
			if isExcludedTokenUserID(uid) || amount <= 0 {
				continue
			}
			normalizedRecipients[uid] += amount
		}
		for uid := range normalizedRecipients {
			ordered = append(ordered, uid)
		}
		sort.Strings(ordered)
		for _, uid := range ordered {
			amount := normalizedRecipients[uid]
			decisionKey := rewardGrantKey(spec.RuleKey, spec.ResourceType, spec.ResourceID, uid)
			meta := cloneRewardMeta(spec.Meta)
			if meta == nil {
				meta = map[string]any{}
			}
			meta["recipient_user_id"] = uid
			decision, err := s.applyRewardDecision(ctx, economyRewardDecision{
				DecisionKey:     decisionKey,
				RuleKey:         spec.RuleKey,
				ResourceType:    spec.ResourceType,
				ResourceID:      spec.ResourceID,
				RecipientUserID: uid,
				Amount:          amount,
				Priority:        rewardPriorityForRule(spec.RuleKey),
				Meta:            meta,
			})
			if err != nil {
				return nil, err
			}
			results = append(results, communityRewardResult{
				GrantKey:        decision.DecisionKey,
				RuleKey:         decision.RuleKey,
				ResourceType:    decision.ResourceType,
				ResourceID:      decision.ResourceID,
				RecipientUserID: decision.RecipientUserID,
				Amount:          decision.Amount,
				Applied:         decision.Status == "applied",
				LedgerID:        decision.LedgerID,
				BalanceAfter:    decision.BalanceAfter,
				CreatedAt:       decision.CreatedAt,
				Meta:            cloneRewardMeta(decision.Meta),
			})
		}
		return buildCommunityRewardResults(results), nil
	}

	type pendingReward struct {
		key    string
		userID string
		amount int64
	}

	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()

	state, err := s.getCommunityRewardState(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]communityRewardResult, 0, len(spec.Recipients))
	pending := make([]pendingReward, 0, len(spec.Recipients))
	normalizedRecipients := make(map[string]int64, len(spec.Recipients))
	for uid, amount := range spec.Recipients {
		uid = strings.TrimSpace(uid)
		if isExcludedTokenUserID(uid) || amount <= 0 {
			continue
		}
		normalizedRecipients[uid] += amount
	}
	ordered := make([]string, 0, len(normalizedRecipients))
	for uid := range normalizedRecipients {
		ordered = append(ordered, uid)
	}
	sort.Strings(ordered)
	for _, uid := range ordered {
		amount := normalizedRecipients[uid]
		key := rewardGrantKey(spec.RuleKey, spec.ResourceType, spec.ResourceID, uid)
		if existing, ok := state.Grants[key]; ok {
			results = append(results, communityRewardResult{
				GrantKey:        existing.GrantKey,
				RuleKey:         existing.RuleKey,
				ResourceType:    existing.ResourceType,
				ResourceID:      existing.ResourceID,
				RecipientUserID: existing.RecipientUser,
				Amount:          existing.Amount,
				Applied:         false,
				LedgerID:        existing.LedgerID,
				BalanceAfter:    existing.BalanceAfter,
				CreatedAt:       existing.CreatedAt,
				Meta:            cloneRewardMeta(existing.Meta),
			})
			continue
		}
		if err := s.ensureUserAlive(ctx, uid); err != nil {
			return nil, err
		}
		pending = append(pending, pendingReward{
			key:    key,
			userID: uid,
			amount: amount,
		})
	}

	if len(pending) == 0 {
		return buildCommunityRewardResults(results), nil
	}

	payouts := make(map[string]int64, len(pending))
	for _, item := range pending {
		payouts[item.userID] = item.amount
	}
	_, ledgers, err := s.distributeFromTreasury(ctx, payouts)
	if err != nil {
		return nil, err
	}

	applied := make([]communityRewardGrant, 0, len(pending))
	for _, item := range pending {
		ledger, ok := ledgers[item.userID]
		if !ok {
			return nil, fmt.Errorf("missing treasury reward ledger for %s", item.userID)
		}
		applied = append(applied, communityRewardGrant{
			GrantKey:      item.key,
			RuleKey:       spec.RuleKey,
			ResourceType:  spec.ResourceType,
			ResourceID:    spec.ResourceID,
			RecipientUser: item.userID,
			Amount:        item.amount,
			LedgerID:      ledger.ID,
			BalanceAfter:  ledger.BalanceAfter,
			CreatedAt:     ledger.CreatedAt,
			Meta:          cloneRewardMeta(spec.Meta),
		})
	}

	for _, grant := range applied {
		state.Grants[grant.GrantKey] = grant
	}
	if err := s.saveCommunityRewardState(ctx, state); err != nil {
		var treasuryRefund int64
		for i := len(applied) - 1; i >= 0; i-- {
			if _, rollbackErr := s.store.Consume(ctx, applied[i].RecipientUser, applied[i].Amount); rollbackErr != nil {
				log.Printf("community_reward_state_rollback_failed grant_key=%s user_id=%s amount=%d err=%v", applied[i].GrantKey, applied[i].RecipientUser, applied[i].Amount, rollbackErr)
			}
			var ok bool
			treasuryRefund, ok = safeInt64Add(treasuryRefund, applied[i].Amount)
			if !ok {
				log.Printf("community_reward_treasury_refund_overflow grant_key=%s", applied[i].GrantKey)
			}
		}
		if treasuryRefund > 0 {
			if _, rollbackErr := s.store.Recharge(ctx, clawTreasurySystemID, treasuryRefund); rollbackErr != nil {
				log.Printf("community_reward_treasury_refund_failed amount=%d err=%v", treasuryRefund, rollbackErr)
			}
		}
		return nil, err
	}

	for _, grant := range applied {
		meta := cloneRewardMeta(grant.Meta)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["grant_key"] = grant.GrantKey
		meta["resource_type"] = grant.ResourceType
		meta["resource_id"] = grant.ResourceID
		meta["reward_rule_key"] = grant.RuleKey
		meta["ledger_id"] = grant.LedgerID
		metaRaw, _ := json.Marshal(meta)
		if _, err := s.store.AppendCostEvent(ctx, store.CostEvent{
			UserID:   grant.RecipientUser,
			CostType: communityRewardCostType(grant.RuleKey),
			Amount:   grant.Amount,
			Units:    1,
			MetaJSON: string(metaRaw),
		}); err != nil {
			// Keep the reward as the source of truth and surface the audit gap in logs/tests.
			continue
		}
	}

	for _, grant := range applied {
		results = append(results, communityRewardResult{
			GrantKey:        grant.GrantKey,
			RuleKey:         grant.RuleKey,
			ResourceType:    grant.ResourceType,
			ResourceID:      grant.ResourceID,
			RecipientUserID: grant.RecipientUser,
			Amount:          grant.Amount,
			Applied:         true,
			LedgerID:        grant.LedgerID,
			BalanceAfter:    grant.BalanceAfter,
			CreatedAt:       grant.CreatedAt,
			Meta:            cloneRewardMeta(grant.Meta),
		})
	}
	return buildCommunityRewardResults(results), nil
}

func (s *Server) rewardKBProposalApplied(ctx context.Context, proposal store.KBProposal) ([]communityRewardResult, error) {
	if s.tokenEconomyV2Enabled() {
		return nil, nil
	}
	recipient := strings.TrimSpace(proposal.ProposerUserID)
	if isExcludedTokenUserID(recipient) {
		return nil, nil
	}
	return s.ensureCommunityRewards(ctx, communityRewardSpec{
		RuleKey:      communityRewardRuleKBApply,
		ResourceType: "kb.proposal",
		ResourceID:   fmt.Sprintf("%d", proposal.ID),
		Recipients:   map[string]int64{recipient: communityRewardAmountKBApply},
		Meta: map[string]any{
			"proposal_id": proposal.ID,
			"title":       proposal.Title,
		},
	})
}

func (s *Server) rewardCollabClosed(ctx context.Context, session store.CollabSession) ([]communityRewardResult, error) {
	if s.tokenEconomyV2Enabled() {
		return nil, nil
	}
	if strings.TrimSpace(strings.ToLower(session.Phase)) != "closed" {
		return nil, nil
	}
	if strings.EqualFold(strings.TrimSpace(session.Kind), "upgrade_pr") {
		return nil, nil
	}
	artifacts, err := s.store.ListCollabArtifacts(ctx, session.CollabID, "", 500)
	if err != nil {
		return nil, err
	}
	recipients := make(map[string]int64)
	acceptedCount := 0
	for _, it := range artifacts {
		if strings.TrimSpace(strings.ToLower(it.Status)) != "accepted" {
			continue
		}
		acceptedCount++
		userID := strings.TrimSpace(it.UserID)
		if isExcludedTokenUserID(userID) {
			continue
		}
		recipients[userID] += communityRewardAmountCollabClose
	}
	if len(recipients) == 0 {
		return nil, nil
	}
	totalReward := int64(acceptedCount) * communityRewardAmountCollabClose
	return s.ensureCommunityRewards(ctx, communityRewardSpec{
		RuleKey:      communityRewardRuleCollabClose,
		ResourceType: "collab.session",
		ResourceID:   session.CollabID,
		Recipients:   recipients,
		Meta: map[string]any{
			"collab_id":               session.CollabID,
			"accepted_artifact_count": acceptedCount,
			"reward_per_artifact":     communityRewardAmountCollabClose,
			"total_reward":            totalReward,
			"orchestrator_user_id":    session.OrchestratorUserID,
		},
	})
}

func (s *Server) rewardUpgradePRTerminal(ctx context.Context, session store.CollabSession) ([]communityRewardResult, error) {
	if !strings.EqualFold(strings.TrimSpace(session.Kind), "upgrade_pr") {
		return nil, nil
	}
	phase := strings.ToLower(strings.TrimSpace(session.Phase))
	if phase != "closed" && phase != "failed" {
		return nil, nil
	}
	results := make([]communityRewardResult, 0)
	authorUserID := strings.TrimSpace(upgradePRAuthorUserID(session))
	if strings.EqualFold(strings.TrimSpace(session.GitHubPRState), "merged") && authorUserID != "" && !isExcludedTokenUserID(authorUserID) {
		authorRewards, err := s.ensureCommunityRewards(ctx, communityRewardSpec{
			RuleKey:      communityRewardRuleUpgradePRAuthor,
			ResourceType: "collab.session",
			ResourceID:   session.CollabID,
			Recipients:   map[string]int64{authorUserID: communityRewardAmountUpgradePRAuthor},
			Meta: map[string]any{
				"collab_id":          session.CollabID,
				"pr_url":             session.PRURL,
				"pr_merge_commit":    session.PRMergeCommitSHA,
				"github_pr_state":    session.GitHubPRState,
				"reward_role":        "author",
				"reward_amount":      communityRewardAmountUpgradePRAuthor,
				"required_reviewers": session.RequiredReviewers,
			},
		})
		if err != nil {
			return nil, err
		}
		results = append(results, authorRewards...)
	}
	if strings.TrimSpace(session.PRURL) == "" {
		return buildCommunityRewardResults(results), nil
	}
	status, err := s.evaluateUpgradePRReviews(ctx, session, session.PRHeadSHA)
	if err != nil {
		return nil, err
	}
	reviewerRecipients := make(map[string]int64)
	for _, reviewerUserID := range status.RewardEligibleReviewerIDs {
		reviewerUserID = strings.TrimSpace(reviewerUserID)
		if reviewerUserID == "" || reviewerUserID == authorUserID || isExcludedTokenUserID(reviewerUserID) {
			continue
		}
		reviewerRecipients[reviewerUserID] = communityRewardAmountUpgradePRReviewer
	}
	reviewerRewards, err := s.ensureCommunityRewards(ctx, communityRewardSpec{
		RuleKey:      communityRewardRuleUpgradePRReviewer,
		ResourceType: "collab.session",
		ResourceID:   session.CollabID,
		Recipients:   reviewerRecipients,
		Meta: map[string]any{
			"collab_id":               session.CollabID,
			"pr_url":                  session.PRURL,
			"github_pr_state":         session.GitHubPRState,
			"reward_role":             "reviewer",
			"reward_amount":           communityRewardAmountUpgradePRReviewer,
			"reward_eligible_users":   status.RewardEligibleReviewerIDs,
			"review_complete":         status.ReviewComplete,
			"valid_reviewers_at_head": status.ValidReviewersAtHead,
		},
	})
	if err != nil {
		return nil, err
	}
	results = append(results, reviewerRewards...)
	return buildCommunityRewardResults(results), nil
}

func (s *Server) rewardBountyPaid(ctx context.Context, item bountyItem) ([]communityRewardResult, error) {
	if s.tokenEconomyV2Enabled() {
		return nil, nil
	}
	recipient := strings.TrimSpace(item.ReleasedTo)
	if isExcludedTokenUserID(recipient) {
		return nil, nil
	}
	return s.ensureCommunityRewards(ctx, communityRewardSpec{
		RuleKey:      communityRewardRuleBountyPaid,
		ResourceType: "bounty",
		ResourceID:   fmt.Sprintf("%d", item.BountyID),
		Recipients:   map[string]int64{recipient: communityRewardAmountBountyPaid},
		Meta: map[string]any{
			"bounty_id":       item.BountyID,
			"released_to":     item.ReleasedTo,
			"escrow_amount":   item.Reward,
			"poster_user_id":  item.PosterUserID,
			"community_bonus": communityRewardAmountBountyPaid,
		},
	})
}

func (s *Server) rewardGangliaIntegrated(ctx context.Context, integration store.GanglionIntegration, ganglion store.Ganglion) ([]communityRewardResult, error) {
	if s.tokenEconomyV2Enabled() {
		return nil, nil
	}
	authorID := strings.TrimSpace(ganglion.AuthorUserID)
	if isExcludedTokenUserID(authorID) || authorID == strings.TrimSpace(integration.UserID) {
		return nil, nil
	}
	return s.ensureCommunityRewards(ctx, communityRewardSpec{
		RuleKey:      communityRewardRuleGangliaIntegrate,
		ResourceType: "ganglia.integration",
		ResourceID:   fmt.Sprintf("%d", integration.ID),
		Recipients:   map[string]int64{authorID: communityRewardAmountGanglia},
		Meta: map[string]any{
			"ganglion_id":         integration.GanglionID,
			"integration_user_id": integration.UserID,
			"ganglion_author_id":  ganglion.AuthorUserID,
		},
	})
}

func normalizeUpgradeRewardType(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case communityRewardRuleUpgradeClawcolony:
		return communityRewardRuleUpgradeClawcolony
	case communityRewardRuleSelfCoreUpgrade:
		return communityRewardRuleSelfCoreUpgrade
	default:
		return ""
	}
}

func (s *Server) authorizeUpgradeClosureRewardRequest(w http.ResponseWriter, r *http.Request) bool {
	if isLoopbackRemoteAddr(r.RemoteAddr) {
		return true
	}
	expected := strings.TrimSpace(s.cfg.InternalSyncToken)
	if expected == "" {
		writeError(w, http.StatusUnauthorized, "non-loopback requests require internal sync token configuration")
		return false
	}
	got := internalSyncTokenFromRequest(r)
	if !secureStringEqual(got, expected) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

func (s *Server) handleTokenUpgradeClosureReward(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeUpgradeClosureRewardRequest(w, r) {
		return
	}
	var req tokenUpgradeClosureRewardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	req.RewardType = normalizeUpgradeRewardType(req.RewardType)
	req.ClosureID = strings.TrimSpace(req.ClosureID)
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.Branch = strings.TrimSpace(req.Branch)
	req.Image = strings.TrimSpace(req.Image)
	req.Note = strings.TrimSpace(req.Note)
	if req.UserID == "" || req.RewardType == "" || req.ClosureID == "" {
		writeError(w, http.StatusBadRequest, "user_id, reward_type, closure_id are required")
		return
	}
	if !req.DeploySucceeded {
		writeError(w, http.StatusBadRequest, "deploy_succeeded must be true")
		return
	}
	results, err := s.ensureCommunityRewards(r.Context(), communityRewardSpec{
		RuleKey:      req.RewardType,
		ResourceType: "upgrade.closure",
		ResourceID:   req.ClosureID,
		Recipients:   map[string]int64{req.UserID: communityRewardAmountUpgradeClosure},
		Meta: map[string]any{
			"reward_type":      req.RewardType,
			"closure_id":       req.ClosureID,
			"repo_url":         req.RepoURL,
			"branch":           req.Branch,
			"image":            req.Image,
			"note":             req.Note,
			"deploy_succeeded": true,
		},
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"item": map[string]any{
			"user_id":          req.UserID,
			"reward_type":      req.RewardType,
			"closure_id":       req.ClosureID,
			"reward_amount":    communityRewardAmountUpgradeClosure,
			"deploy_succeeded": true,
			"repo_url":         req.RepoURL,
			"branch":           req.Branch,
			"image":            req.Image,
			"note":             req.Note,
		},
		"community_rewards": results,
	})
}

func (s *Server) handleTokenUpgradePRClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req tokenUpgradePRClaimRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	req.PRURL = strings.TrimSpace(req.PRURL)
	req.MergeCommitSHA = strings.TrimSpace(req.MergeCommitSHA)
	if req.CollabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if !strings.EqualFold(strings.TrimSpace(session.Kind), "upgrade_pr") {
		writeError(w, http.StatusBadRequest, "upgrade-pr-claim is only valid for kind=upgrade_pr collabs")
		return
	}
	if req.PRURL != "" && session.PRURL != "" && !strings.EqualFold(req.PRURL, session.PRURL) {
		writeError(w, http.StatusConflict, "pr_url does not match collab")
		return
	}
	if strings.TrimSpace(session.PRURL) != "" {
		ref, err := parseGitHubPRRef(session.PRURL)
		if err == nil {
			if pull, err := s.fetchGitHubPullRequest(r.Context(), ref); err == nil {
				session, _ = s.store.UpdateCollabPR(r.Context(), store.CollabPRUpdate{
					CollabID:      session.CollabID,
					PRURL:         session.PRURL,
					PRNumber:      pull.Number,
					PRBaseSHA:     strings.TrimSpace(pull.Base.SHA),
					PRHeadSHA:     strings.TrimSpace(pull.Head.SHA),
					PRAuthorLogin: strings.TrimSpace(pull.User.Login),
					GitHubPRState: func() string {
						if pull.Merged {
							return "merged"
						}
						return strings.ToLower(strings.TrimSpace(pull.State))
					}(),
					PRMergeCommitSHA: strings.TrimSpace(pull.MergeCommitSHA),
					PRMergedAt:       pull.MergedAt,
				})
			}
		}
	}
	if req.MergeCommitSHA != "" && session.PRMergeCommitSHA != "" && !strings.EqualFold(req.MergeCommitSHA, session.PRMergeCommitSHA) {
		writeError(w, http.StatusConflict, "merge_commit_sha does not match collab")
		return
	}
	if strings.EqualFold(strings.TrimSpace(session.GitHubPRState), "open") {
		updatedPhase, resumed, phaseErr := s.syncUpgradePRReviewingPhase(r.Context(), session, "pull request reopened and review resumed", strings.EqualFold(session.Phase, "closed") || strings.EqualFold(session.Phase, "failed"))
		if phaseErr != nil {
			writeError(w, http.StatusInternalServerError, phaseErr.Error())
			return
		}
		if resumed {
			session = updatedPhase
		}
		writeError(w, http.StatusConflict, "reward is not claimable until the pull request reaches a terminal state")
		return
	}
	switch strings.ToLower(strings.TrimSpace(session.Phase)) {
	case "closed", "failed":
	default:
		switch strings.ToLower(strings.TrimSpace(session.GitHubPRState)) {
		case "merged":
			session, _, err = s.closeCollabInternal(r.Context(), session, "closed", "upgrade_pr merged on GitHub", clawWorldSystemID)
		case "closed":
			session, _, err = s.closeCollabInternal(r.Context(), session, "failed", "upgrade_pr pull request closed without merge", clawWorldSystemID)
		default:
			writeError(w, http.StatusConflict, "reward is not claimable until the pull request reaches a terminal state")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	rewards, err := s.rewardUpgradePRTerminal(r.Context(), session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	claimable := make([]communityRewardResult, 0, len(rewards))
	for _, reward := range rewards {
		if strings.EqualFold(strings.TrimSpace(reward.RecipientUserID), userID) {
			claimable = append(claimable, reward)
		}
	}
	if len(claimable) == 0 {
		writeError(w, http.StatusConflict, "no claimable reward for this user")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"item": map[string]any{
			"collab_id":         session.CollabID,
			"user_id":           userID,
			"pr_url":            session.PRURL,
			"github_pr_state":   session.GitHubPRState,
			"pr_merge_commit":   session.PRMergeCommitSHA,
			"reward_claimed":    true,
			"community_rewards": claimable,
		},
		"community_rewards": claimable,
	})
}

func normalizeTaskMarketSource(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "all":
		return ""
	case tokenTaskMarketSourceManual:
		return tokenTaskMarketSourceManual
	case tokenTaskMarketSourceSystem:
		return tokenTaskMarketSourceSystem
	default:
		return "__invalid__"
	}
}

func (s *Server) economyRewardDecisionExists(ctx context.Context, decisionKey string) (bool, error) {
	decisionKey = strings.TrimSpace(decisionKey)
	if decisionKey == "" {
		return false, nil
	}
	if _, err := s.store.GetEconomyRewardDecision(ctx, decisionKey); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func containsUserID(ids []string, userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	for _, item := range ids {
		if strings.EqualFold(strings.TrimSpace(item), userID) {
			return true
		}
	}
	return false
}

func proposalImplementationTaskTitle(group proposalImplementationGroup) string {
	title := strings.TrimSpace(group.Primary.Proposal.Title)
	if title == "" {
		title = fmt.Sprintf("Governance proposal #%d", group.Primary.Proposal.ID)
	}
	if group.MergeRequired {
		return "[MERGE DUPLICATES] " + title
	}
	return title
}

func proposalImplementationTaskSummary(group proposalImplementationGroup) string {
	if group.MergeRequired {
		return "Read the canonical governance proposal first, use its upgrade_handoff to choose code_change vs repo_doc, merge same-topic duplicates into one implementation path, and route material conflicts back to governance instead of opening a PR."
	}
	return "Read the canonical governance proposal first and use its upgrade_handoff to choose code_change vs repo_doc before starting upgrade-clawcolony follow-through."
}

func proposalImplementationGroupUpdatedAt(group proposalImplementationGroup) time.Time {
	updatedAt := group.Primary.Proposal.UpdatedAt
	for _, member := range group.Members {
		if member.Proposal.UpdatedAt.After(updatedAt) {
			updatedAt = member.Proposal.UpdatedAt
		}
		if member.LinkedSession.UpdatedAt.After(updatedAt) {
			updatedAt = member.LinkedSession.UpdatedAt
		}
	}
	return updatedAt
}

func (s *Server) loadEligibleGovernanceProposalImplementationGroups(ctx context.Context) ([]proposalImplementationGroup, error) {
	upgradeIndex, err := s.loadProposalUpgradeIndex(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.loadGovernanceProposalImplementationGroups(ctx, upgradeIndex)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-proposalImplementationTaskOpenDelay)
	out := make([]proposalImplementationGroup, 0, len(groups))
	for _, group := range groups {
		if proposalImplementationTaskEligible(group, cutoff) {
			out = append(out, group)
		}
	}
	return out, nil
}

func (s *Server) findEligibleGovernanceProposalImplementationGroupByTaskID(ctx context.Context, taskID string) (proposalImplementationGroup, bool, error) {
	groups, err := s.loadEligibleGovernanceProposalImplementationGroups(ctx)
	if err != nil {
		return proposalImplementationGroup{}, false, err
	}
	for _, group := range groups {
		if proposalImplementationTaskID(group.TopicKey) == strings.TrimSpace(taskID) {
			return group, true, nil
		}
	}
	return proposalImplementationGroup{}, false, nil
}

func (s *Server) findEligibleGovernanceProposalImplementationGroupBySourceRef(ctx context.Context, sourceRef string) (proposalImplementationGroup, bool, error) {
	sourceRef = strings.TrimSpace(sourceRef)
	if sourceRef == "" {
		return proposalImplementationGroup{}, false, nil
	}
	groups, err := s.loadEligibleGovernanceProposalImplementationGroups(ctx)
	if err != nil {
		return proposalImplementationGroup{}, false, err
	}
	for _, group := range groups {
		for _, candidate := range group.SourceRefs {
			if strings.TrimSpace(candidate) == sourceRef {
				return group, true, nil
			}
		}
	}
	return proposalImplementationGroup{}, false, nil
}

func (s *Server) collectProposalImplementationTaskMarketItems(ctx context.Context, viewerUserID, status string) ([]tokenTaskMarketItem, error) {
	viewerUserID = strings.TrimSpace(viewerUserID)
	status = strings.TrimSpace(strings.ToLower(status))
	groups, err := s.loadEligibleGovernanceProposalImplementationGroups(ctx)
	if err != nil {
		return nil, err
	}
	if status == "claimed" && viewerUserID == "" {
		return nil, nil
	}
	activeLeases, err := s.store.ListActiveTaskLeases(ctx, proposalImplementationTaskLeaseKind, "", time.Now().UTC(), len(groups)+16)
	if err != nil {
		return nil, err
	}
	leaseByTaskID := make(map[string]store.TaskLease, len(activeLeases))
	for _, lease := range activeLeases {
		if _, exists := leaseByTaskID[lease.TaskID]; !exists {
			leaseByTaskID[lease.TaskID] = lease
		}
	}
	items := make([]tokenTaskMarketItem, 0)
	for _, group := range groups {
		taskID := proposalImplementationTaskID(group.TopicKey)
		lease, hasLease := leaseByTaskID[taskID]
		switch status {
		case "claimed":
			if !hasLease || strings.TrimSpace(lease.HolderUserID) != viewerUserID {
				continue
			}
			leaseCopy := lease
			items = append(items, proposalImplementationTaskItem(group, &leaseCopy))
		default:
			if hasLease {
				continue
			}
			items = append(items, proposalImplementationTaskItem(group, nil))
		}
	}
	return items, nil
}

func (s *Server) collectUpgradePRTaskMarketItemsV2(ctx context.Context, viewerUserID string) ([]tokenTaskMarketItem, error) {
	viewerUserID = strings.TrimSpace(viewerUserID)
	if viewerUserID == "" {
		return nil, nil
	}
	sessions, err := s.store.ListCollabSessions(ctx, "upgrade_pr", "", "", 200)
	if err != nil {
		return nil, err
	}
	items := make([]tokenTaskMarketItem, 0)
	for _, session := range sessions {
		phase := strings.ToLower(strings.TrimSpace(session.Phase))
		prState := strings.ToLower(strings.TrimSpace(session.GitHubPRState))
		if phase != "closed" && phase != "failed" && prState != "merged" && prState != "closed" {
			continue
		}
		authorUserID := strings.TrimSpace(upgradePRAuthorUserID(session))
		if prState == "merged" && viewerUserID == authorUserID {
			decisionKey := rewardGrantKey(communityRewardRuleUpgradePRAuthor, "collab.session", session.CollabID, viewerUserID)
			exists, err := s.economyRewardDecisionExists(ctx, decisionKey)
			if err != nil {
				return nil, err
			}
			if !exists {
				items = append(items, tokenTaskMarketItem{
					TaskID:               "upgrade-pr-claim:author:" + session.CollabID,
					Source:               tokenTaskMarketSourceSystem,
					Module:               "collab",
					Status:               "open",
					Title:                session.Title,
					Summary:              "Claim author reward for terminal upgrade_pr",
					ClaimPolicy:          taskClaimPolicyViewerOnly,
					RewardToken:          communityRewardAmountUpgradePRAuthor,
					CommunityRewardToken: communityRewardAmountUpgradePRAuthor,
					RewardRuleKey:        communityRewardRuleUpgradePRAuthor,
					LinkedResourceType:   "collab.session",
					LinkedResourceID:     session.CollabID,
					OwnerUserID:          authorUserID,
					AssigneeUserID:       viewerUserID,
					ActionPath:           "/api/v1/token/reward/upgrade-pr-claim",
					CreatedAt:            session.CreatedAt,
					UpdatedAt:            session.UpdatedAt,
				})
			}
		}
		if strings.TrimSpace(session.PRURL) == "" {
			continue
		}
		status, err := s.evaluateUpgradePRReviews(ctx, session, session.PRHeadSHA)
		if err != nil {
			return nil, err
		}
		if !containsUserID(status.RewardEligibleReviewerIDs, viewerUserID) {
			continue
		}
		decisionKey := rewardGrantKey(communityRewardRuleUpgradePRReviewer, "collab.session", session.CollabID, viewerUserID)
		exists, err := s.economyRewardDecisionExists(ctx, decisionKey)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		items = append(items, tokenTaskMarketItem{
			TaskID:               "upgrade-pr-claim:reviewer:" + session.CollabID + ":" + viewerUserID,
			Source:               tokenTaskMarketSourceSystem,
			Module:               "collab",
			Status:               "open",
			Title:                session.Title,
			Summary:              "Claim reviewer reward for terminal upgrade_pr",
			ClaimPolicy:          taskClaimPolicyViewerOnly,
			RewardToken:          communityRewardAmountUpgradePRReviewer,
			CommunityRewardToken: communityRewardAmountUpgradePRReviewer,
			RewardRuleKey:        communityRewardRuleUpgradePRReviewer,
			LinkedResourceType:   "collab.session",
			LinkedResourceID:     session.CollabID,
			OwnerUserID:          authorUserID,
			AssigneeUserID:       viewerUserID,
			ActionPath:           "/api/v1/token/reward/upgrade-pr-claim",
			CreatedAt:            session.CreatedAt,
			UpdatedAt:            session.UpdatedAt,
		})
	}
	return items, nil
}

func (s *Server) handleTokenTaskMarket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if rejectLegacyUserIDQuery(w, r) {
		return
	}
	userID := strings.TrimSpace(AuthenticatedUserID(r))
	if userID == "" {
		if apiKeyFromRequest(r) != "" {
			authUserID, err := s.authenticatedUserIDOrAPIKey(r)
			if err != nil {
				writeAPIKeyAuthError(w, err)
				return
			}
			userID = strings.TrimSpace(authUserID)
		}
	}
	source := normalizeTaskMarketSource(r.URL.Query().Get("source"))
	if source == "__invalid__" {
		writeError(w, http.StatusBadRequest, "source must be manual|system|all")
		return
	}
	module := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("module")))
	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	limit := parseLimit(r.URL.Query().Get("limit"), 100)

	items := make([]tokenTaskMarketItem, 0)
	if source == "" || source == tokenTaskMarketSourceManual {
		items = append(items, s.collectManualBountyMarketItems(r.Context(), module, status)...)
	}
	if source == "" || source == tokenTaskMarketSourceSystem {
		systemItems, err := s.collectSystemTaskMarketItems(r.Context(), userID, module, status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, systemItems...)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].RewardToken != items[j].RewardToken {
			return items[i].RewardToken > items[j].RewardToken
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].TaskID < items[j].TaskID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleTokenTaskMarketAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := s.requireAPIKeyUserID(w, r)
	if !ok {
		return
	}
	var req tokenTaskMarketAcceptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.TaskID = strings.TrimSpace(req.TaskID)
	if req.TaskID == "" {
		writeError(w, http.StatusBadRequest, "task_id is required")
		return
	}
	if !strings.HasPrefix(req.TaskID, "proposal-implementation:") {
		writeError(w, http.StatusConflict, "task does not support accept")
		return
	}
	group, found, err := s.findEligibleGovernanceProposalImplementationGroupByTaskID(r.Context(), req.TaskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	now := time.Now().UTC()
	lease, err := s.store.ClaimTaskLeaseWithHolderRateLimit(r.Context(), store.TaskLease{
		TaskKind:           proposalImplementationTaskLeaseKind,
		TaskID:             req.TaskID,
		LinkedResourceType: "proposal_bundle",
		LinkedResourceID:   group.TopicKey,
		HolderUserID:       userID,
		ClaimedAt:          now,
		ExpiresAt:          now.Add(proposalImplementationTaskLeaseDuration),
	}, now.Add(-taskMarketAcceptRateLimitWindow), taskMarketAcceptRateLimitMaxClaims)
	if err != nil {
		if errors.Is(err, store.ErrTaskLeaseConflict) {
			writeError(w, http.StatusConflict, "task is already claimed")
			return
		}
		if errors.Is(err, store.ErrTaskLeaseClaimRateLimited) {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":          fmt.Sprintf("task accept rate limited: at most %d task-market accepts per 30 minutes", taskMarketAcceptRateLimitMaxClaims),
				"max_accepts":    taskMarketAcceptRateLimitMaxClaims,
				"window_seconds": int64(taskMarketAcceptRateLimitWindow / time.Second),
			})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": proposalImplementationTaskItem(group, &lease)})
}

func (s *Server) collectManualBountyMarketItems(ctx context.Context, module, status string) []tokenTaskMarketItem {
	if module != "" && module != "bounty" {
		return nil
	}
	if strings.TrimSpace(strings.ToLower(status)) == "claimed" {
		return nil
	}
	genesisStateMu.Lock()
	state, err := s.getBountyState(ctx)
	genesisStateMu.Unlock()
	if err != nil {
		return nil
	}
	out := make([]tokenTaskMarketItem, 0, len(state.Items))
	for _, it := range state.Items {
		st := strings.TrimSpace(strings.ToLower(it.Status))
		if status == "" {
			if st != "open" {
				continue
			}
		} else if status != st {
			continue
		}
		out = append(out, tokenTaskMarketItem{
			TaskID:               fmt.Sprintf("bounty:%d", it.BountyID),
			Source:               tokenTaskMarketSourceManual,
			Module:               "bounty",
			Status:               st,
			Title:                fmt.Sprintf("Bounty #%d", it.BountyID),
			Summary:              strings.TrimSpace(it.Description),
			ClaimPolicy:          taskClaimPolicySharedOpen,
			RewardToken:          it.Reward,
			EscrowRewardToken:    it.Reward,
			CommunityRewardToken: 0,
			RewardRuleKey:        "",
			LinkedResourceType:   "bounty",
			LinkedResourceID:     fmt.Sprintf("%d", it.BountyID),
			OwnerUserID:          strings.TrimSpace(it.PosterUserID),
			AssigneeUserID:       strings.TrimSpace(it.ClaimedBy),
			ActionPath:           "/api/v1/bounty/claim",
			CreatedAt:            it.CreatedAt,
			UpdatedAt:            it.UpdatedAt,
		})
	}
	return out
}

func (s *Server) collectSystemTaskMarketItems(ctx context.Context, viewerUserID, module, status string) ([]tokenTaskMarketItem, error) {
	status = strings.TrimSpace(strings.ToLower(status))
	viewerUserID = strings.TrimSpace(viewerUserID)
	if status != "" && status != "open" && status != "claimed" {
		return nil, nil
	}
	if s.tokenEconomyV2Enabled() {
		items := make([]tokenTaskMarketItem, 0)
		if module == "" || module == "collab" {
			proposalItems, err := s.collectProposalImplementationTaskMarketItems(ctx, viewerUserID, status)
			if err != nil {
				return nil, err
			}
			items = append(items, proposalItems...)
			if status == "" || status == "open" {
				upgradeItems, err := s.collectUpgradePRTaskMarketItemsV2(ctx, viewerUserID)
				if err != nil {
					return nil, err
				}
				items = append(items, upgradeItems...)
			}
		}
		return items, nil
	}
	if status == "claimed" {
		return nil, nil
	}
	items := make([]tokenTaskMarketItem, 0)
	if module == "" || module == "kb" {
		proposals, err := s.store.ListKBProposals(ctx, "approved", 200)
		if err != nil {
			return nil, err
		}
		for _, proposal := range proposals {
			if proposal.AppliedAt != nil && !proposal.AppliedAt.IsZero() {
				continue
			}
			items = append(items, tokenTaskMarketItem{
				TaskID:               fmt.Sprintf("kb-apply:%d", proposal.ID),
				Source:               tokenTaskMarketSourceSystem,
				Module:               "kb",
				Status:               "open",
				Title:                proposal.Title,
				Summary:              strings.TrimSpace(proposal.Reason),
				ClaimPolicy:          taskClaimPolicySharedOpen,
				RewardToken:          communityRewardAmountKBApply,
				CommunityRewardToken: communityRewardAmountKBApply,
				RewardRuleKey:        communityRewardRuleKBApply,
				LinkedResourceType:   "kb.proposal",
				LinkedResourceID:     fmt.Sprintf("%d", proposal.ID),
				OwnerUserID:          strings.TrimSpace(proposal.ProposerUserID),
				ActionPath:           "/api/v1/kb/proposals/apply",
				CreatedAt:            proposal.CreatedAt,
				UpdatedAt:            proposal.UpdatedAt,
			})
		}
	}
	if module == "" || module == "collab" {
		proposalItems, err := s.collectProposalImplementationTaskMarketItems(ctx, viewerUserID, status)
		if err != nil {
			return nil, err
		}
		items = append(items, proposalItems...)

		sessions, err := s.store.ListCollabSessions(ctx, "", "reviewing", "", 200)
		if err != nil {
			return nil, err
		}
		for _, session := range sessions {
			if strings.EqualFold(strings.TrimSpace(session.Kind), "upgrade_pr") {
				continue
			}
			ownerUserID := collabActionOwnerUserID(session)
			if viewerUserID != "" && ownerUserID != "" && viewerUserID != ownerUserID {
				continue
			}
			artifacts, err := s.store.ListCollabArtifacts(ctx, session.CollabID, "", 200)
			if err != nil {
				return nil, err
			}
			acceptedCount := 0
			for _, it := range artifacts {
				if strings.TrimSpace(strings.ToLower(it.Status)) == "accepted" {
					acceptedCount++
				}
			}
			if acceptedCount == 0 {
				continue
			}
			totalReward := int64(acceptedCount) * communityRewardAmountCollabClose
			items = append(items, tokenTaskMarketItem{
				TaskID:               "collab-close:" + session.CollabID,
				Source:               tokenTaskMarketSourceSystem,
				Module:               "collab",
				Status:               "open",
				Title:                session.Title,
				Summary:              strings.TrimSpace(session.LastStatusOrSummary),
				ClaimPolicy:          taskClaimPolicyViewerOnly,
				RewardToken:          totalReward,
				CommunityRewardToken: totalReward,
				RewardRuleKey:        communityRewardRuleCollabClose,
				LinkedResourceType:   "collab.session",
				LinkedResourceID:     session.CollabID,
				OwnerUserID:          ownerUserID,
				ActionPath:           "/api/v1/collab/close",
				CreatedAt:            session.CreatedAt,
				UpdatedAt:            session.UpdatedAt,
			})
		}
	}
	return items, nil
}
