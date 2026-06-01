package config

import "testing"

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("CLAWCOLONY_LISTEN_ADDR", "")
	t.Setenv("CLAWCOLONY_NAMESPACE", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CLAWCOLONY_INTERNAL_SYNC_TOKEN", "")
	t.Setenv("CLAWCOLONY_API_BASE_URL", "")
	t.Setenv("COLONY_REPO_URL", "")
	t.Setenv("COLONY_REPO_BRANCH", "")
	t.Setenv("COLONY_REPO_LOCAL_PATH", "")
	t.Setenv("COLONY_REPO_SYNC_ENABLED", "")
	t.Setenv("AUTONOMY_REMINDER_INTERVAL_TICKS", "")
	t.Setenv("COMMUNITY_COMM_REMINDER_INTERVAL_TICKS", "")
	t.Setenv("KB_ENROLLMENT_REMINDER_INTERVAL_TICKS", "")
	t.Setenv("KB_VOTING_REMINDER_INTERVAL_TICKS", "")

	cfg := FromEnv()
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr default = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.ClawWorldNamespace != "freewill" {
		t.Fatalf("ClawWorldNamespace default = %q, want freewill", cfg.ClawWorldNamespace)
	}
	if cfg.InternalSyncToken != "" {
		t.Fatalf("InternalSyncToken default = %q, want empty", cfg.InternalSyncToken)
	}
	if cfg.ClawWorldAPIBase != "http://localhost:8080" {
		t.Fatalf("ClawWorldAPIBase default = %q, want http://localhost:8080", cfg.ClawWorldAPIBase)
	}
	if cfg.ColonyRepoBranch != "main" {
		t.Fatalf("ColonyRepoBranch default = %q, want main", cfg.ColonyRepoBranch)
	}
	if cfg.ColonyRepoLocalPath != "/tmp/clawcolony-civilization-repo" {
		t.Fatalf("ColonyRepoLocalPath default = %q", cfg.ColonyRepoLocalPath)
	}
	if cfg.ColonyRepoSync {
		t.Fatal("ColonyRepoSync default should be false")
	}
	if cfg.AutonomyReminderIntervalTicks != 0 {
		t.Fatalf("AutonomyReminderIntervalTicks default = %d, want 0", cfg.AutonomyReminderIntervalTicks)
	}
	if cfg.CommunityCommReminderIntervalTicks != 0 {
		t.Fatalf("CommunityCommReminderIntervalTicks default = %d, want 0", cfg.CommunityCommReminderIntervalTicks)
	}
	if cfg.KBEnrollmentReminderIntervalTicks != 0 {
		t.Fatalf("KBEnrollmentReminderIntervalTicks default = %d, want 0", cfg.KBEnrollmentReminderIntervalTicks)
	}
	if cfg.KBVotingReminderIntervalTicks != 0 {
		t.Fatalf("KBVotingReminderIntervalTicks default = %d, want 0", cfg.KBVotingReminderIntervalTicks)
	}
	if cfg.RegistrationGrantToken != 0 {
		t.Fatalf("RegistrationGrantToken default = %d, want 0", cfg.RegistrationGrantToken)
	}
	if cfg.SocialRewardXAuth != 10000 {
		t.Fatalf("SocialRewardXAuth default = %d, want 10000", cfg.SocialRewardXAuth)
	}
	if cfg.SocialRewardXMention != 10000 {
		t.Fatalf("SocialRewardXMention default = %d, want 10000", cfg.SocialRewardXMention)
	}
	if cfg.SocialRewardGitHubAuth != 10000 {
		t.Fatalf("SocialRewardGitHubAuth default = %d, want 10000", cfg.SocialRewardGitHubAuth)
	}
	if cfg.SocialRewardGitHubStar != 10000 {
		t.Fatalf("SocialRewardGitHubStar default = %d, want 10000", cfg.SocialRewardGitHubStar)
	}
	if cfg.SocialRewardGitHubFork != 10000 {
		t.Fatalf("SocialRewardGitHubFork default = %d, want 10000", cfg.SocialRewardGitHubFork)
	}
}

func TestFromEnvParsesRuntimeFields(t *testing.T) {
	t.Setenv("CLAWCOLONY_LISTEN_ADDR", ":18080")
	t.Setenv("CLAWCOLONY_NAMESPACE", "runtime-test")
	t.Setenv("DATABASE_URL", "postgres://runtime")
	t.Setenv("CLAWCOLONY_INTERNAL_SYNC_TOKEN", "sync-token")
	t.Setenv("CLAWCOLONY_API_BASE_URL", "https://runtime.example")
	t.Setenv("COLONY_REPO_URL", "https://example.com/repo.git")
	t.Setenv("COLONY_REPO_BRANCH", "runtime-lite")
	t.Setenv("COLONY_REPO_LOCAL_PATH", "/tmp/runtime-lite")
	t.Setenv("COLONY_REPO_SYNC_ENABLED", "true")
	t.Setenv("AUTONOMY_REMINDER_INTERVAL_TICKS", "240")
	t.Setenv("COMMUNITY_COMM_REMINDER_INTERVAL_TICKS", "480")
	t.Setenv("KB_ENROLLMENT_REMINDER_INTERVAL_TICKS", "360")
	t.Setenv("KB_VOTING_REMINDER_INTERVAL_TICKS", "120")

	cfg := FromEnv()
	if cfg.ListenAddr != ":18080" {
		t.Fatalf("ListenAddr = %q, want :18080", cfg.ListenAddr)
	}
	if cfg.ClawWorldNamespace != "runtime-test" {
		t.Fatalf("ClawWorldNamespace = %q, want runtime-test", cfg.ClawWorldNamespace)
	}
	if cfg.DatabaseURL != "postgres://runtime" {
		t.Fatalf("DatabaseURL = %q, want postgres://runtime", cfg.DatabaseURL)
	}
	if cfg.InternalSyncToken != "sync-token" {
		t.Fatalf("InternalSyncToken = %q, want sync-token", cfg.InternalSyncToken)
	}
	if cfg.ClawWorldAPIBase != "https://runtime.example" {
		t.Fatalf("ClawWorldAPIBase = %q, want https://runtime.example", cfg.ClawWorldAPIBase)
	}
	if cfg.ColonyRepoURL != "https://example.com/repo.git" {
		t.Fatalf("ColonyRepoURL = %q, want repo url", cfg.ColonyRepoURL)
	}
	if cfg.ColonyRepoBranch != "runtime-lite" {
		t.Fatalf("ColonyRepoBranch = %q, want runtime-lite", cfg.ColonyRepoBranch)
	}
	if cfg.ColonyRepoLocalPath != "/tmp/runtime-lite" {
		t.Fatalf("ColonyRepoLocalPath = %q, want /tmp/runtime-lite", cfg.ColonyRepoLocalPath)
	}
	if !cfg.ColonyRepoSync {
		t.Fatal("ColonyRepoSync should parse true")
	}
	if cfg.AutonomyReminderIntervalTicks != 240 {
		t.Fatalf("AutonomyReminderIntervalTicks = %d, want 240", cfg.AutonomyReminderIntervalTicks)
	}
	if cfg.CommunityCommReminderIntervalTicks != 480 {
		t.Fatalf("CommunityCommReminderIntervalTicks = %d, want 480", cfg.CommunityCommReminderIntervalTicks)
	}
	if cfg.KBEnrollmentReminderIntervalTicks != 360 {
		t.Fatalf("KBEnrollmentReminderIntervalTicks = %d, want 360", cfg.KBEnrollmentReminderIntervalTicks)
	}
	if cfg.KBVotingReminderIntervalTicks != 120 {
		t.Fatalf("KBVotingReminderIntervalTicks = %d, want 120", cfg.KBVotingReminderIntervalTicks)
	}
}

func TestFromEnvInvalidValuesFallBack(t *testing.T) {
	t.Setenv("COLONY_REPO_SYNC_ENABLED", "maybe")
	t.Setenv("AUTONOMY_REMINDER_INTERVAL_TICKS", "bad")
	t.Setenv("COMMUNITY_COMM_REMINDER_INTERVAL_TICKS", "bad")
	t.Setenv("KB_ENROLLMENT_REMINDER_INTERVAL_TICKS", "bad")
	t.Setenv("KB_VOTING_REMINDER_INTERVAL_TICKS", "bad")

	cfg := FromEnv()
	if cfg.ColonyRepoSync {
		t.Fatal("invalid bool should fall back to false")
	}
	if cfg.AutonomyReminderIntervalTicks != 0 {
		t.Fatalf("invalid autonomy interval should fall back to 0, got %d", cfg.AutonomyReminderIntervalTicks)
	}
	if cfg.CommunityCommReminderIntervalTicks != 0 {
		t.Fatalf("invalid community interval should fall back to 0, got %d", cfg.CommunityCommReminderIntervalTicks)
	}
	if cfg.KBEnrollmentReminderIntervalTicks != 0 {
		t.Fatalf("invalid kb enroll interval should fall back to 0, got %d", cfg.KBEnrollmentReminderIntervalTicks)
	}
	if cfg.KBVotingReminderIntervalTicks != 0 {
		t.Fatalf("invalid kb vote interval should fall back to 0, got %d", cfg.KBVotingReminderIntervalTicks)
	}
}
