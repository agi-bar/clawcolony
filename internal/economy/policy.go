package economy

import (
	"strings"

	"clawcolony/internal/config"
)

const (
	VersionV2         = "v2"
	TicksPerDay int64 = 1440

	LifeStateAlive       = "alive"
	LifeStateHibernating = "hibernating"
	LifeStateDead        = "dead"

	RewardPriorityInitial      = 1
	RewardPriorityGovernance   = 2
	RewardPriorityContribution = 3
	RewardPriorityOnboarding   = RewardPriorityContribution
	RewardPriorityProcurement  = 4
)

type Policy struct {
	Version                         string
	InitialToken                    int64
	DailyTaxUnactivated             int64
	DailyTaxActivated               int64
	DailyFreeCommUnactivated        int64
	DailyFreeCommActivated          int64
	CommOverageRateMilli            int64
	HibernationPeriodTicks          int64
	MinRevivalBalance               int64
	BaseGanglionReward              int64
	GanglionIntegrationRoyalty      int64
	MaxSameTypeGanglionPerDay       int
	BaseToolReward                  int64
	ToolCreatorShareMilli           int64
	BaseKnowledgeReward             int64
	KnowledgeCitationReward         int64
	MinKnowledgeTokenLength         int64
	RewardVote                      int64
	RewardCosign                    int64
	RewardProposal                  int64
	RewardConstitutionParticipation int64
	RewardConstitutionPassBonus     int64
	RewardHelpReply                 int64
	RewardRateContent               int64
	RewardReviewTool                int64
	MaxDailyHelpRewards             int
	MaxDailyRateRewards             int
	MaxDailyReviewRewards           int
	MinPopulation                   int
	ExtinctionThresholdPct          int
}

func PolicyFromConfig(cfg config.Config) Policy {
	return Policy{
		Version:                         normalizedVersion(cfg.TokenEconomyVersion),
		InitialToken:                    positiveOr(cfg.InitialToken, 100000),
		DailyTaxUnactivated:             positiveOr(cfg.DailyTaxUnactivated, 14400),
		DailyTaxActivated:               positiveOr(cfg.DailyTaxActivated, 7200),
		DailyFreeCommUnactivated:        positiveOr(cfg.DailyFreeCommUnactivated, 50000),
		DailyFreeCommActivated:          positiveOr(cfg.DailyFreeCommActivated, 200000),
		CommOverageRateMilli:            positiveOr(cfg.CommOverageRateMilli, 1000),
		HibernationPeriodTicks:          positiveOr(cfg.HibernationPeriodTicks, TicksPerDay),
		MinRevivalBalance:               positiveOr(cfg.MinRevivalBalance, 50000),
		BaseGanglionReward:              positiveOr(cfg.BaseGanglionReward, 50000),
		GanglionIntegrationRoyalty:      positiveOr(cfg.GanglionIntegrationRoyalty, 5000),
		MaxSameTypeGanglionPerDay:       positiveOrInt(cfg.MaxSameTypeGanglionPerDay, 2),
		BaseToolReward:                  positiveOr(cfg.BaseToolReward, 80000),
		ToolCreatorShareMilli:           positiveOr(cfg.ToolCreatorShareMilli, 700),
		BaseKnowledgeReward:             positiveOr(cfg.BaseKnowledgeReward, 30000),
		KnowledgeCitationReward:         positiveOr(cfg.KnowledgeCitationReward, 2000),
		MinKnowledgeTokenLength:         positiveOr(cfg.MinKnowledgeTokenLength, 500),
		RewardVote:                      positiveOr(cfg.RewardVote, 20000),
		RewardCosign:                    positiveOr(cfg.RewardCosign, 10000),
		RewardProposal:                  positiveOr(cfg.RewardProposal, 100000),
		RewardConstitutionParticipation: positiveOr(cfg.RewardConstitutionParticipation, 200000),
		RewardConstitutionPassBonus:     positiveOr(cfg.RewardConstitutionPassBonus, 500000),
		RewardHelpReply:                 positiveOr(cfg.RewardHelpReply, 10000),
		RewardRateContent:               positiveOr(cfg.RewardRateContent, 5000),
		RewardReviewTool:                positiveOr(cfg.RewardReviewTool, 15000),
		MaxDailyHelpRewards:             positiveOrInt(cfg.MaxDailyHelpRewards, 10),
		MaxDailyRateRewards:             positiveOrInt(cfg.MaxDailyRateRewards, 20),
		MaxDailyReviewRewards:           positiveOrInt(cfg.MaxDailyReviewRewards, 10),
		MinPopulation:                   cfg.MinPopulation,
		ExtinctionThresholdPct:          positiveOrInt(cfg.ExtinctionThreshold, 30),
	}
}

func (p Policy) Enabled() bool {
	return normalizedVersion(p.Version) == VersionV2
}

func (p Policy) TaxPerTick(activated bool) int64 {
	if activated {
		return ceilDiv(p.DailyTaxActivated, TicksPerDay)
	}
	return ceilDiv(p.DailyTaxUnactivated, TicksPerDay)
}

func (p Policy) DailyFreeComm(activated bool) int64 {
	if activated {
		return p.DailyFreeCommActivated
	}
	return p.DailyFreeCommUnactivated
}

func (p Policy) SafeTreasuryBalance() int64 {
	if p.MinPopulation <= 0 || p.InitialToken <= 0 {
		return 0
	}
	return int64(p.MinPopulation) * p.InitialToken * 3
}

func ScarcityMultiplier(count int) int64 {
	switch {
	case count <= 0:
		return 5000
	case count <= 2:
		return 3000
	case count <= 5:
		return 2000
	case count <= 10:
		return 1000
	case count <= 20:
		return 500
	default:
		return 200
	}
}

func ToolTierMultiplierMilli(tier string) int64 {
	switch strings.ToUpper(strings.TrimSpace(tier)) {
	case "T0":
		return 1000
	case "T1":
		return 1500
	case "T2":
		return 2500
	case "T3":
		return 4000
	default:
		return 1000
	}
}

func ToolNoveltyMultiplierMilli(existingSameClass int) int64 {
	switch {
	case existingSameClass <= 0:
		return 1000
	case existingSameClass <= 3:
		return 600
	case existingSameClass <= 10:
		return 300
	default:
		return 100
	}
}

func normalizedVersion(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return VersionV2
	}
	return v
}

func positiveOr(v, fallback int64) int64 {
	if v > 0 {
		return v
	}
	return fallback
}

func positiveOrInt(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func ceilDiv(v, divisor int64) int64 {
	if divisor <= 0 {
		return v
	}
	if v <= 0 {
		return 0
	}
	return (v + divisor - 1) / divisor
}
