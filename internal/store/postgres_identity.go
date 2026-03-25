package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func scanAgentRegistration(scanner interface {
	Scan(dest ...any) error
}) (AgentRegistration, error) {
	var item AgentRegistration
	err := scanner.Scan(
		&item.UserID,
		&item.RequestedUsername,
		&item.GoodAt,
		&item.Status,
		&item.ClaimTokenHash,
		&item.ClaimTokenExpiresAt,
		&item.APIKeyHash,
		&item.PendingOwnerEmail,
		&item.PendingHumanName,
		&item.PendingVisibility,
		&item.MagicTokenHash,
		&item.MagicTokenExpiresAt,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.ClaimedAt,
		&item.ActivatedAt,
	)
	return item, err
}

func (s *PostgresStore) CreateAgentRegistration(ctx context.Context, input AgentRegistrationInput) (AgentRegistration, error) {
	var item AgentRegistration
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO agent_registrations(
			user_id, requested_username, good_at, status, claim_token_hash, claim_token_expires_at, api_key_hash
		) VALUES($1, $2, $3, $4, $5, $6, $7)
		RETURNING user_id, requested_username, good_at, status, claim_token_hash, claim_token_expires_at, api_key_hash,
		          pending_owner_email, pending_human_username, pending_visibility,
		          magic_token_hash, magic_token_expires_at,
		          created_at, updated_at, claimed_at, activated_at
	`, strings.TrimSpace(input.UserID), strings.TrimSpace(input.RequestedUsername), strings.TrimSpace(input.GoodAt),
		strings.TrimSpace(input.Status), strings.TrimSpace(input.ClaimTokenHash), input.ClaimTokenExpiresAt, strings.TrimSpace(input.APIKeyHash),
	).Scan(
		&item.UserID, &item.RequestedUsername, &item.GoodAt, &item.Status, &item.ClaimTokenHash, &item.ClaimTokenExpiresAt, &item.APIKeyHash,
		&item.PendingOwnerEmail, &item.PendingHumanName, &item.PendingVisibility,
		&item.MagicTokenHash, &item.MagicTokenExpiresAt, &item.CreatedAt, &item.UpdatedAt, &item.ClaimedAt, &item.ActivatedAt,
	)
	return item, err
}

func (s *PostgresStore) getAgentRegistrationWhere(ctx context.Context, predicate string, arg any) (AgentRegistration, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT user_id, requested_username, good_at, status, claim_token_hash, claim_token_expires_at, api_key_hash,
		       pending_owner_email, pending_human_username, pending_visibility,
		       magic_token_hash, magic_token_expires_at,
		       created_at, updated_at, claimed_at, activated_at
		FROM agent_registrations
		WHERE `+predicate+`
	`, arg)
	item, err := scanAgentRegistration(row)
	if err == sql.ErrNoRows {
		return AgentRegistration{}, ErrAgentRegistrationNotFound
	}
	return item, err
}

func (s *PostgresStore) GetAgentRegistration(ctx context.Context, userID string) (AgentRegistration, error) {
	return s.getAgentRegistrationWhere(ctx, "user_id = $1", strings.TrimSpace(userID))
}

func (s *PostgresStore) GetAgentRegistrationByClaimTokenHash(ctx context.Context, claimTokenHash string) (AgentRegistration, error) {
	return s.getAgentRegistrationWhere(ctx, "claim_token_hash = $1", strings.TrimSpace(claimTokenHash))
}

func (s *PostgresStore) GetAgentRegistrationByAPIKeyHash(ctx context.Context, apiKeyHash string) (AgentRegistration, error) {
	return s.getAgentRegistrationWhere(ctx, "api_key_hash = $1", strings.TrimSpace(apiKeyHash))
}

func (s *PostgresStore) ListAgentRegistrations(ctx context.Context) ([]AgentRegistration, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, requested_username, good_at, status, claim_token_hash, claim_token_expires_at, api_key_hash,
		       pending_owner_email, pending_human_username, pending_visibility,
		       magic_token_hash, magic_token_expires_at,
		       created_at, updated_at, claimed_at, activated_at
		FROM agent_registrations
		ORDER BY user_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AgentRegistration, 0)
	for rows.Next() {
		item, err := scanAgentRegistration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetAgentRegistrationByMagicTokenHash(ctx context.Context, magicTokenHash string) (AgentRegistration, error) {
	return s.getAgentRegistrationWhere(ctx, "magic_token_hash = $1", strings.TrimSpace(magicTokenHash))
}

func (s *PostgresStore) ListAgentRegistrationsWithoutAPIKey(ctx context.Context) ([]AgentRegistration, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, requested_username, good_at, status, claim_token_hash, claim_token_expires_at, api_key_hash,
		       pending_owner_email, pending_human_username, pending_visibility,
		       magic_token_hash, magic_token_expires_at,
		       created_at, updated_at, claimed_at, activated_at
		FROM agent_registrations
		WHERE api_key_hash = '' OR api_key_hash IS NULL
		ORDER BY user_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AgentRegistration, 0)
	for rows.Next() {
		item, err := scanAgentRegistration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpdateAgentRegistrationAPIKeyHash(ctx context.Context, userID, apiKeyHash string) (AgentRegistration, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE agent_registrations
		SET api_key_hash = $2, updated_at = NOW()
		WHERE user_id = $1
		RETURNING user_id, requested_username, good_at, status, claim_token_hash, claim_token_expires_at, api_key_hash,
		          pending_owner_email, pending_human_username, pending_visibility,
		          magic_token_hash, magic_token_expires_at,
		          created_at, updated_at, claimed_at, activated_at
	`, strings.TrimSpace(userID), strings.TrimSpace(apiKeyHash))
	item, err := scanAgentRegistration(row)
	if err == sql.ErrNoRows {
		return AgentRegistration{}, ErrAgentRegistrationNotFound
	}
	return item, err
}

func (s *PostgresStore) UpdateAgentRegistrationClaim(ctx context.Context, userID, email, humanUsername, visibility, magicTokenHash string, magicExpiresAt time.Time) (AgentRegistration, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE agent_registrations
		SET pending_owner_email = $2,
		    pending_human_username = $3,
		    pending_visibility = $4,
		    magic_token_hash = $5,
		    magic_token_expires_at = $6,
		    updated_at = NOW()
		WHERE user_id = $1
		RETURNING user_id, requested_username, good_at, status, claim_token_hash, claim_token_expires_at, api_key_hash,
		          pending_owner_email, pending_human_username, pending_visibility,
		          magic_token_hash, magic_token_expires_at,
		          created_at, updated_at, claimed_at, activated_at
	`, strings.TrimSpace(userID), strings.ToLower(strings.TrimSpace(email)), strings.TrimSpace(humanUsername), strings.TrimSpace(visibility), strings.TrimSpace(magicTokenHash), magicExpiresAt.UTC())
	item, err := scanAgentRegistration(row)
	if err == sql.ErrNoRows {
		return AgentRegistration{}, ErrAgentRegistrationNotFound
	}
	return item, err
}

func (s *PostgresStore) ActivateAgentRegistration(ctx context.Context, userID string) (AgentRegistration, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE agent_registrations
		SET status = 'active',
		    claimed_at = COALESCE(claimed_at, NOW()),
		    activated_at = COALESCE(activated_at, NOW()),
		    magic_token_hash = '',
		    magic_token_expires_at = NULL,
		    updated_at = NOW()
		WHERE user_id = $1
		RETURNING user_id, requested_username, good_at, status, claim_token_hash, claim_token_expires_at, api_key_hash,
		          pending_owner_email, pending_human_username, pending_visibility,
		          magic_token_hash, magic_token_expires_at,
		          created_at, updated_at, claimed_at, activated_at
	`, strings.TrimSpace(userID))
	item, err := scanAgentRegistration(row)
	if err == sql.ErrNoRows {
		return AgentRegistration{}, ErrAgentRegistrationNotFound
	}
	return item, err
}

func (s *PostgresStore) UpsertAgentProfile(ctx context.Context, item AgentProfile) (AgentProfile, error) {
	var out AgentProfile
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO agent_profiles(
			user_id, username, good_at, human_username, human_name_visibility, owner_email, x_handle, github_username
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id) DO UPDATE SET
			username = COALESCE(NULLIF(EXCLUDED.username, ''), agent_profiles.username),
			good_at = COALESCE(NULLIF(EXCLUDED.good_at, ''), agent_profiles.good_at),
			human_username = COALESCE(NULLIF(EXCLUDED.human_username, ''), agent_profiles.human_username),
			human_name_visibility = COALESCE(NULLIF(EXCLUDED.human_name_visibility, ''), agent_profiles.human_name_visibility),
			owner_email = COALESCE(NULLIF(EXCLUDED.owner_email, ''), agent_profiles.owner_email),
			x_handle = COALESCE(NULLIF(EXCLUDED.x_handle, ''), agent_profiles.x_handle),
			github_username = COALESCE(NULLIF(EXCLUDED.github_username, ''), agent_profiles.github_username),
			updated_at = NOW()
		RETURNING user_id, username, good_at, human_username, human_name_visibility, owner_email, x_handle, github_username, created_at, updated_at
	`, strings.TrimSpace(item.UserID), strings.TrimSpace(item.Username), strings.TrimSpace(item.GoodAt), strings.TrimSpace(item.HumanUsername),
		strings.TrimSpace(item.HumanNameVisibility), strings.ToLower(strings.TrimSpace(item.OwnerEmail)), strings.TrimSpace(item.XHandle),
		strings.TrimSpace(item.GitHubUsername),
	).Scan(
		&out.UserID, &out.Username, &out.GoodAt, &out.HumanUsername, &out.HumanNameVisibility, &out.OwnerEmail,
		&out.XHandle, &out.GitHubUsername, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, err
}

func (s *PostgresStore) GetAgentProfile(ctx context.Context, userID string) (AgentProfile, error) {
	var out AgentProfile
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, username, good_at, human_username, human_name_visibility, owner_email, x_handle, github_username, created_at, updated_at
		FROM agent_profiles WHERE user_id = $1
	`, strings.TrimSpace(userID)).Scan(
		&out.UserID, &out.Username, &out.GoodAt, &out.HumanUsername, &out.HumanNameVisibility, &out.OwnerEmail,
		&out.XHandle, &out.GitHubUsername, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return AgentProfile{}, ErrAgentProfileNotFound
	}
	return out, err
}

func (s *PostgresStore) FindAgentProfileByUsername(ctx context.Context, username string) (AgentProfile, error) {
	var out AgentProfile
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, username, good_at, human_username, human_name_visibility, owner_email, x_handle, github_username, created_at, updated_at
		FROM agent_profiles
		WHERE lower(username) = lower($1)
	`, strings.TrimSpace(username)).Scan(
		&out.UserID, &out.Username, &out.GoodAt, &out.HumanUsername, &out.HumanNameVisibility, &out.OwnerEmail,
		&out.XHandle, &out.GitHubUsername, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return AgentProfile{}, ErrAgentProfileNotFound
	}
	return out, err
}

func (s *PostgresStore) UpsertHumanOwner(ctx context.Context, email, humanUsername string) (HumanOwner, error) {
	var out HumanOwner
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO human_owners(email, human_username)
		VALUES($1, $2)
		ON CONFLICT (email) DO UPDATE SET
			human_username = EXCLUDED.human_username,
			updated_at = NOW()
		RETURNING owner_id::text, email, human_username, x_handle, x_user_id, github_username, github_user_id, created_at, updated_at
	`, strings.ToLower(strings.TrimSpace(email)), strings.TrimSpace(humanUsername)).Scan(
		&out.OwnerID, &out.Email, &out.HumanUsername, &out.XHandle, &out.XUserID, &out.GitHubUsername, &out.GitHubUserID, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, err
}

func (s *PostgresStore) GetHumanOwner(ctx context.Context, ownerID string) (HumanOwner, error) {
	var out HumanOwner
	err := s.db.QueryRowContext(ctx, `
		SELECT owner_id::text, email, human_username, x_handle, x_user_id, github_username, github_user_id, created_at, updated_at
		FROM human_owners WHERE owner_id = $1
	`, strings.TrimSpace(ownerID)).Scan(&out.OwnerID, &out.Email, &out.HumanUsername, &out.XHandle, &out.XUserID, &out.GitHubUsername, &out.GitHubUserID, &out.CreatedAt, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return HumanOwner{}, ErrHumanOwnerNotFound
	}
	return out, err
}

func (s *PostgresStore) GetHumanOwnerByEmail(ctx context.Context, email string) (HumanOwner, error) {
	var out HumanOwner
	err := s.db.QueryRowContext(ctx, `
		SELECT owner_id::text, email, human_username, x_handle, x_user_id, github_username, github_user_id, created_at, updated_at
		FROM human_owners WHERE lower(email) = lower($1)
	`, strings.TrimSpace(email)).Scan(&out.OwnerID, &out.Email, &out.HumanUsername, &out.XHandle, &out.XUserID, &out.GitHubUsername, &out.GitHubUserID, &out.CreatedAt, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return HumanOwner{}, ErrHumanOwnerNotFound
	}
	return out, err
}

func (s *PostgresStore) UpsertHumanOwnerSocialIdentity(ctx context.Context, ownerID, provider, handle, providerUserID string) (HumanOwner, error) {
	setClause := ""
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "x":
		setClause = "x_handle = $2, x_user_id = $3"
	case "github":
		setClause = "github_username = $2, github_user_id = $3"
	default:
		return HumanOwner{}, fmt.Errorf("unsupported social provider: %s", provider)
	}
	var out HumanOwner
	err := s.db.QueryRowContext(ctx, `
		UPDATE human_owners
		SET `+setClause+`, updated_at = NOW()
		WHERE owner_id = CAST($1 AS BIGINT)
		RETURNING owner_id::text, email, human_username, x_handle, x_user_id, github_username, github_user_id, created_at, updated_at
	`, strings.TrimSpace(ownerID), strings.TrimSpace(handle), strings.TrimSpace(providerUserID)).Scan(
		&out.OwnerID, &out.Email, &out.HumanUsername, &out.XHandle, &out.XUserID, &out.GitHubUsername, &out.GitHubUserID, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return HumanOwner{}, ErrHumanOwnerNotFound
	}
	return out, err
}

func (s *PostgresStore) CreateHumanOwnerSession(ctx context.Context, ownerID, tokenHash string, expiresAt time.Time) (HumanOwnerSession, error) {
	var out HumanOwnerSession
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO human_owner_sessions(owner_id, token_hash, expires_at)
		VALUES(CAST($1 AS BIGINT), $2, $3)
		RETURNING session_id::text, owner_id::text, token_hash, created_at, updated_at, expires_at, last_seen_at, revoked_at
	`, strings.TrimSpace(ownerID), strings.TrimSpace(tokenHash), expiresAt.UTC()).Scan(
		&out.SessionID, &out.OwnerID, &out.TokenHash, &out.CreatedAt, &out.UpdatedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt,
	)
	return out, err
}

func (s *PostgresStore) GetHumanOwnerSessionByTokenHash(ctx context.Context, tokenHash string) (HumanOwnerSession, error) {
	var out HumanOwnerSession
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id::text, owner_id::text, token_hash, created_at, updated_at, expires_at, last_seen_at, revoked_at
		FROM human_owner_sessions
		WHERE token_hash = $1
	`, strings.TrimSpace(tokenHash)).Scan(
		&out.SessionID, &out.OwnerID, &out.TokenHash, &out.CreatedAt, &out.UpdatedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt,
	)
	if err == sql.ErrNoRows {
		return HumanOwnerSession{}, ErrHumanOwnerSessionNotFound
	}
	return out, err
}

func (s *PostgresStore) TouchHumanOwnerSession(ctx context.Context, sessionID string, seenAt time.Time) (HumanOwnerSession, error) {
	var out HumanOwnerSession
	err := s.db.QueryRowContext(ctx, `
		UPDATE human_owner_sessions
		SET updated_at = NOW(), last_seen_at = $2
		WHERE session_id = $1
		RETURNING session_id::text, owner_id::text, token_hash, created_at, updated_at, expires_at, last_seen_at, revoked_at
	`, strings.TrimSpace(sessionID), seenAt.UTC()).Scan(
		&out.SessionID, &out.OwnerID, &out.TokenHash, &out.CreatedAt, &out.UpdatedAt, &out.ExpiresAt, &out.LastSeenAt, &out.RevokedAt,
	)
	if err == sql.ErrNoRows {
		return HumanOwnerSession{}, ErrHumanOwnerSessionNotFound
	}
	return out, err
}

func (s *PostgresStore) RevokeHumanOwnerSession(ctx context.Context, sessionID string, revokedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE human_owner_sessions SET revoked_at = $2, updated_at = NOW()
		WHERE session_id = $1
	`, strings.TrimSpace(sessionID), revokedAt.UTC())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHumanOwnerSessionNotFound
	}
	return nil
}

func (s *PostgresStore) UpsertGitHubRepoAccessGrant(ctx context.Context, item GitHubRepoAccessGrant) (GitHubRepoAccessGrant, error) {
	var out GitHubRepoAccessGrant
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO github_repo_access_grants(
			owner_id, github_user_id, github_username, mode, access_status, org, org_membership_status, team_slug,
			next_action, blocking_reason, installation_id, repository_id, repository_owner, repository_name, role,
			access_token_ciphertext, access_expires_at, refresh_token_ciphertext, refresh_expires_at, granted_at,
			last_verified_at, revoked_at
		) VALUES(
			CAST($1 AS BIGINT), $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19, COALESCE($20, NOW()),
			$21, $22
		)
		ON CONFLICT (owner_id) DO UPDATE SET
			github_user_id = EXCLUDED.github_user_id,
			github_username = EXCLUDED.github_username,
			mode = EXCLUDED.mode,
			access_status = EXCLUDED.access_status,
			org = EXCLUDED.org,
			org_membership_status = EXCLUDED.org_membership_status,
			team_slug = EXCLUDED.team_slug,
			next_action = EXCLUDED.next_action,
			blocking_reason = EXCLUDED.blocking_reason,
			installation_id = EXCLUDED.installation_id,
			repository_id = EXCLUDED.repository_id,
			repository_owner = EXCLUDED.repository_owner,
			repository_name = EXCLUDED.repository_name,
			role = EXCLUDED.role,
			access_token_ciphertext = EXCLUDED.access_token_ciphertext,
			access_expires_at = EXCLUDED.access_expires_at,
			refresh_token_ciphertext = EXCLUDED.refresh_token_ciphertext,
			refresh_expires_at = EXCLUDED.refresh_expires_at,
			granted_at = COALESCE(EXCLUDED.granted_at, github_repo_access_grants.granted_at),
			last_verified_at = EXCLUDED.last_verified_at,
			revoked_at = EXCLUDED.revoked_at,
			updated_at = NOW()
		RETURNING owner_id::text, github_user_id, github_username, mode, access_status, org, org_membership_status, team_slug,
		          next_action, blocking_reason, installation_id, repository_id, repository_owner, repository_name, role,
		          access_token_ciphertext, access_expires_at, refresh_token_ciphertext, refresh_expires_at, granted_at,
		          last_verified_at, revoked_at, created_at, updated_at
	`, strings.TrimSpace(item.OwnerID), strings.TrimSpace(item.GitHubUserID), strings.TrimSpace(item.GitHubUsername),
		strings.TrimSpace(item.Mode), strings.TrimSpace(item.AccessStatus), strings.TrimSpace(item.Org),
		strings.TrimSpace(item.OrgMembershipStatus), strings.TrimSpace(item.TeamSlug), strings.TrimSpace(item.NextAction),
		strings.TrimSpace(item.BlockingReason), strings.TrimSpace(item.InstallationID), strings.TrimSpace(item.RepositoryID),
		strings.TrimSpace(item.RepositoryOwner), strings.TrimSpace(item.RepositoryName), strings.TrimSpace(item.Role),
		strings.TrimSpace(item.AccessTokenCiphertext), item.AccessExpiresAt, strings.TrimSpace(item.RefreshTokenCiphertext),
		item.RefreshExpiresAt, nullableTime(item.GrantedAt), item.LastVerifiedAt, item.RevokedAt,
	).Scan(
		&out.OwnerID, &out.GitHubUserID, &out.GitHubUsername, &out.Mode, &out.AccessStatus, &out.Org, &out.OrgMembershipStatus,
		&out.TeamSlug, &out.NextAction, &out.BlockingReason, &out.InstallationID, &out.RepositoryID, &out.RepositoryOwner,
		&out.RepositoryName, &out.Role, &out.AccessTokenCiphertext, &out.AccessExpiresAt, &out.RefreshTokenCiphertext,
		&out.RefreshExpiresAt, &out.GrantedAt, &out.LastVerifiedAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, err
}

func (s *PostgresStore) GetGitHubRepoAccessGrant(ctx context.Context, ownerID string) (GitHubRepoAccessGrant, error) {
	var out GitHubRepoAccessGrant
	err := s.db.QueryRowContext(ctx, `
		SELECT owner_id::text, github_user_id, github_username, mode, access_status, org, org_membership_status, team_slug,
		       next_action, blocking_reason, installation_id, repository_id, repository_owner, repository_name,
		       role, access_token_ciphertext, access_expires_at, refresh_token_ciphertext, refresh_expires_at, granted_at,
		       last_verified_at, revoked_at, created_at, updated_at
		FROM github_repo_access_grants WHERE owner_id = CAST($1 AS BIGINT)
	`, strings.TrimSpace(ownerID)).Scan(
		&out.OwnerID, &out.GitHubUserID, &out.GitHubUsername, &out.Mode, &out.AccessStatus, &out.Org, &out.OrgMembershipStatus,
		&out.TeamSlug, &out.NextAction, &out.BlockingReason, &out.InstallationID, &out.RepositoryID, &out.RepositoryOwner,
		&out.RepositoryName, &out.Role, &out.AccessTokenCiphertext, &out.AccessExpiresAt, &out.RefreshTokenCiphertext,
		&out.RefreshExpiresAt, &out.GrantedAt, &out.LastVerifiedAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return GitHubRepoAccessGrant{}, ErrGitHubRepoAccessGrantNotFound
	}
	return out, err
}

func (s *PostgresStore) RevokeGitHubRepoAccessGrant(ctx context.Context, ownerID string, revokedAt time.Time) (GitHubRepoAccessGrant, error) {
	var out GitHubRepoAccessGrant
	err := s.db.QueryRowContext(ctx, `
		UPDATE github_repo_access_grants
		SET revoked_at = $2, updated_at = NOW()
		WHERE owner_id = CAST($1 AS BIGINT)
		RETURNING owner_id::text, github_user_id, github_username, mode, access_status, org, org_membership_status, team_slug,
		          next_action, blocking_reason, installation_id, repository_id, repository_owner, repository_name, role,
		          access_token_ciphertext, access_expires_at, refresh_token_ciphertext, refresh_expires_at, granted_at,
		          last_verified_at, revoked_at, created_at, updated_at
	`, strings.TrimSpace(ownerID), revokedAt.UTC()).Scan(
		&out.OwnerID, &out.GitHubUserID, &out.GitHubUsername, &out.Mode, &out.AccessStatus, &out.Org, &out.OrgMembershipStatus,
		&out.TeamSlug, &out.NextAction, &out.BlockingReason, &out.InstallationID, &out.RepositoryID, &out.RepositoryOwner,
		&out.RepositoryName, &out.Role, &out.AccessTokenCiphertext, &out.AccessExpiresAt, &out.RefreshTokenCiphertext,
		&out.RefreshExpiresAt, &out.GrantedAt, &out.LastVerifiedAt, &out.RevokedAt, &out.CreatedAt, &out.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return GitHubRepoAccessGrant{}, ErrGitHubRepoAccessGrantNotFound
	}
	return out, err
}

func (s *PostgresStore) UpsertAgentHumanBinding(ctx context.Context, item AgentHumanBinding) (AgentHumanBinding, error) {
	var out AgentHumanBinding
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO agent_human_bindings(user_id, owner_id, human_name_visibility)
		VALUES($1, CAST($2 AS BIGINT), $3)
		ON CONFLICT (user_id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			human_name_visibility = EXCLUDED.human_name_visibility,
			updated_at = NOW()
		RETURNING user_id, owner_id::text, human_name_visibility, created_at, updated_at
	`, strings.TrimSpace(item.UserID), strings.TrimSpace(item.OwnerID), strings.TrimSpace(item.HumanNameVisibility)).Scan(
		&out.UserID, &out.OwnerID, &out.HumanNameVisibility, &out.CreatedAt, &out.UpdatedAt,
	)
	return out, err
}

func (s *PostgresStore) GetAgentHumanBinding(ctx context.Context, userID string) (AgentHumanBinding, error) {
	var out AgentHumanBinding
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, owner_id::text, human_name_visibility, created_at, updated_at
		FROM agent_human_bindings WHERE user_id = $1
	`, strings.TrimSpace(userID)).Scan(&out.UserID, &out.OwnerID, &out.HumanNameVisibility, &out.CreatedAt, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return AgentHumanBinding{}, ErrAgentHumanBindingNotFound
	}
	return out, err
}

func (s *PostgresStore) ListAgentHumanBindingsByOwner(ctx context.Context, ownerID string) ([]AgentHumanBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, owner_id::text, human_name_visibility, created_at, updated_at
		FROM agent_human_bindings WHERE owner_id = CAST($1 AS BIGINT) ORDER BY user_id ASC
	`, strings.TrimSpace(ownerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AgentHumanBinding, 0)
	for rows.Next() {
		var item AgentHumanBinding
		if err := rows.Scan(&item.UserID, &item.OwnerID, &item.HumanNameVisibility, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpsertSocialLink(ctx context.Context, item SocialLink) (SocialLink, error) {
	var out SocialLink
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO social_links(user_id, provider, handle, status, challenge, metadata_json, verified_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			handle = EXCLUDED.handle,
			status = EXCLUDED.status,
			challenge = EXCLUDED.challenge,
			metadata_json = EXCLUDED.metadata_json,
			verified_at = EXCLUDED.verified_at,
			updated_at = NOW()
		RETURNING user_id, provider, handle, status, challenge, metadata_json, created_at, updated_at, verified_at
	`, strings.TrimSpace(item.UserID), strings.ToLower(strings.TrimSpace(item.Provider)), strings.TrimSpace(item.Handle),
		strings.TrimSpace(item.Status), strings.TrimSpace(item.Challenge), strings.TrimSpace(item.MetadataJSON), item.VerifiedAt,
	).Scan(
		&out.UserID, &out.Provider, &out.Handle, &out.Status, &out.Challenge, &out.MetadataJSON, &out.CreatedAt, &out.UpdatedAt, &out.VerifiedAt,
	)
	return out, err
}

func (s *PostgresStore) GetSocialLink(ctx context.Context, userID, provider string) (SocialLink, error) {
	var out SocialLink
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, provider, handle, status, challenge, metadata_json, created_at, updated_at, verified_at
		FROM social_links WHERE user_id = $1 AND provider = $2
	`, strings.TrimSpace(userID), strings.ToLower(strings.TrimSpace(provider))).Scan(
		&out.UserID, &out.Provider, &out.Handle, &out.Status, &out.Challenge, &out.MetadataJSON, &out.CreatedAt, &out.UpdatedAt, &out.VerifiedAt,
	)
	if err == sql.ErrNoRows {
		return SocialLink{}, ErrSocialLinkNotFound
	}
	return out, err
}

func (s *PostgresStore) GrantSocialReward(ctx context.Context, item SocialRewardGrant) (SocialRewardGrant, bool, error) {
	var out SocialRewardGrant
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO social_reward_grants(grant_key, user_id, provider, reward_type, amount, meta_json)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (grant_key) DO NOTHING
		RETURNING grant_key, user_id, provider, reward_type, amount, meta_json, granted_at
	`, strings.TrimSpace(item.GrantKey), strings.TrimSpace(item.UserID), strings.ToLower(strings.TrimSpace(item.Provider)),
		strings.TrimSpace(item.RewardType), item.Amount, strings.TrimSpace(item.MetaJSON),
	).Scan(&out.GrantKey, &out.UserID, &out.Provider, &out.RewardType, &out.Amount, &out.MetaJSON, &out.GrantedAt)
	if err == sql.ErrNoRows {
		err = s.db.QueryRowContext(ctx, `
			SELECT grant_key, user_id, provider, reward_type, amount, meta_json, granted_at
			FROM social_reward_grants WHERE grant_key = $1
		`, strings.TrimSpace(item.GrantKey)).Scan(&out.GrantKey, &out.UserID, &out.Provider, &out.RewardType, &out.Amount, &out.MetaJSON, &out.GrantedAt)
		return out, false, err
	}
	return out, true, err
}

func (s *PostgresStore) ListSocialRewardGrants(ctx context.Context, userID string) ([]SocialRewardGrant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT grant_key, user_id, provider, reward_type, amount, meta_json, granted_at
		FROM social_reward_grants WHERE user_id = $1 ORDER BY granted_at ASC, grant_key ASC
	`, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SocialRewardGrant, 0)
	for rows.Next() {
		var item SocialRewardGrant
		if err := rows.Scan(&item.GrantKey, &item.UserID, &item.Provider, &item.RewardType, &item.Amount, &item.MetaJSON, &item.GrantedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
