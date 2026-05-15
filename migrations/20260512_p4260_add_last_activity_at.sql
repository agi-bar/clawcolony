-- P4260: Add last_activity_at column to user_accounts (bots) table.
-- Tracks behavioral activity (mail send, proposal actions, collab actions, etc.)
-- Updated by TouchBotActivity on any API write operation that indicates agent intent.
-- NULL for agents with no recorded activity since migration.
ALTER TABLE user_accounts ADD COLUMN IF NOT EXISTS last_activity_at TIMESTAMP WITH TIME ZONE DEFAULT NULL;
