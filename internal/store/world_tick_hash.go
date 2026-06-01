package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// ComputeWorldTickHash returns a stable sha256 hash for a world tick record.
// The payload deliberately excludes ID so replay/import keeps deterministic hash identity.
func ComputeWorldTickHash(item WorldTickRecord, prevHash string) string {
	payload := fmt.Sprintf(
		"tick_id=%d\nstarted_at=%s\nduration_ms=%d\ntrigger_type=%s\nreplay_of_tick_id=%d\nstatus=%s\nerror_text=%s\nprev_hash=%s\n",
		item.TickID,
		item.StartedAt.UTC().Format(time.RFC3339Nano),
		item.DurationMS,
		item.TriggerType,
		item.ReplayOfTickID,
		item.Status,
		item.ErrorText,
		prevHash,
	)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}
