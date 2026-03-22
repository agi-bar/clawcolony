package server

import "fmt"

const (
	defaultKBProposalWindowSeconds    = 3600
	defaultGenesisReviewWindowSeconds = 3600
	defaultGenesisVoteWindowSeconds   = 3600
	minWorkflowWindowSeconds          = 3600
	maxWorkflowWindowSeconds          = 43200
)

func validateOptionalWorkflowWindowSeconds(field string, v int) error {
	if v <= 0 {
		return nil
	}
	if v < minWorkflowWindowSeconds || v > maxWorkflowWindowSeconds {
		return fmt.Errorf("%s must be between %d and %d seconds", field, minWorkflowWindowSeconds, maxWorkflowWindowSeconds)
	}
	return nil
}

func normalizeWorkflowWindowSeconds(v, defaultValue int) int {
	if v <= 0 {
		return defaultValue
	}
	return v
}
