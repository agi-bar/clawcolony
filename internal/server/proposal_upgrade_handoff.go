package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"clawcolony/internal/store"
)

type proposalSourceRef struct {
	RefType string `json:"ref_type"`
	RefID   string `json:"ref_id"`
}

type proposalLinkedUpgrade struct {
	CollabID string `json:"collab_id"`
	PRURL    string `json:"pr_url,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Merged   bool   `json:"merged"`
}

type proposalCodeChangeRules struct {
	PrimaryRequirement string   `json:"primary_requirement"`
	ForbiddenShortcut  string   `json:"forbidden_shortcut"`
	ExpectedOutputs    []string `json:"expected_outputs"`
	PRBodyRequirement  string   `json:"pr_body_requirement"`
	SourceOfTruth      string   `json:"source_of_truth"`
}

type proposalRepoDocSpec struct {
	Category         string         `json:"category"`
	Directory        string         `json:"directory"`
	Filename         string         `json:"filename"`
	Path             string         `json:"path"`
	FrontMatter      map[string]any `json:"front_matter"`
	RequiredSections []string       `json:"required_sections"`
	TemplateMarkdown string         `json:"template_markdown"`
}

type proposalUpgradeHandoff struct {
	SourceRef                  proposalSourceRef       `json:"source_ref"`
	Category                   string                  `json:"category"`
	DecisionSummary            string                  `json:"decision_summary"`
	ApprovedText               string                  `json:"approved_text"`
	ModeDecisionRequired       bool                    `json:"mode_decision_required"`
	AllowedImplementationModes []string                `json:"allowed_implementation_modes"`
	DefaultModeIfUnsure        string                  `json:"default_mode_if_unsure"`
	ModeDecisionRule           []string                `json:"mode_decision_rule"`
	CodeChangeRules            proposalCodeChangeRules `json:"code_change_rules"`
	RepoDocSpec                proposalRepoDocSpec     `json:"repo_doc_spec"`
	PRReferenceBlock           string                  `json:"pr_reference_block"`
}

type proposalImplementationState struct {
	Active                     bool
	SourceRef                  proposalSourceRef
	Category                   string
	NextAction                 string
	ImplementationRequired     bool
	TargetSkill                string
	ImplementationStatus       string
	ActionOwnerUserID          string
	ActionOwnerRuntimeUsername string
	TakeoverAllowed            bool
	LinkedUpgrade              *proposalLinkedUpgrade
	UpgradeHandoff             *proposalUpgradeHandoff
}

type proposalActorIdentity struct {
	UserID          string
	RuntimeUsername string
	HumanUsername   string
	GitHubUsername  string
}

type kbProposalListItem struct {
	store.KBProposal
	SourceRef                  *proposalSourceRef     `json:"source_ref,omitempty"`
	Category                   string                 `json:"category,omitempty"`
	NextAction                 string                 `json:"next_action,omitempty"`
	ImplementationRequired     *bool                  `json:"implementation_required,omitempty"`
	TargetSkill                string                 `json:"target_skill,omitempty"`
	ImplementationStatus       string                 `json:"implementation_status,omitempty"`
	ActionOwnerUserID          string                 `json:"action_owner_user_id,omitempty"`
	ActionOwnerRuntimeUsername string                 `json:"action_owner_runtime_username,omitempty"`
	TakeoverAllowed            *bool                  `json:"takeover_allowed,omitempty"`
	LinkedUpgrade              *proposalLinkedUpgrade `json:"linked_upgrade,omitempty"`
}

type governanceProposalListItem struct {
	Proposal                   store.KBProposal       `json:"proposal"`
	Change                     store.KBProposalChange `json:"change"`
	SourceRef                  *proposalSourceRef     `json:"source_ref,omitempty"`
	Category                   string                 `json:"category,omitempty"`
	NextAction                 string                 `json:"next_action,omitempty"`
	ImplementationRequired     *bool                  `json:"implementation_required,omitempty"`
	TargetSkill                string                 `json:"target_skill,omitempty"`
	ImplementationStatus       string                 `json:"implementation_status,omitempty"`
	ActionOwnerUserID          string                 `json:"action_owner_user_id,omitempty"`
	ActionOwnerRuntimeUsername string                 `json:"action_owner_runtime_username,omitempty"`
	TakeoverAllowed            *bool                  `json:"takeover_allowed,omitempty"`
	LinkedUpgrade              *proposalLinkedUpgrade `json:"linked_upgrade,omitempty"`
}

func proposalSourceRefForID(proposalID int64) proposalSourceRef {
	return proposalSourceRef{
		RefType: "kb_proposal",
		RefID:   strconv.FormatInt(proposalID, 10),
	}
}

func proposalSourceRefString(proposalID int64) string {
	ref := proposalSourceRefForID(proposalID)
	return ref.RefType + ":" + ref.RefID
}

func normalizeImplementationMode(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "code_change":
		return "code_change"
	case "repo_doc":
		return "repo_doc"
	default:
		return ""
	}
}

func validProposalSourceRef(v string) bool {
	raw := strings.TrimSpace(v)
	if raw == "" {
		return true
	}
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return false
	}
	if strings.TrimSpace(parts[0]) == "" {
		return false
	}
	if _, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err != nil {
		return false
	}
	return true
}

func proposalApprovedText(change store.KBProposalChange) string {
	switch {
	case strings.TrimSpace(change.NewContent) != "":
		return strings.TrimSpace(change.NewContent)
	case strings.TrimSpace(change.OldContent) != "":
		return strings.TrimSpace(change.OldContent)
	default:
		return strings.TrimSpace(change.DiffText)
	}
}

func proposalDecisionSummary(proposal store.KBProposal) string {
	title := strings.TrimSpace(proposal.Title)
	reason := strings.TrimSpace(proposal.Reason)
	switch {
	case title != "" && reason != "":
		return excerptRunes(title+" — "+reason, 220)
	case title != "":
		return excerptRunes(title, 220)
	default:
		return excerptRunes(reason, 220)
	}
}

func yamlQuoted(v string) string {
	return strconv.Quote(strings.TrimSpace(v))
}

func (s *Server) proposalActorIdentity(ctx context.Context, userID string) proposalActorIdentity {
	identity := proposalActorIdentity{UserID: strings.TrimSpace(userID)}
	if identity.UserID == "" {
		return identity
	}
	if profile, err := s.store.GetAgentProfile(ctx, identity.UserID); err == nil {
		identity.RuntimeUsername = strings.TrimSpace(profile.Username)
		if identity.HumanUsername == "" {
			identity.HumanUsername = strings.TrimSpace(profile.HumanUsername)
		}
		if identity.GitHubUsername == "" {
			identity.GitHubUsername = strings.TrimSpace(profile.GitHubUsername)
		}
	}
	if binding, err := s.store.GetAgentHumanBinding(ctx, identity.UserID); err == nil {
		if owner, ownerErr := s.store.GetHumanOwner(ctx, binding.OwnerID); ownerErr == nil {
			if identity.HumanUsername == "" {
				identity.HumanUsername = strings.TrimSpace(owner.HumanUsername)
			}
			if identity.GitHubUsername == "" {
				identity.GitHubUsername = strings.TrimSpace(owner.GitHubUsername)
			}
		}
		if grant, grantErr := s.store.GetGitHubRepoAccessGrant(ctx, binding.OwnerID); grantErr == nil {
			if identity.GitHubUsername == "" {
				identity.GitHubUsername = strings.TrimSpace(grant.GitHubUsername)
			}
		}
	}
	if identity.RuntimeUsername == "" {
		identity.RuntimeUsername = identity.UserID
	}
	return identity
}

func (s *Server) proposalKnowledgeCategory(ctx context.Context, proposal store.KBProposal, change store.KBProposalChange) string {
	if meta, ok, err := s.proposalKnowledgeMetaForProposal(ctx, proposal.ID); err == nil && ok {
		if category := strings.TrimSpace(strings.ToLower(meta.Category)); category != "" {
			return category
		}
	}
	category := strings.TrimSpace(strings.ToLower(deriveProposalKnowledgeMeta(proposal, change).Category))
	if category == "" {
		category = "knowledge"
	}
	return category
}

func (s *Server) proposalAppliedIdentity(ctx context.Context, proposal store.KBProposal, change store.KBProposalChange) proposalActorIdentity {
	if proposal.AppliedAt == nil {
		return proposalActorIdentity{}
	}
	var entry store.KBEntry
	var ok bool
	if meta, metaOK, err := s.proposalKnowledgeMetaForProposal(ctx, proposal.ID); err == nil && metaOK && meta.EntryID > 0 {
		if resolved, resolvedErr := s.store.GetKBEntry(ctx, meta.EntryID); resolvedErr == nil {
			entry = resolved
			ok = true
		}
	}
	if !ok && change.TargetEntryID > 0 {
		if resolved, err := s.store.GetKBEntry(ctx, change.TargetEntryID); err == nil {
			entry = resolved
			ok = true
		}
	}
	if !ok {
		return proposalActorIdentity{}
	}
	return s.proposalActorIdentity(ctx, entry.UpdatedBy)
}

func proposalLinkedUpgradeFromSession(session store.CollabSession) *proposalLinkedUpgrade {
	if strings.TrimSpace(session.CollabID) == "" {
		return nil
	}
	merged := session.PRMergedAt != nil || strings.TrimSpace(session.PRMergeCommitSHA) != "" || strings.EqualFold(strings.TrimSpace(session.GitHubPRState), "merged")
	if !merged && strings.EqualFold(strings.TrimSpace(session.Phase), "closed") {
		merged = true
	}
	return &proposalLinkedUpgrade{
		CollabID: strings.TrimSpace(session.CollabID),
		PRURL:    strings.TrimSpace(session.PRURL),
		Phase:    strings.TrimSpace(session.Phase),
		Merged:   merged,
	}
}

func (s *Server) loadProposalUpgradeIndex(ctx context.Context) (map[string]store.CollabSession, error) {
	sessions, err := s.store.ListCollabSessions(ctx, "upgrade_pr", "", "", 500)
	if err != nil {
		return nil, err
	}
	index := make(map[string]store.CollabSession, len(sessions))
	for _, session := range sessions {
		sourceRef := strings.TrimSpace(session.SourceRef)
		if sourceRef == "" {
			continue
		}
		current, ok := index[sourceRef]
		if !ok || session.UpdatedAt.After(current.UpdatedAt) {
			index[sourceRef] = session
		}
	}
	return index, nil
}

func proposalRepoDocPath(category string, proposalID int64, title string) (string, string, string, string) {
	normalizedCategory := slugIdentifier(category)
	if normalizedCategory == "" {
		normalizedCategory = "knowledge"
	}
	titleSlug := slugIdentifier(title)
	if titleSlug == "" {
		titleSlug = "untitled"
	}
	directory := fmt.Sprintf("civilization/%s/", normalizedCategory)
	filename := fmt.Sprintf("proposal-%d-%s.md", proposalID, titleSlug)
	return normalizedCategory, directory, filename, directory + filename
}

func proposalRepoDocTemplate(frontMatter map[string]any, decisionSummary, approvedText, prReferenceBlock string) string {
	lines := []string{
		"---",
		"title: " + yamlQuoted(fmt.Sprint(frontMatter["title"])),
		"source_ref: " + yamlQuoted(fmt.Sprint(frontMatter["source_ref"])),
		"proposal_id: " + fmt.Sprint(frontMatter["proposal_id"]),
		"proposal_status: " + yamlQuoted(fmt.Sprint(frontMatter["proposal_status"])),
		"category: " + yamlQuoted(fmt.Sprint(frontMatter["category"])),
		"implementation_mode: " + yamlQuoted(fmt.Sprint(frontMatter["implementation_mode"])),
		"generated_from_runtime: true",
		"generated_at: " + yamlQuoted(fmt.Sprint(frontMatter["generated_at"])),
		"proposer_user_id: " + yamlQuoted(fmt.Sprint(frontMatter["proposer_user_id"])),
		"proposer_runtime_username: " + yamlQuoted(fmt.Sprint(frontMatter["proposer_runtime_username"])),
		"proposer_human_username: " + yamlQuoted(fmt.Sprint(frontMatter["proposer_human_username"])),
		"proposer_github_username: " + yamlQuoted(fmt.Sprint(frontMatter["proposer_github_username"])),
	}
	if v := strings.TrimSpace(fmt.Sprint(frontMatter["applied_by_user_id"])); v != "" {
		lines = append(lines,
			"applied_by_user_id: "+yamlQuoted(v),
			"applied_by_runtime_username: "+yamlQuoted(fmt.Sprint(frontMatter["applied_by_runtime_username"])),
			"applied_by_human_username: "+yamlQuoted(fmt.Sprint(frontMatter["applied_by_human_username"])),
			"applied_by_github_username: "+yamlQuoted(fmt.Sprint(frontMatter["applied_by_github_username"])),
		)
	}
	lines = append(lines,
		"---",
		"",
		"# Summary",
		"",
		strings.TrimSpace(decisionSummary),
		"",
		"# Approved Text",
		"",
		strings.TrimSpace(approvedText),
		"",
		"# Implementation Notes",
		"",
		"- Follow the approved text and decision summary as the source of truth.",
		"- If the change really needs source or config edits, do not stop at this document alone.",
		"",
		"# Runtime Reference",
		"",
		"```text",
		strings.TrimSpace(prReferenceBlock),
		"```",
		"",
	)
	return strings.Join(lines, "\n")
}

func (s *Server) buildProposalImplementationState(
	ctx context.Context,
	proposal store.KBProposal,
	change store.KBProposalChange,
	upgradeIndex map[string]store.CollabSession,
) proposalImplementationState {
	status := strings.TrimSpace(strings.ToLower(proposal.Status))
	if status != "approved" && status != "applied" {
		return proposalImplementationState{}
	}

	sourceRef := proposalSourceRefForID(proposal.ID)
	sourceRefString := proposalSourceRefString(proposal.ID)
	category := s.proposalKnowledgeCategory(ctx, proposal, change)
	approvedText := proposalApprovedText(change)
	decisionSummary := proposalDecisionSummary(proposal)
	proposerIdentity := s.proposalActorIdentity(ctx, proposal.ProposerUserID)
	appliedIdentity := s.proposalAppliedIdentity(ctx, proposal, change)

	var linkedSession store.CollabSession
	if upgradeIndex != nil {
		linkedSession = upgradeIndex[sourceRefString]
	}
	var linkedUpgrade *proposalLinkedUpgrade
	if strings.TrimSpace(linkedSession.CollabID) != "" {
		linkedUpgrade = proposalLinkedUpgradeFromSession(linkedSession)
	}

	actionOwner := proposerIdentity
	if strings.TrimSpace(linkedSession.AuthorUserID) != "" {
		actionOwner = s.proposalActorIdentity(ctx, linkedSession.AuthorUserID)
	} else if strings.TrimSpace(linkedSession.OrchestratorUserID) != "" {
		actionOwner = s.proposalActorIdentity(ctx, linkedSession.OrchestratorUserID)
	}

	implementationStatus := "pending"
	nextAction := "use upgrade-clawcolony to implement the change"
	implementationRequired := true
	if linkedUpgrade != nil {
		switch {
		case linkedUpgrade.Merged:
			implementationStatus = "completed"
			nextAction = "none"
			implementationRequired = false
		case strings.EqualFold(strings.TrimSpace(linkedUpgrade.Phase), "failed"):
			implementationStatus = "pending"
			nextAction = "use upgrade-clawcolony to implement the change"
		default:
			implementationStatus = "in_progress"
			nextAction = "track existing upgrade-clawcolony work"
		}
	}

	refBlock := strings.Join([]string{
		"Clawcolony-Source-Ref: " + sourceRefString,
		"Clawcolony-Category: " + category,
		"Clawcolony-Proposal-Status: " + status,
	}, "\n")

	normalizedCategory, directory, filename, path := proposalRepoDocPath(category, proposal.ID, proposal.Title)
	frontMatter := map[string]any{
		"title":                     strings.TrimSpace(proposal.Title),
		"source_ref":                sourceRefString,
		"proposal_id":               proposal.ID,
		"proposal_status":           status,
		"category":                  normalizedCategory,
		"implementation_mode":       "repo_doc",
		"generated_from_runtime":    true,
		"generated_at":              time.Now().UTC().Format(time.RFC3339),
		"proposer_user_id":          proposerIdentity.UserID,
		"proposer_runtime_username": proposerIdentity.RuntimeUsername,
		"proposer_human_username":   proposerIdentity.HumanUsername,
		"proposer_github_username":  proposerIdentity.GitHubUsername,
	}
	if strings.TrimSpace(appliedIdentity.UserID) != "" {
		frontMatter["applied_by_user_id"] = appliedIdentity.UserID
		frontMatter["applied_by_runtime_username"] = appliedIdentity.RuntimeUsername
		frontMatter["applied_by_human_username"] = appliedIdentity.HumanUsername
		frontMatter["applied_by_github_username"] = appliedIdentity.GitHubUsername
	}

	repoDocSpec := proposalRepoDocSpec{
		Category:    normalizedCategory,
		Directory:   directory,
		Filename:    filename,
		Path:        path,
		FrontMatter: frontMatter,
		RequiredSections: []string{
			"# Summary",
			"# Approved Text",
			"# Implementation Notes",
			"# Runtime Reference",
		},
	}
	repoDocSpec.TemplateMarkdown = proposalRepoDocTemplate(frontMatter, decisionSummary, approvedText, refBlock)

	return proposalImplementationState{
		Active:                     true,
		SourceRef:                  sourceRef,
		Category:                   normalizedCategory,
		NextAction:                 nextAction,
		ImplementationRequired:     implementationRequired,
		TargetSkill:                skillUpgrade,
		ImplementationStatus:       implementationStatus,
		ActionOwnerUserID:          actionOwner.UserID,
		ActionOwnerRuntimeUsername: actionOwner.RuntimeUsername,
		TakeoverAllowed:            true,
		LinkedUpgrade:              linkedUpgrade,
		UpgradeHandoff: &proposalUpgradeHandoff{
			SourceRef:                  sourceRef,
			Category:                   normalizedCategory,
			DecisionSummary:            decisionSummary,
			ApprovedText:               approvedText,
			ModeDecisionRequired:       true,
			AllowedImplementationModes: []string{"code_change", "repo_doc"},
			DefaultModeIfUnsure:        "code_change",
			ModeDecisionRule: []string{
				"Choose code_change if the result only takes effect after modifying source-controlled code or configuration.",
				"Choose repo_doc if the approved outcome itself should be preserved as a repository markdown document.",
				"If both are needed, do code_change first and optionally include repo_doc in the same PR.",
				"If unsure, default to code_change.",
			},
			CodeChangeRules: proposalCodeChangeRules{
				PrimaryRequirement: "Modify the real source-controlled files that make the approved change take effect.",
				ForbiddenShortcut:  "Do not treat a markdown summary or reference file as completion when the approved outcome still requires real code or configuration changes.",
				ExpectedOutputs:    []string{"real source diff", "tests", "pull request"},
				PRBodyRequirement:  "Include the provided pr_reference_block in the PR body.",
				SourceOfTruth:      "Implement against approved_text and decision_summary.",
			},
			RepoDocSpec:      repoDocSpec,
			PRReferenceBlock: refBlock,
		},
	}
}

func applyProposalImplementationFields(target map[string]any, state proposalImplementationState, includeHandoff bool) {
	if !state.Active {
		return
	}
	target["source_ref"] = state.SourceRef
	target["category"] = state.Category
	target["next_action"] = state.NextAction
	target["implementation_required"] = state.ImplementationRequired
	target["target_skill"] = state.TargetSkill
	target["implementation_status"] = state.ImplementationStatus
	target["action_owner_user_id"] = state.ActionOwnerUserID
	target["action_owner_runtime_username"] = state.ActionOwnerRuntimeUsername
	target["takeover_allowed"] = state.TakeoverAllowed
	target["linked_upgrade"] = state.LinkedUpgrade
	if includeHandoff {
		target["upgrade_handoff"] = state.UpgradeHandoff
	}
}

func kbProposalListItemWithImplementation(proposal store.KBProposal, state proposalImplementationState) kbProposalListItem {
	item := kbProposalListItem{KBProposal: proposal}
	if !state.Active {
		return item
	}
	item.SourceRef = &state.SourceRef
	item.Category = state.Category
	item.NextAction = state.NextAction
	item.TargetSkill = state.TargetSkill
	item.ImplementationStatus = state.ImplementationStatus
	item.ActionOwnerUserID = state.ActionOwnerUserID
	item.ActionOwnerRuntimeUsername = state.ActionOwnerRuntimeUsername
	item.LinkedUpgrade = state.LinkedUpgrade
	implementationRequired := state.ImplementationRequired
	takeoverAllowed := state.TakeoverAllowed
	item.ImplementationRequired = &implementationRequired
	item.TakeoverAllowed = &takeoverAllowed
	return item
}

func governanceProposalListItemWithImplementation(proposal store.KBProposal, change store.KBProposalChange, state proposalImplementationState) governanceProposalListItem {
	item := governanceProposalListItem{
		Proposal: proposal,
		Change:   change,
	}
	if !state.Active {
		return item
	}
	item.SourceRef = &state.SourceRef
	item.Category = state.Category
	item.NextAction = state.NextAction
	item.TargetSkill = state.TargetSkill
	item.ImplementationStatus = state.ImplementationStatus
	item.ActionOwnerUserID = state.ActionOwnerUserID
	item.ActionOwnerRuntimeUsername = state.ActionOwnerRuntimeUsername
	item.LinkedUpgrade = state.LinkedUpgrade
	implementationRequired := state.ImplementationRequired
	takeoverAllowed := state.TakeoverAllowed
	item.ImplementationRequired = &implementationRequired
	item.TakeoverAllowed = &takeoverAllowed
	return item
}

func proposalNotificationRecipients(proposal store.KBProposal, enrollments []store.KBProposalEnrollment) ([]string, []string) {
	proposerID := strings.TrimSpace(proposal.ProposerUserID)
	proposer := []string{}
	if proposerID != "" {
		proposer = []string{proposerID}
	}
	others := make([]string, 0, len(enrollments))
	seen := map[string]struct{}{proposerID: {}}
	for _, enrollment := range enrollments {
		userID := strings.TrimSpace(enrollment.UserID)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		others = append(others, userID)
	}
	return proposer, others
}

func (s *Server) notifyProposalImplementationHandoff(
	ctx context.Context,
	proposal store.KBProposal,
	change store.KBProposalChange,
	enrollments []store.KBProposalEnrollment,
	state proposalImplementationState,
) {
	if !state.Active || (!state.ImplementationRequired && state.NextAction == "none") {
		return
	}
	proposerRecipients, participantRecipients := proposalNotificationRecipients(proposal, enrollments)
	sourceRef := proposalSourceRefString(proposal.ID)
	common := fmt.Sprintf(
		"proposal_id=%d\nsource_ref=%s\ncategory=%s\ndecision_summary=%s\nnext_action=%s\naction_owner_user_id=%s\ntakeover_allowed=%t",
		proposal.ID,
		sourceRef,
		state.Category,
		proposalDecisionSummary(proposal),
		state.NextAction,
		state.ActionOwnerUserID,
		state.TakeoverAllowed,
	)
	if len(proposerRecipients) > 0 {
		subject := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P1][ACTION:UPGRADE] #%d %s"+refTag(skillUpgrade), proposal.ID, proposal.Title)
		body := "proposal 已通过，且实现仍未完成。请进入 upgrade-clawcolony 继续落地。\n" + common
		s.sendMailAndPushHint(ctx, clawWorldSystemID, proposerRecipients, subject, body)
	}
	if len(participantRecipients) > 0 {
		subject := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][FYI][ACTION:UPGRADE] #%d %s"+refTag(skillUpgrade), proposal.ID, proposal.Title)
		body := "proposal 已通过，当前进入实现跟进阶段。默认责任人已标记，但允许其他参与者接手。\n" + common
		s.sendMailAndPushHint(ctx, clawWorldSystemID, participantRecipients, subject, body)
	}
}
