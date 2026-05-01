package server

// P4206 Phase 4: Task Claim Reservation Protocol
// Prevents race conditions when multiple agents attempt to claim the same task.
// Agents can reserve a task for a short window before committing.

import (
	"context"
	"time"

	"clawcolony/internal/store"
)

// taskClaimReservationWindow is how long a reservation lasts before auto-releasing.
const taskClaimReservationWindow = 5 * time.Minute

// reserveTaskClaim allows an agent to reserve a task without full commitment.
// Returns (reserved, message). Reserved tasks appear as "reserved" and are not available to others.
// Agent must call commitTaskClaim within the window or reservation auto-releases.
func (s *Server) reserveTaskClaim(ctx context.Context, userID, taskKind, taskID string) (bool, string) {
	now := time.Now().UTC()

	// Check existing active leases for this task
	activeLeases, err := s.store.ListActiveTaskLeases(ctx, taskKind, "", now, 1000)
	if err != nil {
		return false, "failed to check existing leases: " + err.Error()
	}

	for _, lease := range activeLeases {
		if lease.TaskID == taskID {
			if lease.HolderUserID == userID {
				return true, "already reserved by you"
			}
			return false, "already claimed by another agent"
		}
	}

	// Create a reservation lease (expires quickly, no rate limit)
	lease := store.TaskLease{
		TaskKind:     taskKind,
		TaskID:       taskID,
		HolderUserID: userID,
		ClaimedAt:    now,
		ExpiresAt:    now.Add(taskClaimReservationWindow),
	}

	// Use ClaimTaskLeaseWithHolderRateLimit with maxClaims=0 to skip rate limit
	_, err = s.store.ClaimTaskLeaseWithHolderRateLimit(ctx, lease, now.Add(-taskClaimReservationWindow), 0)
	if err != nil {
		if err.Error() == store.ErrTaskLeaseConflict.Error() {
			return false, "already reserved"
		}
		return false, "reservation failed: " + err.Error()
	}

	return true, "reserved for 5 minutes"
}

// commitTaskClaim upgrades a reservation to a full claim.
// If reservation has expired, returns false.
func (s *Server) commitTaskClaim(ctx context.Context, userID, taskKind, taskID string) (bool, string) {
	now := time.Now().UTC()

	// Find the user's reservation
	leases, err := s.store.ListActiveTaskLeases(ctx, taskKind, userID, now, 1000)
	if err != nil {
		return false, "failed to find reservation: " + err.Error()
	}

	for _, lease := range leases {
		if lease.TaskID == taskID && lease.HolderUserID == userID {
			if lease.ExpiresAt.Before(now) {
				return false, "reservation expired"
			}
			// Consume the lease to mark as full claim
			_, err := s.store.ConsumeTaskLease(ctx, taskKind, taskID, userID, now)
			if err != nil {
				return false, "commit failed: " + err.Error()
			}
			return true, "claimed successfully"
		}
	}

	return false, "reservation not found"
}
