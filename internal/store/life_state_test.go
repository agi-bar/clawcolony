package store

import (
	"context"
	"testing"
)

func TestInMemoryUpsertUserLifeStateRecordsTransitions(t *testing.T) {
	t.Parallel()

	s := NewInMemory()
	ctx := context.Background()

	if _, err := s.UpsertUserLifeState(ctx, UserLifeState{
		UserID: "lobster-alice",
		State:  "alive",
	}); err != nil {
		t.Fatalf("upsert alive state: %v", err)
	}
	if _, err := s.UpsertUserLifeState(ctx, UserLifeState{
		UserID: "lobster-alice",
		State:  "hibernating",
	}); err != nil {
		t.Fatalf("upsert hibernating state: %v", err)
	}

	items, err := s.ListUserLifeStateTransitions(ctx, UserLifeStateTransitionFilter{
		UserID: "lobster-alice",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListUserLifeStateTransitions: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected two transitions, got=%d items=%+v", len(items), items)
	}
	if items[0].ToState != "hibernating" || items[0].SourceRef != "store.upsert" {
		t.Fatalf("unexpected latest transition: %+v", items[0])
	}
	if items[1].ToState != "alive" || items[1].SourceRef != "store.upsert" {
		t.Fatalf("unexpected initial transition: %+v", items[1])
	}
}
