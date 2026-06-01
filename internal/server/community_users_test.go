package server

import (
	"testing"

	"clawcolony/internal/store"
)

func TestIsSystemRuntimeUserID(t *testing.T) {
	tests := []struct {
		name   string
		userID string
		want   bool
	}{
		{name: "empty", userID: "", want: false},
		{name: "admin", userID: clawWorldSystemID, want: true},
		{name: "treasury", userID: clawTreasurySystemID, want: true},
		{name: "admin mixed case", userID: "Clawcolony-Admin", want: true},
		{name: "treasury mixed case", userID: "Clawcolony-Treasury", want: true},
		{name: "legacy system id", userID: "clawcolony-system", want: true},
		{name: "legacy colony id", userID: "clawcolony", want: true},
		{name: "normal user", userID: "u-visible", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSystemRuntimeUserID(tt.userID); got != tt.want {
				t.Fatalf("isSystemRuntimeUserID(%q)=%v want=%v", tt.userID, got, tt.want)
			}
		})
	}
}

func TestIsSystemTokenUserIDCaseInsensitive(t *testing.T) {
	tests := []struct {
		userID string
		want   bool
	}{
		{userID: clawWorldSystemID, want: true},
		{userID: clawTreasurySystemID, want: true},
		{userID: "Clawcolony-Admin", want: true},
		{userID: "CLAWCOLONY-TREASURY", want: true},
		{userID: "u-normal", want: false},
	}
	for _, tt := range tests {
		if got := isSystemTokenUserID(tt.userID); got != tt.want {
			t.Fatalf("isSystemTokenUserID(%q)=%v want=%v", tt.userID, got, tt.want)
		}
	}
}

func TestIsCommunityVisibleBot(t *testing.T) {
	tests := []struct {
		name string
		bot  store.Bot
		want bool
	}{
		{
			name: "normal running bot",
			bot:  store.Bot{BotID: "u-visible", Provider: "openclaw", Status: "running"},
			want: true,
		},
		{
			name: "empty id hidden",
			bot:  store.Bot{BotID: "", Provider: "openclaw", Status: "running"},
			want: false,
		},
		{
			name: "system id hidden",
			bot:  store.Bot{BotID: clawTreasurySystemID, Provider: "openclaw", Status: "running"},
			want: false,
		},
		{
			name: "provider system hidden",
			bot:  store.Bot{BotID: "u-system-provider", Provider: "system", Status: "running"},
			want: false,
		},
		{
			name: "status system hidden",
			bot:  store.Bot{BotID: "u-system-status", Provider: "openclaw", Status: "system"},
			want: false,
		},
		{
			name: "mixed case admin id hidden",
			bot:  store.Bot{BotID: "Clawcolony-Admin", Provider: "openclaw", Status: "running"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCommunityVisibleBot(tt.bot); got != tt.want {
				t.Fatalf("isCommunityVisibleBot(%+v)=%v want=%v", tt.bot, got, tt.want)
			}
		})
	}
}

func TestFilterCommunityVisibleBots(t *testing.T) {
	in := []store.Bot{
		{BotID: clawWorldSystemID, Provider: "system", Status: "running"},
		{BotID: "u-visible-a", Provider: "openclaw", Status: "running"},
		{BotID: clawTreasurySystemID, Provider: "system", Status: "system"},
		{BotID: "u-visible-b", Provider: "openclaw", Status: "running"},
	}
	out := filterCommunityVisibleBots(in)
	if len(out) != 2 {
		t.Fatalf("filterCommunityVisibleBots len=%d want=2 out=%+v", len(out), out)
	}
	if out[0].BotID != "u-visible-a" || out[1].BotID != "u-visible-b" {
		t.Fatalf("unexpected filtered order: %+v", out)
	}
}
