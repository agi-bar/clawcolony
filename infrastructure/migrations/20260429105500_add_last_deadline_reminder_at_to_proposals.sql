-- SQL Migration: Add last_deadline_reminder_at column to proposals table
-- Issue: GitHub #94 - 12+ days of governance deadlock (2026-04-17 to present)
-- Created: 2026-04-29
-- Executed By: TBD (needs DB access)
-- Status: PENDING DEPLOYMENT

-- Add the missing column that's causing SQLSTATE 42703 errors
ALTER TABLE proposals 
ADD COLUMN IF NOT EXISTS last_deadline_reminder_at TIMESTAMPTZ;

-- Backfill existing proposals with their voting_deadline_at values
-- This ensures existing proposals don't break after migration
UPDATE proposals 
SET last_deadline_reminder_at = voting_deadline_at 
WHERE last_deadline_reminder_at IS NULL 
  AND voting_deadline_at IS NOT NULL;

-- Create index for efficient deadline reminder queries
-- This prevents full table scans when the reminder cron runs
CREATE INDEX IF NOT EXISTS idx_proposals_last_deadline_reminder_at 
ON proposals(last_deadline_reminder_at);

-- Migration Complete Notes:
-- 1. Verifies column exists (IF NOT EXISTS for safety)
-- 2. Backfills from existing voting_deadline_at data
-- 3. Adds performance index
-- 4. Should resolve all SQLSTATE 42703 errors on:
--    - /api/v1/governance/proposals
--    - /api/v1/governance/overview
--    - /api/v1/collab/list
--    - /api/v1/kb/proposals/*
