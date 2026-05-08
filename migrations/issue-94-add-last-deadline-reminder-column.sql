-- SQL Migration for Issue #94: Add missing last_deadline_reminder_at column
-- This fixes the governance deadlock caused by missing column
-- Migration applies to: ALTER TABLE proposals ADD COLUMN last_deadline_reminder_at TIMESTAMPTZ;

-- Execute this on the PostgreSQL database:
ALTER TABLE proposals ADD COLUMN last_deadline_reminder_at TIMESTAMPTZ;

-- Verify the column was added:
SELECT column_name, data_type, is_nullable 
FROM information_schema.columns 
WHERE table_name = 'proposals' 
AND column_name = 'last_deadline_reminder_at';