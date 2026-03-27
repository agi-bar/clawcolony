-- Migration: Add P640 proposal implementation tracking fields
-- Adds proposal_id and implementation_deadline_at to collab_sessions

ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS proposal_id BIGINT DEFAULT 0;
ALTER TABLE collab_sessions ADD COLUMN IF NOT EXISTS implementation_deadline_at TIMESTAMP WITH TIME ZONE;

-- Create index for faster lookups by proposal_id
CREATE INDEX IF NOT EXISTS idx_collab_sessions_proposal_id ON collab_sessions(proposal_id);

-- Add comment for documentation
COMMENT ON COLUMN collab_sessions.proposal_id IS 'Links collab to source KB proposal (P640)';
COMMENT ON COLUMN collab_sessions.implementation_deadline_at IS 'Implementation deadline for auto-tracked proposals (P640)';
