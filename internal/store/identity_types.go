package store

import (
	"errors"
	"time"
)

var ErrAgentRegistrationNotFound = errors.New("agent registration not found")
var ErrAgentProfileNotFound = errors.New("agent profile not found")
var ErrHumanOwnerNotFound = errors.New("human owner not found")
var ErrHumanOwnerSessionNotFound = errors.New("human owner session not found")
var ErrAgentHumanBindingNotFound = errors.New("agent human binding not found")
var ErrSocialLinkNotFound = errors.New("social link not found")
var ErrGitHubRepoAccessGrantNotFound = errors.New("github repo access grant not found")

type AgentRegistration struct {
	UserID              string     `json:"user_id"`
	RequestedUsername   string     `json:"requested_username"`
	GoodAt              string     `json:"good_at"`
	Status              string     `json:"status"`
	ClaimTokenHash      string     `json:"-"`
	ClaimTokenExpiresAt *time.Time `json:"claim_token_expires_at,omitempty"`
	APIKeyHash          string     `json:"-"`
	PendingOwnerEmail   string     `json:"pending_owner_email,omitempty"`
	PendingHumanName    string     `json:"pending_human_username,omitempty"`
	PendingVisibility   string     `json:"pending_visibility,omitempty"`
	MagicTokenHash      string     `json:"-"`
	MagicTokenExpiresAt *time.Time `json:"magic_token_expires_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	ClaimedAt           *time.Time `json:"claimed_at,omitempty"`
	ActivatedAt         *time.Time `json:"activated_at,omitempty"`
}

type AgentRegistrationInput struct {
	UserID              string
	RequestedUsername   string
	GoodAt              string
	Status              string
	ClaimTokenHash      string
	ClaimTokenExpiresAt *time.Time
	APIKeyHash          string
}

type AgentProfile struct {
	UserID              string    `json:"user_id"`
	Username            string    `json:"username"`
	GoodAt              string    `json:"good_at"`
	HumanUsername       string    `json:"human_username,omitempty"`
	HumanNameVisibility string    `json:"human_name_visibility,omitempty"`
	OwnerEmail          string    `json:"owner_email,omitempty"`
	XHandle             string    `json:"x_handle,omitempty"`
	GitHubUsername      string    `json:"github_username,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type HumanOwner struct {
	OwnerID        string    `json:"owner_id"`
	Email          string    `json:"email"`
	HumanUsername  string    `json:"human_username"`
	XHandle        string    `json:"x_handle,omitempty"`
	XUserID        string    `json:"x_user_id,omitempty"`
	GitHubUsername string    `json:"github_username,omitempty"`
	GitHubUserID   string    `json:"github_user_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type GitHubRepoAccessGrant struct {
	OwnerID                string     `json:"owner_id"`
	GitHubUserID           string     `json:"github_user_id,omitempty"`
	GitHubUsername         string     `json:"github_username,omitempty"`
	Mode                   string     `json:"mode,omitempty"`
	AccessStatus           string     `json:"access_status,omitempty"`
	Org                    string     `json:"org,omitempty"`
	OrgMembershipStatus    string     `json:"org_membership_status,omitempty"`
	TeamSlug               string     `json:"team_slug,omitempty"`
	NextAction             string     `json:"next_action,omitempty"`
	BlockingReason         string     `json:"blocking_reason,omitempty"`
	InstallationID         string     `json:"installation_id,omitempty"`
	RepositoryID           string     `json:"repository_id,omitempty"`
	RepositoryOwner        string     `json:"repository_owner,omitempty"`
	RepositoryName         string     `json:"repository_name,omitempty"`
	Role                   string     `json:"role,omitempty"`
	AccessTokenCiphertext  string     `json:"-"`
	AccessExpiresAt        *time.Time `json:"access_expires_at,omitempty"`
	RefreshTokenCiphertext string     `json:"-"`
	RefreshExpiresAt       *time.Time `json:"refresh_expires_at,omitempty"`
	GrantedAt              time.Time  `json:"granted_at"`
	LastVerifiedAt         *time.Time `json:"last_verified_at,omitempty"`
	RevokedAt              *time.Time `json:"revoked_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type HumanOwnerSession struct {
	SessionID  string     `json:"session_id"`
	OwnerID    string     `json:"owner_id"`
	TokenHash  string     `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type AgentHumanBinding struct {
	UserID              string    `json:"user_id"`
	OwnerID             string    `json:"owner_id"`
	HumanNameVisibility string    `json:"human_name_visibility"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type SocialLink struct {
	UserID       string     `json:"user_id"`
	Provider     string     `json:"provider"`
	Handle       string     `json:"handle"`
	Status       string     `json:"status"`
	Challenge    string     `json:"-"`
	MetadataJSON string     `json:"metadata_json,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	VerifiedAt   *time.Time `json:"verified_at,omitempty"`
}

type SocialRewardGrant struct {
	GrantKey   string    `json:"grant_key"`
	UserID     string    `json:"user_id"`
	Provider   string    `json:"provider"`
	RewardType string    `json:"reward_type"`
	Amount     int64     `json:"amount"`
	MetaJSON   string    `json:"meta_json,omitempty"`
	GrantedAt  time.Time `json:"granted_at"`
}
