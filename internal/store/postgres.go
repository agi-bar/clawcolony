package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgres(ctx context.Context, dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	s := &PostgresStore{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) migrate(ctx context.Context) error {
	const migrateLockKey int64 = 864209731
	if _, err := s.db.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrateLockKey); err != nil {
		return fmt.Errorf("migrate postgres: acquire advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.db.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, migrateLockKey)
	}()

	stmts := []string{
		`DO $$
		BEGIN
			IF to_regclass('public.user_accounts') IS NULL AND to_regclass('public.bot_accounts') IS NOT NULL THEN
				ALTER TABLE bot_accounts RENAME TO user_accounts;
			END IF;
		END $$`,
		`DO $$
		BEGIN
			IF to_regclass('public.user_accounts') IS NOT NULL
			   AND EXISTS (
			     SELECT 1 FROM information_schema.columns
			     WHERE table_schema='public' AND table_name='user_accounts' AND column_name='bot_id'
			   )
			   AND NOT EXISTS (
			     SELECT 1 FROM information_schema.columns
			     WHERE table_schema='public' AND table_name='user_accounts' AND column_name='user_id'
			   ) THEN
				ALTER TABLE user_accounts RENAME COLUMN bot_id TO user_id;
			END IF;
		END $$`,
		`DO $$
		BEGIN
			IF to_regclass('public.token_accounts') IS NOT NULL
			   AND EXISTS (
			     SELECT 1 FROM information_schema.columns
			     WHERE table_schema='public' AND table_name='token_accounts' AND column_name='bot_id'
			   )
			   AND NOT EXISTS (
			     SELECT 1 FROM information_schema.columns
			     WHERE table_schema='public' AND table_name='token_accounts' AND column_name='user_id'
			   ) THEN
				ALTER TABLE token_accounts RENAME COLUMN bot_id TO user_id;
			END IF;
		END $$`,
		`DO $$
		BEGIN
			IF to_regclass('public.token_ledger') IS NOT NULL
			   AND EXISTS (
			     SELECT 1 FROM information_schema.columns
			     WHERE table_schema='public' AND table_name='token_ledger' AND column_name='bot_id'
			   )
			   AND NOT EXISTS (
			     SELECT 1 FROM information_schema.columns
			     WHERE table_schema='public' AND table_name='token_ledger' AND column_name='user_id'
			   ) THEN
				ALTER TABLE token_ledger RENAME COLUMN bot_id TO user_id;
			END IF;
		END $$`,
		`CREATE TABLE IF NOT EXISTS user_accounts (
			user_id TEXT PRIMARY KEY,
			user_name TEXT NOT NULL DEFAULT '',
			nickname TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT 'generic',
			status TEXT NOT NULL DEFAULT 'unknown',
			initialized BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE user_accounts ADD COLUMN IF NOT EXISTS nickname TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS agent_registrations (
			user_id TEXT PRIMARY KEY,
			requested_username TEXT NOT NULL,
			good_at TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending_claim',
			claim_token_hash TEXT NOT NULL DEFAULT '',
			claim_token_expires_at TIMESTAMPTZ NULL,
			api_key_hash TEXT NOT NULL DEFAULT '',
			pending_owner_email TEXT NOT NULL DEFAULT '',
			pending_human_username TEXT NOT NULL DEFAULT '',
			pending_visibility TEXT NOT NULL DEFAULT 'private',
			magic_token_hash TEXT NOT NULL DEFAULT '',
			magic_token_expires_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			claimed_at TIMESTAMPTZ NULL,
			activated_at TIMESTAMPTZ NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_registrations_claim_token_hash ON agent_registrations(claim_token_hash) WHERE claim_token_hash <> ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_registrations_api_key_hash ON agent_registrations(api_key_hash) WHERE api_key_hash <> ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_registrations_magic_token_hash ON agent_registrations(magic_token_hash) WHERE magic_token_hash <> ''`,
		`CREATE TABLE IF NOT EXISTS agent_profiles (
			user_id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			good_at TEXT NOT NULL DEFAULT '',
			human_username TEXT NOT NULL DEFAULT '',
			human_name_visibility TEXT NOT NULL DEFAULT 'private',
			owner_email TEXT NOT NULL DEFAULT '',
			x_handle TEXT NOT NULL DEFAULT '',
			github_username TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_profiles_username_ci ON agent_profiles(lower(username))`,
		`CREATE TABLE IF NOT EXISTS human_owners (
			owner_id BIGSERIAL PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			human_username TEXT NOT NULL DEFAULT '',
			x_handle TEXT NOT NULL DEFAULT '',
			x_user_id TEXT NOT NULL DEFAULT '',
			github_username TEXT NOT NULL DEFAULT '',
			github_user_id TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE human_owners ADD COLUMN IF NOT EXISTS x_handle TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE human_owners ADD COLUMN IF NOT EXISTS x_user_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE human_owners ADD COLUMN IF NOT EXISTS github_username TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE human_owners ADD COLUMN IF NOT EXISTS github_user_id TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS human_owner_sessions (
			session_id BIGSERIAL PRIMARY KEY,
			owner_id BIGINT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NOT NULL,
			last_seen_at TIMESTAMPTZ NULL,
			revoked_at TIMESTAMPTZ NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_human_bindings (
			user_id TEXT PRIMARY KEY,
			owner_id BIGINT NOT NULL,
			human_name_visibility TEXT NOT NULL DEFAULT 'private',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE human_owner_sessions ALTER COLUMN owner_id TYPE BIGINT USING NULLIF(owner_id::text, '')::bigint`,
		`ALTER TABLE agent_human_bindings ALTER COLUMN owner_id TYPE BIGINT USING NULLIF(owner_id::text, '')::bigint`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'human_owner_sessions_owner_id_fkey'
			) THEN
				ALTER TABLE human_owner_sessions
				ADD CONSTRAINT human_owner_sessions_owner_id_fkey
				FOREIGN KEY (owner_id) REFERENCES human_owners(owner_id);
			END IF;
		END
		$$`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'agent_human_bindings_owner_id_fkey'
			) THEN
				ALTER TABLE agent_human_bindings
				ADD CONSTRAINT agent_human_bindings_owner_id_fkey
				FOREIGN KEY (owner_id) REFERENCES human_owners(owner_id);
			END IF;
		END
		$$`,
		`CREATE TABLE IF NOT EXISTS github_repo_access_grants (
			owner_id BIGINT PRIMARY KEY,
			github_user_id TEXT NOT NULL DEFAULT '',
			github_username TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT '',
			access_status TEXT NOT NULL DEFAULT '',
			org TEXT NOT NULL DEFAULT '',
			org_membership_status TEXT NOT NULL DEFAULT '',
			team_slug TEXT NOT NULL DEFAULT '',
			next_action TEXT NOT NULL DEFAULT '',
			blocking_reason TEXT NOT NULL DEFAULT '',
			installation_id TEXT NOT NULL DEFAULT '',
			repository_id TEXT NOT NULL DEFAULT '',
			repository_owner TEXT NOT NULL DEFAULT '',
			repository_name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			access_token_ciphertext TEXT NOT NULL DEFAULT '',
			access_expires_at TIMESTAMPTZ NULL,
			refresh_token_ciphertext TEXT NOT NULL DEFAULT '',
			refresh_expires_at TIMESTAMPTZ NULL,
			granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_verified_at TIMESTAMPTZ NULL,
			revoked_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_constraint WHERE conname = 'github_repo_access_grants_owner_id_fkey'
			) THEN
				ALTER TABLE github_repo_access_grants
				ADD CONSTRAINT github_repo_access_grants_owner_id_fkey
				FOREIGN KEY (owner_id) REFERENCES human_owners(owner_id);
			END IF;
		END
		$$`,
		`ALTER TABLE github_repo_access_grants ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE github_repo_access_grants ADD COLUMN IF NOT EXISTS access_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE github_repo_access_grants ADD COLUMN IF NOT EXISTS org TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE github_repo_access_grants ADD COLUMN IF NOT EXISTS org_membership_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE github_repo_access_grants ADD COLUMN IF NOT EXISTS team_slug TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE github_repo_access_grants ADD COLUMN IF NOT EXISTS next_action TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE github_repo_access_grants ADD COLUMN IF NOT EXISTS blocking_reason TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_agent_human_bindings_owner ON agent_human_bindings(owner_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS social_links (
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			handle TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			challenge TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			verified_at TIMESTAMPTZ NULL,
			PRIMARY KEY(user_id, provider)
		)`,
		`CREATE TABLE IF NOT EXISTS social_reward_grants (
			grant_key TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			reward_type TEXT NOT NULL,
			amount BIGINT NOT NULL,
			meta_json TEXT NOT NULL DEFAULT '',
			granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_social_reward_grants_user ON social_reward_grants(user_id, granted_at ASC)`,
		`CREATE TABLE IF NOT EXISTS token_accounts (
			user_id TEXT PRIMARY KEY,
			balance BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS token_ledger (
			id BIGSERIAL PRIMARY KEY,
			user_id TEXT NOT NULL,
			op_type TEXT NOT NULL,
			amount BIGINT NOT NULL,
			balance_after BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_token_ledger_user ON token_ledger(user_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS mail_messages (
			id BIGSERIAL PRIMARY KEY,
			sender_address TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			reply_to_mailbox_id BIGINT NOT NULL DEFAULT 0,
			sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE mail_messages ADD COLUMN IF NOT EXISTS reply_to_mailbox_id BIGINT NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS mail_mailboxes (
			id BIGSERIAL PRIMARY KEY,
			message_id BIGINT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
			owner_address TEXT NOT NULL,
			folder TEXT NOT NULL,
			to_address TEXT NOT NULL,
			is_read BOOLEAN NOT NULL DEFAULT false,
			read_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_mailboxes_owner ON mail_mailboxes(owner_address, folder, is_read, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_mailboxes_message ON mail_mailboxes(message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_subject ON mail_messages USING gin (to_tsvector('simple', subject || ' ' || body))`,
		`CREATE TABLE IF NOT EXISTS mail_messages_archive (
			id BIGINT PRIMARY KEY,
			sender_address TEXT NOT NULL,
			subject TEXT NOT NULL,
			body TEXT NOT NULL,
			reply_to_mailbox_id BIGINT NOT NULL DEFAULT 0,
			sent_at TIMESTAMPTZ NOT NULL,
			archived_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			archive_reason TEXT NOT NULL DEFAULT '',
			archive_batch_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_messages_archive_batch ON mail_messages_archive(archive_batch_id, archived_at DESC)`,
		`CREATE TABLE IF NOT EXISTS mail_mailboxes_archive (
			id BIGINT PRIMARY KEY,
			message_id BIGINT NOT NULL,
			owner_address TEXT NOT NULL,
			folder TEXT NOT NULL,
			to_address TEXT NOT NULL,
			is_read BOOLEAN NOT NULL DEFAULT false,
			read_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL,
			archived_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			archive_reason TEXT NOT NULL DEFAULT '',
			archive_batch_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_mailboxes_archive_owner ON mail_mailboxes_archive(owner_address, folder, archived_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mail_mailboxes_archive_batch ON mail_mailboxes_archive(archive_batch_id, archived_at DESC)`,
		`CREATE TABLE IF NOT EXISTS notification_delivery_state (
			owner_address TEXT NOT NULL,
			category TEXT NOT NULL,
			state_hash TEXT NOT NULL DEFAULT '',
			last_sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_reminded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			last_seen_at TIMESTAMPTZ NULL,
			outstanding_message_id BIGINT NOT NULL DEFAULT 0,
			outstanding_mailbox_id BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(owner_address, category)
		)`,
		`ALTER TABLE notification_delivery_state ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ NULL`,
		`ALTER TABLE notification_delivery_state ADD COLUMN IF NOT EXISTS outstanding_message_id BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE notification_delivery_state ADD COLUMN IF NOT EXISTS outstanding_mailbox_id BIGINT NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_notification_delivery_state_category ON notification_delivery_state(category, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS mail_contacts (
			owner_address TEXT NOT NULL,
			contact_address TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			skills TEXT NOT NULL DEFAULT '',
			current_project TEXT NOT NULL DEFAULT '',
			availability TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(owner_address, contact_address)
		)`,
		`ALTER TABLE mail_contacts ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mail_contacts ADD COLUMN IF NOT EXISTS skills TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mail_contacts ADD COLUMN IF NOT EXISTS current_project TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE mail_contacts ADD COLUMN IF NOT EXISTS availability TEXT NOT NULL DEFAULT ''`,
		`UPDATE mail_messages
		 SET sender_address = 'clawcolony-admin'
		 WHERE sender_address IN ('clawcolony-system', 'clawcolony')`,
		`UPDATE mail_mailboxes
		 SET owner_address = 'clawcolony-admin'
		 WHERE owner_address IN ('clawcolony-system', 'clawcolony')`,
		`UPDATE mail_mailboxes
		 SET to_address = 'clawcolony-admin'
		 WHERE to_address IN ('clawcolony-system', 'clawcolony')`,
		`UPDATE mail_contacts
		 SET owner_address = 'clawcolony-admin'
		 WHERE owner_address IN ('clawcolony-system', 'clawcolony')`,
		`UPDATE mail_contacts
		 SET contact_address = 'clawcolony-admin'
		 WHERE contact_address IN ('clawcolony-system', 'clawcolony')`,
		`CREATE TABLE IF NOT EXISTS collab_sessions (
			collab_id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			goal TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'general',
			complexity TEXT NOT NULL DEFAULT 'normal',
			phase TEXT NOT NULL,
			proposer_user_id TEXT NOT NULL,
			author_user_id TEXT NOT NULL DEFAULT '',
			orchestrator_user_id TEXT NOT NULL DEFAULT '',
			min_members INT NOT NULL DEFAULT 2,
			max_members INT NOT NULL DEFAULT 3,
			required_reviewers INT NOT NULL DEFAULT 2,
			pr_repo TEXT NOT NULL DEFAULT '',
			pr_branch TEXT NOT NULL DEFAULT '',
			pr_url TEXT NOT NULL DEFAULT '',
			pr_number INT NOT NULL DEFAULT 0,
			pr_base_sha TEXT NOT NULL DEFAULT '',
			pr_head_sha TEXT NOT NULL DEFAULT '',
			pr_author_login TEXT NOT NULL DEFAULT '',
			github_pr_state TEXT NOT NULL DEFAULT '',
			pr_merge_commit_sha TEXT NOT NULL DEFAULT '',
			source_ref TEXT NOT NULL DEFAULT '',
			implementation_mode TEXT NOT NULL DEFAULT '',
			repo_doc_path TEXT NOT NULL DEFAULT '',
			status_summary TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			review_deadline_at TIMESTAMPTZ NULL,
			pr_merged_at TIMESTAMPTZ NULL,
			closed_at TIMESTAMPTZ NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_collab_sessions_phase_updated ON collab_sessions(phase, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_collab_sessions_proposer_updated ON collab_sessions(proposer_user_id, updated_at DESC)`,
		// idx_collab_sessions_kind created below, after ALTER TABLE adds kind column
		`CREATE TABLE IF NOT EXISTS collab_participants (
			id BIGSERIAL PRIMARY KEY,
			collab_id TEXT NOT NULL REFERENCES collab_sessions(collab_id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			pitch TEXT NOT NULL DEFAULT '',
			application_kind TEXT NOT NULL DEFAULT '',
			evidence_url TEXT NOT NULL DEFAULT '',
			verified BOOLEAN NOT NULL DEFAULT FALSE,
			github_login TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(collab_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_collab_participants_collab_status ON collab_participants(collab_id, status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS collab_artifacts (
			id BIGSERIAL PRIMARY KEY,
			collab_id TEXT NOT NULL REFERENCES collab_sessions(collab_id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'submitted',
			review_note TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_collab_artifacts_collab_updated ON collab_artifacts(collab_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS collab_events (
			id BIGSERIAL PRIMARY KEY,
			collab_id TEXT NOT NULL REFERENCES collab_sessions(collab_id) ON DELETE CASCADE,
			actor_user_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			payload TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_collab_events_collab_id ON collab_events(collab_id, id DESC)`,
		// collab_sessions PR collaboration fields (multi-agent PR collab P0)
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'general'`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS author_user_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_repo TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_branch TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_number INT NOT NULL DEFAULT 0`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_base_sha TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_head_sha TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS required_reviewers INT NOT NULL DEFAULT 2`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_author_login TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS github_pr_state TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_merge_commit_sha TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS source_ref TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS implementation_mode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS repo_doc_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS review_deadline_at TIMESTAMPTZ NULL`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS pr_merged_at TIMESTAMPTZ NULL`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ NULL`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS proposal_id BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS implementation_deadline_at TIMESTAMPTZ NULL`,
		`ALTER TABLE collab_participants ADD COLUMN IF NOT EXISTS application_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_participants ADD COLUMN IF NOT EXISTS evidence_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE collab_participants ADD COLUMN IF NOT EXISTS verified BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE collab_participants ADD COLUMN IF NOT EXISTS github_login TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_collab_sessions_kind ON collab_sessions(kind, phase, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS task_leases (
			id BIGSERIAL PRIMARY KEY,
			task_kind TEXT NOT NULL,
			task_id TEXT NOT NULL,
			linked_resource_type TEXT NOT NULL DEFAULT '',
			linked_resource_id TEXT NOT NULL DEFAULT '',
			holder_user_id TEXT NOT NULL,
			claimed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NOT NULL,
			consumed_at TIMESTAMPTZ NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_leases_task_claimed ON task_leases(task_kind, task_id, claimed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_task_leases_holder_claimed ON task_leases(task_kind, holder_user_id, claimed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS kb_entries (
			id BIGSERIAL PRIMARY KEY,
			section TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL DEFAULT '',
			version BIGINT NOT NULL DEFAULT 1,
			updated_by TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			is_deleted BOOLEAN NOT NULL DEFAULT false
		)`,
		`CREATE INDEX IF NOT EXISTS idx_kb_entries_section_updated ON kb_entries(section, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS kb_proposals (
			id BIGSERIAL PRIMARY KEY,
			proposer_user_id TEXT NOT NULL,
			title TEXT NOT NULL,
			reason TEXT NOT NULL,
			status TEXT NOT NULL,
			current_revision_id BIGINT NOT NULL DEFAULT 0,
			voting_revision_id BIGINT NOT NULL DEFAULT 0,
			vote_threshold_pct INT NOT NULL DEFAULT 80,
			vote_window_seconds INT NOT NULL DEFAULT 3600,
			enrolled_count INT NOT NULL DEFAULT 0,
			vote_yes INT NOT NULL DEFAULT 0,
			vote_no INT NOT NULL DEFAULT 0,
			vote_abstain INT NOT NULL DEFAULT 0,
			participation_count INT NOT NULL DEFAULT 0,
			decision_reason TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			voting_deadline_at TIMESTAMPTZ NULL,
			closed_at TIMESTAMPTZ NULL,
			applied_at TIMESTAMPTZ NULL
		)`,
		`ALTER TABLE kb_proposals ADD COLUMN IF NOT EXISTS current_revision_id BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE kb_proposals ADD COLUMN IF NOT EXISTS voting_revision_id BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE kb_proposals ADD COLUMN IF NOT EXISTS discussion_deadline_at TIMESTAMPTZ NULL`,
		`ALTER TABLE kb_proposals ALTER COLUMN vote_window_seconds SET DEFAULT 3600`,
		`CREATE INDEX IF NOT EXISTS idx_kb_proposals_status_updated ON kb_proposals(status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS kb_proposal_changes (
			id BIGSERIAL PRIMARY KEY,
			proposal_id BIGINT NOT NULL UNIQUE REFERENCES kb_proposals(id) ON DELETE CASCADE,
			op_type TEXT NOT NULL,
			target_entry_id BIGINT NULL,
			section TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			old_content TEXT NOT NULL DEFAULT '',
			new_content TEXT NOT NULL DEFAULT '',
			diff_text TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS kb_proposal_enrollments (
			id BIGSERIAL PRIMARY KEY,
			proposal_id BIGINT NOT NULL REFERENCES kb_proposals(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(proposal_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_kb_enrollments_proposal ON kb_proposal_enrollments(proposal_id, created_at ASC, id ASC)`,
		`CREATE TABLE IF NOT EXISTS kb_votes (
			id BIGSERIAL PRIMARY KEY,
			proposal_id BIGINT NOT NULL REFERENCES kb_proposals(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			vote TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(proposal_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_kb_votes_proposal ON kb_votes(proposal_id, updated_at ASC, id ASC)`,
		`CREATE TABLE IF NOT EXISTS kb_revisions (
			id BIGSERIAL PRIMARY KEY,
			proposal_id BIGINT NOT NULL REFERENCES kb_proposals(id) ON DELETE CASCADE,
			revision_no BIGINT NOT NULL,
			base_revision_id BIGINT NOT NULL DEFAULT 0,
			created_by TEXT NOT NULL,
			op_type TEXT NOT NULL,
			target_entry_id BIGINT NULL,
			section TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			old_content TEXT NOT NULL DEFAULT '',
			new_content TEXT NOT NULL DEFAULT '',
			diff_text TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_kb_revisions_proposal_revision ON kb_revisions(proposal_id, revision_no ASC, id ASC)`,
		`CREATE TABLE IF NOT EXISTS kb_acks (
			id BIGSERIAL PRIMARY KEY,
			proposal_id BIGINT NOT NULL REFERENCES kb_proposals(id) ON DELETE CASCADE,
			revision_id BIGINT NOT NULL REFERENCES kb_revisions(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(proposal_id, revision_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_kb_acks_proposal_revision ON kb_acks(proposal_id, revision_id, id ASC)`,
		`CREATE TABLE IF NOT EXISTS kb_threads (
			id BIGSERIAL PRIMARY KEY,
			proposal_id BIGINT NOT NULL REFERENCES kb_proposals(id) ON DELETE CASCADE,
			author_user_id TEXT NOT NULL,
			message_type TEXT NOT NULL DEFAULT 'comment',
			content TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_kb_threads_proposal ON kb_threads(proposal_id, created_at ASC, id ASC)`,
		`CREATE TABLE IF NOT EXISTS request_logs (
			id BIGSERIAL PRIMARY KEY,
			req_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			method TEXT NOT NULL,
			path TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			status_code INT NOT NULL,
			duration_ms BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_time_id ON request_logs(req_time DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_user_time ON request_logs(user_id, req_time DESC)`,
		`CREATE TABLE IF NOT EXISTS world_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS owner_economy_profiles (
			owner_id TEXT PRIMARY KEY,
			github_user_id TEXT NOT NULL DEFAULT '',
			github_username TEXT NOT NULL DEFAULT '',
			activated BOOLEAN NOT NULL DEFAULT false,
			activated_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS owner_onboarding_grants (
			grant_key TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			grant_type TEXT NOT NULL,
			recipient_user_id TEXT NOT NULL,
			amount BIGINT NOT NULL,
			decision_key TEXT NOT NULL DEFAULT '',
			github_user_id TEXT NOT NULL DEFAULT '',
			github_username TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_owner_onboarding_grants_owner_created ON owner_onboarding_grants(owner_id, created_at ASC, grant_key ASC)`,
		`CREATE TABLE IF NOT EXISTS economy_comm_quota_windows (
			user_id TEXT PRIMARY KEY,
			window_start_tick BIGINT NOT NULL DEFAULT 0,
			used_free_tokens BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS economy_contribution_events (
			event_key TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			user_id TEXT NOT NULL DEFAULT '',
			resource_type TEXT NOT NULL DEFAULT '',
			resource_id TEXT NOT NULL DEFAULT '',
			meta_json TEXT NOT NULL DEFAULT '',
			decision_keys_json TEXT NOT NULL DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			processed_at TIMESTAMPTZ NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_economy_contribution_events_pending ON economy_contribution_events(processed_at, created_at, event_key)`,
		`CREATE INDEX IF NOT EXISTS idx_economy_contribution_events_kind_user ON economy_contribution_events(kind, user_id, created_at, event_key)`,
		`CREATE TABLE IF NOT EXISTS economy_reward_decisions (
			decision_key TEXT PRIMARY KEY,
			rule_key TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			recipient_user_id TEXT NOT NULL,
			amount BIGINT NOT NULL,
			priority INT NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			queue_reason TEXT NOT NULL DEFAULT '',
			ledger_id BIGINT NOT NULL DEFAULT 0,
			balance_after BIGINT NOT NULL DEFAULT 0,
			meta_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			applied_at TIMESTAMPTZ NULL,
			enqueued_at TIMESTAMPTZ NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_economy_reward_decisions_queue ON economy_reward_decisions(status, priority, created_at, decision_key)`,
		`CREATE INDEX IF NOT EXISTS idx_economy_reward_decisions_recipient ON economy_reward_decisions(recipient_user_id, created_at DESC, decision_key DESC)`,
		`CREATE TABLE IF NOT EXISTS economy_knowledge_meta (
			proposal_id BIGINT NOT NULL DEFAULT 0,
			entry_id BIGINT NOT NULL DEFAULT 0,
			category TEXT NOT NULL DEFAULT '',
			references_json TEXT NOT NULL DEFAULT '[]',
			author_user_id TEXT NOT NULL DEFAULT '',
			content_tokens BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (proposal_id, entry_id)
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_economy_knowledge_meta_proposal ON economy_knowledge_meta(proposal_id) WHERE proposal_id > 0`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_economy_knowledge_meta_entry ON economy_knowledge_meta(entry_id) WHERE entry_id > 0`,
		`CREATE TABLE IF NOT EXISTS economy_tool_meta (
			tool_id TEXT PRIMARY KEY,
			author_user_id TEXT NOT NULL DEFAULT '',
			category_hint TEXT NOT NULL DEFAULT '',
			functional_cluster_key TEXT NOT NULL DEFAULT '',
			price_token BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_economy_tool_meta_cluster ON economy_tool_meta(functional_cluster_key, updated_at DESC, tool_id ASC)`,
		`CREATE TABLE IF NOT EXISTS ganglia (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			ganglion_type TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			implementation TEXT NOT NULL DEFAULT '',
			validation TEXT NOT NULL DEFAULT '',
			author_user_id TEXT NOT NULL,
			supersedes_id BIGINT NOT NULL DEFAULT 0,
			temporality TEXT NOT NULL DEFAULT 'durable',
			life_state TEXT NOT NULL DEFAULT 'nascent',
			score_avg_milli BIGINT NOT NULL DEFAULT 0,
			score_count BIGINT NOT NULL DEFAULT 0,
			integrations_count BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ganglia_type_state_updated ON ganglia(ganglion_type, life_state, updated_at DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS ganglion_integrations (
			id BIGSERIAL PRIMARY KEY,
			ganglion_id BIGINT NOT NULL REFERENCES ganglia(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(ganglion_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ganglion_integrations_user_updated ON ganglion_integrations(user_id, updated_at DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS ganglion_ratings (
			id BIGSERIAL PRIMARY KEY,
			ganglion_id BIGINT NOT NULL REFERENCES ganglia(id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			score INT NOT NULL,
			feedback TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(ganglion_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ganglion_ratings_ganglion_updated ON ganglion_ratings(ganglion_id, updated_at DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS user_life_state (
			user_id TEXT PRIMARY KEY,
			state TEXT NOT NULL DEFAULT 'alive',
			dying_since_tick BIGINT NOT NULL DEFAULT 0,
			dead_at_tick BIGINT NOT NULL DEFAULT 0,
			reason TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_life_state_state_updated ON user_life_state(state, updated_at DESC, user_id ASC)`,
		`CREATE TABLE IF NOT EXISTS user_life_state_transitions (
			id BIGSERIAL PRIMARY KEY,
			user_id TEXT NOT NULL,
			from_state TEXT NOT NULL DEFAULT '',
			to_state TEXT NOT NULL DEFAULT '',
			from_dying_since_tick BIGINT NOT NULL DEFAULT 0,
			to_dying_since_tick BIGINT NOT NULL DEFAULT 0,
			from_dead_at_tick BIGINT NOT NULL DEFAULT 0,
			to_dead_at_tick BIGINT NOT NULL DEFAULT 0,
			from_reason TEXT NOT NULL DEFAULT '',
			to_reason TEXT NOT NULL DEFAULT '',
			tick_id BIGINT NOT NULL DEFAULT 0,
			source_module TEXT NOT NULL DEFAULT 'life.state',
			source_ref TEXT NOT NULL DEFAULT '',
			actor_user_id TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_life_state_transitions_user_created ON user_life_state_transitions(user_id, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_user_life_state_transitions_to_state_created ON user_life_state_transitions(to_state, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_user_life_state_transitions_tick_created ON user_life_state_transitions(tick_id DESC, created_at DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS world_ticks (
			id BIGSERIAL PRIMARY KEY,
			tick_id BIGINT NOT NULL,
			started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			duration_ms BIGINT NOT NULL DEFAULT 0,
			trigger_type TEXT NOT NULL DEFAULT 'scheduled',
			replay_of_tick_id BIGINT NOT NULL DEFAULT 0,
			prev_hash TEXT NOT NULL DEFAULT '',
			entry_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'ok',
			error_text TEXT NOT NULL DEFAULT ''
		)`,
		`ALTER TABLE world_ticks ADD COLUMN IF NOT EXISTS trigger_type TEXT NOT NULL DEFAULT 'scheduled'`,
		`ALTER TABLE world_ticks ADD COLUMN IF NOT EXISTS replay_of_tick_id BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE world_ticks ADD COLUMN IF NOT EXISTS prev_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE world_ticks ADD COLUMN IF NOT EXISTS entry_hash TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_world_ticks_tick_id ON world_ticks(tick_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_world_ticks_started_at ON world_ticks(started_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_world_ticks_entry_hash ON world_ticks(entry_hash)`,
		`CREATE TABLE IF NOT EXISTS world_tick_steps (
			id BIGSERIAL PRIMARY KEY,
			tick_id BIGINT NOT NULL,
			step_name TEXT NOT NULL,
			started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			duration_ms BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'ok',
			error_text TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_world_tick_steps_tick_id ON world_tick_steps(tick_id DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_world_tick_steps_started_at ON world_tick_steps(started_at DESC, id DESC)`,
		`CREATE OR REPLACE FUNCTION deny_world_tick_mutation()
		 RETURNS trigger
		 LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'world_ticks is append-only';
		END;
		$$`,
		`DROP TRIGGER IF EXISTS trg_world_ticks_append_only ON world_ticks`,
		`CREATE TRIGGER trg_world_ticks_append_only
			BEFORE UPDATE OR DELETE ON world_ticks
			FOR EACH ROW
			EXECUTE FUNCTION deny_world_tick_mutation()`,
		`CREATE OR REPLACE FUNCTION deny_world_tick_step_mutation()
		 RETURNS trigger
		 LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'world_tick_steps is append-only';
		END;
		$$`,
		`DROP TRIGGER IF EXISTS trg_world_tick_steps_append_only ON world_tick_steps`,
		`CREATE TRIGGER trg_world_tick_steps_append_only
			BEFORE UPDATE OR DELETE ON world_tick_steps
			FOR EACH ROW
			EXECUTE FUNCTION deny_world_tick_step_mutation()`,
		`CREATE OR REPLACE FUNCTION deny_user_life_state_transition_mutation()
		 RETURNS trigger
		 LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'user_life_state_transitions is append-only';
		END;
		$$`,
		`DROP TRIGGER IF EXISTS trg_user_life_state_transitions_append_only ON user_life_state_transitions`,
		`CREATE TRIGGER trg_user_life_state_transitions_append_only
			BEFORE UPDATE OR DELETE ON user_life_state_transitions
			FOR EACH ROW
			EXECUTE FUNCTION deny_user_life_state_transition_mutation()`,
		`CREATE OR REPLACE FUNCTION cost_event_to_user_id(meta TEXT)
		 RETURNS TEXT
		 LANGUAGE plpgsql
		 IMMUTABLE
		AS $$
		DECLARE
			payload JSONB;
		BEGIN
			IF NULLIF(BTRIM(meta), '') IS NULL THEN
				RETURN NULL;
			END IF;
			BEGIN
				payload := meta::jsonb;
			EXCEPTION
				WHEN others THEN
					RETURN NULL;
			END;
			RETURN payload ->> 'to_user_id';
		END;
		$$`,
		`CREATE TABLE IF NOT EXISTS cost_events (
			id BIGSERIAL PRIMARY KEY,
			user_id TEXT NOT NULL,
			tick_id BIGINT NOT NULL DEFAULT 0,
			cost_type TEXT NOT NULL,
			amount BIGINT NOT NULL,
			units BIGINT NOT NULL DEFAULT 0,
			meta_json TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cost_events_user_created ON cost_events(user_id, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_cost_events_tick_id ON cost_events(tick_id DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_cost_events_to_user_id ON cost_events (
			cost_event_to_user_id(meta_json),
			id DESC
		)`,
		`CREATE TABLE IF NOT EXISTS tian_dao_laws (
			law_key TEXT PRIMARY KEY,
			version BIGINT NOT NULL,
			manifest_json TEXT NOT NULL,
			manifest_sha256 TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE OR REPLACE FUNCTION deny_tian_dao_law_mutation()
		 RETURNS trigger
		 LANGUAGE plpgsql
		AS $$
		BEGIN
			RAISE EXCEPTION 'tian_dao_laws is immutable';
		END;
		$$`,
		`DROP TRIGGER IF EXISTS trg_tian_dao_law_mutation ON tian_dao_laws`,
		`CREATE TRIGGER trg_tian_dao_law_mutation
			BEFORE UPDATE OR DELETE ON tian_dao_laws
			FOR EACH ROW
			EXECUTE FUNCTION deny_tian_dao_law_mutation()`,
		`DO $$
		BEGIN
			IF to_regclass('public.prompt_templates') IS NOT NULL THEN
				DELETE FROM prompt_templates
				  WHERE key IN ('agents_append', 'soul_append', 'bootstrap_append', 'tools_append');
			END IF;
		END $$`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_user_accounts_active_name_ci ON user_accounts(lower(user_name)) WHERE initialized = true AND status NOT IN ('deleted', 'inactive', 'system')`,
	}
	if runtimeSchemaShrinkEnabled() {
		stmts = append(stmts,
			`ALTER TABLE user_accounts DROP COLUMN IF EXISTS gateway_token`,
			`ALTER TABLE user_accounts DROP COLUMN IF EXISTS upgrade_token`,
			`DROP TABLE IF EXISTS register_task_steps`,
			`DROP TABLE IF EXISTS register_tasks`,
			`DROP TABLE IF EXISTS upgrade_steps`,
			`DROP TABLE IF EXISTS upgrade_audits`,
			`DROP TABLE IF EXISTS chat_messages`,
			`DROP TABLE IF EXISTS prompt_templates`,
		)
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate postgres: %w", err)
		}
	}
	return nil
}

func runtimeSchemaShrinkEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("CLAWCOLONY_RUNTIME_SCHEMA_SHRINK")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *PostgresStore) ensureBotTx(ctx context.Context, tx *sql.Tx, botID string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_accounts(user_id, user_name, provider, status, initialized, updated_at)
		VALUES($1, $1, 'system', 'active', true, NOW())
		ON CONFLICT (user_id) DO NOTHING
	`, botID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO token_accounts(user_id, balance) VALUES($1, 0) ON CONFLICT (user_id) DO NOTHING`, botID); err != nil {
		return err
	}
	return nil
}

func (s *PostgresStore) ListBots(ctx context.Context) ([]Bot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, user_name, nickname, provider, status, initialized, created_at, updated_at
		FROM user_accounts
		ORDER BY user_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Bot, 0)
	for rows.Next() {
		var b Bot
		if err := rows.Scan(&b.BotID, &b.Name, &b.Nickname, &b.Provider, &b.Status, &b.Initialized, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, b)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetBot(ctx context.Context, botID string) (Bot, error) {
	var b Bot
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, user_name, nickname, provider, status, initialized, created_at, updated_at
		FROM user_accounts WHERE user_id = $1
	`, botID).Scan(&b.BotID, &b.Name, &b.Nickname, &b.Provider, &b.Status, &b.Initialized, &b.CreatedAt, &b.UpdatedAt)
	if err == sql.ErrNoRows {
		return Bot{}, fmt.Errorf("%w: %s", ErrBotNotFound, botID)
	}
	if err != nil {
		return Bot{}, err
	}
	return b, nil
}

func (s *PostgresStore) UpsertBot(ctx context.Context, input BotUpsertInput) (Bot, error) {
	var b Bot
	var nicknameParam any
	if input.Nickname != nil {
		nicknameParam = strings.TrimSpace(*input.Nickname)
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO user_accounts(user_id, user_name, nickname, provider, status, initialized, updated_at)
		VALUES($1, $2, COALESCE($3::text, ''), $4, $5, $6, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			user_name = EXCLUDED.user_name,
			nickname = COALESCE($3::text, user_accounts.nickname),
			provider = EXCLUDED.provider,
			status = EXCLUDED.status,
			initialized = EXCLUDED.initialized,
			updated_at = NOW()
		RETURNING user_id, user_name, nickname, provider, status, initialized, created_at, updated_at
	`, input.BotID, input.Name, nicknameParam, input.Provider, input.Status, input.Initialized).Scan(
		&b.BotID, &b.Name, &b.Nickname, &b.Provider, &b.Status, &b.Initialized, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return Bot{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO token_accounts(user_id, balance) VALUES($1, 0) ON CONFLICT (user_id) DO NOTHING`, b.BotID); err != nil {
		return Bot{}, err
	}
	return b, nil
}

func (s *PostgresStore) ActivateBotWithUniqueName(ctx context.Context, botID, name string) (Bot, error) {
	uid := strings.TrimSpace(botID)
	if uid == "" {
		return Bot{}, fmt.Errorf("user_id is required")
	}
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return Bot{}, fmt.Errorf("name is required")
	}
	var b Bot
	err := s.db.QueryRowContext(ctx, `
		UPDATE user_accounts
		SET user_name = $2,
			status = 'running',
			initialized = true,
			updated_at = NOW()
		WHERE user_id = $1
		RETURNING user_id, user_name, nickname, provider, status, initialized, created_at, updated_at
	`, uid, n).Scan(
		&b.BotID, &b.Name, &b.Nickname, &b.Provider, &b.Status, &b.Initialized, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "idx_user_accounts_active_name_ci") || strings.Contains(errStr, "duplicate key") {
			return Bot{}, fmt.Errorf("%w: %s", ErrBotNameTaken, n)
		}
		if err == sql.ErrNoRows {
			return Bot{}, fmt.Errorf("%w: %s", ErrBotNotFound, uid)
		}
		return Bot{}, err
	}
	return b, nil
}

func (s *PostgresStore) UpdateBotNickname(ctx context.Context, botID, nickname string) (Bot, error) {
	uid := strings.TrimSpace(botID)
	if uid == "" {
		return Bot{}, fmt.Errorf("user_id is required")
	}
	nick := strings.TrimSpace(nickname)
	var b Bot
	err := s.db.QueryRowContext(ctx, `
		UPDATE user_accounts
		SET nickname = $2,
			updated_at = NOW()
		WHERE user_id = $1
		RETURNING user_id, user_name, nickname, provider, status, initialized, created_at, updated_at
	`, uid, nick).Scan(
		&b.BotID, &b.Name, &b.Nickname, &b.Provider, &b.Status, &b.Initialized, &b.CreatedAt, &b.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return Bot{}, fmt.Errorf("%w: %s", ErrBotNotFound, uid)
	}
	if err != nil {
		return Bot{}, err
	}
	return b, nil
}

func (s *PostgresStore) EnsureTianDaoLaw(ctx context.Context, item TianDaoLaw) (TianDaoLaw, error) {
	item.LawKey = strings.TrimSpace(item.LawKey)
	if item.LawKey == "" {
		return TianDaoLaw{}, fmt.Errorf("law_key is required")
	}
	if strings.TrimSpace(item.ManifestJSON) == "" || strings.TrimSpace(item.ManifestSHA256) == "" {
		return TianDaoLaw{}, fmt.Errorf("manifest_json and manifest_sha256 are required")
	}
	var current TianDaoLaw
	err := s.db.QueryRowContext(ctx, `
		SELECT law_key, version, manifest_json, manifest_sha256, created_at
		FROM tian_dao_laws
		WHERE law_key = $1
	`, item.LawKey).Scan(&current.LawKey, &current.Version, &current.ManifestJSON, &current.ManifestSHA256, &current.CreatedAt)
	if err == nil {
		if current.Version != item.Version ||
			current.ManifestSHA256 != item.ManifestSHA256 ||
			current.ManifestJSON != item.ManifestJSON {
			return TianDaoLaw{}, fmt.Errorf("tian dao law %s is immutable and does not match existing manifest", item.LawKey)
		}
		return current, nil
	}
	if err != sql.ErrNoRows {
		return TianDaoLaw{}, err
	}
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO tian_dao_laws(law_key, version, manifest_json, manifest_sha256, created_at)
		VALUES($1, $2, $3, $4, NOW())
		RETURNING law_key, version, manifest_json, manifest_sha256, created_at
	`, item.LawKey, item.Version, item.ManifestJSON, item.ManifestSHA256).Scan(
		&item.LawKey, &item.Version, &item.ManifestJSON, &item.ManifestSHA256, &item.CreatedAt,
	)
	if err != nil {
		return TianDaoLaw{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetTianDaoLaw(ctx context.Context, lawKey string) (TianDaoLaw, error) {
	key := strings.TrimSpace(lawKey)
	if key == "" {
		return TianDaoLaw{}, fmt.Errorf("law_key is required")
	}
	var item TianDaoLaw
	err := s.db.QueryRowContext(ctx, `
		SELECT law_key, version, manifest_json, manifest_sha256, created_at
		FROM tian_dao_laws
		WHERE law_key = $1
	`, key).Scan(&item.LawKey, &item.Version, &item.ManifestJSON, &item.ManifestSHA256, &item.CreatedAt)
	if err != nil {
		return TianDaoLaw{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListTianDaoLaws(ctx context.Context) ([]TianDaoLaw, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT law_key, version, manifest_json, manifest_sha256, created_at
		FROM tian_dao_laws
		ORDER BY created_at ASC, law_key ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TianDaoLaw, 0)
	for rows.Next() {
		var item TianDaoLaw
		if err := rows.Scan(&item.LawKey, &item.Version, &item.ManifestJSON, &item.ManifestSHA256, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) AppendWorldTick(ctx context.Context, item WorldTickRecord) (WorldTickRecord, error) {
	if strings.TrimSpace(item.TriggerType) == "" {
		item.TriggerType = "scheduled"
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = time.Now().UTC()
	} else {
		item.StartedAt = item.StartedAt.UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorldTickRecord{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	prevHash := ""
	var prev WorldTickRecord
	prevErr := tx.QueryRowContext(ctx, `
		SELECT tick_id, started_at, duration_ms, trigger_type, replay_of_tick_id, prev_hash, entry_hash, status, error_text
		FROM world_ticks
		ORDER BY tick_id DESC, id DESC
		LIMIT 1
	`).Scan(
		&prev.TickID,
		&prev.StartedAt,
		&prev.DurationMS,
		&prev.TriggerType,
		&prev.ReplayOfTickID,
		&prev.PrevHash,
		&prev.EntryHash,
		&prev.Status,
		&prev.ErrorText,
	)
	if prevErr == nil {
		prevHash = strings.TrimSpace(prev.EntryHash)
		if prevHash == "" {
			prevHash = ComputeWorldTickHash(prev, strings.TrimSpace(prev.PrevHash))
		}
	} else if prevErr != sql.ErrNoRows {
		return WorldTickRecord{}, prevErr
	}

	item.PrevHash = prevHash
	item.EntryHash = ComputeWorldTickHash(item, prevHash)
	err = tx.QueryRowContext(ctx, `
		INSERT INTO world_ticks(tick_id, started_at, duration_ms, trigger_type, replay_of_tick_id, prev_hash, entry_hash, status, error_text)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, started_at
	`, item.TickID, item.StartedAt, item.DurationMS, item.TriggerType, item.ReplayOfTickID, item.PrevHash, item.EntryHash, item.Status, item.ErrorText).Scan(&item.ID, &item.StartedAt)
	if err != nil {
		return WorldTickRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorldTickRecord{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListWorldTicks(ctx context.Context, limit int) ([]WorldTickRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tick_id, started_at, duration_ms, trigger_type, replay_of_tick_id, prev_hash, entry_hash, status, error_text
		FROM world_ticks
		ORDER BY tick_id DESC, id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]WorldTickRecord, 0, limit)
	for rows.Next() {
		var it WorldTickRecord
		if err := rows.Scan(&it.ID, &it.TickID, &it.StartedAt, &it.DurationMS, &it.TriggerType, &it.ReplayOfTickID, &it.PrevHash, &it.EntryHash, &it.Status, &it.ErrorText); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetWorldTick(ctx context.Context, tickID int64) (WorldTickRecord, error) {
	if tickID <= 0 {
		return WorldTickRecord{}, fmt.Errorf("tick_id is required")
	}
	var it WorldTickRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tick_id, started_at, duration_ms, trigger_type, replay_of_tick_id, prev_hash, entry_hash, status, error_text
		FROM world_ticks
		WHERE tick_id = $1
		ORDER BY id DESC
		LIMIT 1
	`, tickID).Scan(&it.ID, &it.TickID, &it.StartedAt, &it.DurationMS, &it.TriggerType, &it.ReplayOfTickID, &it.PrevHash, &it.EntryHash, &it.Status, &it.ErrorText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorldTickRecord{}, fmt.Errorf("%w: %d", ErrWorldTickNotFound, tickID)
		}
		return WorldTickRecord{}, err
	}
	return it, nil
}

func (s *PostgresStore) GetFirstWorldTick(ctx context.Context) (WorldTickRecord, bool, error) {
	var it WorldTickRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tick_id, started_at, duration_ms, trigger_type, replay_of_tick_id, prev_hash, entry_hash, status, error_text
		FROM world_ticks
		ORDER BY tick_id ASC, id ASC
		LIMIT 1
	`).Scan(&it.ID, &it.TickID, &it.StartedAt, &it.DurationMS, &it.TriggerType, &it.ReplayOfTickID, &it.PrevHash, &it.EntryHash, &it.Status, &it.ErrorText)
	if err == sql.ErrNoRows {
		return WorldTickRecord{}, false, nil
	}
	if err != nil {
		return WorldTickRecord{}, false, err
	}
	return it, true, nil
}

func (s *PostgresStore) AppendWorldTickStep(ctx context.Context, item WorldTickStepRecord) (WorldTickStepRecord, error) {
	item.StepName = strings.TrimSpace(item.StepName)
	if item.StepName == "" {
		return WorldTickStepRecord{}, fmt.Errorf("step_name is required")
	}
	if item.Status == "" {
		item.Status = "ok"
	}
	var started any
	if !item.StartedAt.IsZero() {
		started = item.StartedAt
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO world_tick_steps(tick_id, step_name, started_at, duration_ms, status, error_text)
		VALUES($1, $2, COALESCE($3, NOW()), $4, $5, $6)
		RETURNING id, started_at
	`, item.TickID, item.StepName, started, item.DurationMS, item.Status, item.ErrorText).Scan(&item.ID, &item.StartedAt)
	if err != nil {
		return WorldTickStepRecord{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListWorldTickSteps(ctx context.Context, tickID int64, limit int) ([]WorldTickStepRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tick_id, step_name, started_at, duration_ms, status, error_text
		FROM world_tick_steps
		WHERE ($1 = 0 OR tick_id = $1)
		ORDER BY id DESC
		LIMIT $2
	`, tickID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]WorldTickStepRecord, 0, limit)
	for rows.Next() {
		var it WorldTickStepRecord
		if err := rows.Scan(&it.ID, &it.TickID, &it.StepName, &it.StartedAt, &it.DurationMS, &it.Status, &it.ErrorText); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpsertUserLifeState(ctx context.Context, item UserLifeState) (UserLifeState, error) {
	updated, _, err := s.ApplyUserLifeState(ctx, item, UserLifeStateAuditMeta{
		SourceModule: "life.state",
		SourceRef:    "store.upsert",
	})
	if err != nil {
		return UserLifeState{}, err
	}
	return updated, nil
}

func (s *PostgresStore) ApplyUserLifeState(ctx context.Context, item UserLifeState, audit UserLifeStateAuditMeta) (UserLifeState, *UserLifeStateTransition, error) {
	item.UserID = strings.TrimSpace(item.UserID)
	item.State = normalizeLifeState(item.State)
	if item.UserID == "" {
		return UserLifeState{}, nil, fmt.Errorf("user_id is required")
	}
	if item.State == "" {
		item.State = "alive"
	}
	audit.SourceModule = strings.TrimSpace(audit.SourceModule)
	audit.SourceRef = strings.TrimSpace(audit.SourceRef)
	audit.ActorUserID = strings.TrimSpace(audit.ActorUserID)
	if audit.SourceModule == "" {
		audit.SourceModule = "life.state"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserLifeState{}, nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// Serialize life-state writes per user_id so the first-write path cannot
	// race and append duplicate transition rows when no state row exists yet.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), 0)`, item.UserID); err != nil {
		return UserLifeState{}, nil, err
	}

	var (
		current UserLifeState
		found   bool
	)
	err = tx.QueryRowContext(ctx, `
		SELECT user_id, state, dying_since_tick, dead_at_tick, reason, updated_at
		FROM user_life_state
		WHERE user_id = $1
		FOR UPDATE
	`, item.UserID).Scan(&current.UserID, &current.State, &current.DyingSinceTick, &current.DeadAtTick, &current.Reason, &current.UpdatedAt)
	if err == nil {
		found = true
		if normalizeLifeState(current.State) == "dead" && item.State != "dead" {
			return UserLifeState{}, nil, fmt.Errorf("user life state is immutable once dead: %s", item.UserID)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return UserLifeState{}, nil, err
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO user_life_state(user_id, state, dying_since_tick, dead_at_tick, reason, updated_at)
		VALUES($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			state = EXCLUDED.state,
			dying_since_tick = EXCLUDED.dying_since_tick,
			dead_at_tick = EXCLUDED.dead_at_tick,
			reason = EXCLUDED.reason,
			updated_at = NOW()
		RETURNING user_id, state, dying_since_tick, dead_at_tick, reason, updated_at
	`, item.UserID, item.State, item.DyingSinceTick, item.DeadAtTick, item.Reason).
		Scan(&item.UserID, &item.State, &item.DyingSinceTick, &item.DeadAtTick, &item.Reason, &item.UpdatedAt)
	if err != nil {
		return UserLifeState{}, nil, err
	}

	var transition *UserLifeStateTransition
	prevState := ""
	if found {
		prevState = normalizeLifeState(current.State)
	}
	if !found || prevState != item.State {
		it := UserLifeStateTransition{
			UserID:             item.UserID,
			FromState:          prevState,
			ToState:            item.State,
			FromDyingSinceTick: current.DyingSinceTick,
			ToDyingSinceTick:   item.DyingSinceTick,
			FromDeadAtTick:     current.DeadAtTick,
			ToDeadAtTick:       item.DeadAtTick,
			FromReason:         strings.TrimSpace(current.Reason),
			ToReason:           strings.TrimSpace(item.Reason),
			TickID:             audit.TickID,
			SourceModule:       audit.SourceModule,
			SourceRef:          audit.SourceRef,
			ActorUserID:        audit.ActorUserID,
		}
		err = tx.QueryRowContext(ctx, `
			INSERT INTO user_life_state_transitions(
				user_id,
				from_state,
				to_state,
				from_dying_since_tick,
				to_dying_since_tick,
				from_dead_at_tick,
				to_dead_at_tick,
				from_reason,
				to_reason,
				tick_id,
				source_module,
				source_ref,
				actor_user_id,
				created_at
			)
			VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
			RETURNING id, created_at
		`, it.UserID, it.FromState, it.ToState, it.FromDyingSinceTick, it.ToDyingSinceTick, it.FromDeadAtTick, it.ToDeadAtTick, it.FromReason, it.ToReason, it.TickID, it.SourceModule, it.SourceRef, it.ActorUserID).
			Scan(&it.ID, &it.CreatedAt)
		if err != nil {
			return UserLifeState{}, nil, err
		}
		transition = &it
	}

	if err := tx.Commit(); err != nil {
		return UserLifeState{}, nil, err
	}
	return item, transition, nil
}

func (s *PostgresStore) GetUserLifeState(ctx context.Context, userID string) (UserLifeState, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return UserLifeState{}, fmt.Errorf("user_id is required")
	}
	var item UserLifeState
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, state, dying_since_tick, dead_at_tick, reason, updated_at
		FROM user_life_state
		WHERE user_id = $1
	`, userID).Scan(&item.UserID, &item.State, &item.DyingSinceTick, &item.DeadAtTick, &item.Reason, &item.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserLifeState{}, fmt.Errorf("%w: %s", ErrUserLifeStateNotFound, userID)
		}
		return UserLifeState{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListUserLifeStates(ctx context.Context, userID, state string, limit int) ([]UserLifeState, error) {
	userID = strings.TrimSpace(userID)
	state = normalizeLifeState(state)
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, state, dying_since_tick, dead_at_tick, reason, updated_at
		FROM user_life_state
		WHERE ($1 = '' OR user_id = $1) AND ($2 = '' OR state = $2)
		ORDER BY updated_at DESC, user_id ASC
		LIMIT $3
	`, userID, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UserLifeState, 0, limit)
	for rows.Next() {
		var it UserLifeState
		if err := rows.Scan(&it.UserID, &it.State, &it.DyingSinceTick, &it.DeadAtTick, &it.Reason, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListUserLifeStateTransitions(ctx context.Context, filter UserLifeStateTransitionFilter) ([]UserLifeStateTransition, error) {
	filter.UserID = strings.TrimSpace(filter.UserID)
	if strings.TrimSpace(filter.FromState) != "" {
		filter.FromState = normalizeLifeState(filter.FromState)
	}
	if strings.TrimSpace(filter.ToState) != "" {
		filter.ToState = normalizeLifeState(filter.ToState)
	}
	filter.SourceModule = strings.TrimSpace(filter.SourceModule)
	filter.ActorUserID = strings.TrimSpace(filter.ActorUserID)
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 2000 {
		filter.Limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id,
			user_id,
			from_state,
			to_state,
			from_dying_since_tick,
			to_dying_since_tick,
			from_dead_at_tick,
			to_dead_at_tick,
			from_reason,
			to_reason,
			tick_id,
			source_module,
			source_ref,
			actor_user_id,
			created_at
		FROM user_life_state_transitions
		WHERE ($1 = '' OR user_id = $1)
		  AND ($2 = '' OR from_state = $2)
		  AND ($3 = '' OR to_state = $3)
		  AND ($4 = 0 OR tick_id = $4)
		  AND ($5 = '' OR source_module = $5)
		  AND ($6 = '' OR actor_user_id = $6)
		ORDER BY created_at DESC, id DESC
		LIMIT $7
	`, filter.UserID, filter.FromState, filter.ToState, filter.TickID, filter.SourceModule, filter.ActorUserID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UserLifeStateTransition, 0, filter.Limit)
	for rows.Next() {
		var it UserLifeStateTransition
		if err := rows.Scan(
			&it.ID,
			&it.UserID,
			&it.FromState,
			&it.ToState,
			&it.FromDyingSinceTick,
			&it.ToDyingSinceTick,
			&it.FromDeadAtTick,
			&it.ToDeadAtTick,
			&it.FromReason,
			&it.ToReason,
			&it.TickID,
			&it.SourceModule,
			&it.SourceRef,
			&it.ActorUserID,
			&it.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) AppendCostEvent(ctx context.Context, item CostEvent) (CostEvent, error) {
	item.UserID = strings.TrimSpace(item.UserID)
	item.CostType = strings.TrimSpace(item.CostType)
	if item.UserID == "" {
		return CostEvent{}, fmt.Errorf("user_id is required")
	}
	if item.CostType == "" {
		return CostEvent{}, fmt.Errorf("cost_type is required")
	}
	var created any
	if !item.CreatedAt.IsZero() {
		created = item.CreatedAt
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO cost_events(user_id, tick_id, cost_type, amount, units, meta_json, created_at)
		VALUES($1, $2, $3, $4, $5, $6, COALESCE($7, NOW()))
		RETURNING id, created_at
	`, item.UserID, item.TickID, item.CostType, item.Amount, item.Units, item.MetaJSON, created).Scan(&item.ID, &item.CreatedAt)
	if err != nil {
		return CostEvent{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListCostEvents(ctx context.Context, userID string, limit int) ([]CostEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	userID = strings.TrimSpace(userID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, tick_id, cost_type, amount, units, meta_json, created_at
		FROM cost_events
		WHERE ($1 = '' OR user_id = $1)
		ORDER BY id DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CostEvent, 0, limit)
	for rows.Next() {
		var it CostEvent
		if err := rows.Scan(&it.ID, &it.UserID, &it.TickID, &it.CostType, &it.Amount, &it.Units, &it.MetaJSON, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListCostEventsByInvolvement(ctx context.Context, userID string, limit int) ([]CostEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return s.ListCostEvents(ctx, "", limit)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, tick_id, cost_type, amount, units, meta_json, created_at
		FROM cost_events
		WHERE user_id = $1
		   OR cost_event_to_user_id(meta_json) = $1
		ORDER BY id DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CostEvent, 0, limit)
	for rows.Next() {
		var it CostEvent
		if err := rows.Scan(&it.ID, &it.UserID, &it.TickID, &it.CostType, &it.Amount, &it.Units, &it.MetaJSON, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) SendMail(ctx context.Context, input MailSendInput) (MailSendResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MailSendResult{}, err
	}
	defer tx.Rollback()

	var messageID int64
	var sentAt time.Time
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO mail_messages(sender_address, subject, body, reply_to_mailbox_id)
		VALUES($1, $2, $3, $4)
		RETURNING id, sent_at
	`, input.From, input.Subject, input.Body, input.ReplyToMailboxID).Scan(&messageID, &sentAt); err != nil {
		return MailSendResult{}, err
	}

	for _, recipient := range input.To {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mail_mailboxes(message_id, owner_address, folder, to_address, is_read)
			VALUES($1, $2, 'inbox', $3, false)
		`, messageID, recipient, recipient); err != nil {
			return MailSendResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mail_mailboxes(message_id, owner_address, folder, to_address, is_read, read_at)
			VALUES($1, $2, 'outbox', $3, true, NOW())
		`, messageID, input.From, recipient); err != nil {
			return MailSendResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return MailSendResult{}, err
	}
	return MailSendResult{
		MessageID:        messageID,
		From:             input.From,
		To:               input.To,
		Subject:          input.Subject,
		ReplyToMailboxID: input.ReplyToMailboxID,
		SentAt:           sentAt,
	}, nil
}

func (s *PostgresStore) ListMailbox(ctx context.Context, ownerAddress, folder, scope, keyword string, fromTime, toTime *time.Time, limit int) ([]MailItem, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 5000 {
		limit = 5000
	}
	var fromArg any
	if fromTime != nil {
		fromArg = *fromTime
	}
	var toArg any
	if toTime != nil {
		toArg = *toTime
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT mb.id, mb.message_id, mb.owner_address, mb.folder, mm.sender_address, mb.to_address,
		       mm.subject, mm.body, mm.reply_to_mailbox_id, mb.is_read, mb.read_at, mm.sent_at
		FROM mail_mailboxes mb
		JOIN mail_messages mm ON mm.id = mb.message_id
		WHERE mb.owner_address = $1
		  AND ($2 = '' OR mb.folder = $2)
		  AND ($3 = '' OR ($3 = 'read' AND mb.is_read = true) OR ($3 = 'unread' AND mb.is_read = false))
		  AND ($4 = '' OR mm.subject ILIKE '%' || $4 || '%' OR mm.body ILIKE '%' || $4 || '%')
		  AND ($5::timestamptz IS NULL OR mm.sent_at >= $5)
		  AND ($6::timestamptz IS NULL OR mm.sent_at <= $6)
		ORDER BY mm.sent_at DESC, mb.id DESC
		LIMIT $7
	`, ownerAddress, folder, scope, keyword, fromArg, toArg, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MailItem, 0)
	for rows.Next() {
		var it MailItem
		if err := rows.Scan(&it.MailboxID, &it.MessageID, &it.OwnerAddress, &it.Folder, &it.FromAddress, &it.ToAddress, &it.Subject, &it.Body, &it.ReplyToMailboxID, &it.IsRead, &it.ReadAt, &it.SentAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetMailboxItem(ctx context.Context, mailboxID int64) (MailItem, error) {
	var item MailItem
	err := s.db.QueryRowContext(ctx, `
		SELECT mb.id, mb.message_id, mb.owner_address, mb.folder, mm.sender_address, mb.to_address,
		       mm.subject, mm.body, mm.reply_to_mailbox_id, mb.is_read, mb.read_at, mm.sent_at
		FROM mail_mailboxes mb
		JOIN mail_messages mm ON mm.id = mb.message_id
		WHERE mb.id = $1
	`, mailboxID).Scan(&item.MailboxID, &item.MessageID, &item.OwnerAddress, &item.Folder, &item.FromAddress, &item.ToAddress, &item.Subject, &item.Body, &item.ReplyToMailboxID, &item.IsRead, &item.ReadAt, &item.SentAt)
	if err == sql.ErrNoRows {
		return MailItem{}, fmt.Errorf("mailbox item not found: %d", mailboxID)
	}
	if err != nil {
		return MailItem{}, err
	}
	return item, nil
}

func (s *PostgresStore) MarkMailboxRead(ctx context.Context, ownerAddress string, mailboxIDs []int64) error {
	if len(mailboxIDs) == 0 {
		return nil
	}
	ids := make([]any, 0, len(mailboxIDs))
	holders := make([]string, 0, len(mailboxIDs))
	for i, id := range mailboxIDs {
		holders = append(holders, fmt.Sprintf("$%d", i+2))
		ids = append(ids, id)
	}
	args := []any{ownerAddress}
	args = append(args, ids...)
	_, err := s.db.ExecContext(ctx, `
		UPDATE mail_mailboxes
		SET is_read = true, read_at = NOW()
		WHERE owner_address = $1
		  AND folder = 'inbox'
		  AND id IN (`+strings.Join(holders, ",")+`)`,
		args...,
	)
	return err
}

func (s *PostgresStore) UpdateMailMessage(ctx context.Context, messageID int64, subject, body string, sentAt time.Time) error {
	if messageID <= 0 {
		return fmt.Errorf("message_id is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE mail_messages
		SET subject = $2,
		    body = $3,
		    sent_at = $4
		WHERE id = $1
	`, messageID, strings.TrimSpace(subject), strings.TrimSpace(body), sentAt.UTC())
	return err
}

func normalizedArchiveCategories(categories []string) []string {
	seen := make(map[string]struct{}, len(categories))
	out := make([]string, 0, len(categories))
	for _, category := range categories {
		normalized := normalizeSystemMailArchiveCategory(category)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

func mailArchiveCategorySQL() string {
	return `CASE
		WHEN mm.sender_address = 'clawcolony-admin' AND mm.subject LIKE '[WORLD-COST-ALERT]%' THEN 'world_cost'
		WHEN mm.sender_address = 'clawcolony-admin' AND mm.subject LIKE '[LOW-TOKEN]%' THEN 'low_token'
		WHEN mm.sender_address = 'clawcolony-admin' AND mm.subject LIKE '[AUTONOMY-LOOP]%' THEN 'autonomy_loop'
		WHEN mm.sender_address = 'clawcolony-admin' AND mm.subject LIKE '[COMMUNITY-COLLAB]%' THEN 'community_collab'
		ELSE ''
	END`
}

func placeholderList(start, count int) string {
	if count <= 0 {
		return ""
	}
	holders := make([]string, 0, count)
	for i := 0; i < count; i++ {
		holders = append(holders, fmt.Sprintf("$%d", start+i))
	}
	return strings.Join(holders, ", ")
}

func valuesTable(placeholders string) string {
	if strings.TrimSpace(placeholders) == "" {
		return ""
	}
	parts := strings.Split(placeholders, ", ")
	rows := make([]string, 0, len(parts))
	for _, part := range parts {
		rows = append(rows, "("+part+")")
	}
	return strings.Join(rows, ", ")
}

func (s *PostgresStore) GetNotificationDeliveryState(ctx context.Context, ownerAddress, category string) (NotificationDeliveryState, bool, error) {
	ownerAddress = strings.TrimSpace(ownerAddress)
	category = strings.TrimSpace(category)
	if ownerAddress == "" || category == "" {
		return NotificationDeliveryState{}, false, nil
	}
	var state NotificationDeliveryState
	var lastSeenAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT owner_address, category, state_hash, last_sent_at, last_reminded_at, last_seen_at,
		       outstanding_message_id, outstanding_mailbox_id, updated_at
		FROM notification_delivery_state
		WHERE owner_address = $1 AND category = $2
	`, ownerAddress, category).Scan(
		&state.OwnerAddress,
		&state.Category,
		&state.StateHash,
		&state.LastSentAt,
		&state.LastRemindedAt,
		&lastSeenAt,
		&state.OutstandingMessageID,
		&state.OutstandingMailboxID,
		&state.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return NotificationDeliveryState{}, false, nil
	}
	if err != nil {
		return NotificationDeliveryState{}, false, err
	}
	if lastSeenAt.Valid {
		state.LastSeenAt = lastSeenAt.Time.UTC()
	}
	return state, true, nil
}

func (s *PostgresStore) UpsertNotificationDeliveryState(ctx context.Context, state NotificationDeliveryState) (NotificationDeliveryState, error) {
	state.OwnerAddress = strings.TrimSpace(state.OwnerAddress)
	state.Category = strings.TrimSpace(state.Category)
	if state.OwnerAddress == "" || state.Category == "" {
		return NotificationDeliveryState{}, fmt.Errorf("owner_address and category are required")
	}
	var lastSeenAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO notification_delivery_state(
			owner_address, category, state_hash, last_sent_at, last_reminded_at, last_seen_at,
			outstanding_message_id, outstanding_mailbox_id, updated_at
		)
		VALUES($1, $2, $3, COALESCE($4, NOW()), COALESCE($5, NOW()), $6, $7, $8, NOW())
		ON CONFLICT (owner_address, category) DO UPDATE SET
			state_hash = EXCLUDED.state_hash,
			last_sent_at = EXCLUDED.last_sent_at,
			last_reminded_at = EXCLUDED.last_reminded_at,
			last_seen_at = EXCLUDED.last_seen_at,
			outstanding_message_id = EXCLUDED.outstanding_message_id,
			outstanding_mailbox_id = EXCLUDED.outstanding_mailbox_id,
			updated_at = NOW()
		RETURNING owner_address, category, state_hash, last_sent_at, last_reminded_at, last_seen_at,
		          outstanding_message_id, outstanding_mailbox_id, updated_at
	`, state.OwnerAddress, state.Category, state.StateHash, nullIfZeroTime(state.LastSentAt), nullIfZeroTime(state.LastRemindedAt), nullIfZeroTime(state.LastSeenAt), state.OutstandingMessageID, state.OutstandingMailboxID).Scan(
		&state.OwnerAddress,
		&state.Category,
		&state.StateHash,
		&state.LastSentAt,
		&state.LastRemindedAt,
		&lastSeenAt,
		&state.OutstandingMessageID,
		&state.OutstandingMailboxID,
		&state.UpdatedAt,
	)
	if err != nil {
		return NotificationDeliveryState{}, err
	}
	if lastSeenAt.Valid {
		state.LastSeenAt = lastSeenAt.Time.UTC()
	} else {
		state.LastSeenAt = time.Time{}
	}
	return state, nil
}

func (s *PostgresStore) DeleteNotificationDeliveryState(ctx context.Context, ownerAddress, category string) error {
	ownerAddress = strings.TrimSpace(ownerAddress)
	category = strings.TrimSpace(category)
	if ownerAddress == "" || category == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM notification_delivery_state
		WHERE owner_address = $1 AND category = $2
	`, ownerAddress, category)
	return err
}

func (s *PostgresStore) PreviewSystemMailArchive(ctx context.Context, categories []string) (MailArchivePreview, error) {
	categories = normalizedArchiveCategories(categories)
	if len(categories) == 0 {
		return MailArchivePreview{}, nil
	}
	categoryPlaceholders := placeholderList(1, len(categories))
	valuesExpr := valuesTable(categoryPlaceholders)
	args := make([]any, 0, len(categories))
	for _, category := range categories {
		args = append(args, category)
	}
	statsQuery := `
		WITH category_input(category) AS (VALUES ` + valuesExpr + `),
		categorized AS (
			SELECT
				mb.id,
				mb.message_id,
				mb.owner_address,
				` + mailArchiveCategorySQL() + ` AS category,
				ROW_NUMBER() OVER (
					PARTITION BY mb.owner_address, ` + mailArchiveCategorySQL() + `
					ORDER BY mm.sent_at DESC, mb.id DESC
				) AS rn
			FROM mail_mailboxes mb
			JOIN mail_messages mm ON mm.id = mb.message_id
			WHERE mb.folder = 'inbox'
		),
		inbox_stats AS (
			SELECT
				category,
				COUNT(*) FILTER (WHERE rn = 1) AS keep_count,
				COUNT(*) FILTER (WHERE rn > 1) AS archive_count
			FROM categorized
			WHERE category IN (SELECT category FROM category_input)
			GROUP BY category
		),
		outbox_stats AS (
			SELECT
				d.category,
				COUNT(*) AS archive_count
			FROM categorized d
			JOIN mail_mailboxes outbox
			  ON outbox.message_id = d.message_id
			 AND outbox.folder = 'outbox'
			 AND outbox.owner_address = 'clawcolony-admin'
			 AND outbox.to_address = d.owner_address
			WHERE d.rn > 1
			  AND d.category IN (SELECT category FROM category_input)
			GROUP BY d.category
		)
		SELECT
			ci.category,
			COALESCE(is1.keep_count, 0),
			COALESCE(is1.archive_count, 0),
			COALESCE(os1.archive_count, 0)
		FROM category_input ci
		LEFT JOIN inbox_stats is1 ON is1.category = ci.category
		LEFT JOIN outbox_stats os1 ON os1.category = ci.category
		ORDER BY ci.category ASC
	`
	rows, err := s.db.QueryContext(ctx, statsQuery, args...)
	if err != nil {
		return MailArchivePreview{}, err
	}
	defer rows.Close()
	stats := make([]MailArchiveCategoryStat, 0, len(categories))
	for rows.Next() {
		var stat MailArchiveCategoryStat
		if err := rows.Scan(&stat.Category, &stat.InboxKeepCount, &stat.InboxArchiveCnt, &stat.OutboxArchiveCt); err != nil {
			return MailArchivePreview{}, err
		}
		stats = append(stats, stat)
	}
	if err := rows.Err(); err != nil {
		return MailArchivePreview{}, err
	}
	countQuery := `
		WITH category_input(category) AS (VALUES ` + valuesExpr + `),
		categorized AS (
			SELECT
				mb.id,
				mb.message_id,
				mb.owner_address,
				` + mailArchiveCategorySQL() + ` AS category,
				ROW_NUMBER() OVER (
					PARTITION BY mb.owner_address, ` + mailArchiveCategorySQL() + `
					ORDER BY mm.sent_at DESC, mb.id DESC
				) AS rn
			FROM mail_mailboxes mb
			JOIN mail_messages mm ON mm.id = mb.message_id
			WHERE mb.folder = 'inbox'
		),
		duplicate_inbox AS (
			SELECT id, message_id, owner_address, category
			FROM categorized
			WHERE rn > 1
			  AND category IN (SELECT category FROM category_input)
		),
		archive_mailboxes AS (
			SELECT id AS mailbox_id, message_id
			FROM duplicate_inbox
			UNION
			SELECT outbox.id AS mailbox_id, outbox.message_id
			FROM duplicate_inbox d
			JOIN mail_mailboxes outbox
			  ON outbox.message_id = d.message_id
			 AND outbox.folder = 'outbox'
			 AND outbox.owner_address = 'clawcolony-admin'
			 AND outbox.to_address = d.owner_address
		)
		SELECT
			COALESCE((SELECT COUNT(*) FROM archive_mailboxes), 0),
			COALESCE((SELECT COUNT(DISTINCT message_id) FROM archive_mailboxes), 0)
	`
	var preview MailArchivePreview
	preview.Categories = stats
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&preview.ArchiveMailboxCount, &preview.ArchiveMessageCount); err != nil {
		return MailArchivePreview{}, err
	}
	return preview, nil
}

func (s *PostgresStore) ArchiveSystemMailBatch(ctx context.Context, input MailArchiveBatchInput) (MailArchiveBatchResult, error) {
	categories := normalizedArchiveCategories(input.Categories)
	if len(categories) == 0 {
		return MailArchiveBatchResult{BatchID: strings.TrimSpace(input.BatchID)}, nil
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 10000
	}
	args := make([]any, 0, len(categories)+1)
	for _, category := range categories {
		args = append(args, category)
	}
	args = append(args, limit)
	categoryPlaceholders := placeholderList(1, len(categories))
	candidateQuery := `
		WITH categorized AS (
			SELECT
				mb.id,
				mb.message_id,
				mb.owner_address,
				` + mailArchiveCategorySQL() + ` AS category,
				ROW_NUMBER() OVER (
					PARTITION BY mb.owner_address, ` + mailArchiveCategorySQL() + `
					ORDER BY mm.sent_at DESC, mb.id DESC
				) AS rn
			FROM mail_mailboxes mb
			JOIN mail_messages mm ON mm.id = mb.message_id
			WHERE mb.folder = 'inbox'
		)
		SELECT id, message_id, owner_address, category
		FROM categorized
		WHERE rn > 1
		  AND category IN (` + categoryPlaceholders + `)
		ORDER BY id ASC
		LIMIT $` + fmt.Sprintf("%d", len(args)) + `
	`
	type candidateRow struct {
		MailboxID    int64
		MessageID    int64
		OwnerAddress string
		Category     string
	}
	rows, err := s.db.QueryContext(ctx, candidateQuery, args...)
	if err != nil {
		return MailArchiveBatchResult{}, err
	}
	defer rows.Close()
	candidates := make([]candidateRow, 0, limit)
	statsByCategory := make(map[string]*MailArchiveCategoryStat, len(categories))
	for rows.Next() {
		var row candidateRow
		if err := rows.Scan(&row.MailboxID, &row.MessageID, &row.OwnerAddress, &row.Category); err != nil {
			return MailArchiveBatchResult{}, err
		}
		candidates = append(candidates, row)
		stat := statsByCategory[row.Category]
		if stat == nil {
			stat = &MailArchiveCategoryStat{Category: row.Category}
			statsByCategory[row.Category] = stat
		}
		stat.InboxArchiveCnt++
	}
	if err := rows.Err(); err != nil {
		return MailArchiveBatchResult{}, err
	}
	if len(candidates) == 0 {
		return MailArchiveBatchResult{BatchID: strings.TrimSpace(input.BatchID)}, nil
	}
	selectedMailboxIDs := make(map[int64]struct{}, len(candidates)*2)
	impactedMessageIDs := make(map[int64]struct{}, len(candidates))
	lastInboxMailboxID := int64(0)
	for _, candidate := range candidates {
		selectedMailboxIDs[candidate.MailboxID] = struct{}{}
		impactedMessageIDs[candidate.MessageID] = struct{}{}
		if candidate.MailboxID > lastInboxMailboxID {
			lastInboxMailboxID = candidate.MailboxID
		}
	}
	matchArgs := make([]any, 0, len(candidates)*2)
	cond := make([]string, 0, len(candidates))
	argi := 1
	for _, candidate := range candidates {
		cond = append(cond, fmt.Sprintf("(message_id = $%d AND folder = 'outbox' AND owner_address = 'clawcolony-admin' AND to_address = $%d)", argi, argi+1))
		matchArgs = append(matchArgs, candidate.MessageID, candidate.OwnerAddress)
		argi += 2
	}
	outboxQuery := `
		SELECT id, message_id, to_address
		FROM mail_mailboxes
		WHERE ` + strings.Join(cond, " OR ")
	outboxRows, err := s.db.QueryContext(ctx, outboxQuery, matchArgs...)
	if err != nil {
		return MailArchiveBatchResult{}, err
	}
	for outboxRows.Next() {
		var mailboxID, messageID int64
		var toAddress string
		if err := outboxRows.Scan(&mailboxID, &messageID, &toAddress); err != nil {
			outboxRows.Close()
			return MailArchiveBatchResult{}, err
		}
		selectedMailboxIDs[mailboxID] = struct{}{}
		for _, candidate := range candidates {
			if candidate.MessageID == messageID && strings.TrimSpace(candidate.OwnerAddress) == strings.TrimSpace(toAddress) {
				if stat := statsByCategory[candidate.Category]; stat != nil {
					stat.OutboxArchiveCt++
				}
				break
			}
		}
	}
	if err := outboxRows.Err(); err != nil {
		outboxRows.Close()
		return MailArchiveBatchResult{}, err
	}
	outboxRows.Close()
	mailboxIDs := make([]int64, 0, len(selectedMailboxIDs))
	for mailboxID := range selectedMailboxIDs {
		mailboxIDs = append(mailboxIDs, mailboxID)
	}
	sort.Slice(mailboxIDs, func(i, j int) bool { return mailboxIDs[i] < mailboxIDs[j] })
	messageIDs := make([]int64, 0, len(impactedMessageIDs))
	for messageID := range impactedMessageIDs {
		messageIDs = append(messageIDs, messageID)
	}
	sort.Slice(messageIDs, func(i, j int) bool { return messageIDs[i] < messageIDs[j] })
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MailArchiveBatchResult{}, err
	}
	defer tx.Rollback()
	reason := "system_mail_duplicate_trim"
	batchID := strings.TrimSpace(input.BatchID)
	if batchID == "" {
		batchID = fmt.Sprintf("mail-archive-%d", time.Now().UTC().Unix())
	}
	messagePlaceholders := placeholderList(4, len(messageIDs))
	mailboxPlaceholders := placeholderList(4, len(mailboxIDs))
	if len(messageIDs) > 0 {
		copyArgs := []any{batchID, reason, time.Now().UTC()}
		for _, messageID := range messageIDs {
			copyArgs = append(copyArgs, messageID)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mail_messages_archive(id, sender_address, subject, body, reply_to_mailbox_id, sent_at, archived_at, archive_reason, archive_batch_id)
			SELECT id, sender_address, subject, body, reply_to_mailbox_id, sent_at, $3, $2, $1
			FROM mail_messages
			WHERE id IN (`+messagePlaceholders+`)
			ON CONFLICT (id) DO NOTHING
		`, copyArgs...); err != nil {
			return MailArchiveBatchResult{}, err
		}
	}
	mailboxArgs := []any{batchID, reason, time.Now().UTC()}
	for _, mailboxID := range mailboxIDs {
		mailboxArgs = append(mailboxArgs, mailboxID)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO mail_mailboxes_archive(id, message_id, owner_address, folder, to_address, is_read, read_at, created_at, archived_at, archive_reason, archive_batch_id)
		SELECT id, message_id, owner_address, folder, to_address, is_read, read_at, created_at, $3, $2, $1
		FROM mail_mailboxes
		WHERE id IN (`+mailboxPlaceholders+`)
		ON CONFLICT (id) DO NOTHING
	`, mailboxArgs...); err != nil {
		return MailArchiveBatchResult{}, err
	}
	deleteArgs := make([]any, 0, len(mailboxIDs))
	for _, mailboxID := range mailboxIDs {
		deleteArgs = append(deleteArgs, mailboxID)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM mail_mailboxes
		WHERE id IN (`+placeholderList(1, len(deleteArgs))+`)
	`, deleteArgs...); err != nil {
		return MailArchiveBatchResult{}, err
	}
	deletableMessageIDs := make([]int64, 0, len(messageIDs))
	if len(messageIDs) > 0 {
		checkArgs := make([]any, 0, len(messageIDs))
		for _, messageID := range messageIDs {
			checkArgs = append(checkArgs, messageID)
		}
		checkRows, err := tx.QueryContext(ctx, `
			SELECT id
			FROM mail_messages
			WHERE id IN (`+placeholderList(1, len(checkArgs))+`)
			  AND NOT EXISTS (
				SELECT 1
				FROM mail_mailboxes
				WHERE mail_mailboxes.message_id = mail_messages.id
			  )
		`, checkArgs...)
		if err != nil {
			return MailArchiveBatchResult{}, err
		}
		for checkRows.Next() {
			var messageID int64
			if err := checkRows.Scan(&messageID); err != nil {
				checkRows.Close()
				return MailArchiveBatchResult{}, err
			}
			deletableMessageIDs = append(deletableMessageIDs, messageID)
		}
		if err := checkRows.Err(); err != nil {
			checkRows.Close()
			return MailArchiveBatchResult{}, err
		}
		checkRows.Close()
	}
	if len(deletableMessageIDs) > 0 {
		deleteMessageArgs := make([]any, 0, len(deletableMessageIDs))
		for _, messageID := range deletableMessageIDs {
			deleteMessageArgs = append(deleteMessageArgs, messageID)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM mail_messages
			WHERE id IN (`+placeholderList(1, len(deleteMessageArgs))+`)
		`, deleteMessageArgs...); err != nil {
			return MailArchiveBatchResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MailArchiveBatchResult{}, err
	}
	stats := make([]MailArchiveCategoryStat, 0, len(categories))
	for _, category := range categories {
		if stat, ok := statsByCategory[category]; ok {
			stats = append(stats, *stat)
		}
	}
	preview, err := s.PreviewSystemMailArchive(ctx, categories)
	if err != nil {
		return MailArchiveBatchResult{}, err
	}
	return MailArchiveBatchResult{
		BatchID:             batchID,
		ArchivedMailboxIDs:  mailboxIDs,
		ArchivedMessageIDs:  deletableMessageIDs,
		ArchiveMailboxCount: int64(len(mailboxIDs)),
		ArchiveMessageCount: int64(len(deletableMessageIDs)),
		LastInboxMailboxID:  lastInboxMailboxID,
		HasMore:             preview.ArchiveMailboxCount > 0,
		Categories:          stats,
	}, nil
}

func (s *PostgresStore) UpsertMailContact(ctx context.Context, c MailContact) (MailContact, error) {
	tags := strings.Join(c.Tags, ",")
	skills := strings.Join(c.Skills, ",")
	c.UpdatedAt = time.Now().UTC()
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO mail_contacts(owner_address, contact_address, display_name, tags, role, skills, current_project, availability, updated_at)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (owner_address, contact_address) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			tags = EXCLUDED.tags,
			role = EXCLUDED.role,
			skills = EXCLUDED.skills,
			current_project = EXCLUDED.current_project,
			availability = EXCLUDED.availability,
			updated_at = NOW()
		RETURNING updated_at
	`, c.OwnerAddress, c.ContactAddress, c.DisplayName, tags, c.Role, skills, c.CurrentProject, c.Availability).Scan(&c.UpdatedAt)
	if err != nil {
		return MailContact{}, err
	}
	return c, nil
}

func (s *PostgresStore) ListMailContacts(ctx context.Context, ownerAddress, keyword string, limit int) ([]MailContact, error) {
	return s.listMailContacts(ctx, ownerAddress, keyword, nil, nil, limit)
}

func (s *PostgresStore) ListMailContactsUpdated(ctx context.Context, ownerAddress, keyword string, fromTime, toTime *time.Time, limit int) ([]MailContact, error) {
	return s.listMailContacts(ctx, ownerAddress, keyword, fromTime, toTime, limit)
}

func (s *PostgresStore) listMailContacts(ctx context.Context, ownerAddress, keyword string, fromTime, toTime *time.Time, limit int) ([]MailContact, error) {
	if limit <= 0 {
		limit = 100
	}
	var fromArg any
	if fromTime != nil {
		fromArg = *fromTime
	}
	var toArg any
	if toTime != nil {
		toArg = *toTime
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT owner_address, contact_address, display_name, tags, role, skills, current_project, availability, updated_at
		FROM mail_contacts
		WHERE owner_address = $1
		  AND ($2 = '' OR contact_address ILIKE '%' || $2 || '%' OR display_name ILIKE '%' || $2 || '%' OR tags ILIKE '%' || $2 || '%' OR role ILIKE '%' || $2 || '%' OR skills ILIKE '%' || $2 || '%' OR current_project ILIKE '%' || $2 || '%')
		  AND ($3::timestamptz IS NULL OR updated_at >= $3)
		  AND ($4::timestamptz IS NULL OR updated_at <= $4)
		ORDER BY updated_at DESC, contact_address ASC
		LIMIT $5
	`, ownerAddress, keyword, fromArg, toArg, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MailContact, 0)
	for rows.Next() {
		var c MailContact
		var tags string
		var skills string
		if err := rows.Scan(&c.OwnerAddress, &c.ContactAddress, &c.DisplayName, &tags, &c.Role, &skills, &c.CurrentProject, &c.Availability, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if strings.TrimSpace(tags) != "" {
			c.Tags = strings.Split(tags, ",")
		}
		if strings.TrimSpace(skills) != "" {
			c.Skills = strings.Split(skills, ",")
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListTokenAccounts(ctx context.Context) ([]TokenAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, balance, updated_at FROM token_accounts ORDER BY user_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TokenAccount, 0)
	for rows.Next() {
		var a TokenAccount
		if err := rows.Scan(&a.BotID, &a.Balance, &a.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func (s *PostgresStore) Recharge(ctx context.Context, botID string, amount int64) (TokenLedger, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TokenLedger{}, err
	}
	defer tx.Rollback()
	if err := s.ensureBotTx(ctx, tx, botID); err != nil {
		return TokenLedger{}, err
	}
	var balance int64
	if err := tx.QueryRowContext(ctx, `SELECT balance FROM token_accounts WHERE user_id = $1 FOR UPDATE`, botID).Scan(&balance); err != nil {
		return TokenLedger{}, err
	}
	if amount > 0 && balance > (math.MaxInt64-amount) {
		return TokenLedger{}, ErrBalanceOverflow
	}
	balance += amount
	if _, err := tx.ExecContext(ctx, `UPDATE token_accounts SET balance = $2, updated_at = NOW() WHERE user_id = $1`, botID, balance); err != nil {
		return TokenLedger{}, err
	}
	var entry TokenLedger
	err = tx.QueryRowContext(ctx, `
		INSERT INTO token_ledger(user_id, op_type, amount, balance_after)
		VALUES($1, 'recharge', $2, $3)
		RETURNING id, user_id, op_type, amount, balance_after, created_at
	`, botID, amount, balance).Scan(&entry.ID, &entry.BotID, &entry.OpType, &entry.Amount, &entry.BalanceAfter, &entry.CreatedAt)
	if err != nil {
		return TokenLedger{}, err
	}
	if err := tx.Commit(); err != nil {
		return TokenLedger{}, err
	}
	return entry, nil
}

func (s *PostgresStore) Consume(ctx context.Context, botID string, amount int64) (TokenLedger, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TokenLedger{}, err
	}
	defer tx.Rollback()
	if err := s.ensureBotTx(ctx, tx, botID); err != nil {
		return TokenLedger{}, err
	}
	var balance int64
	if err := tx.QueryRowContext(ctx, `SELECT balance FROM token_accounts WHERE user_id = $1 FOR UPDATE`, botID).Scan(&balance); err != nil {
		return TokenLedger{}, err
	}
	if balance < amount {
		return TokenLedger{}, ErrInsufficientBalance
	}
	balance -= amount
	if _, err := tx.ExecContext(ctx, `UPDATE token_accounts SET balance = $2, updated_at = NOW() WHERE user_id = $1`, botID, balance); err != nil {
		return TokenLedger{}, err
	}
	var entry TokenLedger
	err = tx.QueryRowContext(ctx, `
		INSERT INTO token_ledger(user_id, op_type, amount, balance_after)
		VALUES($1, 'consume', $2, $3)
		RETURNING id, user_id, op_type, amount, balance_after, created_at
	`, botID, amount, balance).Scan(&entry.ID, &entry.BotID, &entry.OpType, &entry.Amount, &entry.BalanceAfter, &entry.CreatedAt)
	if err != nil {
		return TokenLedger{}, err
	}
	if err := tx.Commit(); err != nil {
		return TokenLedger{}, err
	}
	return entry, nil
}

func (s *PostgresStore) Transfer(ctx context.Context, fromBotID, toBotID string, amount int64) (TokenTransfer, error) {
	return s.transfer(ctx, fromBotID, toBotID, amount, false)
}

func (s *PostgresStore) TransferWithFloor(ctx context.Context, fromBotID, toBotID string, amount int64) (TokenTransfer, error) {
	return s.transfer(ctx, fromBotID, toBotID, amount, true)
}

func (s *PostgresStore) transfer(ctx context.Context, fromBotID, toBotID string, amount int64, floor bool) (TokenTransfer, error) {
	fromBotID = strings.TrimSpace(fromBotID)
	toBotID = strings.TrimSpace(toBotID)
	if fromBotID == "" || toBotID == "" || amount <= 0 || fromBotID == toBotID {
		return TokenTransfer{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TokenTransfer{}, err
	}
	defer tx.Rollback()
	if err := s.ensureBotTx(ctx, tx, fromBotID); err != nil {
		return TokenTransfer{}, err
	}
	if err := s.ensureBotTx(ctx, tx, toBotID); err != nil {
		return TokenTransfer{}, err
	}
	ids := []string{fromBotID, toBotID}
	sort.Strings(ids)
	balances := map[string]int64{}
	for _, id := range ids {
		var balance int64
		if err := tx.QueryRowContext(ctx, `SELECT balance FROM token_accounts WHERE user_id = $1 FOR UPDATE`, id).Scan(&balance); err != nil {
			return TokenTransfer{}, err
		}
		balances[id] = balance
	}
	deducted := amount
	if balances[fromBotID] < deducted && !floor {
		return TokenTransfer{}, ErrInsufficientBalance
	}
	if balances[fromBotID] < deducted && floor {
		deducted = balances[fromBotID]
	}
	if deducted <= 0 {
		if err := tx.Commit(); err != nil {
			return TokenTransfer{}, err
		}
		return TokenTransfer{}, nil
	}
	fromBalance := balances[fromBotID] - deducted
	toBalance := balances[toBotID]
	if toBalance > (math.MaxInt64 - deducted) {
		return TokenTransfer{}, ErrBalanceOverflow
	}
	toBalance += deducted
	if _, err := tx.ExecContext(ctx, `UPDATE token_accounts SET balance = $2, updated_at = NOW() WHERE user_id = $1`, fromBotID, fromBalance); err != nil {
		return TokenTransfer{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE token_accounts SET balance = $2, updated_at = NOW() WHERE user_id = $1`, toBotID, toBalance); err != nil {
		return TokenTransfer{}, err
	}
	var fromEntry TokenLedger
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO token_ledger(user_id, op_type, amount, balance_after)
		VALUES($1, 'consume', $2, $3)
		RETURNING id, user_id, op_type, amount, balance_after, created_at
	`, fromBotID, deducted, fromBalance).Scan(&fromEntry.ID, &fromEntry.BotID, &fromEntry.OpType, &fromEntry.Amount, &fromEntry.BalanceAfter, &fromEntry.CreatedAt); err != nil {
		return TokenTransfer{}, err
	}
	var toEntry TokenLedger
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO token_ledger(user_id, op_type, amount, balance_after)
		VALUES($1, 'recharge', $2, $3)
		RETURNING id, user_id, op_type, amount, balance_after, created_at
	`, toBotID, deducted, toBalance).Scan(&toEntry.ID, &toEntry.BotID, &toEntry.OpType, &toEntry.Amount, &toEntry.BalanceAfter, &toEntry.CreatedAt); err != nil {
		return TokenTransfer{}, err
	}
	if err := tx.Commit(); err != nil {
		return TokenTransfer{}, err
	}
	return TokenTransfer{Deducted: deducted, FromLedger: fromEntry, ToLedger: toEntry}, nil
}

func (s *PostgresStore) ListTokenLedger(ctx context.Context, botID string, limit int) ([]TokenLedger, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, op_type, amount, balance_after, created_at
		FROM token_ledger
		WHERE ($1 = '' OR user_id = $1)
		ORDER BY created_at DESC
		LIMIT $2
	`, botID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TokenLedger, 0)
	for rows.Next() {
		var l TokenLedger
		if err := rows.Scan(&l.ID, &l.BotID, &l.OpType, &l.Amount, &l.BalanceAfter, &l.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, rows.Err()
}

func scanCollabSession(scanner interface{ Scan(dest ...any) error }, item *CollabSession) error {
	var reviewDeadline sql.NullTime
	var mergedAt sql.NullTime
	var closed sql.NullTime
	var implDeadline sql.NullTime
	var proposalID sql.NullInt64
	if err := scanner.Scan(
		&item.CollabID,
		&item.Title,
		&item.Goal,
		&item.Kind,
		&item.Complexity,
		&item.Phase,
		&item.ProposerUserID,
		&item.AuthorUserID,
		&item.OrchestratorUserID,
		&item.MinMembers,
		&item.MaxMembers,
		&item.RequiredReviewers,
		&item.PRRepo,
		&item.PRBranch,
		&item.PRURL,
		&item.PRNumber,
		&item.PRBaseSHA,
		&item.PRHeadSHA,
		&item.PRAuthorLogin,
		&item.GitHubPRState,
		&item.PRMergeCommitSHA,
		&item.SourceRef,
		&item.ImplementationMode,
		&item.RepoDocPath,
		&item.LastStatusOrSummary,
		&item.CreatedAt,
		&item.UpdatedAt,
		&reviewDeadline,
		&mergedAt,
		&closed,
		&proposalID,
		&implDeadline,
	); err != nil {
		return err
	}
	if reviewDeadline.Valid {
		item.ReviewDeadlineAt = &reviewDeadline.Time
	} else {
		item.ReviewDeadlineAt = nil
	}
	if mergedAt.Valid {
		item.PRMergedAt = &mergedAt.Time
	} else {
		item.PRMergedAt = nil
	}
	if closed.Valid {
		item.ClosedAt = &closed.Time
	} else {
		item.ClosedAt = nil
	}
	if proposalID.Valid {
		item.ProposalID = proposalID.Int64
	} else {
		item.ProposalID = 0
	}
	if implDeadline.Valid {
		item.ImplementationDeadlineAt = &implDeadline.Time
	} else {
		item.ImplementationDeadlineAt = nil
	}
	return nil
}

func scanCollabParticipant(scanner interface{ Scan(dest ...any) error }, item *CollabParticipant) error {
	return scanner.Scan(
		&item.ID,
		&item.CollabID,
		&item.UserID,
		&item.Role,
		&item.Status,
		&item.Pitch,
		&item.ApplicationKind,
		&item.EvidenceURL,
		&item.Verified,
		&item.GitHubLogin,
		&item.CreatedAt,
		&item.UpdatedAt,
	)
}

func scanTaskLease(scanner interface{ Scan(dest ...any) error }, item *TaskLease) error {
	var consumed sql.NullTime
	if err := scanner.Scan(
		&item.TaskKind,
		&item.TaskID,
		&item.LinkedResourceType,
		&item.LinkedResourceID,
		&item.HolderUserID,
		&item.ClaimedAt,
		&item.ExpiresAt,
		&consumed,
	); err != nil {
		return err
	}
	if consumed.Valid {
		item.ConsumedAt = &consumed.Time
	} else {
		item.ConsumedAt = nil
	}
	return nil
}

func (s *PostgresStore) ClaimTaskLease(ctx context.Context, item TaskLease) (TaskLease, error) {
	return s.claimTaskLease(ctx, item, time.Time{}, 0)
}

func (s *PostgresStore) ClaimTaskLeaseWithHolderRateLimit(ctx context.Context, item TaskLease, claimedSince time.Time, maxClaims int) (TaskLease, error) {
	return s.claimTaskLease(ctx, item, claimedSince, maxClaims)
}

func (s *PostgresStore) claimTaskLease(ctx context.Context, item TaskLease, claimedSince time.Time, maxClaims int) (TaskLease, error) {
	item.TaskKind = strings.TrimSpace(item.TaskKind)
	item.TaskID = strings.TrimSpace(item.TaskID)
	item.HolderUserID = strings.TrimSpace(item.HolderUserID)
	item.LinkedResourceType = strings.TrimSpace(item.LinkedResourceType)
	item.LinkedResourceID = strings.TrimSpace(item.LinkedResourceID)
	if item.TaskKind == "" || item.TaskID == "" || item.HolderUserID == "" {
		return TaskLease{}, fmt.Errorf("task_kind, task_id, and holder_user_id are required")
	}
	item.ClaimedAt = item.ClaimedAt.UTC()
	item.ExpiresAt = item.ExpiresAt.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskLease{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, "task_lease_holder", item.HolderUserID); err != nil {
		return TaskLease{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, item.TaskKind, item.TaskID); err != nil {
		return TaskLease{}, err
	}
	var existing int64
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM task_leases
		WHERE task_kind = $1
		  AND task_id = $2
		  AND consumed_at IS NULL
		  AND expires_at > $3
		ORDER BY claimed_at DESC, id DESC
		LIMIT 1
	`, item.TaskKind, item.TaskID, item.ClaimedAt).Scan(&existing)
	switch {
	case err == nil:
		return TaskLease{}, ErrTaskLeaseConflict
	case !errors.Is(err, sql.ErrNoRows):
		return TaskLease{}, err
	}
	if maxClaims > 0 {
		var recentClaimCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM task_leases
			WHERE holder_user_id = $1
			  AND claimed_at >= $2
		`, item.HolderUserID, claimedSince.UTC()).Scan(&recentClaimCount); err != nil {
			return TaskLease{}, err
		}
		if recentClaimCount >= maxClaims {
			return TaskLease{}, ErrTaskLeaseClaimRateLimited
		}
	}
	row := tx.QueryRowContext(ctx, `
		INSERT INTO task_leases(
			task_kind, task_id, linked_resource_type, linked_resource_id, holder_user_id, claimed_at, expires_at, consumed_at
		)
		VALUES($1, $2, $3, $4, $5, $6, $7, NULL)
		RETURNING task_kind, task_id, linked_resource_type, linked_resource_id, holder_user_id, claimed_at, expires_at, consumed_at
	`, item.TaskKind, item.TaskID, item.LinkedResourceType, item.LinkedResourceID, item.HolderUserID, item.ClaimedAt, item.ExpiresAt)
	if err := scanTaskLease(row, &item); err != nil {
		return TaskLease{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskLease{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetActiveTaskLease(ctx context.Context, taskKind, taskID string, now time.Time) (TaskLease, bool, error) {
	var item TaskLease
	row := s.db.QueryRowContext(ctx, `
		SELECT task_kind, task_id, linked_resource_type, linked_resource_id, holder_user_id, claimed_at, expires_at, consumed_at
		FROM task_leases
		WHERE task_kind = $1
		  AND task_id = $2
		  AND consumed_at IS NULL
		  AND expires_at > $3
		ORDER BY claimed_at DESC, id DESC
		LIMIT 1
	`, strings.TrimSpace(taskKind), strings.TrimSpace(taskID), now.UTC())
	if err := scanTaskLease(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskLease{}, false, nil
		}
		return TaskLease{}, false, err
	}
	return item, true, nil
}

func (s *PostgresStore) ListActiveTaskLeases(ctx context.Context, taskKind, holderUserID string, now time.Time, limit int) ([]TaskLease, error) {
	taskKind = strings.TrimSpace(taskKind)
	holderUserID = strings.TrimSpace(holderUserID)
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_kind, task_id, linked_resource_type, linked_resource_id, holder_user_id, claimed_at, expires_at, consumed_at
		FROM task_leases
		WHERE ($1 = '' OR task_kind = $1)
		  AND ($2 = '' OR holder_user_id = $2)
		  AND consumed_at IS NULL
		  AND expires_at > $3
		ORDER BY claimed_at DESC, id DESC
		LIMIT $4
	`, taskKind, holderUserID, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]TaskLease, 0)
	for rows.Next() {
		var item TaskLease
		if err := scanTaskLease(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ConsumeTaskLease(ctx context.Context, taskKind, taskID, holderUserID string, consumedAt time.Time) (TaskLease, error) {
	taskKind = strings.TrimSpace(taskKind)
	taskID = strings.TrimSpace(taskID)
	holderUserID = strings.TrimSpace(holderUserID)
	if taskKind == "" || taskID == "" || holderUserID == "" {
		return TaskLease{}, fmt.Errorf("task_kind, task_id, and holder_user_id are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskLease{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, taskKind, taskID); err != nil {
		return TaskLease{}, err
	}
	var item TaskLease
	row := tx.QueryRowContext(ctx, `
		UPDATE task_leases
		SET consumed_at = $4
		WHERE id = (
			SELECT id
			FROM task_leases
			WHERE task_kind = $1
			  AND task_id = $2
			  AND holder_user_id = $3
			  AND consumed_at IS NULL
			  AND expires_at > $4
			ORDER BY claimed_at DESC, id DESC
			LIMIT 1
		)
		RETURNING task_kind, task_id, linked_resource_type, linked_resource_id, holder_user_id, claimed_at, expires_at, consumed_at
	`, taskKind, taskID, holderUserID, consumedAt.UTC())
	if err := scanTaskLease(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskLease{}, ErrTaskLeaseNotFound
		}
		return TaskLease{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskLease{}, err
	}
	return item, nil
}

func (s *PostgresStore) CreateCollabSession(ctx context.Context, item CollabSession) (CollabSession, error) {
	item.CollabID = strings.TrimSpace(item.CollabID)
	if item.CollabID == "" {
		return CollabSession{}, fmt.Errorf("collab_id is required")
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO collab_sessions(
			collab_id, title, goal, kind, complexity, phase, proposer_user_id, author_user_id,
			orchestrator_user_id, min_members, max_members, required_reviewers,
			pr_repo, pr_branch, pr_url, pr_number, pr_base_sha, pr_head_sha,
			pr_author_login, github_pr_state, pr_merge_commit_sha,
			source_ref, implementation_mode, repo_doc_path,
			status_summary, created_at, updated_at, review_deadline_at, pr_merged_at, closed_at,
			proposal_id, implementation_deadline_at
		)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, NOW(), NOW(), $26, $27, $28, $29, $30)
		RETURNING collab_id, title, goal, kind, complexity, phase, proposer_user_id, author_user_id, orchestrator_user_id,
			min_members, max_members, required_reviewers, pr_repo, pr_branch, pr_url, pr_number, pr_base_sha, pr_head_sha,
			pr_author_login, github_pr_state, pr_merge_commit_sha,
			source_ref, implementation_mode, repo_doc_path,
			status_summary, created_at, updated_at, review_deadline_at, pr_merged_at, closed_at,
			proposal_id, implementation_deadline_at
	`, item.CollabID, item.Title, item.Goal, item.Kind, item.Complexity, item.Phase, item.ProposerUserID, item.AuthorUserID, item.OrchestratorUserID,
		item.MinMembers, item.MaxMembers, item.RequiredReviewers, item.PRRepo, item.PRBranch, item.PRURL, item.PRNumber, item.PRBaseSHA, item.PRHeadSHA,
		item.PRAuthorLogin, item.GitHubPRState, item.PRMergeCommitSHA, item.SourceRef, item.ImplementationMode, item.RepoDocPath,
		item.LastStatusOrSummary, item.ReviewDeadlineAt, item.PRMergedAt, item.ClosedAt,
		item.ProposalID, item.ImplementationDeadlineAt)
	if err := scanCollabSession(row, &item); err != nil {
		return CollabSession{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetCollabSession(ctx context.Context, collabID string) (CollabSession, error) {
	var item CollabSession
	row := s.db.QueryRowContext(ctx, `
		SELECT collab_id, title, goal, kind, complexity, phase, proposer_user_id, author_user_id, orchestrator_user_id,
			min_members, max_members, required_reviewers, pr_repo, pr_branch, pr_url, pr_number, pr_base_sha, pr_head_sha,
			pr_author_login, github_pr_state, pr_merge_commit_sha,
			source_ref, implementation_mode, repo_doc_path,
			status_summary, created_at, updated_at, review_deadline_at, pr_merged_at, closed_at,
			proposal_id, implementation_deadline_at
		FROM collab_sessions WHERE collab_id = $1
	`, strings.TrimSpace(collabID))
	if err := scanCollabSession(row, &item); err != nil {
		return CollabSession{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListCollabSessions(ctx context.Context, kind, phase, proposerUserID string, limit int) ([]CollabSession, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT collab_id, title, goal, kind, complexity, phase, proposer_user_id, author_user_id, orchestrator_user_id,
			min_members, max_members, required_reviewers, pr_repo, pr_branch, pr_url, pr_number, pr_base_sha, pr_head_sha,
			pr_author_login, github_pr_state, pr_merge_commit_sha,
			source_ref, implementation_mode, repo_doc_path,
			status_summary, created_at, updated_at, review_deadline_at, pr_merged_at, closed_at,
			proposal_id, implementation_deadline_at
		FROM collab_sessions
		WHERE ($1 = '' OR kind = $1)
		  AND ($2 = '' OR phase = $2)
		  AND ($3 = '' OR proposer_user_id = $3)
		ORDER BY updated_at DESC, collab_id DESC
		LIMIT $4
	`, strings.TrimSpace(kind), strings.TrimSpace(phase), strings.TrimSpace(proposerUserID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CollabSession, 0)
	for rows.Next() {
		var it CollabSession
		if err := scanCollabSession(rows, &it); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpdateCollabPhase(ctx context.Context, collabID, phase, orchestratorUserID, statusSummary string, closedAt *time.Time) (CollabSession, error) {
	var item CollabSession
	row := s.db.QueryRowContext(ctx, `
		UPDATE collab_sessions
		SET phase = CASE WHEN $2 = '' THEN phase ELSE $2 END,
			orchestrator_user_id = CASE WHEN $3 = '' THEN orchestrator_user_id ELSE $3 END,
			status_summary = $4,
			closed_at = $5,
			updated_at = NOW()
		WHERE collab_id = $1
		RETURNING collab_id, title, goal, kind, complexity, phase, proposer_user_id, author_user_id, orchestrator_user_id,
			min_members, max_members, required_reviewers, pr_repo, pr_branch, pr_url, pr_number, pr_base_sha, pr_head_sha,
			pr_author_login, github_pr_state, pr_merge_commit_sha,
			source_ref, implementation_mode, repo_doc_path,
			status_summary, created_at, updated_at, review_deadline_at, pr_merged_at, closed_at
	`, strings.TrimSpace(collabID), strings.TrimSpace(phase), strings.TrimSpace(orchestratorUserID), strings.TrimSpace(statusSummary), closedAt)
	if err := scanCollabSession(row, &item); err != nil {
		return CollabSession{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateCollabPR(ctx context.Context, input CollabPRUpdate) (CollabSession, error) {
	var item CollabSession
	row := s.db.QueryRowContext(ctx, `
		UPDATE collab_sessions
		SET pr_branch = CASE WHEN $2 = '' THEN pr_branch ELSE $2 END,
			pr_url = CASE WHEN $3 = '' THEN pr_url ELSE $3 END,
			pr_number = CASE WHEN $4 <= 0 THEN pr_number ELSE $4 END,
			pr_base_sha = CASE WHEN $5 = '' THEN pr_base_sha ELSE $5 END,
			pr_head_sha = CASE WHEN $6 = '' THEN pr_head_sha ELSE $6 END,
			pr_author_login = CASE WHEN $7 = '' THEN pr_author_login ELSE $7 END,
			github_pr_state = CASE WHEN $8 = '' THEN github_pr_state ELSE $8 END,
			pr_merge_commit_sha = CASE WHEN $9 = '' THEN pr_merge_commit_sha ELSE $9 END,
			review_deadline_at = COALESCE($10, review_deadline_at),
			pr_merged_at = COALESCE($11, pr_merged_at),
			updated_at = NOW()
		WHERE collab_id = $1
		RETURNING collab_id, title, goal, kind, complexity, phase, proposer_user_id, author_user_id, orchestrator_user_id,
			min_members, max_members, required_reviewers, pr_repo, pr_branch, pr_url, pr_number, pr_base_sha, pr_head_sha,
			pr_author_login, github_pr_state, pr_merge_commit_sha,
			source_ref, implementation_mode, repo_doc_path,
			status_summary, created_at, updated_at, review_deadline_at, pr_merged_at, closed_at
	`, strings.TrimSpace(input.CollabID), strings.TrimSpace(input.PRBranch), strings.TrimSpace(input.PRURL), input.PRNumber, strings.TrimSpace(input.PRBaseSHA), strings.TrimSpace(input.PRHeadSHA), strings.TrimSpace(input.PRAuthorLogin), strings.TrimSpace(input.GitHubPRState), strings.TrimSpace(input.PRMergeCommitSHA), input.ReviewDeadlineAt, input.PRMergedAt)
	if err := scanCollabSession(row, &item); err != nil {
		return CollabSession{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpsertCollabParticipant(ctx context.Context, item CollabParticipant) (CollabParticipant, error) {
	item.CollabID = strings.TrimSpace(item.CollabID)
	item.UserID = strings.TrimSpace(item.UserID)
	if item.CollabID == "" || item.UserID == "" {
		return CollabParticipant{}, fmt.Errorf("collab_id and user_id are required")
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO collab_participants(collab_id, user_id, role, status, pitch, application_kind, evidence_url, verified, github_login, created_at, updated_at)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		ON CONFLICT (collab_id, user_id) DO UPDATE SET
			role = EXCLUDED.role,
			status = EXCLUDED.status,
			pitch = EXCLUDED.pitch,
			application_kind = EXCLUDED.application_kind,
			evidence_url = EXCLUDED.evidence_url,
			verified = EXCLUDED.verified,
			github_login = EXCLUDED.github_login,
			updated_at = NOW()
		RETURNING id, collab_id, user_id, role, status, pitch, application_kind, evidence_url, verified, github_login, created_at, updated_at
	`, item.CollabID, item.UserID, item.Role, item.Status, item.Pitch, item.ApplicationKind, item.EvidenceURL, item.Verified, item.GitHubLogin)
	if err := scanCollabParticipant(row, &item); err != nil {
		return CollabParticipant{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListCollabParticipants(ctx context.Context, collabID, status string, limit int) ([]CollabParticipant, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, collab_id, user_id, role, status, pitch, application_kind, evidence_url, verified, github_login, created_at, updated_at
		FROM collab_participants
		WHERE ($1 = '' OR collab_id = $1)
		  AND ($2 = '' OR status = $2)
		ORDER BY updated_at DESC, id DESC
		LIMIT $3
	`, strings.TrimSpace(collabID), strings.TrimSpace(status), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CollabParticipant, 0)
	for rows.Next() {
		var it CollabParticipant
		if err := scanCollabParticipant(rows, &it); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateCollabArtifact(ctx context.Context, item CollabArtifact) (CollabArtifact, error) {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO collab_artifacts(collab_id, user_id, role, kind, summary, content, status, review_note, created_at, updated_at)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		RETURNING id, collab_id, user_id, role, kind, summary, content, status, review_note, created_at, updated_at
	`, item.CollabID, item.UserID, item.Role, item.Kind, item.Summary, item.Content, item.Status, item.ReviewNote).Scan(
		&item.ID, &item.CollabID, &item.UserID, &item.Role, &item.Kind, &item.Summary, &item.Content, &item.Status, &item.ReviewNote, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return CollabArtifact{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateCollabArtifactReview(ctx context.Context, artifactID int64, status, reviewNote string) (CollabArtifact, error) {
	var item CollabArtifact
	err := s.db.QueryRowContext(ctx, `
		UPDATE collab_artifacts
		SET status = $2, review_note = $3, updated_at = NOW()
		WHERE id = $1
		RETURNING id, collab_id, user_id, role, kind, summary, content, status, review_note, created_at, updated_at
	`, artifactID, strings.TrimSpace(status), strings.TrimSpace(reviewNote)).Scan(
		&item.ID, &item.CollabID, &item.UserID, &item.Role, &item.Kind, &item.Summary, &item.Content, &item.Status, &item.ReviewNote, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return CollabArtifact{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListCollabArtifacts(ctx context.Context, collabID, userID string, limit int) ([]CollabArtifact, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, collab_id, user_id, role, kind, summary, content, status, review_note, created_at, updated_at
		FROM collab_artifacts
		WHERE ($1 = '' OR collab_id = $1)
		  AND ($2 = '' OR user_id = $2)
		ORDER BY updated_at DESC, id DESC
		LIMIT $3
	`, strings.TrimSpace(collabID), strings.TrimSpace(userID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CollabArtifact, 0)
	for rows.Next() {
		var it CollabArtifact
		if err := rows.Scan(&it.ID, &it.CollabID, &it.UserID, &it.Role, &it.Kind, &it.Summary, &it.Content, &it.Status, &it.ReviewNote, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) AppendCollabEvent(ctx context.Context, item CollabEvent) (CollabEvent, error) {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO collab_events(collab_id, actor_user_id, event_type, payload, created_at)
		VALUES($1, $2, $3, $4, COALESCE($5, NOW()))
		RETURNING id, created_at
	`, strings.TrimSpace(item.CollabID), strings.TrimSpace(item.ActorID), strings.TrimSpace(item.EventType), item.Payload, nullIfZeroTime(item.CreatedAt)).Scan(&item.ID, &item.CreatedAt)
	if err != nil {
		return CollabEvent{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListCollabEvents(ctx context.Context, collabID string, limit int) ([]CollabEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, collab_id, actor_user_id, event_type, payload, created_at
		FROM collab_events
		WHERE ($1 = '' OR collab_id = $1)
		ORDER BY id DESC
		LIMIT $2
	`, strings.TrimSpace(collabID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CollabEvent, 0)
	for rows.Next() {
		var it CollabEvent
		if err := rows.Scan(&it.ID, &it.CollabID, &it.ActorID, &it.EventType, &it.Payload, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListKBEntries(ctx context.Context, section, keyword string, limit int) ([]KBEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, section, title, content, version, updated_by, updated_at, is_deleted
		FROM kb_entries
		WHERE is_deleted = false
		  AND ($1 = '' OR section = $1)
		  AND ($2 = '' OR section ILIKE '%' || $2 || '%' OR title ILIKE '%' || $2 || '%' OR content ILIKE '%' || $2 || '%')
		ORDER BY updated_at DESC, id DESC
		LIMIT $3
	`, strings.TrimSpace(section), strings.TrimSpace(keyword), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBEntry, 0)
	for rows.Next() {
		var it KBEntry
		if err := rows.Scan(&it.ID, &it.Section, &it.Title, &it.Content, &it.Version, &it.UpdatedBy, &it.UpdatedAt, &it.Deleted); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListKBSections(ctx context.Context, keyword string, limit int) ([]KBSection, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT section, COUNT(*)::BIGINT AS entry_count, MAX(updated_at) AS last_updated_at
		FROM kb_entries
		WHERE is_deleted = false
		  AND section <> ''
		  AND ($1 = '' OR section ILIKE '%' || $1 || '%')
		GROUP BY section
		ORDER BY last_updated_at DESC, section ASC
		LIMIT $2
	`, strings.TrimSpace(keyword), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBSection, 0)
	for rows.Next() {
		var it KBSection
		if err := rows.Scan(&it.Section, &it.EntryCount, &it.LastUpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetKBEntry(ctx context.Context, entryID int64) (KBEntry, error) {
	var it KBEntry
	err := s.db.QueryRowContext(ctx, `
		SELECT id, section, title, content, version, updated_by, updated_at, is_deleted
		FROM kb_entries
		WHERE id = $1 AND is_deleted = false
	`, entryID).Scan(&it.ID, &it.Section, &it.Title, &it.Content, &it.Version, &it.UpdatedBy, &it.UpdatedAt, &it.Deleted)
	if err != nil {
		return KBEntry{}, err
	}
	return it, nil
}

func (s *PostgresStore) ListKBEntryHistory(ctx context.Context, entryID int64, limit int) ([]KBEntryHistoryItem, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.target_entry_id,
		       p.id, p.title, p.status, p.reason, p.created_at, p.closed_at, p.applied_at,
		       c.op_type, c.diff_text, c.old_content, c.new_content
		FROM kb_proposal_changes c
		JOIN kb_proposals p ON p.id = c.proposal_id
		WHERE c.target_entry_id = $1
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $2
	`, entryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBEntryHistoryItem, 0)
	for rows.Next() {
		var it KBEntryHistoryItem
		var closedAt, appliedAt sql.NullTime
		if err := rows.Scan(
			&it.EntryID, &it.ProposalID, &it.ProposalTitle, &it.ProposalStatus, &it.ProposalReason, &it.ProposalCreatedAt, &closedAt, &appliedAt,
			&it.OpType, &it.DiffText, &it.OldContent, &it.NewContent,
		); err != nil {
			return nil, err
		}
		if closedAt.Valid {
			it.ProposalClosedAt = &closedAt.Time
		}
		if appliedAt.Valid {
			it.ProposalAppliedAt = &appliedAt.Time
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateKBProposal(ctx context.Context, proposal KBProposal, change KBProposalChange) (KBProposal, KBProposalChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KBProposal{}, KBProposalChange{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	var discussionDeadlineArg any
	if proposal.DiscussionDeadlineAt != nil {
		discussionDeadlineArg = nullIfZeroTime(*proposal.DiscussionDeadlineAt)
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO kb_proposals(
			proposer_user_id, title, reason, status, vote_threshold_pct, vote_window_seconds,
			current_revision_id, voting_revision_id,
			enrolled_count, vote_yes, vote_no, vote_abstain, participation_count, decision_reason,
			created_at, updated_at, discussion_deadline_at, voting_deadline_at, closed_at, applied_at
		) VALUES(
			$1, $2, $3, $4, $5, $6,
			0, 0,
			0, 0, 0, 0, 0, '',
			$7, $7, $8, NULL, NULL, NULL
		)
		RETURNING id, created_at, updated_at
	`, proposal.ProposerUserID, proposal.Title, proposal.Reason, proposal.Status, proposal.VoteThresholdPct, proposal.VoteWindowSeconds, now, discussionDeadlineArg).Scan(
		&proposal.ID, &proposal.CreatedAt, &proposal.UpdatedAt,
	)
	if err != nil {
		return KBProposal{}, KBProposalChange{}, err
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO kb_proposal_changes(
			proposal_id, op_type, target_entry_id, section, title, old_content, new_content, diff_text
		) VALUES($1, $2, NULLIF($3,0), $4, $5, $6, $7, $8)
		RETURNING id
	`, proposal.ID, change.OpType, change.TargetEntryID, change.Section, change.Title, change.OldContent, change.NewContent, change.DiffText).Scan(&change.ID)
	if err != nil {
		return KBProposal{}, KBProposalChange{}, err
	}
	change.ProposalID = proposal.ID
	var revID int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO kb_revisions(
			proposal_id, revision_no, base_revision_id, created_by,
			op_type, target_entry_id, section, title, old_content, new_content, diff_text, created_at
		) VALUES(
			$1, 1, 0, $2,
			$3, NULLIF($4,0), $5, $6, $7, $8, $9, $10
		)
		RETURNING id
	`, proposal.ID, proposal.ProposerUserID, change.OpType, change.TargetEntryID, change.Section, change.Title, change.OldContent, change.NewContent, change.DiffText, now).Scan(&revID)
	if err != nil {
		return KBProposal{}, KBProposalChange{}, err
	}
	proposal.CurrentRevisionID = revID
	if _, err := tx.ExecContext(ctx, `
		UPDATE kb_proposals
		SET current_revision_id = $2, updated_at = NOW()
		WHERE id = $1
	`, proposal.ID, revID); err != nil {
		return KBProposal{}, KBProposalChange{}, err
	}

	if err := tx.Commit(); err != nil {
		return KBProposal{}, KBProposalChange{}, err
	}
	return proposal, change, nil
}

func (s *PostgresStore) ListKBRevisions(ctx context.Context, proposalID int64, limit int) ([]KBRevision, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, proposal_id, revision_no, base_revision_id, created_by,
		       op_type, target_entry_id, section, title, old_content, new_content, diff_text, created_at
		FROM kb_revisions
		WHERE proposal_id = $1
		ORDER BY revision_no DESC, id DESC
		LIMIT $2
	`, proposalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBRevision, 0)
	for rows.Next() {
		var it KBRevision
		var target sql.NullInt64
		if err := rows.Scan(
			&it.ID, &it.ProposalID, &it.RevisionNo, &it.BaseRevisionID, &it.CreatedBy,
			&it.OpType, &target, &it.Section, &it.Title, &it.OldContent, &it.NewContent, &it.DiffText, &it.CreatedAt,
		); err != nil {
			return nil, err
		}
		if target.Valid {
			it.TargetEntryID = target.Int64
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateKBRevision(ctx context.Context, proposalID, baseRevisionID int64, createdBy string, change KBProposalChange, discussionDeadline time.Time) (KBRevision, KBProposal, KBProposalChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, err
	}
	defer tx.Rollback()
	var status string
	var currentRev int64
	err = tx.QueryRowContext(ctx, `SELECT status, current_revision_id FROM kb_proposals WHERE id = $1 FOR UPDATE`, proposalID).Scan(&status, &currentRev)
	if err != nil {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, err
	}
	if status != "discussing" {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, fmt.Errorf("proposal is not in discussing phase")
	}
	if currentRev != baseRevisionID {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, fmt.Errorf("base revision is stale")
	}
	var nextRevisionNo int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision_no), 0) + 1 FROM kb_revisions WHERE proposal_id = $1`, proposalID).Scan(&nextRevisionNo); err != nil {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, err
	}
	now := time.Now().UTC()
	var rev KBRevision
	var target sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO kb_revisions(
			proposal_id, revision_no, base_revision_id, created_by,
			op_type, target_entry_id, section, title, old_content, new_content, diff_text, created_at
		) VALUES(
			$1, $2, $3, $4,
			$5, NULLIF($6,0), $7, $8, $9, $10, $11, $12
		)
		RETURNING id, proposal_id, revision_no, base_revision_id, created_by,
		          op_type, target_entry_id, section, title, old_content, new_content, diff_text, created_at
	`, proposalID, nextRevisionNo, baseRevisionID, strings.TrimSpace(createdBy), change.OpType, change.TargetEntryID, change.Section, change.Title, change.OldContent, change.NewContent, change.DiffText, now).Scan(
		&rev.ID, &rev.ProposalID, &rev.RevisionNo, &rev.BaseRevisionID, &rev.CreatedBy,
		&rev.OpType, &target, &rev.Section, &rev.Title, &rev.OldContent, &rev.NewContent, &rev.DiffText, &rev.CreatedAt,
	)
	if err != nil {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, err
	}
	if target.Valid {
		rev.TargetEntryID = target.Int64
	}
	err = tx.QueryRowContext(ctx, `
		UPDATE kb_proposal_changes
		SET op_type = $2,
		    target_entry_id = NULLIF($3,0),
		    section = $4,
		    title = $5,
		    old_content = $6,
		    new_content = $7,
		    diff_text = $8
		WHERE proposal_id = $1
		RETURNING id, proposal_id, op_type, target_entry_id, section, title, old_content, new_content, diff_text
	`, proposalID, change.OpType, change.TargetEntryID, change.Section, change.Title, change.OldContent, change.NewContent, change.DiffText).Scan(
		&change.ID, &change.ProposalID, &change.OpType, &target, &change.Section, &change.Title, &change.OldContent, &change.NewContent, &change.DiffText,
	)
	if err != nil {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, err
	}
	if target.Valid {
		change.TargetEntryID = target.Int64
	} else {
		change.TargetEntryID = 0
	}
	var proposal KBProposal
	var discussionDeadlineAt, votingDeadlineAt, closedAt, appliedAt sql.NullTime
	dlArg := nullIfZeroTime(discussionDeadline)
	err = tx.QueryRowContext(ctx, `
		UPDATE kb_proposals
		SET current_revision_id = $2,
		    discussion_deadline_at = CASE WHEN $3::timestamptz IS NULL THEN discussion_deadline_at ELSE $3 END,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, proposer_user_id, title, reason, status, current_revision_id, voting_revision_id, vote_threshold_pct, vote_window_seconds,
		          enrolled_count, vote_yes, vote_no, vote_abstain, participation_count, decision_reason,
		          created_at, updated_at, discussion_deadline_at, voting_deadline_at, closed_at, applied_at
	`, proposalID, rev.ID, dlArg).Scan(
		&proposal.ID, &proposal.ProposerUserID, &proposal.Title, &proposal.Reason, &proposal.Status, &proposal.CurrentRevisionID, &proposal.VotingRevisionID, &proposal.VoteThresholdPct, &proposal.VoteWindowSeconds,
		&proposal.EnrolledCount, &proposal.VoteYes, &proposal.VoteNo, &proposal.VoteAbstain, &proposal.ParticipationCount, &proposal.DecisionReason,
		&proposal.CreatedAt, &proposal.UpdatedAt, &discussionDeadlineAt, &votingDeadlineAt, &closedAt, &appliedAt,
	)
	if err != nil {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, err
	}
	if discussionDeadlineAt.Valid {
		proposal.DiscussionDeadlineAt = &discussionDeadlineAt.Time
	}
	if votingDeadlineAt.Valid {
		proposal.VotingDeadlineAt = &votingDeadlineAt.Time
	}
	if closedAt.Valid {
		proposal.ClosedAt = &closedAt.Time
	}
	if appliedAt.Valid {
		proposal.AppliedAt = &appliedAt.Time
	}
	if err := tx.Commit(); err != nil {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, err
	}
	return rev, proposal, change, nil
}

func (s *PostgresStore) AckKBProposal(ctx context.Context, proposalID, revisionID int64, userID string) (KBAck, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return KBAck{}, fmt.Errorf("user_id is required")
	}
	var it KBAck
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO kb_acks(proposal_id, revision_id, user_id, created_at)
		VALUES($1, $2, $3, NOW())
		ON CONFLICT (proposal_id, revision_id, user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING id, proposal_id, revision_id, user_id, created_at
	`, proposalID, revisionID, userID).Scan(&it.ID, &it.ProposalID, &it.RevisionID, &it.UserID, &it.CreatedAt)
	if err != nil {
		return KBAck{}, err
	}
	return it, nil
}

func (s *PostgresStore) ListKBAcks(ctx context.Context, proposalID, revisionID int64) ([]KBAck, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, proposal_id, revision_id, user_id, created_at
		FROM kb_acks
		WHERE proposal_id = $1
		  AND ($2 = 0 OR revision_id = $2)
		ORDER BY created_at ASC, id ASC
	`, proposalID, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBAck, 0)
	for rows.Next() {
		var it KBAck
		if err := rows.Scan(&it.ID, &it.ProposalID, &it.RevisionID, &it.UserID, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetKBProposal(ctx context.Context, proposalID int64) (KBProposal, error) {
	var it KBProposal
	var discussionDeadline, deadline, closedAt, appliedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, proposer_user_id, title, reason, status, current_revision_id, voting_revision_id, vote_threshold_pct, vote_window_seconds,
		       enrolled_count, vote_yes, vote_no, vote_abstain, participation_count, decision_reason,
		       created_at, updated_at, discussion_deadline_at, voting_deadline_at, closed_at, applied_at
		FROM kb_proposals
		WHERE id = $1
	`, proposalID).Scan(
		&it.ID, &it.ProposerUserID, &it.Title, &it.Reason, &it.Status, &it.CurrentRevisionID, &it.VotingRevisionID, &it.VoteThresholdPct, &it.VoteWindowSeconds,
		&it.EnrolledCount, &it.VoteYes, &it.VoteNo, &it.VoteAbstain, &it.ParticipationCount, &it.DecisionReason,
		&it.CreatedAt, &it.UpdatedAt, &discussionDeadline, &deadline, &closedAt, &appliedAt,
	)
	if err != nil {
		return KBProposal{}, err
	}
	if discussionDeadline.Valid {
		it.DiscussionDeadlineAt = &discussionDeadline.Time
	}
	if deadline.Valid {
		it.VotingDeadlineAt = &deadline.Time
	}
	if closedAt.Valid {
		it.ClosedAt = &closedAt.Time
	}
	if appliedAt.Valid {
		it.AppliedAt = &appliedAt.Time
	}
	return it, nil
}

func (s *PostgresStore) ListKBProposals(ctx context.Context, status string, limit int) ([]KBProposal, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, proposer_user_id, title, reason, status, current_revision_id, voting_revision_id, vote_threshold_pct, vote_window_seconds,
		       enrolled_count, vote_yes, vote_no, vote_abstain, participation_count, decision_reason,
		       created_at, updated_at, discussion_deadline_at, voting_deadline_at, closed_at, applied_at
		FROM kb_proposals
		WHERE ($1 = '' OR status = $1)
		ORDER BY updated_at DESC, id DESC
		LIMIT $2
	`, strings.TrimSpace(status), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBProposal, 0)
	for rows.Next() {
		var it KBProposal
		var discussionDeadline, deadline, closedAt, appliedAt sql.NullTime
		if err := rows.Scan(
			&it.ID, &it.ProposerUserID, &it.Title, &it.Reason, &it.Status, &it.CurrentRevisionID, &it.VotingRevisionID, &it.VoteThresholdPct, &it.VoteWindowSeconds,
			&it.EnrolledCount, &it.VoteYes, &it.VoteNo, &it.VoteAbstain, &it.ParticipationCount, &it.DecisionReason,
			&it.CreatedAt, &it.UpdatedAt, &discussionDeadline, &deadline, &closedAt, &appliedAt,
		); err != nil {
			return nil, err
		}
		if discussionDeadline.Valid {
			it.DiscussionDeadlineAt = &discussionDeadline.Time
		}
		if deadline.Valid {
			it.VotingDeadlineAt = &deadline.Time
		}
		if closedAt.Valid {
			it.ClosedAt = &closedAt.Time
		}
		if appliedAt.Valid {
			it.AppliedAt = &appliedAt.Time
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetKBProposalChange(ctx context.Context, proposalID int64) (KBProposalChange, error) {
	var it KBProposalChange
	var target sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, proposal_id, op_type, target_entry_id, section, title, old_content, new_content, diff_text
		FROM kb_proposal_changes
		WHERE proposal_id = $1
	`, proposalID).Scan(
		&it.ID, &it.ProposalID, &it.OpType, &target, &it.Section, &it.Title, &it.OldContent, &it.NewContent, &it.DiffText,
	)
	if err != nil {
		return KBProposalChange{}, err
	}
	if target.Valid {
		it.TargetEntryID = target.Int64
	}
	return it, nil
}

func (s *PostgresStore) EnrollKBProposal(ctx context.Context, proposalID int64, userID string) (KBProposalEnrollment, error) {
	var it KBProposalEnrollment
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO kb_proposal_enrollments(proposal_id, user_id, created_at)
		VALUES($1, $2, NOW())
		ON CONFLICT (proposal_id, user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING id, proposal_id, user_id, created_at
	`, proposalID, strings.TrimSpace(userID)).Scan(&it.ID, &it.ProposalID, &it.UserID, &it.CreatedAt)
	if err != nil {
		return KBProposalEnrollment{}, err
	}
	return it, nil
}

func (s *PostgresStore) ListKBProposalEnrollments(ctx context.Context, proposalID int64) ([]KBProposalEnrollment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, proposal_id, user_id, created_at
		FROM kb_proposal_enrollments
		WHERE proposal_id = $1
		ORDER BY created_at ASC, id ASC
	`, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBProposalEnrollment, 0)
	for rows.Next() {
		var it KBProposalEnrollment
		if err := rows.Scan(&it.ID, &it.ProposalID, &it.UserID, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateKBThreadMessage(ctx context.Context, item KBThreadMessage) (KBThreadMessage, error) {
	var created any
	if !item.CreatedAt.IsZero() {
		created = item.CreatedAt
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO kb_threads(proposal_id, author_user_id, message_type, content, created_at)
		VALUES($1, $2, $3, $4, COALESCE($5, NOW()))
		RETURNING id, created_at
	`, item.ProposalID, strings.TrimSpace(item.AuthorID), strings.TrimSpace(item.MessageType), item.Content, created).Scan(&item.ID, &item.CreatedAt)
	if err != nil {
		return KBThreadMessage{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListKBThreadMessages(ctx context.Context, proposalID int64, limit int) ([]KBThreadMessage, error) {
	if limit <= 0 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, proposal_id, author_user_id, message_type, content, created_at
		FROM kb_threads
		WHERE proposal_id = $1
		ORDER BY created_at ASC, id ASC
		LIMIT $2
	`, proposalID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBThreadMessage, 0)
	for rows.Next() {
		var it KBThreadMessage
		if err := rows.Scan(&it.ID, &it.ProposalID, &it.AuthorID, &it.MessageType, &it.Content, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) StartKBProposalVoting(ctx context.Context, proposalID int64, deadline time.Time) (KBProposal, error) {
	var it KBProposal
	var discussionDeadlineAt, deadlineAt, closedAt, appliedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		UPDATE kb_proposals
		SET status = 'voting',
		    voting_revision_id = current_revision_id,
		    voting_deadline_at = $2,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, proposer_user_id, title, reason, status, current_revision_id, voting_revision_id, vote_threshold_pct, vote_window_seconds,
		          enrolled_count, vote_yes, vote_no, vote_abstain, participation_count, decision_reason,
		          created_at, updated_at, discussion_deadline_at, voting_deadline_at, closed_at, applied_at
	`, proposalID, deadline).Scan(
		&it.ID, &it.ProposerUserID, &it.Title, &it.Reason, &it.Status, &it.CurrentRevisionID, &it.VotingRevisionID, &it.VoteThresholdPct, &it.VoteWindowSeconds,
		&it.EnrolledCount, &it.VoteYes, &it.VoteNo, &it.VoteAbstain, &it.ParticipationCount, &it.DecisionReason,
		&it.CreatedAt, &it.UpdatedAt, &discussionDeadlineAt, &deadlineAt, &closedAt, &appliedAt,
	)
	if err != nil {
		return KBProposal{}, err
	}
	if discussionDeadlineAt.Valid {
		it.DiscussionDeadlineAt = &discussionDeadlineAt.Time
	}
	if deadlineAt.Valid {
		it.VotingDeadlineAt = &deadlineAt.Time
	}
	if closedAt.Valid {
		it.ClosedAt = &closedAt.Time
	}
	if appliedAt.Valid {
		it.AppliedAt = &appliedAt.Time
	}
	return it, nil
}

func (s *PostgresStore) CastKBVote(ctx context.Context, vote KBVote) (KBVote, error) {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO kb_votes(proposal_id, user_id, vote, reason, created_at, updated_at)
		VALUES($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (proposal_id, user_id) DO UPDATE SET
			vote = EXCLUDED.vote,
			reason = EXCLUDED.reason,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`, vote.ProposalID, strings.TrimSpace(vote.UserID), strings.TrimSpace(vote.Vote), strings.TrimSpace(vote.Reason)).Scan(
		&vote.ID, &vote.CreatedAt, &vote.UpdatedAt,
	)
	if err != nil {
		return KBVote{}, err
	}
	return vote, nil
}

func (s *PostgresStore) ListKBVotes(ctx context.Context, proposalID int64) ([]KBVote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, proposal_id, user_id, vote, reason, created_at, updated_at
		FROM kb_votes
		WHERE proposal_id = $1
		ORDER BY updated_at ASC, id ASC
	`, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KBVote, 0)
	for rows.Next() {
		var it KBVote
		if err := rows.Scan(&it.ID, &it.ProposalID, &it.UserID, &it.Vote, &it.Reason, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CloseKBProposal(ctx context.Context, proposalID int64, status, decisionReason string, enrolledCount, voteYes, voteNo, voteAbstain, participationCount int, closedAt time.Time) (KBProposal, error) {
	var it KBProposal
	var discussionDeadlineAt, deadlineAt, closed, appliedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		UPDATE kb_proposals
		SET status = $2,
		    decision_reason = $3,
		    enrolled_count = $4,
		    vote_yes = $5,
		    vote_no = $6,
		    vote_abstain = $7,
		    participation_count = $8,
		    closed_at = $9,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, proposer_user_id, title, reason, status, current_revision_id, voting_revision_id, vote_threshold_pct, vote_window_seconds,
		          enrolled_count, vote_yes, vote_no, vote_abstain, participation_count, decision_reason,
		          created_at, updated_at, discussion_deadline_at, voting_deadline_at, closed_at, applied_at
	`, proposalID, strings.TrimSpace(status), strings.TrimSpace(decisionReason), enrolledCount, voteYes, voteNo, voteAbstain, participationCount, closedAt).Scan(
		&it.ID, &it.ProposerUserID, &it.Title, &it.Reason, &it.Status, &it.CurrentRevisionID, &it.VotingRevisionID, &it.VoteThresholdPct, &it.VoteWindowSeconds,
		&it.EnrolledCount, &it.VoteYes, &it.VoteNo, &it.VoteAbstain, &it.ParticipationCount, &it.DecisionReason,
		&it.CreatedAt, &it.UpdatedAt, &discussionDeadlineAt, &deadlineAt, &closed, &appliedAt,
	)
	if err != nil {
		return KBProposal{}, err
	}
	if discussionDeadlineAt.Valid {
		it.DiscussionDeadlineAt = &discussionDeadlineAt.Time
	}
	if deadlineAt.Valid {
		it.VotingDeadlineAt = &deadlineAt.Time
	}
	if closed.Valid {
		it.ClosedAt = &closed.Time
	}
	if appliedAt.Valid {
		it.AppliedAt = &appliedAt.Time
	}
	return it, nil
}

func (s *PostgresStore) ApplyKBProposal(ctx context.Context, proposalID int64, appliedBy string, appliedAt time.Time) (KBEntry, KBProposal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KBEntry{}, KBProposal{}, err
	}
	defer tx.Rollback()

	var proposalStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT status
		FROM kb_proposals
		WHERE id = $1
		FOR UPDATE
	`, proposalID).Scan(&proposalStatus); err != nil {
		return KBEntry{}, KBProposal{}, err
	}
	if strings.ToLower(strings.TrimSpace(proposalStatus)) != "approved" {
		return KBEntry{}, KBProposal{}, fmt.Errorf("proposal is not approved")
	}

	var change KBProposalChange
	var target sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT id, proposal_id, op_type, target_entry_id, section, title, old_content, new_content, diff_text
		FROM kb_proposal_changes
		WHERE proposal_id = $1
		FOR UPDATE
	`, proposalID).Scan(
		&change.ID, &change.ProposalID, &change.OpType, &target, &change.Section, &change.Title, &change.OldContent, &change.NewContent, &change.DiffText,
	); err != nil {
		return KBEntry{}, KBProposal{}, err
	}
	if target.Valid {
		change.TargetEntryID = target.Int64
	}

	var entry KBEntry
	switch strings.ToLower(strings.TrimSpace(change.OpType)) {
	case "add":
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO kb_entries(section, title, content, version, updated_by, updated_at, is_deleted)
			VALUES($1, $2, $3, 1, $4, $5, false)
			RETURNING id, section, title, content, version, updated_by, updated_at, is_deleted
		`, change.Section, change.Title, change.NewContent, strings.TrimSpace(appliedBy), appliedAt).Scan(
			&entry.ID, &entry.Section, &entry.Title, &entry.Content, &entry.Version, &entry.UpdatedBy, &entry.UpdatedAt, &entry.Deleted,
		); err != nil {
			return KBEntry{}, KBProposal{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE kb_proposal_changes SET target_entry_id = $2 WHERE proposal_id = $1`, proposalID, entry.ID); err != nil {
			return KBEntry{}, KBProposal{}, err
		}
	case "update":
		if change.TargetEntryID <= 0 {
			return KBEntry{}, KBProposal{}, fmt.Errorf("target_entry_id is required for update")
		}
		if err := tx.QueryRowContext(ctx, `
			UPDATE kb_entries
			SET section = $2,
			    title = $3,
			    content = $4,
			    version = version + 1,
			    updated_by = $5,
			    updated_at = $6
			WHERE id = $1 AND is_deleted = false
			RETURNING id, section, title, content, version, updated_by, updated_at, is_deleted
		`, change.TargetEntryID, change.Section, change.Title, change.NewContent, strings.TrimSpace(appliedBy), appliedAt).Scan(
			&entry.ID, &entry.Section, &entry.Title, &entry.Content, &entry.Version, &entry.UpdatedBy, &entry.UpdatedAt, &entry.Deleted,
		); err != nil {
			return KBEntry{}, KBProposal{}, err
		}
	case "delete":
		if change.TargetEntryID <= 0 {
			return KBEntry{}, KBProposal{}, fmt.Errorf("target_entry_id is required for delete")
		}
		if err := tx.QueryRowContext(ctx, `
			UPDATE kb_entries
			SET is_deleted = true,
			    version = version + 1,
			    updated_by = $2,
			    updated_at = $3
			WHERE id = $1 AND is_deleted = false
			RETURNING id, section, title, content, version, updated_by, updated_at, is_deleted
		`, change.TargetEntryID, strings.TrimSpace(appliedBy), appliedAt).Scan(
			&entry.ID, &entry.Section, &entry.Title, &entry.Content, &entry.Version, &entry.UpdatedBy, &entry.UpdatedAt, &entry.Deleted,
		); err != nil {
			return KBEntry{}, KBProposal{}, err
		}
	default:
		return KBEntry{}, KBProposal{}, fmt.Errorf("unsupported op_type: %s", change.OpType)
	}

	var proposal KBProposal
	var discussionDeadlineAt, deadlineAt, closedAt, appliedAtDB sql.NullTime
	err = tx.QueryRowContext(ctx, `
		UPDATE kb_proposals
		SET status = 'applied',
		    applied_at = $2,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, proposer_user_id, title, reason, status, current_revision_id, voting_revision_id, vote_threshold_pct, vote_window_seconds,
		          enrolled_count, vote_yes, vote_no, vote_abstain, participation_count, decision_reason,
		          created_at, updated_at, discussion_deadline_at, voting_deadline_at, closed_at, applied_at
	`, proposalID, appliedAt).Scan(
		&proposal.ID, &proposal.ProposerUserID, &proposal.Title, &proposal.Reason, &proposal.Status, &proposal.CurrentRevisionID, &proposal.VotingRevisionID, &proposal.VoteThresholdPct, &proposal.VoteWindowSeconds,
		&proposal.EnrolledCount, &proposal.VoteYes, &proposal.VoteNo, &proposal.VoteAbstain, &proposal.ParticipationCount, &proposal.DecisionReason,
		&proposal.CreatedAt, &proposal.UpdatedAt, &discussionDeadlineAt, &deadlineAt, &closedAt, &appliedAtDB,
	)
	if err != nil {
		return KBEntry{}, KBProposal{}, err
	}
	if discussionDeadlineAt.Valid {
		proposal.DiscussionDeadlineAt = &discussionDeadlineAt.Time
	}
	if deadlineAt.Valid {
		proposal.VotingDeadlineAt = &deadlineAt.Time
	}
	if closedAt.Valid {
		proposal.ClosedAt = &closedAt.Time
	}
	if appliedAtDB.Valid {
		proposal.AppliedAt = &appliedAtDB.Time
	}

	if err := tx.Commit(); err != nil {
		return KBEntry{}, KBProposal{}, err
	}
	return entry, proposal, nil
}

func nullIfZeroTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func (s *PostgresStore) AppendRequestLog(ctx context.Context, item RequestLog) (RequestLog, error) {
	var reqTime any
	if !item.Time.IsZero() {
		reqTime = item.Time
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO request_logs(req_time, method, path, user_id, status_code, duration_ms)
		VALUES(COALESCE($1, NOW()), $2, $3, $4, $5, $6)
		RETURNING id, req_time
	`, reqTime, item.Method, item.Path, item.UserID, item.StatusCode, item.DurationMS).Scan(&item.ID, &item.Time)
	if err != nil {
		return RequestLog{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListRequestLogs(ctx context.Context, filter RequestLogFilter) ([]RequestLog, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 300
	}
	if limit > 20000 {
		limit = 20000
	}
	method := strings.ToUpper(strings.TrimSpace(filter.Method))
	pathContains := strings.TrimSpace(filter.PathContains)
	userID := strings.TrimSpace(filter.UserID)

	query := strings.Builder{}
	query.WriteString(`
		SELECT id, req_time, method, path, user_id, status_code, duration_ms
		FROM request_logs
		WHERE 1=1
	`)
	args := make([]any, 0, 8)
	argi := 1
	add := func(sql string, v any) {
		query.WriteString(" AND ")
		query.WriteString(fmt.Sprintf(sql, argi))
		args = append(args, v)
		argi++
	}
	if filter.Since != nil {
		add("req_time >= $%d", *filter.Since)
	}
	if method != "" {
		add("method = $%d", method)
	}
	if pathContains != "" {
		add("path LIKE $%d", "%"+pathContains+"%")
	}
	if userID != "" {
		add("user_id = $%d", userID)
	}
	if filter.StatusCode > 0 {
		add("status_code = $%d", filter.StatusCode)
	}
	query.WriteString(fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", argi))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]RequestLog, 0, limit)
	for rows.Next() {
		var it RequestLog
		if err := rows.Scan(&it.ID, &it.Time, &it.Method, &it.Path, &it.UserID, &it.StatusCode, &it.DurationMS); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetWorldSetting(ctx context.Context, key string) (WorldSetting, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return WorldSetting{}, fmt.Errorf("setting key is required")
	}
	var it WorldSetting
	err := s.db.QueryRowContext(ctx, `
		SELECT key, value, updated_at
		FROM world_settings
		WHERE key = $1
	`, key).Scan(&it.Key, &it.Value, &it.UpdatedAt)
	if err != nil {
		return WorldSetting{}, err
	}
	return it, nil
}

func (s *PostgresStore) UpsertWorldSetting(ctx context.Context, item WorldSetting) (WorldSetting, error) {
	item.Key = strings.TrimSpace(item.Key)
	if item.Key == "" {
		return WorldSetting{}, fmt.Errorf("setting key is required")
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO world_settings(key, value, updated_at)
		VALUES($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET
			value = EXCLUDED.value,
			updated_at = NOW()
		RETURNING key, value, updated_at
	`, item.Key, item.Value).Scan(&item.Key, &item.Value, &item.UpdatedAt)
	if err != nil {
		return WorldSetting{}, err
	}
	return item, nil
}

func (s *PostgresStore) CreateGanglion(ctx context.Context, item Ganglion) (Ganglion, error) {
	item.Name = strings.TrimSpace(item.Name)
	item.GanglionType = strings.TrimSpace(item.GanglionType)
	item.Description = strings.TrimSpace(item.Description)
	item.Implementation = strings.TrimSpace(item.Implementation)
	item.Validation = strings.TrimSpace(item.Validation)
	item.AuthorUserID = strings.TrimSpace(item.AuthorUserID)
	if item.Name == "" || item.GanglionType == "" || item.Description == "" || item.AuthorUserID == "" {
		return Ganglion{}, fmt.Errorf("name, type, description, author_user_id are required")
	}
	item.Temporality = normalizeGanglionTemporality(item.Temporality)
	item.LifeState = normalizeGanglionLifeState(item.LifeState)
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO ganglia(
			name, ganglion_type, description, implementation, validation,
			author_user_id, supersedes_id, temporality, life_state,
			score_avg_milli, score_count, integrations_count, created_at, updated_at
		)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,0,0,0,NOW(),NOW())
		RETURNING id, name, ganglion_type, description, implementation, validation,
		          author_user_id, supersedes_id, temporality, life_state,
		          score_avg_milli, score_count, integrations_count, created_at, updated_at
	`, item.Name, item.GanglionType, item.Description, item.Implementation, item.Validation,
		item.AuthorUserID, item.SupersedesID, item.Temporality, item.LifeState).Scan(
		&item.ID, &item.Name, &item.GanglionType, &item.Description, &item.Implementation, &item.Validation,
		&item.AuthorUserID, &item.SupersedesID, &item.Temporality, &item.LifeState,
		&item.ScoreAvgMilli, &item.ScoreCount, &item.IntegrationsCount, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return Ganglion{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetGanglion(ctx context.Context, ganglionID int64) (Ganglion, error) {
	var it Ganglion
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, ganglion_type, description, implementation, validation,
		       author_user_id, supersedes_id, temporality, life_state,
		       score_avg_milli, score_count, integrations_count, created_at, updated_at
		FROM ganglia
		WHERE id = $1
	`, ganglionID).Scan(
		&it.ID, &it.Name, &it.GanglionType, &it.Description, &it.Implementation, &it.Validation,
		&it.AuthorUserID, &it.SupersedesID, &it.Temporality, &it.LifeState,
		&it.ScoreAvgMilli, &it.ScoreCount, &it.IntegrationsCount, &it.CreatedAt, &it.UpdatedAt,
	)
	if err != nil {
		return Ganglion{}, err
	}
	return it, nil
}

func (s *PostgresStore) ListGanglia(ctx context.Context, ganglionType, lifeState, keyword string, limit int) ([]Ganglion, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	ganglionType = strings.TrimSpace(strings.ToLower(ganglionType))
	lifeState = strings.TrimSpace(strings.ToLower(lifeState))
	keyword = strings.TrimSpace(strings.ToLower(keyword))
	var (
		query strings.Builder
		args  []any
		argi  = 1
	)
	query.WriteString(`
		SELECT id, name, ganglion_type, description, implementation, validation,
		       author_user_id, supersedes_id, temporality, life_state,
		       score_avg_milli, score_count, integrations_count, created_at, updated_at
		FROM ganglia
		WHERE 1=1
	`)
	if ganglionType != "" {
		query.WriteString(fmt.Sprintf(" AND LOWER(ganglion_type) = $%d", argi))
		args = append(args, ganglionType)
		argi++
	}
	if lifeState != "" {
		query.WriteString(fmt.Sprintf(" AND LOWER(life_state) = $%d", argi))
		args = append(args, lifeState)
		argi++
	}
	if keyword != "" {
		query.WriteString(fmt.Sprintf(" AND (name ILIKE $%d OR description ILIKE $%d OR implementation ILIKE $%d OR validation ILIKE $%d)", argi, argi+1, argi+2, argi+3))
		kw := "%" + keyword + "%"
		args = append(args, kw, kw, kw, kw)
		argi += 4
	}
	query.WriteString(fmt.Sprintf(" ORDER BY updated_at DESC, id DESC LIMIT $%d", argi))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Ganglion, 0, limit)
	for rows.Next() {
		var it Ganglion
		if err := rows.Scan(
			&it.ID, &it.Name, &it.GanglionType, &it.Description, &it.Implementation, &it.Validation,
			&it.AuthorUserID, &it.SupersedesID, &it.Temporality, &it.LifeState,
			&it.ScoreAvgMilli, &it.ScoreCount, &it.IntegrationsCount, &it.CreatedAt, &it.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) IntegrateGanglion(ctx context.Context, ganglionID int64, userID string) (GanglionIntegration, Ganglion, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return GanglionIntegration{}, Ganglion{}, fmt.Errorf("user_id is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ganglion_integrations(ganglion_id, user_id, created_at, updated_at)
		VALUES($1, $2, NOW(), NOW())
		ON CONFLICT (ganglion_id, user_id) DO UPDATE SET
			updated_at = NOW()
	`, ganglionID, userID)
	if err != nil {
		return GanglionIntegration{}, Ganglion{}, err
	}
	var out GanglionIntegration
	err = s.db.QueryRowContext(ctx, `
		SELECT id, ganglion_id, user_id, created_at, updated_at
		FROM ganglion_integrations
		WHERE ganglion_id = $1 AND user_id = $2
	`, ganglionID, userID).Scan(&out.ID, &out.GanglionID, &out.UserID, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return GanglionIntegration{}, Ganglion{}, err
	}
	var g Ganglion
	err = s.db.QueryRowContext(ctx, `
		UPDATE ganglia g
		SET integrations_count = sub.cnt,
		    updated_at = NOW()
		FROM (
			SELECT ganglion_id, COUNT(*)::bigint AS cnt
			FROM ganglion_integrations
			WHERE ganglion_id = $1
			GROUP BY ganglion_id
		) sub
		WHERE g.id = sub.ganglion_id
		RETURNING g.id, g.name, g.ganglion_type, g.description, g.implementation, g.validation,
		          g.author_user_id, g.supersedes_id, g.temporality, g.life_state,
		          g.score_avg_milli, g.score_count, g.integrations_count, g.created_at, g.updated_at
	`, ganglionID).Scan(
		&g.ID, &g.Name, &g.GanglionType, &g.Description, &g.Implementation, &g.Validation,
		&g.AuthorUserID, &g.SupersedesID, &g.Temporality, &g.LifeState,
		&g.ScoreAvgMilli, &g.ScoreCount, &g.IntegrationsCount, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return GanglionIntegration{}, Ganglion{}, err
	}
	return out, g, nil
}

func (s *PostgresStore) ListGanglionIntegrations(ctx context.Context, userID string, ganglionID int64, limit int) ([]GanglionIntegration, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	userID = strings.TrimSpace(userID)
	var (
		query strings.Builder
		args  []any
		argi  = 1
	)
	query.WriteString(`
		SELECT id, ganglion_id, user_id, created_at, updated_at
		FROM ganglion_integrations
		WHERE 1=1
	`)
	if userID != "" {
		query.WriteString(fmt.Sprintf(" AND user_id = $%d", argi))
		args = append(args, userID)
		argi++
	}
	if ganglionID > 0 {
		query.WriteString(fmt.Sprintf(" AND ganglion_id = $%d", argi))
		args = append(args, ganglionID)
		argi++
	}
	query.WriteString(fmt.Sprintf(" ORDER BY updated_at DESC, id DESC LIMIT $%d", argi))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GanglionIntegration, 0, limit)
	for rows.Next() {
		var it GanglionIntegration
		if err := rows.Scan(&it.ID, &it.GanglionID, &it.UserID, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) RateGanglion(ctx context.Context, item GanglionRating) (GanglionRating, Ganglion, error) {
	item.UserID = strings.TrimSpace(item.UserID)
	item.Feedback = strings.TrimSpace(item.Feedback)
	if item.GanglionID <= 0 || item.UserID == "" {
		return GanglionRating{}, Ganglion{}, fmt.Errorf("ganglion_id and user_id are required")
	}
	if item.Score < 1 || item.Score > 5 {
		return GanglionRating{}, Ganglion{}, fmt.Errorf("score must be between 1 and 5")
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO ganglion_ratings(ganglion_id, user_id, score, feedback, created_at, updated_at)
		VALUES($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (ganglion_id, user_id) DO UPDATE SET
			score = EXCLUDED.score,
			feedback = EXCLUDED.feedback,
			updated_at = NOW()
		RETURNING id, ganglion_id, user_id, score, feedback, created_at, updated_at
	`, item.GanglionID, item.UserID, item.Score, item.Feedback).Scan(
		&item.ID, &item.GanglionID, &item.UserID, &item.Score, &item.Feedback, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return GanglionRating{}, Ganglion{}, err
	}
	var g Ganglion
	err = s.db.QueryRowContext(ctx, `
		UPDATE ganglia g
		SET score_avg_milli = sub.avg_milli,
		    score_count = sub.cnt,
		    updated_at = NOW()
		FROM (
			SELECT ganglion_id,
			       COALESCE(AVG(score)::numeric, 0) * 1000 AS avg_milli,
			       COUNT(*)::bigint AS cnt
			FROM ganglion_ratings
			WHERE ganglion_id = $1
			GROUP BY ganglion_id
		) sub
		WHERE g.id = sub.ganglion_id
		RETURNING g.id, g.name, g.ganglion_type, g.description, g.implementation, g.validation,
		          g.author_user_id, g.supersedes_id, g.temporality, g.life_state,
		          g.score_avg_milli, g.score_count, g.integrations_count, g.created_at, g.updated_at
	`, item.GanglionID).Scan(
		&g.ID, &g.Name, &g.GanglionType, &g.Description, &g.Implementation, &g.Validation,
		&g.AuthorUserID, &g.SupersedesID, &g.Temporality, &g.LifeState,
		&g.ScoreAvgMilli, &g.ScoreCount, &g.IntegrationsCount, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return GanglionRating{}, Ganglion{}, err
	}
	return item, g, nil
}

func (s *PostgresStore) ListGanglionRatings(ctx context.Context, ganglionID int64, limit int) ([]GanglionRating, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	var (
		query strings.Builder
		args  []any
		argi  = 1
	)
	query.WriteString(`
		SELECT id, ganglion_id, user_id, score, feedback, created_at, updated_at
		FROM ganglion_ratings
		WHERE 1=1
	`)
	if ganglionID > 0 {
		query.WriteString(fmt.Sprintf(" AND ganglion_id = $%d", argi))
		args = append(args, ganglionID)
		argi++
	}
	query.WriteString(fmt.Sprintf(" ORDER BY updated_at DESC, id DESC LIMIT $%d", argi))
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GanglionRating, 0, limit)
	for rows.Next() {
		var it GanglionRating
		if err := rows.Scan(&it.ID, &it.GanglionID, &it.UserID, &it.Score, &it.Feedback, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpdateGanglionLifeState(ctx context.Context, ganglionID int64, lifeState string) (Ganglion, error) {
	lifeState = normalizeGanglionLifeState(lifeState)
	var g Ganglion
	err := s.db.QueryRowContext(ctx, `
		UPDATE ganglia
		SET life_state = $2,
		    updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, ganglion_type, description, implementation, validation,
		          author_user_id, supersedes_id, temporality, life_state,
		          score_avg_milli, score_count, integrations_count, created_at, updated_at
	`, ganglionID, lifeState).Scan(
		&g.ID, &g.Name, &g.GanglionType, &g.Description, &g.Implementation, &g.Validation,
		&g.AuthorUserID, &g.SupersedesID, &g.Temporality, &g.LifeState,
		&g.ScoreAvgMilli, &g.ScoreCount, &g.IntegrationsCount, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return Ganglion{}, err
	}
	return g, nil
}
