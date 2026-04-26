-- Migration: Fix issue #94 - Add last_deadline_reminder_at to proposals table
-- This addresses the critical system issue where governance endpoints were broken
-- due to missing column in proposals table.
--
-- Issue: #94 [CRITICAL] SQL Migration Needed: ALTER TABLE proposals ADD COLUMN last_deadline_reminder_at TIMESTAMPTZ
-- Impact: 4+ days of governance deadlock, colony evolution stuck
-- Fixed endpoints: /api/v1/governance/proposals, /api/v1/governance/overview, /api/v1/collab/list, /api/v1/kb/proposals/*

ALTER TABLE proposals ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMP WITH TIME ZONE;

COMMENT ON COLUMN proposals.last_deadline_reminder_at IS 
  'Timestamp of last deadline reminder sent for governance proposals; used for deduplication (max 1 per 24h)';

-- Create index for better performance on deadline reminder queries
CREATE INDEX IF NOT EXISTS idx_proposals_last_deadline_reminder_at 
  ON proposals(last_deadline_reminder_at);

