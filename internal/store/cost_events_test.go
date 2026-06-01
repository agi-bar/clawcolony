package store

import (
	"context"
	"testing"
	"time"
)

func TestCostEventRecipientUserID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		metaJSON string
		want     string
	}{
		{
			name:     "empty",
			metaJSON: "",
			want:     "",
		},
		{
			name:     "compact json",
			metaJSON: `{"to_user_id":"lobster-bob","memo":"stipend"}`,
			want:     "lobster-bob",
		},
		{
			name: "pretty json",
			metaJSON: `{
  "memo": "stipend",
  "to_user_id": "lobster-bob"
}`,
			want: "lobster-bob",
		},
		{
			name:     "missing recipient",
			metaJSON: `{"memo":"stipend"}`,
			want:     "",
		},
		{
			name:     "invalid json",
			metaJSON: `{"to_user_id":`,
			want:     "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := costEventRecipientUserID(tc.metaJSON); got != tc.want {
				t.Fatalf("costEventRecipientUserID()=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestInMemoryListCostEventsByInvolvementMatchesRecipientFromMetaJSON(t *testing.T) {
	t.Parallel()

	s := NewInMemory()
	ctx := context.Background()
	relevantTime := time.Date(2026, 3, 10, 20, 0, 0, 0, time.UTC)
	unrelatedTime := relevantTime.Add(time.Minute)

	if _, err := s.AppendCostEvent(ctx, CostEvent{
		UserID:    "lobster-alice",
		CostType:  "econ.transfer.out",
		Amount:    5,
		Units:     1,
		MetaJSON:  "{\n  \"to_user_id\": \"lobster-bob\",\n  \"memo\": \"pretty json\"\n}",
		CreatedAt: relevantTime,
	}); err != nil {
		t.Fatalf("append relevant cost event: %v", err)
	}
	if _, err := s.AppendCostEvent(ctx, CostEvent{
		UserID:    "lobster-charlie",
		CostType:  "econ.transfer.out",
		Amount:    3,
		Units:     1,
		MetaJSON:  `{"to_user_id":"lobster-dora"}`,
		CreatedAt: unrelatedTime,
	}); err != nil {
		t.Fatalf("append unrelated cost event: %v", err)
	}

	items, err := s.ListCostEventsByInvolvement(ctx, "lobster-bob", 10)
	if err != nil {
		t.Fatalf("ListCostEventsByInvolvement: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly one involved cost event, got=%d items=%+v", len(items), items)
	}
	if items[0].UserID != "lobster-alice" || costEventRecipientUserID(items[0].MetaJSON) != "lobster-bob" {
		t.Fatalf("unexpected involved cost event: %+v", items[0])
	}
}
