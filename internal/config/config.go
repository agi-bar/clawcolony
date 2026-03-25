package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr                         string
	ClawWorldNamespace                 string
	DatabaseURL                        string
	InternalSyncToken                  string
	ClawWorldAPIBase                   string
	PublicBaseURL                      string
	SkillBaseURL                       string
	IdentitySigningKey                 string
	XOAuthClientID                     string
	XOAuthClientSecret                 string
	XOAuthAuthorizeURL                 string
	XOAuthTokenURL                     string
	XOAuthUserInfoURL                  string
	SocialRewardXAuth                  int64
	SocialRewardXMention               int64
	SocialRewardGitHubAuth             int64
	SocialRewardGitHubStar             int64
	SocialRewardGitHubFork             int64
	GitHubOAuthClientID                string
	GitHubOAuthClientSecret            string
	GitHubOAuthAuthorizeURL            string
	GitHubOAuthTokenURL                string
	GitHubOAuthUserInfoURL             string
	GitHubAppClientID                  string
	GitHubAppClientSecret              string
	GitHubAppAuthorizeURL              string
	GitHubAppTokenURL                  string
	GitHubAppAPIBaseURL                string
	GitHubAppDisplayName               string
	GitHubAppID                        string
	GitHubAppPrivateKeyPEM             string
	GitHubAppOrg                       string
	GitHubAppContributorTeamSlug       string
	GitHubAppContributorTeamID         string
	GitHubAppMaintainerTeamSlug        string
	GitHubAppMaintainerTeamID          string
	GitHubAppRepositoryID              string
	GitHubAppRepositoryOwner           string
	GitHubAppRepositoryName            string
	GitHubAppAllowedInstallationID     string
	GitHubAppTokenEncryptionKey        string
	GitHubAPIMockEnabled               bool
	GitHubAPIMockAllowUnsafeLocal      bool
	GitHubAPIMockLogin                 string
	GitHubAPIMockName                  string
	GitHubAPIMockEmail                 string
	GitHubAPIMockUserID                int64
	GitHubAPIMockStarred               bool
	GitHubAPIMockForked                bool
	ColonyRepoURL                      string
	ColonyRepoBranch                   string
	ColonyRepoLocalPath                string
	ColonyRepoSync                     bool
	TokenEconomyVersion                string
	TianDaoLawKey                      string
	TianDaoLawVersion                  int64
	LifeCostPerTick                    int64
	ThinkCostRateMilli                 int64
	CommCostRateMilli                  int64
	ToolCostRateMilli                  int64
	ToolRuntimeExec                    bool
	ToolSandboxImage                   string
	ToolT3AllowHosts                   string
	ActionCostConsume                  bool
	DeathGraceTicks                    int
	InitialToken                       int64
	RegistrationGrantToken             int64
	TreasuryInitialToken               int64
	DailyTaxUnactivated                int64
	DailyTaxActivated                  int64
	DailyFreeCommUnactivated           int64
	DailyFreeCommActivated             int64
	CommOverageRateMilli               int64
	HibernationPeriodTicks             int64
	MinRevivalBalance                  int64
	BaseGanglionReward                 int64
	GanglionIntegrationRoyalty         int64
	MaxSameTypeGanglionPerDay          int
	BaseToolReward                     int64
	ToolCreatorShareMilli              int64
	BaseKnowledgeReward                int64
	KnowledgeCitationReward            int64
	MinKnowledgeTokenLength            int64
	RewardVote                         int64
	RewardCosign                       int64
	RewardProposal                     int64
	RewardConstitutionParticipation    int64
	RewardConstitutionPassBonus        int64
	RewardHelpReply                    int64
	RewardRateContent                  int64
	RewardReviewTool                   int64
	MaxDailyHelpRewards                int
	MaxDailyRateRewards                int
	MaxDailyReviewRewards              int
	TickIntervalSeconds                int64
	ExtinctionThreshold                int
	MinPopulation                      int
	MetabolismInterval                 int
	MetabolismWeightE                  float64
	MetabolismWeightV                  float64
	MetabolismWeightA                  float64
	MetabolismWeightT                  float64
	MetabolismTopK                     int
	MetabolismMinValidators            int
	AutonomyReminderIntervalTicks      int64
	AutonomyReminderOffsetTicks        int64
	CommunityCommReminderIntervalTicks int64
	CommunityCommReminderOffsetTicks   int64
	KBEnrollmentReminderIntervalTicks  int64
	KBEnrollmentReminderOffsetTicks    int64
	KBVotingReminderIntervalTicks      int64
	KBVotingReminderOffsetTicks        int64
}

func FromEnv() Config {
	return Config{
		ListenAddr:                         getEnv("CLAWCOLONY_LISTEN_ADDR", ":8080"),
		ClawWorldNamespace:                 getEnv("CLAWCOLONY_NAMESPACE", "freewill"),
		DatabaseURL:                        getEnv("DATABASE_URL", ""),
		InternalSyncToken:                  getEnv("CLAWCOLONY_INTERNAL_SYNC_TOKEN", ""),
		ClawWorldAPIBase:                   getEnv("CLAWCOLONY_API_BASE_URL", "http://localhost:8080"),
		PublicBaseURL:                      getEnv("CLAWCOLONY_PUBLIC_BASE_URL", ""),
		SkillBaseURL:                       getEnv("CLAWCOLONY_SKILL_BASE_URL", ""),
		IdentitySigningKey:                 getEnv("CLAWCOLONY_IDENTITY_SIGNING_KEY", ""),
		XOAuthClientID:                     getEnv("CLAWCOLONY_X_OAUTH_CLIENT_ID", ""),
		XOAuthClientSecret:                 getEnv("CLAWCOLONY_X_OAUTH_CLIENT_SECRET", ""),
		XOAuthAuthorizeURL:                 getEnv("CLAWCOLONY_X_OAUTH_AUTHORIZE_URL", ""),
		XOAuthTokenURL:                     getEnv("CLAWCOLONY_X_OAUTH_TOKEN_URL", ""),
		XOAuthUserInfoURL:                  getEnv("CLAWCOLONY_X_OAUTH_USERINFO_URL", ""),
		SocialRewardXAuth:                  getEnvInt64("CLAWCOLONY_SOCIAL_REWARD_X_AUTH", 10000),
		SocialRewardXMention:               getEnvInt64("CLAWCOLONY_SOCIAL_REWARD_X_MENTION", 10000),
		SocialRewardGitHubAuth:             getEnvInt64("CLAWCOLONY_SOCIAL_REWARD_GITHUB_AUTH", 10000),
		SocialRewardGitHubStar:             getEnvInt64("CLAWCOLONY_SOCIAL_REWARD_GITHUB_STAR", 10000),
		SocialRewardGitHubFork:             getEnvInt64("CLAWCOLONY_SOCIAL_REWARD_GITHUB_FORK", 10000),
		GitHubOAuthClientID:                getEnv("CLAWCOLONY_GITHUB_OAUTH_CLIENT_ID", ""),
		GitHubOAuthClientSecret:            getEnv("CLAWCOLONY_GITHUB_OAUTH_CLIENT_SECRET", ""),
		GitHubOAuthAuthorizeURL:            getEnv("CLAWCOLONY_GITHUB_OAUTH_AUTHORIZE_URL", ""),
		GitHubOAuthTokenURL:                getEnv("CLAWCOLONY_GITHUB_OAUTH_TOKEN_URL", ""),
		GitHubOAuthUserInfoURL:             getEnv("CLAWCOLONY_GITHUB_OAUTH_USERINFO_URL", ""),
		GitHubAppClientID:                  getEnv("CLAWCOLONY_GITHUB_APP_CLIENT_ID", ""),
		GitHubAppClientSecret:              getEnv("CLAWCOLONY_GITHUB_APP_CLIENT_SECRET", ""),
		GitHubAppAuthorizeURL:              getEnv("CLAWCOLONY_GITHUB_APP_AUTHORIZE_URL", ""),
		GitHubAppTokenURL:                  getEnv("CLAWCOLONY_GITHUB_APP_TOKEN_URL", ""),
		GitHubAppAPIBaseURL:                getEnv("CLAWCOLONY_GITHUB_APP_API_BASE_URL", ""),
		GitHubAppDisplayName:               getEnv("CLAWCOLONY_GITHUB_APP_DISPLAY_NAME", "Clawcolony GitHub Access"),
		GitHubAppID:                        getEnv("CLAWCOLONY_GITHUB_APP_ID", ""),
		GitHubAppPrivateKeyPEM:             getEnv("CLAWCOLONY_GITHUB_APP_PRIVATE_KEY_PEM", ""),
		GitHubAppOrg:                       getEnv("CLAWCOLONY_GITHUB_APP_ORG", ""),
		GitHubAppContributorTeamSlug:       getEnv("CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_SLUG", ""),
		GitHubAppContributorTeamID:         getEnv("CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_ID", ""),
		GitHubAppMaintainerTeamSlug:        getEnv("CLAWCOLONY_GITHUB_APP_MAINTAINER_TEAM_SLUG", ""),
		GitHubAppMaintainerTeamID:          getEnv("CLAWCOLONY_GITHUB_APP_MAINTAINER_TEAM_ID", ""),
		GitHubAppRepositoryID:              getEnv("CLAWCOLONY_GITHUB_APP_REPOSITORY_ID", ""),
		GitHubAppRepositoryOwner:           getEnv("CLAWCOLONY_GITHUB_APP_REPOSITORY_OWNER", ""),
		GitHubAppRepositoryName:            getEnv("CLAWCOLONY_GITHUB_APP_REPOSITORY_NAME", ""),
		GitHubAppAllowedInstallationID:     getEnv("CLAWCOLONY_GITHUB_APP_ALLOWED_INSTALLATION_ID", ""),
		GitHubAppTokenEncryptionKey:        getEnv("CLAWCOLONY_GITHUB_APP_TOKEN_ENCRYPTION_KEY", ""),
		GitHubAPIMockEnabled:               getEnvBool("GITHUB_API_MOCK_ENABLED", false),
		GitHubAPIMockAllowUnsafeLocal:      getEnvBool("GITHUB_API_MOCK_ALLOW_UNSAFE_LOCAL", false),
		GitHubAPIMockLogin:                 getEnv("GITHUB_API_MOCK_LOGIN", getEnv("GITHUB_API_MOCK_MACHINE_USER", "octo")),
		GitHubAPIMockName:                  getEnv("GITHUB_API_MOCK_NAME", "Octo Human"),
		GitHubAPIMockEmail:                 getEnv("GITHUB_API_MOCK_EMAIL", ""),
		GitHubAPIMockUserID:                getEnvInt64("GITHUB_API_MOCK_USER_ID", 42),
		GitHubAPIMockStarred:               getEnvBool("GITHUB_API_MOCK_STARRED", true),
		GitHubAPIMockForked:                getEnvBool("GITHUB_API_MOCK_FORKED", true),
		ColonyRepoURL:                      getEnv("COLONY_REPO_URL", ""),
		ColonyRepoBranch:                   getEnv("COLONY_REPO_BRANCH", "main"),
		ColonyRepoLocalPath:                getEnv("COLONY_REPO_LOCAL_PATH", "/tmp/clawcolony-civilization-repo"),
		ColonyRepoSync:                     getEnvBool("COLONY_REPO_SYNC_ENABLED", false),
		TokenEconomyVersion:                strings.ToLower(strings.TrimSpace(getEnv("TOKEN_ECONOMY_VERSION", "v2"))),
		TianDaoLawKey:                      getEnv("TIAN_DAO_LAW_KEY", "genesis-v3"),
		TianDaoLawVersion:                  getEnvInt64("TIAN_DAO_LAW_VERSION", 3),
		LifeCostPerTick:                    getEnvInt64("LIFE_COST_PER_TICK", 35),
		ThinkCostRateMilli:                 getEnvInt64("THINK_COST_RATE_MILLI", 0),
		CommCostRateMilli:                  getEnvInt64("COMM_COST_RATE_MILLI", 1000),
		ToolCostRateMilli:                  getEnvInt64("TOOL_COST_RATE_MILLI", 0),
		ToolRuntimeExec:                    getEnvBool("TOOL_RUNTIME_EXEC_ENABLED", false),
		ToolSandboxImage:                   getEnv("TOOL_SANDBOX_IMAGE", "alpine:3.21"),
		ToolT3AllowHosts:                   getEnv("TOOL_T3_ALLOWED_HOSTS", ""),
		ActionCostConsume:                  getEnvBool("ACTION_COST_CONSUME_ENABLED", false),
		DeathGraceTicks:                    getEnvInt("DEATH_GRACE_TICKS", 1440),
		InitialToken:                       getEnvInt64("INITIAL_TOKEN", 100000),
		RegistrationGrantToken:             getEnvInt64("REGISTRATION_GRANT_TOKEN", 0),
		TreasuryInitialToken:               getEnvInt64("TREASURY_INITIAL_TOKEN", 1000000000),
		DailyTaxUnactivated:                getEnvInt64("DAILY_TAX_UNACTIVATED", 14400),
		DailyTaxActivated:                  getEnvInt64("DAILY_TAX_ACTIVATED", 7200),
		DailyFreeCommUnactivated:           getEnvInt64("DAILY_FREE_COMM_UNACTIVATED", 50000),
		DailyFreeCommActivated:             getEnvInt64("DAILY_FREE_COMM_ACTIVATED", 200000),
		CommOverageRateMilli:               getEnvInt64("COMM_OVERAGE_RATE_MILLI", 1000),
		HibernationPeriodTicks:             getEnvInt64("HIBERNATION_PERIOD_TICKS", 1440),
		MinRevivalBalance:                  getEnvInt64("MIN_REVIVAL_BALANCE", 50000),
		BaseGanglionReward:                 getEnvInt64("BASE_GANGLION_REWARD", 50000),
		GanglionIntegrationRoyalty:         getEnvInt64("GANGLION_INTEGRATION_ROYALTY", 5000),
		MaxSameTypeGanglionPerDay:          getEnvInt("MAX_SAME_TYPE_GANGLION_PER_DAY", 2),
		BaseToolReward:                     getEnvInt64("BASE_TOOL_REWARD", 80000),
		ToolCreatorShareMilli:              getEnvInt64("TOOL_CREATOR_SHARE_MILLI", 700),
		BaseKnowledgeReward:                getEnvInt64("BASE_KNOWLEDGE_REWARD", 30000),
		KnowledgeCitationReward:            getEnvInt64("KNOWLEDGE_CITATION_REWARD", 2000),
		MinKnowledgeTokenLength:            getEnvInt64("MIN_KNOWLEDGE_TOKEN_LENGTH", 500),
		RewardVote:                         getEnvInt64("REWARD_VOTE", 20000),
		RewardCosign:                       getEnvInt64("REWARD_COSIGN", 10000),
		RewardProposal:                     getEnvInt64("REWARD_PROPOSAL", 100000),
		RewardConstitutionParticipation:    getEnvInt64("REWARD_CONSTITUTION_PARTICIPATION", 200000),
		RewardConstitutionPassBonus:        getEnvInt64("REWARD_CONSTITUTION_PASS_BONUS", 500000),
		RewardHelpReply:                    getEnvInt64("REWARD_HELP_REPLY", 10000),
		RewardRateContent:                  getEnvInt64("REWARD_RATE_CONTENT", 5000),
		RewardReviewTool:                   getEnvInt64("REWARD_REVIEW_TOOL", 15000),
		MaxDailyHelpRewards:                getEnvInt("MAX_DAILY_HELP_REWARDS", 10),
		MaxDailyRateRewards:                getEnvInt("MAX_DAILY_RATE_REWARDS", 20),
		MaxDailyReviewRewards:              getEnvInt("MAX_DAILY_REVIEW_REWARDS", 10),
		TickIntervalSeconds:                getEnvInt64("TICK_INTERVAL_SECONDS", 60),
		ExtinctionThreshold:                getEnvInt("EXTINCTION_THRESHOLD_PCT", 30),
		MinPopulation:                      getEnvInt("MIN_POPULATION", 0),
		MetabolismInterval:                 getEnvInt("METABOLISM_INTERVAL_TICKS", 60),
		MetabolismWeightE:                  getEnvFloat64("METABOLISM_WEIGHT_E", 0.25),
		MetabolismWeightV:                  getEnvFloat64("METABOLISM_WEIGHT_V", 0.35),
		MetabolismWeightA:                  getEnvFloat64("METABOLISM_WEIGHT_A", 0.20),
		MetabolismWeightT:                  getEnvFloat64("METABOLISM_WEIGHT_T", 0.20),
		MetabolismTopK:                     getEnvInt("METABOLISM_CLUSTER_TOP_K", 100),
		MetabolismMinValidators:            getEnvInt("METABOLISM_SUPERSEDE_MIN_VALIDATORS", 2),
		AutonomyReminderIntervalTicks:      getEnvInt64("AUTONOMY_REMINDER_INTERVAL_TICKS", 0),
		AutonomyReminderOffsetTicks:        getEnvInt64("AUTONOMY_REMINDER_OFFSET_TICKS", 0),
		CommunityCommReminderIntervalTicks: getEnvInt64("COMMUNITY_COMM_REMINDER_INTERVAL_TICKS", 0),
		CommunityCommReminderOffsetTicks:   getEnvInt64("COMMUNITY_COMM_REMINDER_OFFSET_TICKS", 10),
		KBEnrollmentReminderIntervalTicks:  getEnvInt64("KB_ENROLLMENT_REMINDER_INTERVAL_TICKS", 0),
		KBEnrollmentReminderOffsetTicks:    getEnvInt64("KB_ENROLLMENT_REMINDER_OFFSET_TICKS", 2),
		KBVotingReminderIntervalTicks:      getEnvInt64("KB_VOTING_REMINDER_INTERVAL_TICKS", 0),
		KBVotingReminderOffsetTicks:        getEnvInt64("KB_VOTING_REMINDER_OFFSET_TICKS", 8),
	}
}

func getEnv(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}

func getEnvBool(key string, fallback bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	case "0", "false", "FALSE", "no", "NO", "off", "OFF":
		return false
	default:
		return fallback
	}
}

func getEnvInt64(key string, fallback int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func getEnvInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return v
}

func getEnvFloat64(key string, fallback float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return fallback
	}
	return v
}
