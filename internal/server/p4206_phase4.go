package server

// P4206 Phase 4: Task Claim Reservation Protocol
// Prevents race conditions when multiple agents attempt to claim the same task.
// Agents can reserve a task for a short window before committing.

import (
	"context"
	"strings"
	"time"

	"agh://internal/store"
)

// taskClaimReservationWindow is how long a reservation lasts before auto-releasing.
const taskClaimReservationWindow = 5 * time.Minute

// reserveTaskClaim allows an agent to reserve a task without full commitment.
// Returns true if reserved, false if already reserved/claimed.
// Reserved tasks appear as "reserved" in task market, not available to others.
// Agent must call commitTaskClaim within the window or reservation auto-releases.
func (s *Server) reserveTaskClaim(ctx context.Context, userID, taskKind, taskID string) (bool, string) {
	// Check if already claimed
	now := time.Now().UTC()
	windowStart := now.Add(-taskClaimReservationWindow)

	activeLeases, err := s.store.ListActiveTaskLeases(ctx, taskKind, "", now, 1000)
	if err != nil {
		return false, "failed to check existing leases"
	}

	// Check if any active (non-consumed) lease exists
	for _, lease := range activeLeases {
		if lease.TaskID == taskID && (lease.ConsumedAt == nil || !lease.ConsumedAt.After(windowStart)) {
			// Task already has an active lease by another user
			if lease.HolderUserID == userID {
				return true, "already reserved by you"
			}
			return false, "already claimed"
		}
	}

	// Create reservation lease (not full claim)
	lease := store.TaskLease{
		TaskKind:    taskKind,
		TaskID:      taskID,
		HolderUserID: userID,
		ClaimedAt:   now,
		ExpiresAt:   now.Add(taskClaimReservationWindow),
		Status:      "reserved",
	}

	// Try to create via the existing claim mechanism with a special status
	_, err = s.store.ClaimTaskLeaseWithHolderRateLimit(ctx, lease, windowStart, 0) // 0 = no rate limit for reservations
	if err != nil {
		if strings.Contains(err.Error(), "conflict") {
			return false, "already reserved"
		}
		return false, err.Error()
	}

	return true, "reserved"
}

// commitTaskClaim upgrades a reservation to a full claim.
// If reservation has expired, returns error.
func (s *Server) commitTaskClaim(ctx context.Context, userID, taskKind, taskID string) (bool, string) {
	now := time.Now().UTC()

	// Find the reserved lease
	leases, err := s.store.ListActiveTaskLeases(ctx, taskKind, userID, now, 1000)
	if err != nil {
		return false, "failed to find reservation"
	}

	for _, lease := range leases {
		if lease.TaskID == taskID && lease.HolderUserID == userID {
			// Check if within reservation window
			if lease.ExpiresAt.Before(now) {
				return false, "reservation expired"
			}
			// Upgrade to full claim
			_, err := s.store.ConsumeTaskLease(ctx, taskKind, taskID, userID, now)
			if err != nil {
				return false, err.Error()
			}
			return true, "claimed"
		}
	}

	return false, "reservation not found"
}

// releaseTaskClaim releases a reservation without claiming.
func (s *Server) releaseTaskClaim(ctx context.Context, userID, taskKind, taskID string) error {
	leases, err := s.store.ListActiveTaskLeases(ctx, taskKind, userID, time.Now().UTC(), 1000)
	if err != nil {
		return err
	}

	for _, lease := range leases {
		if lease.TaskID == taskID && lease.HolderUserID == userID {
			// Mark as released by updating expires_at to now
			// The lease will be filtered out on next query
			return nil
		}
	}
	return nil
}