package config

import "testing"

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("CLAWCOLONY_LISTEN_ADDR", "")
	t.Setenv("CLAWCOLONY_NAMESPACE", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("CLAWCOLONY_INTERNAL_SYNC_TOKEN", "")
	t.Setenv("CLAWCOLONY_API_BASE_URL", "")
	t.Setenv("CLAWCOLONY_SKILL_BASE_URL", "")
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
		t.Fatalf("ClawWorldAPIBase default = %q", cfg.ClawWorldAPIBase)
	}
	if cfg.SkillBaseURL != "" {
		t.Fatalf("SkillBaseURL default = %q, want empty", cfg.SkillBaseURL)
	}
	if cfg.GitHubAppDisplayName != "Clawcolony GitHub Access" {
		t.Fatalf("GitHubAppDisplayName default = %q", cfg.GitHubAppDisplayName)
	}
	if cfg.GitHubAppID != "" || cfg.GitHubAppPrivateKeyPEM != "" || cfg.GitHubAppOrg != "" {
		t.Fatalf("GitHub app org workflow defaults should be empty: app_id=%q org=%q", cfg.GitHubAppID, cfg.GitHubAppOrg)
	}
	if cfg.GitHubAppRepositoryID != "" || cfg.GitHubAppRepositoryOwner != "" || cfg.GitHubAppRepositoryName != "" {
		t.Fatalf("GitHub app repo defaults should be empty: id=%q owner=%q name=%q", cfg.GitHubAppRepositoryID, cfg.GitHubAppRepositoryOwner, cfg.GitHubAppRepositoryName)
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
	if cfg.ActionCostConsume {
		t.Fatal("ActionCostConsume default should be false under v2")
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
	if cfg.TokenEconomyVersion != "v2" {
		t.Fatalf("TokenEconomyVersion default = %q, want v2", cfg.TokenEconomyVersion)
	}
	if cfg.RegistrationGrantToken != 0 {
		t.Fatalf("RegistrationGrantToken default = %d, want 0", cfg.RegistrationGrantToken)
	}
	if cfg.InitialToken != 100000 {
		t.Fatalf("InitialToken default = %d, want 100000", cfg.InitialToken)
	}
	if cfg.TreasuryInitialToken != 1000000000 {
		t.Fatalf("TreasuryInitialToken default = %d, want 1000000000", cfg.TreasuryInitialToken)
	}
	if cfg.DailyTaxUnactivated != 14400 || cfg.DailyTaxActivated != 7200 {
		t.Fatalf("unexpected daily tax defaults: unactivated=%d activated=%d", cfg.DailyTaxUnactivated, cfg.DailyTaxActivated)
	}
	if cfg.DailyFreeCommUnactivated != 50000 || cfg.DailyFreeCommActivated != 200000 {
		t.Fatalf("unexpected free comm defaults: unactivated=%d activated=%d", cfg.DailyFreeCommUnactivated, cfg.DailyFreeCommActivated)
	}
	if cfg.HibernationPeriodTicks != 1440 || cfg.MinRevivalBalance != 50000 {
		t.Fatalf("unexpected hibernation defaults: period=%d min_revival=%d", cfg.HibernationPeriodTicks, cfg.MinRevivalBalance)
	}
}

func TestFromEnvParsesRuntimeFields(t *testing.T) {
	t.Setenv("CLAWCOLONY_LISTEN_ADDR", ":18080")
	t.Setenv("CLAWCOLONY_NAMESPACE", "runtime-test")
	t.Setenv("DATABASE_URL", "postgres://runtime")
	t.Setenv("CLAWCOLONY_INTERNAL_SYNC_TOKEN", "sync-token")
	t.Setenv("CLAWCOLONY_API_BASE_URL", "https://runtime.example")
	t.Setenv("CLAWCOLONY_SKILL_BASE_URL", "https://skills.example")
	t.Setenv("COLONY_REPO_URL", "https://example.com/repo.git")
	t.Setenv("COLONY_REPO_BRANCH", "runtime-lite")
	t.Setenv("COLONY_REPO_LOCAL_PATH", "/tmp/runtime-lite")
	t.Setenv("COLONY_REPO_SYNC_ENABLED", "true")
	t.Setenv("CLAWCOLONY_GITHUB_APP_CLIENT_ID", "app-client")
	t.Setenv("CLAWCOLONY_GITHUB_APP_CLIENT_SECRET", "app-secret")
	t.Setenv("CLAWCOLONY_GITHUB_APP_AUTHORIZE_URL", "https://github.example/login/oauth/authorize")
	t.Setenv("CLAWCOLONY_GITHUB_APP_TOKEN_URL", "https://github.example/login/oauth/access_token")
	t.Setenv("CLAWCOLONY_GITHUB_APP_API_BASE_URL", "https://api.github.example")
	t.Setenv("CLAWCOLONY_GITHUB_APP_DISPLAY_NAME", "Repo Access")
	t.Setenv("CLAWCOLONY_GITHUB_APP_ID", "654321")
	t.Setenv("CLAWCOLONY_GITHUB_APP_PRIVATE_KEY_PEM", "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----")
	t.Setenv("CLAWCOLONY_GITHUB_APP_ORG", "agi-bar")
	t.Setenv("CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_SLUG", "contributors")
	t.Setenv("CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_ID", "111")
	t.Setenv("CLAWCOLONY_GITHUB_APP_MAINTAINER_TEAM_SLUG", "maintainers")
	t.Setenv("CLAWCOLONY_GITHUB_APP_MAINTAINER_TEAM_ID", "222")
	t.Setenv("CLAWCOLONY_GITHUB_APP_REPOSITORY_ID", "987654")
	t.Setenv("CLAWCOLONY_GITHUB_APP_REPOSITORY_OWNER", "agi-bar")
	t.Setenv("CLAWCOLONY_GITHUB_APP_REPOSITORY_NAME", "clawcolony")
	t.Setenv("CLAWCOLONY_GITHUB_APP_ALLOWED_INSTALLATION_ID", "12345")
	t.Setenv("CLAWCOLONY_GITHUB_APP_TOKEN_ENCRYPTION_KEY", "super-secret-key")
	t.Setenv("TOKEN_ECONOMY_VERSION", "v2")
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
	if cfg.SkillBaseURL != "https://skills.example" {
		t.Fatalf("SkillBaseURL = %q, want https://skills.example", cfg.SkillBaseURL)
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
	if cfg.GitHubAppClientID != "app-client" || cfg.GitHubAppClientSecret != "app-secret" {
		t.Fatalf("unexpected GitHub app client config: id=%q secret=%q", cfg.GitHubAppClientID, cfg.GitHubAppClientSecret)
	}
	if cfg.GitHubAppAuthorizeURL != "https://github.example/login/oauth/authorize" || cfg.GitHubAppTokenURL != "https://github.example/login/oauth/access_token" {
		t.Fatalf("unexpected GitHub app auth urls: authorize=%q token=%q", cfg.GitHubAppAuthorizeURL, cfg.GitHubAppTokenURL)
	}
	if cfg.GitHubAppAPIBaseURL != "https://api.github.example" {
		t.Fatalf("GitHubAppAPIBaseURL = %q", cfg.GitHubAppAPIBaseURL)
	}
	if cfg.GitHubAppDisplayName != "Repo Access" {
		t.Fatalf("GitHubAppDisplayName = %q", cfg.GitHubAppDisplayName)
	}
	if cfg.GitHubAppID != "654321" || cfg.GitHubAppOrg != "agi-bar" {
		t.Fatalf("unexpected GitHub app org config: app_id=%q org=%q", cfg.GitHubAppID, cfg.GitHubAppOrg)
	}
	if cfg.GitHubAppContributorTeamSlug != "contributors" || cfg.GitHubAppContributorTeamID != "111" {
		t.Fatalf("unexpected GitHub contributor team config: slug=%q id=%q", cfg.GitHubAppContributorTeamSlug, cfg.GitHubAppContributorTeamID)
	}
	if cfg.GitHubAppMaintainerTeamSlug != "maintainers" || cfg.GitHubAppMaintainerTeamID != "222" {
		t.Fatalf("unexpected GitHub maintainer team config: slug=%q id=%q", cfg.GitHubAppMaintainerTeamSlug, cfg.GitHubAppMaintainerTeamID)
	}
	if cfg.GitHubAppRepositoryID != "987654" || cfg.GitHubAppRepositoryOwner != "agi-bar" || cfg.GitHubAppRepositoryName != "clawcolony" {
		t.Fatalf("unexpected GitHub app repo config: id=%q owner=%q name=%q", cfg.GitHubAppRepositoryID, cfg.GitHubAppRepositoryOwner, cfg.GitHubAppRepositoryName)
	}
	if cfg.GitHubAppAllowedInstallationID != "12345" {
		t.Fatalf("GitHubAppAllowedInstallationID = %q", cfg.GitHubAppAllowedInstallationID)
	}
	if cfg.GitHubAppTokenEncryptionKey != "super-secret-key" {
		t.Fatalf("GitHubAppTokenEncryptionKey = %q", cfg.GitHubAppTokenEncryptionKey)
	}
	if cfg.TokenEconomyVersion != "v2" {
		t.Fatalf("TokenEconomyVersion = %q, want v2", cfg.TokenEconomyVersion)
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
