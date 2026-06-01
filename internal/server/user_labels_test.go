package server

import (
	"reflect"
	"testing"

	"clawcolony/internal/store"
)

func TestResolveUserIDFromLabelsFallbackKeys(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{
			name:   "primary label",
			labels: map[string]string{"clawcolony.user_id": "user-a"},
			want:   "user-a",
		},
		{
			name:   "legacy landlord bot id",
			labels: map[string]string{"landlord.bot_id": "bot-a"},
			want:   "bot-a",
		},
		{
			name:   "generic user id",
			labels: map[string]string{"user_id": "user-generic"},
			want:   "user-generic",
		},
		{
			name:   "empty",
			labels: map[string]string{"foo": "bar"},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveUserIDFromLabels(tc.labels); got != tc.want {
				t.Fatalf("resolveUserIDFromLabels()=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestResolveUserIDFromWorkload(t *testing.T) {
	cases := []struct {
		name     string
		workload string
		labels   map[string]string
		want     string
	}{
		{
			name:     "deployment uses user id directly",
			workload: "user-123",
			want:     "user-123",
		},
		{
			name:     "legacy deployment with bot prefix",
			workload: "bot-user-123",
			want:     "user-123",
		},
		{
			name:     "legacy double bot prefix",
			workload: "bot-bot-123",
			want:     "bot-123",
		},
		{
			name:     "keep explicit bot id",
			workload: "bot-foo-bar",
			want:     "bot-foo-bar",
		},
		{
			name:     "pod name from deployment",
			workload: "user-123-6bcf8bfc86-5jkzb",
			want:     "user-123",
		},
		{
			name:     "pod name from legacy deployment",
			workload: "bot-user-123-c5b8d54d6-n27jh",
			want:     "user-123",
		},
		{
			name:     "label wins",
			workload: "bot-user-123-c5b8d54d6-n27jh",
			labels:   map[string]string{"clawcolony.user_id": "label-user"},
			want:     "label-user",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveUserIDFromWorkload(tc.workload, tc.labels); got != tc.want {
				t.Fatalf("resolveUserIDFromWorkload()=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestWorkloadMatchesUserID(t *testing.T) {
	cases := []struct {
		name     string
		workload string
		labels   map[string]string
		userID   string
		want     bool
	}{
		{
			name:     "label match",
			workload: "anything",
			labels:   map[string]string{"clawcolony.user_id": "user-1"},
			userID:   "user-1",
			want:     true,
		},
		{
			name:     "legacy landlord label match",
			workload: "anything",
			labels:   map[string]string{"landlord.bot_id": "bot-1"},
			userID:   "bot-1",
			want:     true,
		},
		{
			name:     "label mismatch should not fallback to name",
			workload: "user-1-c5b8d54d6-n27jh",
			labels:   map[string]string{"clawcolony.user_id": "user-2"},
			userID:   "user-1",
			want:     false,
		},
		{
			name:     "pod name match",
			workload: "user-1-c5b8d54d6-n27jh",
			userID:   "user-1",
			want:     true,
		},
		{
			name:     "legacy pod name match",
			workload: "bot-user-1-c5b8d54d6-n27jh",
			userID:   "user-1",
			want:     true,
		},
		{
			name:     "negative",
			workload: "user-2-c5b8d54d6-n27jh",
			userID:   "user-1",
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workloadMatchesUserID(tc.workload, tc.labels, tc.userID); got != tc.want {
				t.Fatalf("workloadMatchesUserID()=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestMergeMissingActiveBots(t *testing.T) {
	items := []store.Bot{
		{BotID: "user-1", Name: "user-1", Provider: "openclaw", Status: "running", Initialized: true},
	}
	active := map[string]struct{}{
		"user-1": {},
		"user-2": {},
	}
	got := mergeMissingActiveBots(items, active)
	if len(got) != 2 {
		t.Fatalf("mergeMissingActiveBots len=%d want=2", len(got))
	}
	gotIDs := []string{got[0].BotID, got[1].BotID}
	wantIDs := []string{"user-1", "user-2"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("mergeMissingActiveBots ids=%v want=%v", gotIDs, wantIDs)
	}
	if got[1].Provider != "openclaw" || got[1].Status != "running" || !got[1].Initialized {
		t.Fatalf("synthetic bot defaults not applied: %+v", got[1])
	}
}
