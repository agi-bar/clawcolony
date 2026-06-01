package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Server) desiredMinPopulation() int {
	v := s.cfg.MinPopulation
	if v < 0 {
		v = 0
	}
	return v
}

func (s *Server) listLivingUserIDs(ctx context.Context) ([]string, error) {
	ids, err := s.listActiveUserIDs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if isExcludedTokenUserID(id) {
			continue
		}
		life, err := s.store.GetUserLifeState(ctx, id)
		if err != nil {
			// No persisted life-state means this user is considered alive by default.
			out = append(out, id)
			continue
		}
		if normalizeLifeStateForServer(life.State) == "dead" {
			continue
		}
		out = append(out, id)
	}
	return normalizeUniqueUsers(out), nil
}

func (s *Server) runMinPopulationRevival(ctx context.Context, tickID int64) error {
	minPopulation := s.desiredMinPopulation()
	if minPopulation == 0 {
		return nil
	}
	living, err := s.listLivingUserIDs(ctx)
	if err != nil {
		return err
	}
	current := len(living)
	if current >= minPopulation {
		return nil
	}

	genesisStateMu.Lock()
	state, stateErr := s.getAutoRevivalState(ctx)
	if stateErr == nil {
		state.LastTriggerTick = tickID
		state.LastTriggerAt = time.Now().UTC()
		state.LastReason = fmt.Sprintf("population below threshold: min=%d current=%d; runtime cannot auto-register users", minPopulation, current)
		state.LastRequested = 0
		state.LastTaskIDs = []int64{}
		_ = s.saveAutoRevivalState(ctx, state)
	}
	genesisStateMu.Unlock()

	subject := "[WORLD-REVIVAL] min population recovery triggered"
	body := fmt.Sprintf(
		"tick_id=%d\nmin_population=%d\ncurrent_population=%d\naction_required=trigger_user_creation_in_management_plane",
		tickID, minPopulation, current,
	)
	s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{clawWorldSystemID}, subject, body)
	return nil
}
