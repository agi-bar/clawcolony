package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func socialLinkKey(userID, provider string) string {
	return strings.TrimSpace(userID) + "\x00" + strings.ToLower(strings.TrimSpace(provider))
}

func (s *InMemoryStore) CreateAgentRegistration(_ context.Context, input AgentRegistrationInput) (AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	item := AgentRegistration{
		UserID:              strings.TrimSpace(input.UserID),
		RequestedUsername:   strings.TrimSpace(input.RequestedUsername),
		GoodAt:              strings.TrimSpace(input.GoodAt),
		Status:              strings.TrimSpace(input.Status),
		ClaimTokenHash:      strings.TrimSpace(input.ClaimTokenHash),
		ClaimTokenExpiresAt: input.ClaimTokenExpiresAt,
		APIKeyHash:          strings.TrimSpace(input.APIKeyHash),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if item.UserID == "" {
		return AgentRegistration{}, fmt.Errorf("user_id is required")
	}
	if item.Status == "" {
		item.Status = "pending_claim"
	}
	s.agentRegistrations[item.UserID] = item
	return item, nil
}

func (s *InMemoryStore) GetAgentRegistration(_ context.Context, userID string) (AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.agentRegistrations[strings.TrimSpace(userID)]
	if !ok {
		return AgentRegistration{}, fmt.Errorf("%w: %s", ErrAgentRegistrationNotFound, strings.TrimSpace(userID))
	}
	return item, nil
}

func (s *InMemoryStore) GetAgentRegistrationByClaimTokenHash(_ context.Context, claimTokenHash string) (AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := strings.TrimSpace(claimTokenHash)
	for _, item := range s.agentRegistrations {
		if item.ClaimTokenHash == target {
			return item, nil
		}
	}
	return AgentRegistration{}, fmt.Errorf("%w: claim token", ErrAgentRegistrationNotFound)
}

func (s *InMemoryStore) GetAgentRegistrationByAPIKeyHash(_ context.Context, apiKeyHash string) (AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := strings.TrimSpace(apiKeyHash)
	for _, item := range s.agentRegistrations {
		if item.APIKeyHash == target {
			return item, nil
		}
	}
	return AgentRegistration{}, fmt.Errorf("%w: api key", ErrAgentRegistrationNotFound)
}

func (s *InMemoryStore) ListAgentRegistrations(_ context.Context) ([]AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentRegistration, 0, len(s.agentRegistrations))
	for _, item := range s.agentRegistrations {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (s *InMemoryStore) ListAgentRegistrationsWithoutAPIKey(_ context.Context) ([]AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AgentRegistration, 0)
	for _, item := range s.agentRegistrations {
		if strings.TrimSpace(item.APIKeyHash) == "" {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (s *InMemoryStore) UpdateAgentRegistrationAPIKeyHash(_ context.Context, userID, apiKeyHash string) (AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := strings.TrimSpace(userID)
	item, ok := s.agentRegistrations[uid]
	if !ok {
		return AgentRegistration{}, fmt.Errorf("%w: %s", ErrAgentRegistrationNotFound, uid)
	}
	item.APIKeyHash = strings.TrimSpace(apiKeyHash)
	item.UpdatedAt = time.Now().UTC()
	s.agentRegistrations[uid] = item
	return item, nil
}

func (s *InMemoryStore) GetAgentRegistrationByMagicTokenHash(_ context.Context, magicTokenHash string) (AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := strings.TrimSpace(magicTokenHash)
	for _, item := range s.agentRegistrations {
		if item.MagicTokenHash == target {
			return item, nil
		}
	}
	return AgentRegistration{}, fmt.Errorf("%w: magic token", ErrAgentRegistrationNotFound)
}

func (s *InMemoryStore) UpdateAgentRegistrationClaim(_ context.Context, userID, email, humanUsername, visibility, magicTokenHash string, magicExpiresAt time.Time) (AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := strings.TrimSpace(userID)
	item, ok := s.agentRegistrations[uid]
	if !ok {
		return AgentRegistration{}, fmt.Errorf("%w: %s", ErrAgentRegistrationNotFound, uid)
	}
	item.PendingOwnerEmail = strings.TrimSpace(email)
	item.PendingHumanName = strings.TrimSpace(humanUsername)
	item.PendingVisibility = strings.TrimSpace(visibility)
	item.MagicTokenHash = strings.TrimSpace(magicTokenHash)
	item.MagicTokenExpiresAt = &magicExpiresAt
	item.UpdatedAt = time.Now().UTC()
	s.agentRegistrations[uid] = item
	return item, nil
}

func (s *InMemoryStore) ActivateAgentRegistration(_ context.Context, userID string) (AgentRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := strings.TrimSpace(userID)
	item, ok := s.agentRegistrations[uid]
	if !ok {
		return AgentRegistration{}, fmt.Errorf("%w: %s", ErrAgentRegistrationNotFound, uid)
	}
	now := time.Now().UTC()
	item.Status = "active"
	item.MagicTokenHash = ""
	item.MagicTokenExpiresAt = nil
	item.ClaimedAt = &now
	item.ActivatedAt = &now
	item.UpdatedAt = now
	s.agentRegistrations[uid] = item
	return item, nil
}

func (s *InMemoryStore) UpsertAgentProfile(_ context.Context, item AgentProfile) (AgentProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	uid := strings.TrimSpace(item.UserID)
	if uid == "" {
		return AgentProfile{}, fmt.Errorf("user_id is required")
	}
	current, ok := s.agentProfiles[uid]
	if !ok {
		current = AgentProfile{UserID: uid, CreatedAt: now}
	}
	if v := strings.TrimSpace(item.Username); v != "" {
		current.Username = v
	}
	if v := strings.TrimSpace(item.GoodAt); v != "" {
		current.GoodAt = v
	}
	if v := strings.TrimSpace(item.HumanUsername); v != "" {
		current.HumanUsername = v
	}
	if v := strings.TrimSpace(item.HumanNameVisibility); v != "" {
		current.HumanNameVisibility = v
	}
	if v := strings.TrimSpace(item.OwnerEmail); v != "" {
		current.OwnerEmail = v
	}
	if v := strings.TrimSpace(item.XHandle); v != "" {
		current.XHandle = v
	}
	if v := strings.TrimSpace(item.GitHubUsername); v != "" {
		current.GitHubUsername = v
	}
	current.UpdatedAt = now
	s.agentProfiles[uid] = current
	return current, nil
}

func (s *InMemoryStore) GetAgentProfile(_ context.Context, userID string) (AgentProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.agentProfiles[strings.TrimSpace(userID)]
	if !ok {
		return AgentProfile{}, fmt.Errorf("%w: %s", ErrAgentProfileNotFound, strings.TrimSpace(userID))
	}
	return item, nil
}

func (s *InMemoryStore) FindAgentProfileByUsername(_ context.Context, username string) (AgentProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := strings.ToLower(strings.TrimSpace(username))
	for _, item := range s.agentProfiles {
		if strings.EqualFold(strings.TrimSpace(item.Username), target) {
			return item, nil
		}
	}
	return AgentProfile{}, fmt.Errorf("%w: username", ErrAgentProfileNotFound)
}

func (s *InMemoryStore) UpsertHumanOwner(_ context.Context, email, humanUsername string) (HumanOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	em := strings.ToLower(strings.TrimSpace(email))
	if em == "" {
		return HumanOwner{}, fmt.Errorf("email is required")
	}
	if ownerID, ok := s.humanOwnerByEmail[em]; ok {
		item := s.humanOwners[ownerID]
		item.HumanUsername = strings.TrimSpace(humanUsername)
		item.UpdatedAt = time.Now().UTC()
		s.humanOwners[ownerID] = item
		return item, nil
	}
	now := time.Now().UTC()
	ownerID := fmt.Sprintf("owner-%d", len(s.humanOwners)+1)
	item := HumanOwner{
		OwnerID:       ownerID,
		Email:         em,
		HumanUsername: strings.TrimSpace(humanUsername),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.humanOwners[ownerID] = item
	s.humanOwnerByEmail[em] = ownerID
	return item, nil
}

func (s *InMemoryStore) GetHumanOwner(_ context.Context, ownerID string) (HumanOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.humanOwners[strings.TrimSpace(ownerID)]
	if !ok {
		return HumanOwner{}, fmt.Errorf("%w: %s", ErrHumanOwnerNotFound, strings.TrimSpace(ownerID))
	}
	return item, nil
}

func (s *InMemoryStore) GetHumanOwnerByEmail(_ context.Context, email string) (HumanOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.humanOwnerByEmail[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return HumanOwner{}, fmt.Errorf("%w: %s", ErrHumanOwnerNotFound, strings.TrimSpace(email))
	}
	item, ok := s.humanOwners[id]
	if !ok {
		return HumanOwner{}, fmt.Errorf("%w: %s", ErrHumanOwnerNotFound, id)
	}
	return item, nil
}

func (s *InMemoryStore) UpsertHumanOwnerSocialIdentity(_ context.Context, ownerID, provider, handle, providerUserID string) (HumanOwner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strings.TrimSpace(ownerID)
	item, ok := s.humanOwners[id]
	if !ok {
		return HumanOwner{}, fmt.Errorf("%w: %s", ErrHumanOwnerNotFound, id)
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "x":
		item.XHandle = strings.TrimSpace(handle)
		item.XUserID = strings.TrimSpace(providerUserID)
	case "github":
		item.GitHubUsername = strings.TrimSpace(handle)
		item.GitHubUserID = strings.TrimSpace(providerUserID)
	default:
		return HumanOwner{}, fmt.Errorf("unsupported social provider: %s", provider)
	}
	item.UpdatedAt = time.Now().UTC()
	s.humanOwners[id] = item
	return item, nil
}

func (s *InMemoryStore) CreateHumanOwnerSession(_ context.Context, ownerID, tokenHash string, expiresAt time.Time) (HumanOwnerSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	item := HumanOwnerSession{
		SessionID: fmt.Sprintf("session-%d", len(s.humanOwnerSessions)+1),
		OwnerID:   strings.TrimSpace(ownerID),
		TokenHash: strings.TrimSpace(tokenHash),
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: expiresAt.UTC(),
	}
	s.humanOwnerSessions[item.SessionID] = item
	return item, nil
}

func (s *InMemoryStore) GetHumanOwnerSessionByTokenHash(_ context.Context, tokenHash string) (HumanOwnerSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := strings.TrimSpace(tokenHash)
	for _, item := range s.humanOwnerSessions {
		if item.TokenHash == target {
			return item, nil
		}
	}
	return HumanOwnerSession{}, fmt.Errorf("%w: token", ErrHumanOwnerSessionNotFound)
}

func (s *InMemoryStore) TouchHumanOwnerSession(_ context.Context, sessionID string, seenAt time.Time) (HumanOwnerSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strings.TrimSpace(sessionID)
	item, ok := s.humanOwnerSessions[id]
	if !ok {
		return HumanOwnerSession{}, fmt.Errorf("%w: %s", ErrHumanOwnerSessionNotFound, id)
	}
	item.UpdatedAt = seenAt.UTC()
	item.LastSeenAt = ptrTimeRef(seenAt.UTC())
	s.humanOwnerSessions[id] = item
	return item, nil
}

func (s *InMemoryStore) RevokeHumanOwnerSession(_ context.Context, sessionID string, revokedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strings.TrimSpace(sessionID)
	item, ok := s.humanOwnerSessions[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrHumanOwnerSessionNotFound, id)
	}
	item.RevokedAt = ptrTimeRef(revokedAt.UTC())
	item.UpdatedAt = revokedAt.UTC()
	s.humanOwnerSessions[id] = item
	return nil
}

func (s *InMemoryStore) UpsertGitHubRepoAccessGrant(_ context.Context, item GitHubRepoAccessGrant) (GitHubRepoAccessGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	existing, ok := s.githubRepoAccess[strings.TrimSpace(item.OwnerID)]
	if !ok {
		existing = GitHubRepoAccessGrant{
			OwnerID:   strings.TrimSpace(item.OwnerID),
			CreatedAt: now,
		}
	}
	existing.GitHubUserID = strings.TrimSpace(item.GitHubUserID)
	existing.GitHubUsername = strings.TrimSpace(item.GitHubUsername)
	existing.Mode = strings.TrimSpace(item.Mode)
	existing.AccessStatus = strings.TrimSpace(item.AccessStatus)
	existing.Org = strings.TrimSpace(item.Org)
	existing.OrgMembershipStatus = strings.TrimSpace(item.OrgMembershipStatus)
	existing.TeamSlug = strings.TrimSpace(item.TeamSlug)
	existing.NextAction = strings.TrimSpace(item.NextAction)
	existing.BlockingReason = strings.TrimSpace(item.BlockingReason)
	existing.InstallationID = strings.TrimSpace(item.InstallationID)
	existing.RepositoryID = strings.TrimSpace(item.RepositoryID)
	existing.RepositoryOwner = strings.TrimSpace(item.RepositoryOwner)
	existing.RepositoryName = strings.TrimSpace(item.RepositoryName)
	existing.Role = strings.TrimSpace(item.Role)
	existing.AccessTokenCiphertext = strings.TrimSpace(item.AccessTokenCiphertext)
	existing.AccessExpiresAt = ptrTimeCopy(item.AccessExpiresAt)
	existing.RefreshTokenCiphertext = strings.TrimSpace(item.RefreshTokenCiphertext)
	existing.RefreshExpiresAt = ptrTimeCopy(item.RefreshExpiresAt)
	if item.GrantedAt.IsZero() {
		if existing.GrantedAt.IsZero() {
			existing.GrantedAt = now
		}
	} else {
		existing.GrantedAt = item.GrantedAt.UTC()
	}
	existing.LastVerifiedAt = ptrTimeCopy(item.LastVerifiedAt)
	existing.RevokedAt = ptrTimeCopy(item.RevokedAt)
	existing.UpdatedAt = now
	s.githubRepoAccess[existing.OwnerID] = existing
	return existing, nil
}

func (s *InMemoryStore) GetGitHubRepoAccessGrant(_ context.Context, ownerID string) (GitHubRepoAccessGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.githubRepoAccess[strings.TrimSpace(ownerID)]
	if !ok {
		return GitHubRepoAccessGrant{}, fmt.Errorf("%w: %s", ErrGitHubRepoAccessGrantNotFound, strings.TrimSpace(ownerID))
	}
	return item, nil
}

func (s *InMemoryStore) RevokeGitHubRepoAccessGrant(_ context.Context, ownerID string, revokedAt time.Time) (GitHubRepoAccessGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strings.TrimSpace(ownerID)
	item, ok := s.githubRepoAccess[id]
	if !ok {
		return GitHubRepoAccessGrant{}, fmt.Errorf("%w: %s", ErrGitHubRepoAccessGrantNotFound, id)
	}
	item.RevokedAt = ptrTimeRef(revokedAt.UTC())
	item.UpdatedAt = time.Now().UTC()
	s.githubRepoAccess[id] = item
	return item, nil
}

func (s *InMemoryStore) UpsertAgentHumanBinding(_ context.Context, item AgentHumanBinding) (AgentHumanBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	uid := strings.TrimSpace(item.UserID)
	if uid == "" {
		return AgentHumanBinding{}, fmt.Errorf("user_id is required")
	}
	current, ok := s.agentBindings[uid]
	if !ok {
		current = AgentHumanBinding{UserID: uid, CreatedAt: now}
	}
	current.OwnerID = strings.TrimSpace(item.OwnerID)
	current.HumanNameVisibility = strings.TrimSpace(item.HumanNameVisibility)
	current.UpdatedAt = now
	s.agentBindings[uid] = current
	return current, nil
}

func (s *InMemoryStore) GetAgentHumanBinding(_ context.Context, userID string) (AgentHumanBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.agentBindings[strings.TrimSpace(userID)]
	if !ok {
		return AgentHumanBinding{}, fmt.Errorf("%w: %s", ErrAgentHumanBindingNotFound, strings.TrimSpace(userID))
	}
	return item, nil
}

func (s *InMemoryStore) ListAgentHumanBindingsByOwner(_ context.Context, ownerID string) ([]AgentHumanBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := strings.TrimSpace(ownerID)
	out := make([]AgentHumanBinding, 0)
	for _, item := range s.agentBindings {
		if item.OwnerID == target {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

func (s *InMemoryStore) UpsertSocialLink(_ context.Context, item SocialLink) (SocialLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	key := socialLinkKey(item.UserID, item.Provider)
	if key == "\x00" {
		return SocialLink{}, fmt.Errorf("user_id and provider are required")
	}
	current, ok := s.socialLinks[key]
	if !ok {
		current = SocialLink{
			UserID:    strings.TrimSpace(item.UserID),
			Provider:  strings.ToLower(strings.TrimSpace(item.Provider)),
			CreatedAt: now,
		}
	}
	if v := strings.TrimSpace(item.Handle); v != "" {
		current.Handle = v
	}
	if v := strings.TrimSpace(item.Status); v != "" {
		current.Status = v
	}
	current.Challenge = strings.TrimSpace(item.Challenge)
	current.MetadataJSON = strings.TrimSpace(item.MetadataJSON)
	current.VerifiedAt = item.VerifiedAt
	current.UpdatedAt = now
	s.socialLinks[key] = current
	return current, nil
}

func (s *InMemoryStore) GetSocialLink(_ context.Context, userID, provider string) (SocialLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.socialLinks[socialLinkKey(userID, provider)]
	if !ok {
		return SocialLink{}, fmt.Errorf("%w: %s/%s", ErrSocialLinkNotFound, strings.TrimSpace(userID), strings.TrimSpace(provider))
	}
	return item, nil
}

func (s *InMemoryStore) GrantSocialReward(_ context.Context, item SocialRewardGrant) (SocialRewardGrant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(item.GrantKey)
	if key == "" {
		return SocialRewardGrant{}, false, fmt.Errorf("grant_key is required")
	}
	if current, ok := s.socialRewardGrants[key]; ok {
		return current, false, nil
	}
	item.GrantKey = key
	item.UserID = strings.TrimSpace(item.UserID)
	item.Provider = strings.ToLower(strings.TrimSpace(item.Provider))
	item.RewardType = strings.TrimSpace(item.RewardType)
	item.GrantedAt = time.Now().UTC()
	s.socialRewardGrants[key] = item
	return item, true, nil
}

func (s *InMemoryStore) ListSocialRewardGrants(_ context.Context, userID string) ([]SocialRewardGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := strings.TrimSpace(userID)
	out := make([]SocialRewardGrant, 0)
	for _, item := range s.socialRewardGrants {
		if item.UserID == target {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GrantedAt.Equal(out[j].GrantedAt) {
			return out[i].GrantKey < out[j].GrantKey
		}
		return out[i].GrantedAt.Before(out[j].GrantedAt)
	})
	return out, nil
}

func ptrTimeRef(t time.Time) *time.Time {
	v := t
	return &v
}

func ptrTimeCopy(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}
